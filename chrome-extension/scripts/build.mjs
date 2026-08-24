import { cp, mkdir, readFile, rm, stat, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const extensionRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const outputRoot = path.join(extensionRoot, "dist");
const extensionOutput = path.join(outputRoot, "extension");

await rm(outputRoot, { recursive: true, force: true });
await mkdir(extensionOutput, { recursive: true });
await cp(path.join(extensionRoot, "manifest.json"), path.join(extensionOutput, "manifest.json"));
await cp(path.join(extensionRoot, "src"), path.join(extensionOutput, "src"), {
  recursive: true,
});

const manifest = JSON.parse(await readFile(path.join(extensionOutput, "manifest.json"), "utf8"));
const releaseVersion = process.env.MCP_BROWSER_VERSION;
if (releaseVersion) {
  if (!/^\d+(?:\.\d+){0,3}$/u.test(releaseVersion)) {
    throw new Error("MCP_BROWSER_VERSION must be a Chrome manifest version");
  }
  manifest.version = releaseVersion;
  await writeFile(
    path.join(extensionOutput, "manifest.json"),
    `${JSON.stringify(manifest, null, 2)}\n`,
  );
}
const referencedFiles = [
  manifest.background?.service_worker,
  manifest.action?.default_popup,
  manifest.options_ui?.page,
].filter(Boolean);

for (const relativePath of referencedFiles) {
  const metadata = await stat(path.join(extensionOutput, relativePath));
  if (!metadata.isFile()) throw new Error(`Manifest target is not a file: ${relativePath}`);
}

console.log(
  `Built extension ${manifest.version} in ${path.relative(extensionRoot, extensionOutput)}`,
);
