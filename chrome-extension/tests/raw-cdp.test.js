import assert from "node:assert/strict";
import test from "node:test";

import { createRawCDPHandlers } from "../src/handlers/raw-cdp.js";

const target = { tabId: 7, frameId: 0, documentId: "document-1" };
const limits = {
  maxDepth: 12,
  maxNodes: 2_000,
  maxStringChars: 2_000,
  maxBytes: 512 * 1024,
};

const cases = [
  ["Accessibility.getFullAXTree", { depth: 10 }, { nodes: [] }, "Accessibility"],
  [
    "Accessibility.getPartialAXTree",
    { backendNodeId: 7, fetchRelatives: false },
    { nodes: [] },
    "Accessibility",
  ],
  [
    "Accessibility.queryAXTree",
    { backendNodeId: 7, accessibleName: "Save", role: "button" },
    { nodes: [] },
    "Accessibility",
  ],
  [
    "DOM.describeNode",
    { backendNodeId: 7, depth: 2 },
    { node: { backendNodeId: 7, nodeId: 1, nodeName: "DIV", attributes: [] } },
    "DOM",
  ],
  [
    "DOM.getBoxModel",
    { backendNodeId: 7 },
    { model: { content: [0, 0, 10, 0, 10, 10, 0, 10], width: 10, height: 10 } },
    "DOM",
  ],
  ["Page.getLayoutMetrics", {}, { cssContentSize: { x: 0, y: 0, width: 10, height: 20 } }, "Page"],
  ["Performance.getMetrics", {}, { metrics: [{ name: "Timestamp", value: 1.5 }] }, "Performance"],
];

test("raw CDP executes every reviewed method through one exact managed lease", async () => {
  for (const [method, methodParams, commandResult, domain] of cases) {
    let leaseOptions;
    const calls = [];
    const handlers = createRawCDPHandlers(createChromeAPI(), {
      cdpSessions: {
        async withSession(debuggee, options, operation) {
          assert.deepEqual(debuggee, { tabId: 7 });
          leaseOptions = options;
          return operation({
            async sendCommand(actualMethod, actualParams) {
              calls.push([actualMethod, JSON.parse(JSON.stringify(actualParams))]);
              return commandResult;
            },
          });
        },
      },
    });

    const result = await handlers.sendReadOnly(
      request(method, methodParams),
      new AbortController().signal,
    );

    assert.equal(result.method, method);
    assert.equal(result.tabId, 7);
    assert.equal(result.documentId, "document-1");
    assert.deepEqual(JSON.parse(JSON.stringify(result.result)), commandResult);
    assert.equal(result.truncated, false);
    assert.ok(result.nodeCount >= 2);
    assert.deepEqual(result.warnings, []);
    assert.deepEqual(leaseOptions.domains, [domain]);
    assert.deepEqual(leaseOptions.commands, [method]);
    assert.equal(leaseOptions.events, undefined);
    assert.deepEqual(calls, [[method, methodParams]]);
  }
});

test("raw CDP rejects prohibited and unlisted methods before acquiring a lease", async () => {
  let sessions = 0;
  const handlers = createRawCDPHandlers(createChromeAPI(), {
    cdpSessions: {
      async withSession() {
        sessions += 1;
      },
    },
  });

  for (const method of [
    "Runtime.evaluate",
    "Network.getCookies",
    "Target.getTargets",
    "Page.captureSnapshot",
  ]) {
    await assert.rejects(
      handlers.sendReadOnly(request(method, {}), new AbortController().signal),
      (error) => error.code === "INVALID_COMMAND",
    );
  }
  assert.equal(sessions, 0);
});

test("raw CDP validates method parameters independently from the command router", async () => {
  const handlers = createRawCDPHandlers(createChromeAPI(), {
    cdpSessions: {
      async withSession() {
        assert.fail("invalid parameters must not acquire a CDP lease");
      },
    },
  });
  const invalid = [
    request("DOM.describeNode", {}),
    request("DOM.describeNode", { backendNodeId: 7, objectId: "forbidden" }),
    request("Accessibility.getFullAXTree", { depth: 51 }),
    request("Accessibility.getPartialAXTree", { backendNodeId: 7, fetchRelatives: "yes" }),
    request("Accessibility.queryAXTree", { backendNodeId: 0 }),
    request("Page.getLayoutMetrics", { frameId: "root" }),
    {
      ...request("Performance.getMetrics", {}),
      params: { ...limits, method: "Performance.getMetrics", params: {}, maxDepth: 0 },
    },
  ];
  for (const value of invalid) {
    await assert.rejects(
      handlers.sendReadOnly(value, new AbortController().signal),
      (error) => error.code === "INVALID_MESSAGE",
    );
  }
});

test("raw CDP requires Debug, site access, and a stable root document", async () => {
  let sessions = 0;
  const cdpSessions = {
    async withSession(_debuggee, _options, operation) {
      sessions += 1;
      return operation({
        async sendCommand() {
          return { metrics: [] };
        },
      });
    },
  };
  const missingDebug = createRawCDPHandlers(createChromeAPI({ debuggerGranted: false }), {
    cdpSessions,
  });
  await assert.rejects(
    missingDebug.sendReadOnly(request("Performance.getMetrics", {}), new AbortController().signal),
    (error) => error.code === "PERMISSION_REQUIRED",
  );

  const missingSite = createRawCDPHandlers(createChromeAPI({ siteGranted: false }), {
    cdpSessions,
  });
  await assert.rejects(
    missingSite.sendReadOnly(request("Performance.getMetrics", {}), new AbortController().signal),
    (error) => error.code === "PERMISSION_REQUIRED",
  );

  const stale = createRawCDPHandlers(
    createChromeAPI({ documentIds: ["document-1", "document-1", "document-2"] }),
    { cdpSessions },
  );
  await assert.rejects(
    stale.sendReadOnly(request("Performance.getMetrics", {}), new AbortController().signal),
    (error) => error.code === "STALE_TARGET",
  );
  assert.equal(sessions, 1);
});

test("raw CDP bounds results, rejects handles, and redacts flat DOM attributes", async () => {
  const sensitive = createRawCDPHandlers(createChromeAPI(), {
    cdpSessions: sessionReturning({
      node: {
        backendNodeId: 7,
        nodeId: 1,
        nodeName: "INPUT",
        attributes: [
          "type",
          "password",
          "value",
          "swordfish",
          "data-token",
          "raw-token",
          "title",
          "Bearer abc.def",
        ],
      },
    }),
  });
  const result = await sensitive.sendReadOnly(
    request("DOM.describeNode", { backendNodeId: 7 }),
    new AbortController().signal,
  );
  assert.deepEqual(result.result.node.attributes, [
    "type",
    "password",
    "value",
    "[REDACTED]",
    "data-token",
    "[REDACTED]",
    "title",
    "Bearer [REDACTED]",
  ]);
  assert.equal(
    result.warnings.some((warning) => warning.includes("redacted")),
    true,
  );
  assert.equal(JSON.stringify(result).includes("swordfish"), false);
  assert.equal(JSON.stringify(result).includes("raw-token"), false);

  const handle = createRawCDPHandlers(createChromeAPI(), {
    cdpSessions: sessionReturning({ node: { backendNodeId: 7, objectId: "remote-handle" } }),
  });
  await assert.rejects(
    handle.sendReadOnly(
      request("DOM.describeNode", { backendNodeId: 7 }),
      new AbortController().signal,
    ),
    (error) => error.code === "INVALID_MESSAGE",
  );

  const oversized = createRawCDPHandlers(createChromeAPI(), {
    cdpSessions: sessionReturning({
      nodes: Array.from({ length: 20 }, (_, index) => ({
        nodeId: String(index),
        name: { value: "x".repeat(10_000) },
      })),
    }),
  });
  await assert.rejects(
    oversized.sendReadOnly(
      {
        ...request("Accessibility.getFullAXTree", { depth: 10 }),
        params: {
          ...limits,
          method: "Accessibility.getFullAXTree",
          params: { depth: 10 },
          maxStringChars: 10_000,
          maxBytes: 64 * 1024,
        },
      },
      new AbortController().signal,
    ),
    (error) => error.code === "PAYLOAD_TOO_LARGE",
  );
});

function request(method, params) {
  return {
    requestId: `raw-${method}`,
    command: "cdp.sendReadOnly",
    target,
    params: { method, params, ...limits },
    timeoutMs: 1_000,
  };
}

function sessionReturning(value) {
  return {
    async withSession(_debuggee, _options, operation) {
      return operation({
        async sendCommand() {
          return value;
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
    },
  };
}
