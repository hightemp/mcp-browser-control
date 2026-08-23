import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const extensionRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const lock = JSON.parse(await readFile(path.join(extensionRoot, "package-lock.json"), "utf8"));
const forbiddenLicense = /(?:^|[ (])(?:AGPL|GPL|LGPL|SSPL|BUSL|Commons-Clause)(?:-|[ )]|$)/i;
const failures = [];
const report = [];

for (const [packagePath, metadata] of Object.entries(lock.packages || {})) {
  if (!packagePath) continue;
  const packageName = packagePath.split("node_modules/").at(-1);
  const license = typeof metadata.license === "string" ? metadata.license.trim() : "";
  report.push({ packageName, license: license || "UNKNOWN" });
  if (!license) failures.push(`${packageName}: missing license metadata`);
  if (forbiddenLicense.test(license)) failures.push(`${packageName}: forbidden license ${license}`);
}

report.sort((left, right) => left.packageName.localeCompare(right.packageName));
for (const dependency of report) console.log(`${dependency.packageName}\t${dependency.license}`);

if (failures.length > 0) {
  throw new Error(`Extension license check failed:\n${failures.join("\n")}`);
}
