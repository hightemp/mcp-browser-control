import assert from "node:assert/strict";
import test from "node:test";

import { createCookieHandlers } from "../src/handlers/cookies.js";
import { ErrorCode } from "../src/protocol.js";

const target = { tabId: 7, frameId: 0, documentId: "document-7" };

test("cookie list is exact-origin, store-scoped, filtered, paginated, and masked", async () => {
  const harness = createHarness({
    cookies: [cookie("first", "secret-1"), cookie("second", "secret-2")],
  });
  const handlers = createCookieHandlers(harness.chromeAPI);
  const result = await handlers.list(
    request("cookies.list", {
      url: "https://example.com/account?view=1",
      domain: ".example.com",
      secure: true,
      partitionKey: {
        topLevelSite: "https://example.com",
        hasCrossSiteAncestor: false,
      },
      limit: 1,
    }),
    signal(),
  );

  assert.deepEqual(harness.queries[0], {
    url: "https://example.com/account?view=1",
    storeId: "0",
    domain: ".example.com",
    secure: true,
    partitionKey: {
      topLevelSite: "https://example.com",
      hasCrossSiteAncestor: false,
    },
  });
  assert.equal(result.origin, "https://example.com");
  assert.equal(result.documentId, "document-7");
  assert.equal(result.valuesIncluded, false);
  assert.equal(result.cookies.length, 1);
  assert.equal(result.cookies[0].value, "[MASKED]");
  assert.equal(result.cookies[0].valueIncluded, false);
  assert.equal(result.cookies[0].valueLength, 8);
  assert.equal(result.nextCursor, "1");
  assert.equal(JSON.stringify(result).includes("secret-1"), false);
});

test("sensitive cookie reads require the setting and preserve bounded values", async () => {
  const disabled = createHarness({ cookies: [cookie("session", "sensitive-value")] });
  const disabledHandlers = createCookieHandlers(disabled.chromeAPI, {
    getSettings: async () => ({ featureFlags: { sensitiveData: false } }),
  });
  await assert.rejects(
    disabledHandlers.getSensitive(
      request("cookies.getSensitive", { url: "https://example.com/", name: "session" }),
      signal(),
    ),
    (error) => error.code === ErrorCode.CAPABILITY_UNAVAILABLE,
  );
  assert.equal(disabled.calls.get, 0);

  const enabled = createHarness({ cookies: [cookie("session", "sensitive-value")] });
  const enabledHandlers = createCookieHandlers(enabled.chromeAPI, {
    getSettings: async () => ({ featureFlags: { sensitiveData: true } }),
  });
  const result = await enabledHandlers.getSensitive(
    request("cookies.getSensitive", { url: "https://example.com/", name: "session" }),
    signal(),
  );
  assert.equal(result.valuesIncluded, true);
  assert.equal(result.cookies[0].value, "sensitive-value");
  assert.equal(result.cookies[0].valueIncluded, true);
});

test("cookie set and remove use the selected store without echoing supplied values", async () => {
  const harness = createHarness();
  const handlers = createCookieHandlers(harness.chromeAPI);
  const set = await handlers.set(
    request("cookies.set", {
      url: "https://example.com/account",
      name: "session",
      value: "caller-secret",
      path: "/",
      secure: true,
      httpOnly: true,
      sameSite: "lax",
    }),
    signal(),
  );
  assert.equal(harness.setDetails.value, "caller-secret");
  assert.equal(harness.setDetails.storeId, "0");
  assert.equal(set.cookies[0].value, "[MASKED]");
  assert.equal(JSON.stringify(set).includes("caller-secret"), false);

  const removed = await handlers.remove(
    request("cookies.remove", { url: "https://example.com/account", name: "session" }),
    signal(),
  );
  assert.equal(removed.removed, true);
  assert.deepEqual(harness.removeDetails, {
    url: "https://example.com/account",
    name: "session",
    storeId: "0",
  });
});

test("cookie handlers fail closed on origin, permission, store, and document changes", async () => {
  const crossOrigin = createHarness();
  await assert.rejects(
    createCookieHandlers(crossOrigin.chromeAPI).get(
      request("cookies.get", { url: "https://other.example/", name: "session" }),
      signal(),
    ),
    (error) => error.code === ErrorCode.RESTRICTED_URL,
  );
  assert.equal(crossOrigin.calls.get, 0);

  const denied = createHarness({ permission: false });
  await assert.rejects(
    createCookieHandlers(denied.chromeAPI).get(
      request("cookies.get", { url: "https://example.com/", name: "session" }),
      signal(),
    ),
    (error) => error.code === ErrorCode.PERMISSION_REQUIRED,
  );

  const wrongStore = createHarness();
  await assert.rejects(
    createCookieHandlers(wrongStore.chromeAPI).get(
      request("cookies.get", {
        url: "https://example.com/",
        name: "session",
        storeId: "private",
      }),
      signal(),
    ),
    (error) => error.code === ErrorCode.PERMISSION_REQUIRED,
  );

  const stale = createHarness({ frameDocuments: ["document-7", "document-8"] });
  await assert.rejects(
    createCookieHandlers(stale.chromeAPI).get(
      request("cookies.get", { url: "https://example.com/", name: "session" }),
      signal(),
    ),
    (error) => error.code === ErrorCode.STALE_TARGET,
  );
});

function createHarness({ cookies = [], permission = true, frameDocuments = ["document-7"] } = {}) {
  const calls = { get: 0, getAll: 0, set: 0, remove: 0 };
  const queries = [];
  let frameIndex = 0;
  const resultCookie = cookies[0] || cookie("session", "caller-secret");
  const harness = {
    calls,
    queries,
    setDetails: null,
    removeDetails: null,
  };
  harness.chromeAPI = {
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
        assert.deepEqual(details.permissions, ["cookies"]);
        assert.deepEqual(details.origins, ["https://example.com/*"]);
        return permission;
      },
    },
    cookies: {
      async getAll(details) {
        calls.getAll += 1;
        queries.push(structuredClone(details));
        return structuredClone(cookies);
      },
      async get(details) {
        calls.get += 1;
        queries.push(structuredClone(details));
        return structuredClone(cookies[0] || null);
      },
      async set(details) {
        calls.set += 1;
        harness.setDetails = structuredClone(details);
        return { ...structuredClone(resultCookie), value: details.value };
      },
      async remove(details) {
        calls.remove += 1;
        harness.removeDetails = structuredClone(details);
        return { url: details.url, name: details.name, storeId: details.storeId };
      },
      async getAllCookieStores() {
        return [
          { id: "0", tabIds: [7] },
          { id: "private", tabIds: [8] },
        ];
      },
    },
  };
  return harness;
}

function cookie(name, value) {
  return {
    name,
    value,
    domain: ".example.com",
    hostOnly: false,
    path: "/",
    secure: true,
    httpOnly: true,
    sameSite: "lax",
    session: true,
    storeId: "0",
  };
}

function request(command, params) {
  return { command, params, target, timeoutMs: 1_000 };
}

function signal() {
  return new AbortController().signal;
}
