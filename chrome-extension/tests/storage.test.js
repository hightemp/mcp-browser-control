import assert from "node:assert/strict";
import test from "node:test";

import { createStorageHandlers, isolatedStorageOperation } from "../src/handlers/storage.js";
import { ErrorCode } from "../src/protocol.js";

const target = { tabId: 7, frameId: 0, documentId: "document-7" };

test("storage list uses one isolated root document and masks values", async () => {
  const harness = createHarness({
    result(operation, params) {
      assert.equal(operation, "list");
      assert.equal(params.includeValues, false);
      return storageResult("items", {
        storageType: "localStorage",
        items: [{ key: "token", value: "[MASKED]", valueIncluded: false, valueLength: 12 }],
        totalMatched: 2,
        nextCursor: "1",
      });
    },
  });
  const handlers = createStorageHandlers(harness.chromeAPI);
  const result = await handlers.list(
    request("storage.list", {
      origin: "https://example.com",
      storageType: "localStorage",
      limit: 1,
    }),
    signal(),
  );

  assert.equal(harness.injections.length, 1);
  assert.deepEqual(harness.injections[0].target, {
    tabId: 7,
    documentIds: ["document-7"],
  });
  assert.equal(harness.injections[0].world, "ISOLATED");
  assert.equal(result.origin, "https://example.com");
  assert.equal(result.valuesIncluded, false);
  assert.equal(result.items[0].value, "[MASKED]");
  assert.equal(result.nextCursor, "1");
});

test("sensitive storage reads require the setting before page injection", async () => {
  const disabled = createHarness();
  const disabledHandlers = createStorageHandlers(disabled.chromeAPI, {
    getSettings: async () => ({ featureFlags: { sensitiveData: false } }),
  });
  await assert.rejects(
    disabledHandlers.getSensitive(
      request("storage.getSensitive", {
        origin: "https://example.com",
        storageType: "sessionStorage",
        key: "token",
      }),
      signal(),
    ),
    (error) => error.code === ErrorCode.CAPABILITY_UNAVAILABLE,
  );
  assert.equal(disabled.injections.length, 0);

  const enabled = createHarness({
    result() {
      return storageResult("item", {
        storageType: "sessionStorage",
        valuesIncluded: true,
        items: [{ key: "token", value: "secret-value", valueIncluded: true, valueLength: 12 }],
        totalMatched: 1,
      });
    },
  });
  const enabledHandlers = createStorageHandlers(enabled.chromeAPI, {
    getSettings: async () => ({ featureFlags: { sensitiveData: true } }),
  });
  const result = await enabledHandlers.getSensitive(
    request("storage.getSensitive", {
      origin: "https://example.com",
      storageType: "sessionStorage",
      key: "token",
    }),
    signal(),
  );
  assert.equal(result.valuesIncluded, true);
  assert.equal(result.items[0].value, "secret-value");
});

test("storage metadata and confirmed clear return only bounded metadata", async () => {
  const harness = createHarness({
    result(operation) {
      if (operation === "cacheMetadata") {
        return storageResult("cacheMetadata", {
          caches: [{ name: "assets-v1" }],
          totalMatched: 1,
        });
      }
      if (operation === "indexedDBMetadata") {
        return storageResult("indexedDBMetadata", {
          databases: [{ name: "application", version: 3 }],
          totalMatched: 1,
        });
      }
      return storageResult("clear", {
        operation: "clear",
        requestedTypes: ["localStorage", "indexedDB"],
        clearedTypes: ["localStorage", "indexedDB"],
        clearedCounts: { localStorage: 2, indexedDB: 1 },
      });
    },
  });
  const handlers = createStorageHandlers(harness.chromeAPI);
  const cache = await handlers.cacheMetadata(
    request("storage.cacheMetadata", { origin: "https://example.com", limit: 50 }),
    signal(),
  );
  const databases = await handlers.indexedDBMetadata(
    request("storage.indexedDBMetadata", { origin: "https://example.com", limit: 50 }),
    signal(),
  );
  const cleared = await handlers.clear(
    request("storage.clear", {
      origin: "https://example.com",
      types: ["localStorage", "indexedDB"],
      confirm: true,
    }),
    signal(),
  );
  assert.deepEqual(cache.caches, [{ name: "assets-v1" }]);
  assert.deepEqual(databases.databases, [{ name: "application", version: 3 }]);
  assert.deepEqual(cleared.clearedCounts, { localStorage: 2, indexedDB: 1 });
  assert.equal(JSON.stringify(cache).includes("body"), false);
  assert.equal(JSON.stringify(databases).includes("records"), false);
});

test("storage handler fails closed on origin, permission, confirmation, and navigation", async () => {
  const crossOrigin = createHarness();
  await assert.rejects(
    createStorageHandlers(crossOrigin.chromeAPI).get(
      request("storage.get", {
        origin: "https://other.example",
        storageType: "localStorage",
        key: "theme",
      }),
      signal(),
    ),
    (error) => error.code === ErrorCode.RESTRICTED_URL,
  );
  assert.equal(crossOrigin.injections.length, 0);

  const denied = createHarness({ permission: false });
  await assert.rejects(
    createStorageHandlers(denied.chromeAPI).get(
      request("storage.get", {
        origin: "https://example.com",
        storageType: "localStorage",
        key: "theme",
      }),
      signal(),
    ),
    (error) => error.code === ErrorCode.PERMISSION_REQUIRED,
  );

  await assert.rejects(
    createStorageHandlers(createHarness().chromeAPI).clear(
      request("storage.clear", {
        origin: "https://example.com",
        types: ["localStorage"],
        confirm: false,
      }),
      signal(),
    ),
    (error) => error.code === ErrorCode.CONFIRMATION_REQUIRED,
  );

  const stale = createHarness({
    frameDocuments: ["document-7", "document-8"],
    result: () => storageResult("item", { storageType: "localStorage" }),
  });
  await assert.rejects(
    createStorageHandlers(stale.chromeAPI).get(
      request("storage.get", {
        origin: "https://example.com",
        storageType: "localStorage",
        key: "theme",
      }),
      signal(),
    ),
    (error) => error.code === ErrorCode.STALE_TARGET,
  );
});

test("isolated storage operation bounds values and exposes metadata without records", async (t) => {
  const localStorage = new FakeStorage({ theme: "dark", token: "secret" });
  const sessionStorage = new FakeStorage({ session: "active" });
  const deletedCaches = [];
  const deletedDatabases = [];
  installGlobal(t, "location", { origin: "https://example.com" });
  installGlobal(t, "localStorage", localStorage);
  installGlobal(t, "sessionStorage", sessionStorage);
  installGlobal(t, "caches", {
    async keys() {
      return ["assets", "runtime"];
    },
    async delete(name) {
      deletedCaches.push(name);
      return true;
    },
  });
  installGlobal(t, "indexedDB", {
    async databases() {
      return [
        { name: "application", version: 2 },
        { name: "analytics", version: 1 },
      ];
    },
    deleteDatabase(name) {
      deletedDatabases.push(name);
      const operation = {};
      queueMicrotask(() => operation.onsuccess?.());
      return operation;
    },
  });

  const masked = unwrap(
    await isolatedStorageOperation("list", {
      origin: "https://example.com",
      storageType: "localStorage",
      limit: 50,
      includeValues: false,
    }),
  );
  assert.equal(
    masked.items.every((item) => item.value === "[MASKED]"),
    true,
  );

  const sensitive = unwrap(
    await isolatedStorageOperation("get", {
      origin: "https://example.com",
      storageType: "localStorage",
      key: "token",
      includeValues: true,
    }),
  );
  assert.equal(sensitive.items[0].value, "secret");

  unwrap(
    await isolatedStorageOperation("set", {
      origin: "https://example.com",
      storageType: "sessionStorage",
      key: "mode",
      value: "compact",
    }),
  );
  assert.equal(sessionStorage.getItem("mode"), "compact");

  const cache = unwrap(
    await isolatedStorageOperation("cacheMetadata", {
      origin: "https://example.com",
      limit: 50,
    }),
  );
  const databases = unwrap(
    await isolatedStorageOperation("indexedDBMetadata", {
      origin: "https://example.com",
      limit: 50,
    }),
  );
  assert.deepEqual(cache.caches, [{ name: "assets" }, { name: "runtime" }]);
  assert.deepEqual(databases.databases, [
    { name: "analytics", version: 1 },
    { name: "application", version: 2 },
  ]);

  const cleared = unwrap(
    await isolatedStorageOperation("clear", {
      origin: "https://example.com",
      types: ["localStorage", "cacheStorage", "indexedDB"],
      confirm: true,
    }),
  );
  assert.equal(localStorage.length, 0);
  assert.deepEqual(deletedCaches, ["assets", "runtime"]);
  assert.deepEqual(deletedDatabases, ["application", "analytics"]);
  assert.deepEqual(cleared.clearedCounts, {
    localStorage: 2,
    cacheStorage: 2,
    indexedDB: 2,
  });
});

test("storage clear completes preflight before mutating any requested type", async (t) => {
  const localStorage = new FakeStorage({ theme: "dark" });
  installGlobal(t, "location", { origin: "https://example.com" });
  installGlobal(t, "localStorage", localStorage);
  installGlobal(t, "sessionStorage", new FakeStorage());
  installGlobal(t, "indexedDB", {
    async databases() {
      return [{ name: "x".repeat(1_025), version: 1 }];
    },
    deleteDatabase() {
      assert.fail("preflight must reject before deleting a database");
    },
  });

  const result = await isolatedStorageOperation("clear", {
    origin: "https://example.com",
    types: ["localStorage", "indexedDB"],
    confirm: true,
  });

  assert.equal(result.ok, false);
  assert.equal(result.error.code, "PAYLOAD_TOO_LARGE");
  assert.equal(localStorage.getItem("theme"), "dark");
});

function createHarness({
  result = () => storageResult("item"),
  permission = true,
  frameDocuments = ["document-7"],
} = {}) {
  const injections = [];
  let frameIndex = 0;
  return {
    injections,
    chromeAPI: {
      tabs: {
        async get(tabId) {
          assert.equal(tabId, 7);
          return { id: 7, url: "https://example.com/current" };
        },
        async query() {
          return [{ id: 7, url: "https://example.com/current" }];
        },
      },
      webNavigation: {
        async getFrame({ tabId, frameId }) {
          assert.equal(tabId, 7);
          assert.equal(frameId, 0);
          const documentId = frameDocuments[Math.min(frameIndex, frameDocuments.length - 1)];
          frameIndex += 1;
          return { documentId };
        },
      },
      permissions: {
        async contains(details) {
          assert.deepEqual(details.permissions, ["browsingData"]);
          assert.deepEqual(details.origins, ["https://example.com/*"]);
          return permission;
        },
      },
      scripting: {
        async executeScript(injection) {
          injections.push(injection);
          const [operation, params] = injection.args;
          return [
            {
              frameId: 0,
              documentId: "document-7",
              result: { ok: true, data: result(operation, params) },
            },
          ];
        },
      },
    },
  };
}

function storageResult(kind, overrides = {}) {
  return {
    kind,
    storageType: "",
    valuesIncluded: false,
    items: [],
    caches: [],
    databases: [],
    totalMatched: 0,
    nextCursor: "",
    operation: "",
    changed: false,
    supported: true,
    requestedTypes: [],
    clearedTypes: [],
    clearedCounts: null,
    warnings: [],
    ...overrides,
  };
}

class FakeStorage {
  constructor(values = {}) {
    this.values = new Map(Object.entries(values));
  }

  get length() {
    return this.values.size;
  }

  key(index) {
    return [...this.values.keys()][index] ?? null;
  }

  getItem(key) {
    return this.values.has(String(key)) ? this.values.get(String(key)) : null;
  }

  setItem(key, value) {
    this.values.set(String(key), String(value));
  }

  removeItem(key) {
    this.values.delete(String(key));
  }

  clear() {
    this.values.clear();
  }
}

function installGlobal(t, name, value) {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, name);
  Object.defineProperty(globalThis, name, { configurable: true, value });
  t.after(() => {
    if (descriptor) Object.defineProperty(globalThis, name, descriptor);
    else delete globalThis[name];
  });
}

function unwrap(result) {
  assert.equal(result.ok, true, JSON.stringify(result));
  return result.data;
}

function request(command, params) {
  return { command, params, target, timeoutMs: 1_000 };
}

function signal() {
  return new AbortController().signal;
}
