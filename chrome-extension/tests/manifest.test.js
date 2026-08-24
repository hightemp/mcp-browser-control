import assert from "node:assert/strict";
import { readFile, readdir, stat } from "node:fs/promises";
import test from "node:test";

const extensionRoot = new URL("../", import.meta.url);
const manifestURL = new URL("manifest.json", extensionRoot);
const manifest = JSON.parse(await readFile(manifestURL, "utf8"));
const packageJSON = JSON.parse(await readFile(new URL("package.json", extensionRoot), "utf8"));
const packageLock = JSON.parse(await readFile(new URL("package-lock.json", extensionRoot), "utf8"));

const requiredPermissions = ["alarms", "scripting", "storage", "tabs", "webNavigation"];
const optionalPermissions = [
  "bookmarks",
  "browsingData",
  "clipboardRead",
  "clipboardWrite",
  "cookies",
  "debugger",
  "downloads",
  "history",
  "sessions",
  "tabGroups",
  "webRequest",
];

test("manifest uses MV3 with the minimal baseline permission set", () => {
  assert.equal(manifest.manifest_version, 3);
  assert.deepEqual([...manifest.permissions].sort(), requiredPermissions);
  assert.equal("host_permissions" in manifest, false);
});

test("manifest and locked package metadata use one release version", () => {
  assert.match(manifest.version, /^\d+(?:\.\d+){0,3}$/u);
  assert.equal(packageJSON.version, manifest.version);
  assert.equal(packageLock.version, manifest.version);
  assert.equal(packageLock.packages[""].version, manifest.version);
});

test("sensitive capabilities and site access remain optional", () => {
  assert.deepEqual([...manifest.optional_permissions].sort(), optionalPermissions);
  assert.deepEqual(manifest.optional_host_permissions, ["http://*/*", "https://*/*"]);
});

test("manifest entrypoints are local files", async () => {
  const entrypoints = [
    manifest.background?.service_worker,
    manifest.action?.default_popup,
    manifest.options_ui?.page,
  ];

  for (const entrypoint of entrypoints) {
    assert.match(entrypoint, /^src\/[a-z0-9-]+\.(?:html|js)$/);
    assert.equal((await stat(new URL(entrypoint, extensionRoot))).isFile(), true);
  }
});

test("extension source is English-only and does not import remote code", async () => {
  const sourceFiles = await listFiles(new URL("src/", extensionRoot));
  sourceFiles.push(manifestURL);

  for (const file of sourceFiles) {
    const source = await readFile(file, "utf8");
    assert.doesNotMatch(source, /[\u0400-\u04ff]/u, file.pathname);
    assert.doesNotMatch(
      source,
      /(?:<script[^>]+src\s*=|\bimport\s*(?:\(|[^;]*?\bfrom\s*)|\bnew\s+(?:Shared)?Worker\s*\()\s*["']https?:\/\//iu,
      file.pathname,
    );
  }
});

test("runtime command paths cannot silently request optional permissions", async () => {
  const sourceFiles = await listFiles(new URL("src/", extensionRoot));
  for (const file of sourceFiles.filter(
    (candidate) =>
      candidate.pathname.endsWith(".js") && !candidate.pathname.endsWith("/options.js"),
  )) {
    const source = await readFile(file, "utf8");
    assert.doesNotMatch(source, /chrome\.permissions(?:\.|\[)request\b/u, file.pathname);
  }

  const optionsSource = await readFile(new URL("src/options.js", extensionRoot), "utf8");
  assert.match(optionsSource, /toggle\.addEventListener\("click"/u);
  assert.match(optionsSource, /changePermissions\("request"/u);
});

async function listFiles(directory) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const url = new URL(entry.name, directory);
    if (entry.isDirectory()) {
      files.push(...(await listFiles(new URL(`${entry.name}/`, directory))));
    } else if (entry.isFile()) {
      files.push(url);
    }
  }
  return files;
}
