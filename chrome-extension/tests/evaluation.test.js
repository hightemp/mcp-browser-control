import assert from "node:assert/strict";
import test from "node:test";

import { createEvaluationHandlers } from "../src/handlers/evaluation.js";

const target = { tabId: 7, frameId: 0, documentId: "document-1" };
const params = {
  expression: "({ title: document.title, values: [1, 2] })",
  awaitPromise: true,
  maxDepth: 6,
  maxNodes: 1_000,
  maxStringChars: 10_000,
  maxBytes: 512 * 1_024,
};

test("isolated evaluation uses one exact ephemeral CDP lease and releases its object group", async () => {
  const calls = [];
  let leaseOptions;
  const handlers = createEvaluationHandlers(createChromeAPI(), {
    cdpSessions: {
      async withSession(debuggee, options, operation) {
        assert.deepEqual(debuggee, { tabId: 7 });
        leaseOptions = options;
        return operation({
          async sendCommand(method, commandParams) {
            calls.push([method, commandParams]);
            if (method === "Page.getFrameTree") {
              return { frameTree: { frame: { id: "root-frame" } } };
            }
            if (method === "Page.createIsolatedWorld") return { executionContextId: 91 };
            if (method === "Runtime.evaluate") {
              return {
                result: {
                  type: "object",
                  className: "Object",
                  value: { title: "Example", values: [1, 2] },
                },
              };
            }
            return {};
          },
        });
      },
    },
  });

  const result = await handlers.evaluate(
    {
      requestId: "evaluation-1",
      command: "runtime.evaluateIsolated",
      target,
      params,
      timeoutMs: 5_000,
    },
    new AbortController().signal,
  );

  assert.equal(result.completed, true);
  assert.equal(result.world, "isolated");
  assert.equal(result.tabId, 7);
  assert.equal(result.documentId, "document-1");
  assert.equal(result.valueType, "object");
  assert.deepEqual(JSON.parse(JSON.stringify(result.value)), {
    title: "Example",
    values: [1, 2],
  });
  assert.equal(result.nodeCount, 5);
  assert.equal(result.truncated, false);
  assert.deepEqual(leaseOptions.domains, ["Page", "Runtime"]);
  assert.deepEqual(leaseOptions.commands, [
    "Page.createIsolatedWorld",
    "Page.getFrameTree",
    "Runtime.evaluate",
    "Runtime.releaseObjectGroup",
  ]);
  assert.equal(leaseOptions.events, undefined);
  assert.deepEqual(calls[1], [
    "Page.createIsolatedWorld",
    {
      frameId: "root-frame",
      worldName: "mcp-browser-control-isolated",
      grantUniveralAccess: false,
      contentSecurityPolicy:
        "default-src 'none'; connect-src 'none'; img-src 'none'; media-src 'none'; object-src 'none'; frame-src 'none'; child-src 'none'; worker-src 'none'; style-src 'none'; base-uri 'none'; form-action 'none'; script-src 'unsafe-eval'",
    },
  ]);
  assert.deepEqual(calls[2], [
    "Runtime.evaluate",
    {
      expression: params.expression,
      objectGroup: "mcp-isolated-evaluation-7-1",
      includeCommandLineAPI: false,
      silent: true,
      contextId: 91,
      returnByValue: true,
      generatePreview: false,
      userGesture: false,
      awaitPromise: true,
      timeout: 5_000,
      disableBreaks: true,
      allowUnsafeEvalBlockedByCSP: false,
    },
  ]);
  assert.deepEqual(calls[3], [
    "Runtime.releaseObjectGroup",
    { objectGroup: "mcp-isolated-evaluation-7-1" },
  ]);
});

test("isolated evaluation truncates JSON-safe values at independent result limits", async () => {
  const handlers = createEvaluationHandlers(createChromeAPI(), {
    cdpSessions: sessionReturning({
      result: {
        type: "object",
        className: "Object",
        value: {
          message: "abcdef",
          nested: { child: true },
          ignoredAfterNodeLimit: "value",
        },
      },
    }),
  });

  const result = await handlers.evaluate(
    {
      requestId: "evaluation-2",
      command: "runtime.evaluateIsolated",
      target,
      params: { ...params, maxDepth: 1, maxNodes: 3, maxStringChars: 3 },
      timeoutMs: 1_000,
    },
    new AbortController().signal,
  );

  assert.equal(result.valueType, "object");
  assert.deepEqual(JSON.parse(JSON.stringify(result.value)), { message: "abc", nested: {} });
  assert.equal(result.nodeCount, 3);
  assert.equal(result.truncated, true);
  assert.equal(result.warnings.length, 1);
});

test("isolated evaluation returns bounded exceptions without exposing remote handles", async () => {
  const handlers = createEvaluationHandlers(createChromeAPI(), {
    cdpSessions: sessionReturning({
      result: { type: "object", objectId: "must-not-escape" },
      exceptionDetails: {
        text: "Uncaught",
        lineNumber: 2,
        columnNumber: 4,
        exception: { description: `Error: ${"x".repeat(3_000)}` },
      },
    }),
  });

  const result = await handlers.evaluate(
    {
      requestId: "evaluation-3",
      command: "runtime.evaluateIsolated",
      target,
      params,
      timeoutMs: 1_000,
    },
    new AbortController().signal,
  );

  assert.equal(result.completed, false);
  assert.equal(result.valueType, "undefined");
  assert.equal(result.nodeCount, 0);
  assert.equal(result.exception.text, "Uncaught");
  assert.equal(result.exception.description.length, 2_000);
  assert.equal(JSON.stringify(result).includes("must-not-escape"), false);

  const unsupported = createEvaluationHandlers(createChromeAPI(), {
    cdpSessions: sessionReturning({
      result: { type: "function", objectId: "function-handle", description: "function secret()" },
    }),
  });
  const unsupportedResult = await unsupported.evaluate(
    {
      requestId: "evaluation-unsupported",
      command: "runtime.evaluateIsolated",
      target,
      params,
      timeoutMs: 1_000,
    },
    new AbortController().signal,
  );
  assert.equal(unsupportedResult.completed, true);
  assert.equal(unsupportedResult.valueType, "unsupported");
  assert.equal(JSON.stringify(unsupportedResult).includes("function-handle"), false);
});

test("isolated evaluation fails before CDP when Debug or site access is unavailable", async () => {
  let sessions = 0;
  const cdpSessions = {
    async withSession() {
      sessions += 1;
    },
  };
  const missingDebug = createEvaluationHandlers(createChromeAPI({ debuggerGranted: false }), {
    cdpSessions,
  });
  await assert.rejects(
    missingDebug.evaluate(
      { command: "runtime.evaluateIsolated", target, params, timeoutMs: 1_000 },
      new AbortController().signal,
    ),
    (error) => error.code === "PERMISSION_REQUIRED",
  );

  const missingSite = createEvaluationHandlers(createChromeAPI({ siteGranted: false }), {
    cdpSessions,
  });
  await assert.rejects(
    missingSite.evaluate(
      { command: "runtime.evaluateIsolated", target, params, timeoutMs: 1_000 },
      new AbortController().signal,
    ),
    (error) => error.code === "PERMISSION_REQUIRED",
  );
  assert.equal(sessions, 0);
});

test("isolated evaluation rechecks document identity and rejects oversized output", async () => {
  const stale = createEvaluationHandlers(
    createChromeAPI({ documentIds: ["document-1", "document-1", "document-1", "document-2"] }),
    { cdpSessions: sessionReturning({ result: { type: "string", value: "ok" } }) },
  );
  await assert.rejects(
    stale.evaluate(
      { command: "runtime.evaluateIsolated", target, params, timeoutMs: 1_000 },
      new AbortController().signal,
    ),
    (error) => error.code === "STALE_TARGET",
  );

  const oversized = createEvaluationHandlers(createChromeAPI(), {
    cdpSessions: sessionReturning({ result: { type: "string", value: "x".repeat(70_000) } }),
  });
  await assert.rejects(
    oversized.evaluate(
      {
        command: "runtime.evaluateIsolated",
        target,
        params: { ...params, maxStringChars: 100_000, maxBytes: 64 * 1_024 },
        timeoutMs: 1_000,
      },
      new AbortController().signal,
    ),
    (error) => error.code === "PAYLOAD_TOO_LARGE",
  );
});

test("isolated evaluation attempts object-group cleanup after Runtime failure", async () => {
  const calls = [];
  const handlers = createEvaluationHandlers(createChromeAPI(), {
    cdpSessions: {
      async withSession(_debuggee, _options, operation) {
        return operation({
          async sendCommand(method) {
            calls.push(method);
            if (method === "Page.getFrameTree") {
              return { frameTree: { frame: { id: "root-frame" } } };
            }
            if (method === "Page.createIsolatedWorld") return { executionContextId: 91 };
            if (method === "Runtime.evaluate") throw new Error("runtime failed");
            return {};
          },
        });
      },
    },
  });

  await assert.rejects(
    handlers.evaluate(
      { command: "runtime.evaluateIsolated", target, params, timeoutMs: 1_000 },
      new AbortController().signal,
    ),
    /runtime failed/,
  );
  assert.deepEqual(calls.slice(-2), ["Runtime.evaluate", "Runtime.releaseObjectGroup"]);
});

function sessionReturning(evaluation) {
  return {
    async withSession(_debuggee, _options, operation) {
      return operation({
        async sendCommand(method) {
          if (method === "Page.getFrameTree") {
            return { frameTree: { frame: { id: "root-frame" } } };
          }
          if (method === "Page.createIsolatedWorld") return { executionContextId: 91 };
          if (method === "Runtime.evaluate") return evaluation;
          return {};
        },
      });
    },
  };
}

function createChromeAPI({ debuggerGranted = true, siteGranted = true, documentIds } = {}) {
  const documents = [...(documentIds || ["document-1"])];
  let documentIndex = 0;
  return {
    tabs: {
      async get(tabId) {
        assert.equal(tabId, 7);
        return { id: 7, url: "https://example.com/page" };
      },
      async query() {
        return [{ id: 7, url: "https://example.com/page" }];
      },
    },
    permissions: {
      async contains(request) {
        if (request.permissions) return debuggerGranted;
        if (request.origins) return siteGranted;
        return false;
      },
    },
    webNavigation: {
      async getFrame() {
        const value = documents[Math.min(documentIndex, documents.length - 1)];
        documentIndex += 1;
        return { documentId: value };
      },
    },
  };
}
