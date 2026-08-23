import { cp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const extensionRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const output = path.join(extensionRoot, "dist", "e2e-extension");

await rm(output, { recursive: true, force: true });
await mkdir(output, { recursive: true });
await cp(path.join(extensionRoot, "src"), path.join(output, "src"), { recursive: true });

const manifest = JSON.parse(await readFile(path.join(extensionRoot, "manifest.json"), "utf8"));
manifest.name = `${manifest.name} E2E`;
manifest.host_permissions = ["http://127.0.0.1/*"];
manifest.optional_host_permissions = [];
await writeFile(path.join(output, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);

console.log(`Built E2E extension in ${path.relative(extensionRoot, output)}`);
