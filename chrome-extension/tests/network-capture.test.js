import assert from "node:assert/strict";
import test from "node:test";

import { createNetworkHandlers } from "../src/handlers/network.js";

const target = { tabId: 7, frameId: 0, documentId: "document-1" };

test("network capture owns an exact lease and returns bounded redacted redirect metadata", async () => {
  const harness = createHarness();
  const handlers = createNetworkHandlers(harness.chromeAPI, { cdpSessions: harness.sessions });

  const started = await handlers.start(request("network.start", { maxEntries: 2 }), signal());
  assert.equal(started.active, true);
  assert.equal(started.maxEntries, 2);
  assert.deepEqual(harness.options.domains, ["Network"]);
  assert.deepEqual(harness.options.commands, [
    "Network.enable",
    "Network.getRequestPostData",
    "Network.getResponseBody",
  ]);
  assert.deepEqual(harness.options.events, [
    "Network.requestWillBeSent",
    "Network.requestWillBeSentExtraInfo",
    "Network.responseReceived",
    "Network.responseReceivedExtraInfo",
    "Network.loadingFinished",
    "Network.loadingFailed",
    "Network.requestServedFromCache",
  ]);
  assert.deepEqual(harness.calls[0], [
    "Network.enable",
    {
      maxTotalBufferSize: 2_000_000,
      maxResourceBufferSize: 1_000_000,
      maxPostDataSize: 1_000_000,
    },
  ]);

  emitRequest(harness, {
    requestId: "cdp-request-1",
    url: "https://example.com/start?token=secret",
    method: "GET",
    type: "Document",
    timestamp: 1,
    wallTime: 1_700_000_000,
  });
  emitRequest(harness, {
    requestId: "cdp-request-1",
    url: "https://example.com/final?api_key=hidden",
    method: "GET",
    type: "Document",
    timestamp: 1.1,
    wallTime: 1_700_000_000.1,
    redirectResponse: {
      status: 302,
      statusText: "Found",
      protocol: "h2",
      headers: { Location: "https://example.com/final?api_key=hidden", "Set-Cookie": "sid=secret" },
      mimeType: "text/html",
      encodedDataLength: 120,
    },
  });
  harness.emit("Network.responseReceivedExtraInfo", {
    requestId: "cdp-request-1",
    statusCode: 302,
    headers: { "X-Redirect-Hop": "old", "Set-Cookie": "sid=secret" },
  });
  harness.emit("Network.responseReceived", {
    requestId: "cdp-request-1",
    type: "Document",
    hasExtraInfo: true,
    response: {
      status: 200,
      statusText: "OK",
      protocol: "h2",
      headers: { "Content-Type": "text/html", Authorization: "Bearer secret" },
      mimeType: "text/html",
      encodedDataLength: 512,
      timing: { sendStart: 1, sendEnd: 2, receiveHeadersEnd: 10 },
    },
  });
  harness.emit("Network.responseReceivedExtraInfo", {
    requestId: "cdp-request-1",
    statusCode: 200,
    headers: {
      "Content-Type": "text/html",
      Authorization: "Bearer secret",
      "X-Redirect-Hop": "final",
    },
  });
  harness.emit("Network.requestServedFromCache", { requestId: "cdp-request-1" });
  harness.emit("Network.loadingFinished", {
    requestId: "cdp-request-1",
    timestamp: 1.25,
    encodedDataLength: 640,
  });

  const result = await handlers.read(
    request("network.read", { limit: 50, maxBytes: 512 * 1024 }),
    signal(),
  );
  assert.equal(result.entries.length, 2);
  assert.equal(result.entries[0].status, 302);
  assert.equal(result.entries[0].redirectTo, "2");
  assert.equal(result.entries[1].redirectFrom, "1");
  assert.equal(result.entries[1].responseBodyAvailable, true);
  assert.equal(result.entries[1].fromCache, true);
  assert.match(result.entries[0].url, /token=%5BREDACTED%5D/);
  assert.match(result.entries[1].url, /api_key=%5BREDACTED%5D/);
  assert.equal(result.entries[0].responseHeaders["Set-Cookie"], "[REDACTED]");
  assert.equal(result.entries[0].responseHeaders["X-Redirect-Hop"], "old");
  assert.equal(result.entries[1].responseHeaders.Authorization, "[REDACTED]");
  assert.equal(result.entries[1].responseHeaders["X-Redirect-Hop"], "final");
  assert.equal(JSON.stringify(result).includes("cdp-request-1"), false);

  emitRequest(harness, {
    requestId: "cdp-request-2",
    url: "https://example.com/third",
    method: "GET",
    type: "Fetch",
    timestamp: 2,
    wallTime: 1_700_000_001,
  });
  harness.emit("Network.loadingFailed", {
    requestId: "cdp-request-2",
    timestamp: 2.1,
    errorText: "Bearer secret-token",
    canceled: false,
  });
  const evicted = await handlers.read(
    request("network.read", { cursor: "1", limit: 1, failedOnly: true, maxBytes: 64 * 1024 }),
    signal(),
  );
  assert.equal(evicted.entries.length, 1);
  assert.equal(evicted.entries[0].failed, true);
  assert.equal(evicted.entries[0].errorText, "Bearer [REDACTED]");
  assert.equal(evicted.evictedEntries, 1);
});

test("network bodies are same-origin textual artifacts with field redaction", async () => {
  const harness = createHarness({
    results(method) {
      if (method === "Network.getRequestPostData") {
        return { postData: '{"email":"user@example.com","password":"secret"}' };
      }
      if (method === "Network.getResponseBody") {
        return { body: '{"ok":true,"access_token":"secret-token"}', base64Encoded: false };
      }
      return {};
    },
  });
  const handlers = createNetworkHandlers(harness.chromeAPI, { cdpSessions: harness.sessions });
  await handlers.start(request("network.start", { maxEntries: 100 }), signal());
  harness.emit("Network.requestWillBeSentExtraInfo", {
    requestId: "body-request",
    headers: { "Content-Type": "application/json" },
  });
  emitRequest(harness, {
    requestId: "body-request",
    url: "https://example.com/api",
    method: "POST",
    type: "Fetch",
    timestamp: 1,
    wallTime: 1_700_000_000,
    hasPostData: true,
  });
  harness.emit("Network.responseReceived", {
    requestId: "body-request",
    type: "Fetch",
    hasExtraInfo: true,
    response: {
      status: 200,
      headers: {},
      mimeType: "",
    },
  });
  harness.emit("Network.loadingFinished", {
    requestId: "body-request",
    timestamp: 1.2,
    encodedDataLength: 50,
  });
  harness.emit("Network.responseReceivedExtraInfo", {
    requestId: "body-request",
    statusCode: 200,
    headers: { "Content-Type": "application/json" },
  });

  for (const direction of ["request", "response"]) {
    const result = await handlers.getBody(
      request("network.getBody", { entryId: "1", direction, maxBytes: 64 * 1024 }),
      signal(),
    );
    const body = JSON.parse(Buffer.from(result.dataBase64, "base64").toString());
    assert.equal(result.kind, `${direction}Body`);
    assert.equal(result.mimeType, "application/json");
    assert.equal(result.redactionApplied, true);
    assert.equal(body.password ?? body.access_token, "[REDACTED]");
    assert.equal(JSON.stringify(result).includes("secret-token"), false);
  }

  emitRequest(harness, {
    requestId: "cross-origin",
    url: "https://other.example/data",
    method: "GET",
    type: "Fetch",
    timestamp: 2,
    wallTime: 1_700_000_001,
  });
  harness.emit("Network.responseReceived", {
    requestId: "cross-origin",
    type: "Fetch",
    response: { status: 200, headers: { "Content-Type": "text/plain" }, mimeType: "text/plain" },
  });
  harness.emit("Network.loadingFinished", {
    requestId: "cross-origin",
    timestamp: 2.1,
    encodedDataLength: 5,
  });
  await assert.rejects(
    handlers.getBody(
      request("network.getBody", { entryId: "2", direction: "response", maxBytes: 64 * 1024 }),
      signal(),
    ),
    (error) => error.code === "RESTRICTED_URL",
  );

  emitRequest(harness, {
    requestId: "binary",
    url: "https://example.com/archive.zip",
    method: "GET",
    type: "Other",
    timestamp: 3,
    wallTime: 1_700_000_002,
  });
  harness.emit("Network.responseReceived", {
    requestId: "binary",
    type: "Other",
    response: {
      status: 200,
      headers: { "Content-Type": "application/zip" },
      mimeType: "application/zip",
    },
  });
  harness.emit("Network.loadingFinished", {
    requestId: "binary",
    timestamp: 3.1,
    encodedDataLength: 5,
  });
  await assert.rejects(
    handlers.getBody(
      request("network.getBody", { entryId: "3", direction: "response", maxBytes: 64 * 1024 }),
      signal(),
    ),
    (error) => error.code === "CAPABILITY_UNAVAILABLE",
  );

  await handlers.stop(request("network.stop", {}), signal());
  assert.equal(harness.releaseCount, 1);
  await assert.rejects(
    handlers.getBody(
      request("network.getBody", { entryId: "1", direction: "response", maxBytes: 64 * 1024 }),
      signal(),
    ),
    (error) => error.code === "CAPABILITY_UNAVAILABLE",
  );
});

test("clear and root navigation discard internal capture state", async () => {
  const harness = createHarness();
  const handlers = createNetworkHandlers(harness.chromeAPI, { cdpSessions: harness.sessions });
  await handlers.start(request("network.start", { maxEntries: 100 }), signal());
  emitRequest(harness, {
    requestId: "cleared-request",
    url: "https://example.com/data",
    method: "GET",
    type: "Fetch",
    timestamp: 1,
    wallTime: 1_700_000_000,
  });
  const cleared = await handlers.clear(request("network.clear", {}), signal());
  assert.equal(cleared.retainedEntries, 0);
  assert.equal(cleared.evictedEntries, 0);
  const empty = await handlers.read(
    request("network.read", { limit: 50, maxBytes: 64 * 1024 }),
    signal(),
  );
  assert.deepEqual(empty.entries, []);

  harness.commit({ tabId: 7, frameId: 0 });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(harness.releaseCount, 1);
  await assert.rejects(
    handlers.read(request("network.read", { limit: 50, maxBytes: 64 * 1024 }), signal()),
    (error) => error.code === "CAPABILITY_UNAVAILABLE",
  );
});

test("network capture applies per-browser backpressure across active tabs", async () => {
  const harness = createHarness();
  const handlers = createNetworkHandlers(harness.chromeAPI, { cdpSessions: harness.sessions });
  for (let tabId = 1; tabId <= 8; tabId += 1) {
    const startRequest = request("network.start", { maxEntries: 100 });
    startRequest.target = { tabId, frameId: 0, documentId: "document-1" };
    const result = await handlers.start(startRequest, signal());
    assert.equal(result.tabId, tabId);
  }
  const rejected = request("network.start", { maxEntries: 100 });
  rejected.target = { tabId: 9, frameId: 0, documentId: "document-1" };
  await assert.rejects(
    handlers.start(rejected, signal()),
    (error) => error.code === "BACKPRESSURE",
  );
  assert.equal(harness.acquireCount, 8);
});

test("HAR export retains metadata without bodies and expires stopped captures", async () => {
  let now = 1_700_000_000_000;
  const harness = createHarness();
  const handlers = createNetworkHandlers(harness.chromeAPI, {
    cdpSessions: harness.sessions,
    now: () => now,
  });
  await handlers.start(request("network.start", { maxEntries: 100 }), signal());
  emitRequest(harness, {
    requestId: "har-request",
    url: "https://example.com/data?secret=value",
    method: "GET",
    type: "XHR",
    timestamp: 1,
    wallTime: 1_700_000_000,
  });
  harness.emit("Network.responseReceived", {
    requestId: "har-request",
    type: "XHR",
    response: {
      status: 204,
      statusText: "No Content",
      protocol: "h2",
      headers: { "Content-Type": "application/json" },
      mimeType: "application/json",
      encodedDataLength: 20,
    },
  });
  harness.emit("Network.loadingFinished", {
    requestId: "har-request",
    timestamp: 1.1,
    encodedDataLength: 20,
  });
  await handlers.stop(request("network.stop", {}), signal());

  const result = await handlers.exportHAR(
    request("network.exportHAR", { maxBytes: 64 * 1024 }),
    signal(),
  );
  const artifactText = Buffer.from(result.dataBase64, "base64").toString();
  const artifact = JSON.parse(artifactText);
  assert.equal(result.entryCount, 1);
  assert.equal(artifact.log.version, "1.2");
  assert.equal(artifact.log.entries[0].response.status, 204);
  assert.equal(artifact.log._mcp.bodiesIncluded, false);
  assert.equal(artifactText.includes("har-request"), false);
  assert.equal(artifactText.includes("secret=value"), false);

  now += 10 * 60 * 1_000 + 1;
  await assert.rejects(
    handlers.read(request("network.read", { limit: 50, maxBytes: 64 * 1024 }), signal()),
    (error) => error.code === "CAPABILITY_UNAVAILABLE",
  );
});

test("network capture fails closed on permissions, stale targets, and invalid bounds", async () => {
  const missingDebug = createHarness({ debuggerGranted: false });
  const debugHandlers = createNetworkHandlers(missingDebug.chromeAPI, {
    cdpSessions: missingDebug.sessions,
  });
  await assert.rejects(
    debugHandlers.start(request("network.start", { maxEntries: 100 }), signal()),
    (error) => error.code === "PERMISSION_REQUIRED",
  );
  assert.equal(missingDebug.acquireCount, 0);

  const missingSite = createHarness({ siteGranted: false });
  const siteHandlers = createNetworkHandlers(missingSite.chromeAPI, {
    cdpSessions: missingSite.sessions,
  });
  await assert.rejects(
    siteHandlers.start(request("network.start", { maxEntries: 100 }), signal()),
    (error) => error.code === "PERMISSION_REQUIRED",
  );
  assert.equal(missingSite.acquireCount, 0);

  const stale = createHarness({ documentIds: ["document-1", "document-2"] });
  const staleHandlers = createNetworkHandlers(stale.chromeAPI, { cdpSessions: stale.sessions });
  await assert.rejects(
    staleHandlers.start(request("network.start", { maxEntries: 100 }), signal()),
    (error) => error.code === "STALE_TARGET",
  );
  assert.equal(stale.releaseCount, 1);

  const invalid = createHarness();
  const invalidHandlers = createNetworkHandlers(invalid.chromeAPI, {
    cdpSessions: invalid.sessions,
  });
  for (const call of [
    () => invalidHandlers.start(request("network.start", { maxEntries: 0 }), signal()),
    () =>
      invalidHandlers.read(request("network.read", { limit: 0, maxBytes: 64 * 1024 }), signal()),
    () =>
      invalidHandlers.getBody(
        request("network.getBody", { entryId: "raw-id", direction: "response", maxBytes: 1_024 }),
        signal(),
      ),
    () => invalidHandlers.exportHAR(request("network.exportHAR", { maxBytes: 1 }), signal()),
  ]) {
    await assert.rejects(call(), (error) => error.code === "INVALID_MESSAGE");
  }
  assert.equal(invalid.acquireCount, 0);
});

function emitRequest(harness, options) {
  harness.emit("Network.requestWillBeSent", {
    requestId: options.requestId,
    timestamp: options.timestamp,
    wallTime: options.wallTime,
    type: options.type,
    documentURL: "https://example.com/page",
    initiator: { type: "script", url: "https://example.com/app.js" },
    ...(options.redirectResponse ? { redirectResponse: options.redirectResponse } : {}),
    request: {
      url: options.url,
      method: options.method,
      headers: options.headers || {},
      hasPostData: options.hasPostData === true,
    },
  });
}

function createHarness({ debuggerGranted = true, siteGranted = true, documentIds, results } = {}) {
  const calls = [];
  let options;
  let releaseCount = 0;
  let acquireCount = 0;
  const committedListeners = [];
  const documents = [...(documentIds || ["document-1"])];
  let documentIndex = 0;
  const lease = {
    async sendCommand(method, params) {
      calls.push([method, params]);
      return results?.(method, params) || {};
    },
    async release() {
      releaseCount += 1;
    },
  };
  const sessions = {
    async acquire(debuggee, leaseOptions) {
      assert.equal(Number.isInteger(debuggee.tabId), true);
      acquireCount += 1;
      options = leaseOptions;
      return lease;
    },
  };
  const chromeAPI = {
    tabs: {
      async get(tabId) {
        return { id: tabId, url: "https://example.com/page" };
      },
      async query() {
        return [{ id: 7, url: "https://example.com/page" }];
      },
    },
    permissions: {
      async contains(value) {
        if (value.permissions) return debuggerGranted;
        if (value.origins) return siteGranted;
        return false;
      },
    },
    webNavigation: {
      async getFrame() {
        const documentId = documents[Math.min(documentIndex, documents.length - 1)];
        documentIndex += 1;
        return { documentId };
      },
      onCommitted: {
        addListener(listener) {
          committedListeners.push(listener);
        },
      },
    },
  };
  return {
    calls,
    chromeAPI,
    sessions,
    get options() {
      return options;
    },
    get releaseCount() {
      return releaseCount;
    },
    get acquireCount() {
      return acquireCount;
    },
    emit(method, params) {
      options.onEvent({ method, params });
    },
    commit(details) {
      for (const listener of committedListeners) listener(details);
    },
  };
}

function request(command, params, timeoutMs = 2_000) {
  return { requestId: `${command}-1`, command, target, params, timeoutMs };
}

function signal() {
  return new AbortController().signal;
}
