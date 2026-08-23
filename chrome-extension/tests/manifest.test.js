import assert from "node:assert/strict";
import { readFile, readdir, stat } from "node:fs/promises";
import test from "node:test";

const extensionRoot = new URL("../", import.meta.url);
const manifestURL = new URL("manifest.json", extensionRoot);
const manifest = JSON.parse(await readFile(manifestURL, "utf8"));

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
