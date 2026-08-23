import assert from "node:assert/strict";
import test from "node:test";

import { getStoredIdentity, initializeStoredState, resetStoredIdentity } from "../src/identity.js";
import { ErrorCode } from "../src/protocol.js";

const DEFAULT_SETTINGS = Object.freeze({
  endpoint: "ws://127.0.0.1:8090/ws",
  displayName: "",
  autoConnect: true,
});

test("browser identity is generated once in local storage", async () => {
  const storage = fakeStorage();
  const first = await initializeStoredState(storage, DEFAULT_SETTINGS, () => "browser-1");
  const second = await getStoredIdentity(storage, DEFAULT_SETTINGS, () => "browser-2");

  assert.equal(first.browserId, "browser-1");
  assert.equal(second, "browser-1");
  assert.deepEqual(storage.values.settings, DEFAULT_SETTINGS);
  assert.notEqual(storage.values.settings, DEFAULT_SETTINGS);
});

test("identity reset requires confirmation and preserves settings", async () => {
  const storage = fakeStorage({
    browserId: "browser-old",
    credential: "secret-credential",
    connectionDiagnostics: {
      lastConnectedAt: "2026-08-23T19:00:00Z",
      latencyMS: 5,
    },
    settings: { ...DEFAULT_SETTINGS, displayName: "Work Chrome" },
  });

  await assert.rejects(
    resetStoredIdentity(storage, false, () => "browser-new"),
    (error) => error.code === ErrorCode.CONFIRMATION_REQUIRED,
  );
  assert.equal(storage.values.browserId, "browser-old");
  assert.equal(storage.values.credential, "secret-credential");

  assert.equal(await resetStoredIdentity(storage, true, () => "browser-new"), "browser-new");
  assert.equal(storage.values.browserId, "browser-new");
  assert.equal(storage.values.credential, undefined);
  assert.equal(storage.values.connectionDiagnostics, undefined);
  assert.equal(storage.values.settings.displayName, "Work Chrome");
});

function fakeStorage(initial = {}) {
  const values = { ...initial };
  return {
    values,
    async get(keys) {
      return Object.fromEntries(
        keys.filter((key) => key in values).map((key) => [key, values[key]]),
      );
    },
    async set(updates) {
      Object.assign(values, updates);
    },
    async remove(keys) {
      for (const key of Array.isArray(keys) ? keys : [keys]) {
        delete values[key];
      }
    },
  };
}
