import { spawnSync } from "node:child_process";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repositoryRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const releaseDirectory = path.resolve(
  repositoryRoot,
  process.env.RELEASE_DIR || path.join(repositoryRoot, "release"),
);
const version = requiredEnvironment("VERSION");
const commit = requiredEnvironment("COMMIT");
const sourceDateEpoch = Number.parseInt(
  requiredEnvironment("SOURCE_DATE_EPOCH"),
  10,
);
const targets = requiredEnvironment("TARGETS").trim().split(/\s+/u);

if (!/^\d+(?:\.\d+){0,3}$/u.test(version)) {
  throw new Error("VERSION must be a Chrome-compatible dotted numeric version");
}
if (!/^[0-9a-f]{40}$/u.test(commit)) {
  throw new Error("COMMIT must be a full Git object ID");
}
if (!Number.isSafeInteger(sourceDateEpoch) || sourceDateEpoch < 315532800) {
  throw new Error(
    "SOURCE_DATE_EPOCH must be a Unix timestamp on or after 1980-01-01",
  );
}

await mkdir(releaseDirectory, { recursive: true });
const buildDate = new Date(sourceDateEpoch * 1_000).toISOString();
await Promise.all([writeSBOM(), writeReleaseManifest(), writeReleaseNotes()]);

async function writeSBOM() {
  const goModules = command(
    "go",
    ["list", "-m", "-f", "{{.Path}}\t{{.Version}}\t{{.Sum}}", "all"],
    repositoryRoot,
  )
    .trim()
    .split("\n")
    .map((line) => line.split("\t"))
    .filter(([modulePath, moduleVersion]) => modulePath && moduleVersion)
    .map(([modulePath, moduleVersion, sum]) => ({
      type: "library",
      name: modulePath,
      version: moduleVersion,
      purl: `pkg:golang/${encodePURLPath(modulePath)}@${encodeURIComponent(moduleVersion)}`,
      ...(sum?.startsWith("h1:")
        ? { hashes: [{ alg: "SHA-256", content: digestHex(sum.slice(3)) }] }
        : {}),
      properties: [{ name: "mcp-browser-control:ecosystem", value: "go" }],
    }));

  const packageLock = JSON.parse(
    await readFile(
      path.join(repositoryRoot, "chrome-extension", "package-lock.json"),
      "utf8",
    ),
  );
  const npmPackages = Object.entries(packageLock.packages || {})
    .filter(([packagePath, metadata]) => packagePath && metadata.version)
    .map(([packagePath, metadata]) => {
      const name = npmPackageName(packagePath);
      return {
        type: "library",
        name,
        version: metadata.version,
        purl: `pkg:npm/${encodeNPMPURLName(name)}@${encodeURIComponent(metadata.version)}`,
        ...(metadata.integrity
          ? { hashes: integrityHashes(metadata.integrity) }
          : {}),
        ...(metadata.license
          ? { licenses: [{ license: { id: metadata.license } }] }
          : {}),
        scope: metadata.dev ? "excluded" : "required",
        properties: [{ name: "mcp-browser-control:ecosystem", value: "npm" }],
      };
    });

  const components = [...goModules, ...npmPackages].sort((left, right) =>
    left.purl.localeCompare(right.purl, "en"),
  );
  const sbom = {
    $schema: "https://cyclonedx.org/schema/bom-1.6.schema.json",
    bomFormat: "CycloneDX",
    specVersion: "1.6",
    version: 1,
    metadata: {
      timestamp: buildDate,
      component: {
        type: "application",
        name: "mcp-browser-control",
        version,
        "bom-ref": `pkg:generic/mcp-browser-control@${encodeURIComponent(version)}`,
        properties: [
          { name: "mcp-browser-control:commit", value: commit },
          { name: "mcp-browser-control:targets", value: targets.join(",") },
        ],
      },
    },
    components,
  };
  await writeJSON(
    path.join(releaseDirectory, "mcp-browser-control.cdx.json"),
    sbom,
  );
}

async function writeReleaseManifest() {
  await writeJSON(path.join(releaseDirectory, "release-manifest.json"), {
    version,
    commit,
    sourceDateEpoch,
    buildDate,
    targets,
    extension: {
      file: `mcp-browser-control_${version}_extension.zip`,
      manifestVersion: version,
    },
    sbom: "mcp-browser-control.cdx.json",
    checksums: "SHA256SUMS",
  });
}

async function writeReleaseNotes() {
  const previousTag = optionalCommand(
    "git",
    ["describe", "--tags", "--abbrev=0", `${commit}^`],
    repositoryRoot,
  ).trim();
  const range = previousTag ? `${previousTag}..${commit}` : commit;
  const changes = command(
    "git",
    ["log", "--reverse", "--format=%h%x09%s", range],
    repositoryRoot,
  )
    .trim()
    .split("\n")
    .filter(Boolean)
    .map((line) => {
      const [shortCommit, ...subject] = line.split("\t");
      return `- \`${shortCommit}\` ${subject.join(" ")}`;
    });
  const notes = [
    `# MCP Browser Control ${version}`,
    "",
    `Source commit: \`${commit}\``,
    "",
    `Reproducible build date: \`${buildDate}\``,
    "",
    "## Changes",
    "",
    ...(changes.length > 0
      ? changes
      : ["- No commits in the selected release range."]),
    "",
    "## Release Artifacts",
    "",
    "- Cross-platform, statically linked server binaries for every target in `release-manifest.json`.",
    "- A deterministic Chromium extension ZIP with the same release version.",
    "- CycloneDX SBOM, release manifest, and `SHA256SUMS`.",
    "",
    "## Known Limitations",
    "",
    `Review the [published limitations](https://github.com/hightemp/go_mcp_browser_ext_tool/blob/${commit}/docs/known-limitations.md) for this source commit before installation or rollout.`,
    "",
    "Verify every downloaded file with `sha256sum -c SHA256SUMS`.",
    "",
  ].join("\n");
  await writeFile(path.join(releaseDirectory, "RELEASE_NOTES.md"), notes);
}

function requiredEnvironment(name) {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}

function command(executable, args, cwd) {
  const result = spawnSync(executable, args, { cwd, encoding: "utf8" });
  if (result.status !== 0) {
    throw new Error(
      `${executable} ${args.join(" ")} failed: ${result.stderr.trim()}`,
    );
  }
  return result.stdout;
}

function optionalCommand(executable, args, cwd) {
  const result = spawnSync(executable, args, { cwd, encoding: "utf8" });
  return result.status === 0 ? result.stdout : "";
}

function npmPackageName(packagePath) {
  const marker = "node_modules/";
  const offset = packagePath.lastIndexOf(marker);
  return packagePath.slice(offset + marker.length);
}

function encodePURLPath(value) {
  return value.split("/").map(encodeURIComponent).join("/");
}

function encodeNPMPURLName(value) {
  if (!value.startsWith("@")) return encodeURIComponent(value);
  const [scope, name] = value.split("/");
  return `${encodeURIComponent(scope)}/${encodeURIComponent(name)}`;
}

function integrityHashes(value) {
  const hashes = [];
  for (const token of value.split(/\s+/u)) {
    const separator = token.indexOf("-");
    if (separator < 1) continue;
    const algorithm = token.slice(0, separator).toLowerCase();
    const names = { sha256: "SHA-256", sha384: "SHA-384", sha512: "SHA-512" };
    if (names[algorithm]) {
      hashes.push({
        alg: names[algorithm],
        content: digestHex(token.slice(separator + 1)),
      });
    }
  }
  return hashes;
}

function digestHex(base64Value) {
  return Buffer.from(base64Value, "base64").toString("hex");
}

async function writeJSON(file, value) {
  await writeFile(file, `${JSON.stringify(value, null, 2)}\n`);
}
