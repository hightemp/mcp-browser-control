import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import test from "node:test";

import Ajv2020 from "ajv/dist/2020.js";

import { validateIncomingMessage } from "../src/protocol.js";

const schemaURL = new URL("../../protocol/schema/v1.schema.json", import.meta.url);
const fixturesURL = new URL("../../protocol/fixtures/v1/", import.meta.url);

const schema = JSON.parse(await readFile(schemaURL, "utf8"));
const ajv = new Ajv2020({
  allErrors: true,
  strict: true,
  strictRequired: false,
});
const validate = ajv.compile(schema);

test("shared valid protocol fixtures pass Ajv and extension validation", async (t) => {
  for (const fixture of await fixtureNames("valid")) {
    await t.test(fixture, async () => {
      const message = await readFixture("valid", fixture);
      assert.equal(validate(message), true, ajv.errorsText(validate.errors));
      assert.equal(validateIncomingMessage(message, message.browserId), message);
    });
  }
});

test("shared invalid protocol fixtures fail Ajv validation", async (t) => {
  for (const fixture of await fixtureNames("invalid")) {
    await t.test(fixture, async () => {
      const message = await readFixture("invalid", fixture);
      assert.equal(validate(message), false, `fixture unexpectedly passed: ${fixture}`);
    });
  }
});

test("shared runtime-invalid fixtures pass schema and fail extension validation", async (t) => {
  for (const fixture of await fixtureNames("runtime-invalid")) {
    await t.test(fixture, async () => {
      const message = await readFixture("runtime-invalid", fixture);
      assert.equal(validate(message), true, ajv.errorsText(validate.errors));
      assert.throws(
        () => validateIncomingMessage(message, message.browserId),
        (error) => error?.code === "INVALID_MESSAGE",
      );
    });
  }
});

test("shared malformed fixtures fail JSON parsing", async (t) => {
  for (const fixture of await fixtureNames("malformed")) {
    await t.test(fixture, async () => {
      const payload = await readFile(new URL(`malformed/${fixture}`, fixturesURL), "utf8");
      assert.throws(() => JSON.parse(payload), SyntaxError);
    });
  }
});

async function fixtureNames(group) {
  const entries = await readdir(new URL(`${group}/`, fixturesURL), {
    withFileTypes: true,
  });
  return entries
    .filter((entry) => entry.isFile() && entry.name.endsWith(".json"))
    .map((entry) => entry.name)
    .sort();
}

async function readFixture(group, name) {
  return JSON.parse(await readFile(new URL(`${group}/${name}`, fixturesURL), "utf8"));
}
