import assert from "node:assert/strict";
import test from "node:test";

import { CONTENT_BRIDGE_VERSION } from "../src/content-bridge.js";
import { createAccessibilityHandlers } from "../src/handlers/accessibility.js";
import { ErrorCode } from "../src/protocol.js";

test("accessibility handler returns filtered frame-linked nodes and unambiguous references", async () => {
  const cdpCalls = [];
  const leaseOptions = [];
  const chromeAPI = createChromeAPI({
    sendMessage: async (_tabId, message) => {
      if (message.type === "MCP_BROWSER_BRIDGE_READY") {
        return { ready: true, bridgeVersion: CONTENT_BRIDGE_VERSION };
      }
      assert.equal(message.command, "page.query");
      assert.deepEqual(message.params.locator, { role: "button", name: "Save", strict: true });
      return {
        success: true,
        result: {
          matchCount: 1,
          elements: [
            {
              role: "button",
              name: "Save",
              reference: { elementId: "element-save", documentId: "document-1" },
            },
          ],
        },
      };
    },
  });
  const cdpSessions = {
    async withSession(target, options, operation) {
      assert.deepEqual(target, { tabId: 42 });
      leaseOptions.push(options);
      return operation({
        async sendCommand(method, params, options) {
          cdpCalls.push({ method, params, options });
          if (method === "Page.getFrameTree") {
            return {
              frameTree: {
                frame: {
                  id: "frame-root",
                  url: "https://user:password@example.com/account?token=secret#section",
                },
                childFrames: [
                  {
                    frame: { id: "frame-child", url: "https://child.example/frame?q=secret" },
                  },
                ],
              },
            };
          }
          assert.equal(method, "Accessibility.getFullAXTree");
          return {
            nodes: [
              {
                nodeId: "root",
                ignored: false,
                role: { value: "RootWebArea" },
                name: { value: "Example" },
                frameId: "frame-root",
                childIds: ["save", "ignored"],
              },
              {
                nodeId: "save",
                parentId: "root",
                ignored: false,
                role: { value: "button" },
                name: { value: "Save" },
                description: { value: "Save changes" },
                backendDOMNodeId: 17,
                properties: [{ name: "focusable", value: { type: "boolean", value: true } }],
              },
              {
                nodeId: "ignored",
                parentId: "root",
                ignored: true,
                role: { value: "button" },
                name: { value: "Hidden" },
              },
            ],
          };
        },
      });
    },
  };
  const handlers = createAccessibilityHandlers(chromeAPI, { cdpSessions });
  const signal = new AbortController().signal;
  const result = await handlers.getTree(fullRequest(), signal);

  assert.equal(result.mode, "full");
  assert.equal(result.tabId, 42);
  assert.equal(result.documentId, "document-1");
  assert.equal(result.rootFrameId, "frame-root");
  assert.equal(result.frameCount, 2);
  assert.equal(result.frames[0].url, "https://example.com/account");
  assert.equal(result.frames[1].parentFrameId, "frame-root");
  assert.equal(result.totalNodeCount, 3);
  assert.equal(result.matchingNodeCount, 1);
  assert.equal(result.returnedNodeCount, 1);
  assert.deepEqual(result.nodes[0].locator, { role: "button", name: "Save", strict: true });
  assert.deepEqual(result.nodes[0].reference, {
    elementId: "element-save",
    documentId: "document-1",
  });
  assert.equal(result.nodes[0].frameId, "frame-root");
  assert.equal(result.nodes[0].backendNodeId, 17);
  assert.deepEqual(leaseOptions[0].domains, ["Accessibility", "Page"]);
  assert.deepEqual(leaseOptions[0].commands, ["Accessibility.getFullAXTree", "Page.getFrameTree"]);
  assert.deepEqual(
    cdpCalls.map(({ method, params }) => [method, params]),
    [
      ["Page.getFrameTree", {}],
      ["Accessibility.getFullAXTree", { depth: 20 }],
    ],
  );
});

test("accessibility handler uses the exact partial-tree command and redacts protected values", async () => {
  const calls = [];
  const chromeAPI = createChromeAPI();
  const handlers = createAccessibilityHandlers(chromeAPI, {
    cdpSessions: {
      async withSession(_target, options, operation) {
        assert.deepEqual(options.commands, ["Accessibility.getPartialAXTree", "Page.getFrameTree"]);
        return operation({
          async sendCommand(method, params) {
            calls.push([method, params]);
            if (method === "Page.getFrameTree") {
              return { frameTree: { frame: { id: "frame-root", url: "https://example.com/" } } };
            }
            return {
              nodes: [
                {
                  nodeId: "password",
                  ignored: false,
                  role: { value: "textbox" },
                  name: { value: "Password" },
                  value: { value: "top-secret" },
                  backendDOMNodeId: 99,
                  properties: [{ name: "protected", value: { type: "boolean", value: true } }],
                },
              ],
            };
          },
        });
      },
    },
  });
  const result = await handlers.getTree(
    {
      requestId: "partial",
      target: { tabId: 42, frameId: 0, documentId: "document-1" },
      params: {
        ...commonParams(),
        mode: "partial",
        backendNodeId: 99,
        fetchRelatives: false,
        includeElementReferences: false,
        maxElementReferences: 0,
      },
    },
    new AbortController().signal,
  );

  assert.equal(result.nodes[0].value, "[REDACTED]");
  assert.deepEqual(calls, [
    ["Page.getFrameTree", {}],
    ["Accessibility.getPartialAXTree", { backendNodeId: 99, fetchRelatives: false }],
  ]);
});

test("accessibility handler rejects missing Debug permission before attaching", async () => {
  let attached = false;
  const chromeAPI = createChromeAPI({ debuggerGranted: false });
  const handlers = createAccessibilityHandlers(chromeAPI, {
    cdpSessions: {
      async withSession() {
        attached = true;
      },
    },
  });
  await assert.rejects(
    handlers.getTree(fullRequest(), new AbortController().signal),
    (error) => error.code === ErrorCode.PERMISSION_REQUIRED,
  );
  assert.equal(attached, false);
});

test("accessibility handler deterministically trims normalized nodes to maxBytes", async () => {
  const chromeAPI = createChromeAPI();
  const nodes = Array.from({ length: 400 }, (_, index) => ({
    nodeId: `node-${index}`,
    ignored: false,
    role: { value: "generic" },
    name: { value: `Node ${index} ${"x".repeat(500)}` },
    frameId: "frame-root",
  }));
  const handlers = createAccessibilityHandlers(chromeAPI, {
    cdpSessions: {
      async withSession(_target, _options, operation) {
        return operation({
          async sendCommand(method) {
            return method === "Page.getFrameTree"
              ? { frameTree: { frame: { id: "frame-root", url: "https://example.com/" } } }
              : { nodes };
          },
        });
      },
    },
  });
  const request = fullRequest();
  request.params.roles = [];
  request.params.includeElementReferences = false;
  request.params.maxElementReferences = 0;
  request.params.maxBytes = 64 * 1_024;
  const result = await handlers.getTree(request, new AbortController().signal);

  assert.equal(result.truncated, true);
  assert.ok(result.returnedNodeCount < nodes.length);
  assert.ok(result.warnings.includes("Accessibility output was truncated by maxBytes"));
  assert.ok(new TextEncoder().encode(JSON.stringify(result)).byteLength <= request.params.maxBytes);
});

function fullRequest() {
  return {
    requestId: "full",
    target: { tabId: 42, frameId: 0, documentId: "document-1" },
    params: {
      ...commonParams(),
      mode: "full",
      roles: ["button"],
      maxDepth: 20,
    },
  };
}

function commonParams() {
  return {
    roles: [],
    nameContains: "",
    includeIgnored: false,
    includeLocators: true,
    includeElementReferences: true,
    maxNodes: 1_000,
    maxProperties: 20,
    maxValueChars: 500,
    maxElementReferences: 50,
    maxBytes: 1_000_000,
  };
}

function createChromeAPI({ debuggerGranted = true, sendMessage } = {}) {
  return {
    tabs: {
      get: async (tabId) => ({ id: tabId, url: "https://example.com/account" }),
      query: async () => [],
      sendMessage:
        sendMessage ||
        (async () => {
          throw new Error("content bridge should not be used");
        }),
    },
    permissions: {
      contains: async (request) =>
        request.permissions?.includes("debugger") ? debuggerGranted : true,
    },
    webNavigation: {
      getFrame: async () => ({ frameId: 0, documentId: "document-1" }),
    },
    scripting: {
      executeScript: async () => {
        throw new Error("content bridge injection should not be required");
      },
    },
  };
}
