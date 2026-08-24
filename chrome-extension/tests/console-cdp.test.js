import assert from "node:assert/strict";
import test from "node:test";

import { createConsoleHandlers } from "../src/handlers/console.js";

test("console capture enriches root-frame entries through one bounded managed CDP lease", async () => {
  const ingested = [];
  const commandCalls = [];
  let navigationListener;
  let leaseOptions;
  let released = 0;
  const chromeAPI = createChromeAPI({
    sendMessage: async (_tabId, message) => {
      if (message.type === "MCP_BROWSER_CONSOLE_READY") {
        return { ready: true, bridgeVersion: "1.0" };
      }
      if (message.type === "MCP_BROWSER_CONSOLE_CDP_EVENT") {
        ingested.push(message);
        return { accepted: true };
      }
      return {
        success: true,
        result: {
          active: message.command !== "console.stop",
          warnings: [],
        },
      };
    },
    onCommitted: (listener) => {
      navigationListener = listener;
    },
  });
  const lease = {
    async sendCommand(method, params) {
      commandCalls.push([method, params]);
      return method === "Page.getFrameTree" ? { frameTree: { frame: { id: "frame-root" } } } : {};
    },
    frameContexts(frameId) {
      assert.equal(frameId, "frame-root");
      return [{ contextId: 11, frameId: "frame-root", sessionId: "" }];
    },
    async release() {
      released += 1;
    },
  };
  const handlers = createConsoleHandlers(chromeAPI, {
    cdpSessions: {
      async acquire(target, options) {
        assert.deepEqual(target, { tabId: 7 });
        leaseOptions = options;
        return lease;
      },
    },
  });
  const target = { tabId: 7, frameId: 0, documentId: "document-1" };
  const signal = new AbortController().signal;
  const started = await handlers.start(
    {
      requestId: "console-start",
      command: "console.start",
      target,
      params: { bufferSize: 250, captureConsole: true, captureErrors: true },
    },
    signal,
  );

  assert.equal(started.active, true);
  assert.equal(started.cdpEnriched, true);
  assert.deepEqual(started.backends, ["bridge", "cdp"]);
  assert.deepEqual(leaseOptions.domains, ["Runtime", "Log", "Network", "Page"]);
  assert.deepEqual(leaseOptions.commands, [
    "Page.getFrameTree",
    "Runtime.enable",
    "Log.enable",
    "Network.enable",
  ]);
  assert.deepEqual(leaseOptions.events, [
    "Runtime.consoleAPICalled",
    "Runtime.exceptionThrown",
    "Log.entryAdded",
    "Network.loadingFailed",
  ]);
  assert.deepEqual(commandCalls, [
    ["Page.getFrameTree", {}],
    ["Runtime.enable", {}],
    ["Log.enable", {}],
    ["Network.enable", {}],
  ]);

  await leaseOptions.onEvent({
    method: "Runtime.consoleAPICalled",
    params: {
      type: "warning",
      executionContextId: 11,
      timestamp: 1_787_520_000,
      args: [
        { type: "string", value: "warning" },
        {
          type: "object",
          description: "Object",
          preview: { properties: [{ name: "status", value: "failed" }] },
        },
      ],
      stackTrace: {
        callFrames: [
          {
            functionName: "run",
            url: "https://example.com/app.js?token=secret",
            lineNumber: 4,
            columnNumber: 2,
          },
        ],
      },
    },
  });
  await leaseOptions.onEvent({
    method: "Runtime.consoleAPICalled",
    params: { type: "log", executionContextId: 99, args: [] },
  });
  await leaseOptions.onEvent({
    method: "Runtime.exceptionThrown",
    params: {
      timestamp: 1_787_520_001,
      exceptionDetails: {
        executionContextId: 11,
        text: "Uncaught TypeError",
        url: "https://example.com/app.js",
        lineNumber: 8,
        columnNumber: 3,
        exception: { type: "object", subtype: "error", description: "TypeError: failed" },
      },
    },
  });
  await leaseOptions.onEvent({
    method: "Log.entryAdded",
    params: {
      entry: {
        source: "security",
        level: "error",
        text: "Blocked insecure content",
        timestamp: 1_787_520_002,
      },
    },
  });
  await leaseOptions.onEvent({
    method: "Network.loadingFailed",
    params: {
      requestId: "request-1",
      type: "Script",
      errorText: "net::ERR_FAILED",
      canceled: false,
    },
  });

  assert.equal(ingested.length, 4);
  assert.equal(ingested[0].entry.backend, "cdp");
  assert.equal(ingested[0].entry.scope, "frame");
  assert.equal(ingested[0].entry.level, "warn");
  assert.equal(ingested[0].entry.args[1].preview.status, "failed");
  assert.match(ingested[0].entry.stack, /run/);
  assert.equal(ingested[1].entry.kind, "exception");
  assert.equal(ingested[1].entry.scope, "frame");
  assert.equal(ingested[2].entry.method, "security");
  assert.equal(ingested[2].entry.scope, "tab");
  assert.equal(ingested[3].entry.kind, "resourceError");
  assert.equal(ingested[3].entry.scope, "tab");

  navigationListener({ tabId: 7, frameId: 1, documentId: "child-document" });
  await Promise.resolve();
  assert.equal(released, 0);
  navigationListener({ tabId: 7, frameId: 0, documentId: "document-2" });
  await Promise.resolve();
  assert.equal(released, 1);

  const stopped = await handlers.stop(
    { requestId: "console-stop", command: "console.stop", target, params: {} },
    signal,
  );
  assert.equal(stopped.cdpEnriched, false);
  assert.equal(released, 1);
});

test("console capture remains available when optional CDP enrichment cannot attach", async () => {
  const chromeAPI = createChromeAPI();
  const handlers = createConsoleHandlers(chromeAPI, {
    cdpSessions: {
      async acquire() {
        throw new Error("Another debugger is already attached");
      },
    },
  });
  const result = await handlers.start(
    {
      requestId: "console-start",
      command: "console.start",
      target: { tabId: 7, frameId: 0, documentId: "document-1" },
      params: {},
    },
    new AbortController().signal,
  );

  assert.equal(result.active, true);
  assert.equal(result.cdpEnriched, false);
  assert.deepEqual(result.backends, ["bridge"]);
  assert.ok(
    result.warnings.includes(
      "CDP console enrichment was unavailable; bridge capture remains active",
    ),
  );
});

function createChromeAPI({ sendMessage, onCommitted } = {}) {
  return {
    tabs: {
      get: async (tabId) => ({ id: tabId, url: "https://example.com/" }),
      query: async () => [],
      sendMessage:
        sendMessage ||
        (async (_tabId, message) => {
          if (message.type === "MCP_BROWSER_CONSOLE_READY") {
            return { ready: true, bridgeVersion: "1.0" };
          }
          return {
            success: true,
            result: { active: message.command === "console.start", warnings: [] },
          };
        }),
    },
    permissions: { contains: async () => true },
    scripting: { executeScript: async () => undefined },
    webNavigation: {
      getFrame: async () => ({ documentId: "document-1", url: "https://example.com/" }),
      onCommitted: { addListener: onCommitted || (() => undefined) },
    },
  };
}
