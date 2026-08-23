import assert from "node:assert/strict";
import test from "node:test";

import {
  CONTENT_BRIDGE_VERSION,
  ContentScriptBridge,
} from "../src/content-bridge.js";
import { ErrorCode } from "../src/protocol.js";

test("bridge performs readiness handshake and document-scoped command messaging", async () => {
  const messages = [];
  const bridge = new ContentScriptBridge({
    tabs: {
      sendMessage: async (...args) => {
        messages.push(args);
        if (args[1].type === "MCP_BROWSER_BRIDGE_READY") {
          return { ready: true, bridgeVersion: CONTENT_BRIDGE_VERSION };
        }
        return { success: true, result: { html: "<main></main>" } };
      },
    },
    scripting: { executeScript: async () => assert.fail("unexpected injection") },
  });

  const result = await bridge.execute({
    tabId: 5,
    frameId: 2,
    documentId: "document-1",
    command: "page.getHTML",
    params: {},
    signal: new AbortController().signal,
  });
  assert.deepEqual(result, { html: "<main></main>" });
  assert.equal(messages.length, 2);
  assert.deepEqual(messages[1][2], { frameId: 2, documentId: "document-1" });
  assert.equal(messages[1][1].bridgeVersion, CONTENT_BRIDGE_VERSION);
  assert.equal(messages[1][1].documentId, "document-1");
});

test("bridge injects on demand when navigation removed the content script", async () => {
  let readyAttempts = 0;
  const injections = [];
  const bridge = new ContentScriptBridge({
    tabs: {
      sendMessage: async (_tabId, message) => {
        if (message.type === "MCP_BROWSER_BRIDGE_READY") {
          readyAttempts += 1;
          if (readyAttempts === 1) {
            throw new Error("Receiving end does not exist");
          }
          return { ready: true, bridgeVersion: CONTENT_BRIDGE_VERSION };
        }
        return { success: true, result: { clicked: true } };
      },
    },
    scripting: {
      executeScript: async (injection) => injections.push(injection),
    },
  });

  const result = await bridge.execute({
    tabId: 9,
    frameId: 0,
    command: "page.click",
    params: { selector: "button" },
    signal: new AbortController().signal,
  });
  assert.deepEqual(result, { clicked: true });
  assert.equal(readyAttempts, 2);
  assert.deepEqual(injections, [{
    target: { tabId: 9, frameIds: [0] },
    files: ["src/locator-engine.js", "src/content.js"],
  }]);
});

test("bridge rejects incompatible and untrusted responses", async (t) => {
  await t.test("incompatible readiness response", async () => {
    const bridge = bridgeWithResponses([{ ready: true, bridgeVersion: "2.0" }]);
    await assert.rejects(
      bridge.execute(command()),
      (error) => error.code === ErrorCode.CAPABILITY_UNAVAILABLE,
    );
  });

  await t.test("malformed command response", async () => {
    const bridge = bridgeWithResponses([
      { ready: true, bridgeVersion: CONTENT_BRIDGE_VERSION },
      { html: "unwrapped" },
    ]);
    await assert.rejects(
      bridge.execute(command()),
      (error) => error.code === ErrorCode.INTERNAL_ERROR,
    );
  });

  await t.test("unknown content error code", async () => {
    const bridge = bridgeWithResponses([
      { ready: true, bridgeVersion: CONTENT_BRIDGE_VERSION },
      { success: false, error: { code: "PAGE_OWNED_CODE", message: "unsafe" } },
    ]);
    await assert.rejects(
      bridge.execute(command()),
      (error) => error.code === ErrorCode.INTERNAL_ERROR,
    );
  });
});

test("bridge cancellation stops delivery before the page command", async () => {
  let resolveReady;
  let commands = 0;
  const bridge = new ContentScriptBridge({
    tabs: {
      sendMessage: async (_tabId, message) => {
        if (message.type === "MCP_BROWSER_BRIDGE_READY") {
          return new Promise((resolve) => { resolveReady = resolve; });
        }
        commands += 1;
        return { success: true, result: {} };
      },
    },
    scripting: { executeScript: async () => undefined },
  });
  const controller = new AbortController();
  const execution = bridge.execute({ ...command(), signal: controller.signal });
  await waitFor(() => resolveReady !== undefined);
  controller.abort();

  await assert.rejects(execution, (error) => error.code === ErrorCode.CANCELLED);
  resolveReady({ ready: true, bridgeVersion: CONTENT_BRIDGE_VERSION });
  assert.equal(commands, 0);
});

function bridgeWithResponses(responses) {
  return new ContentScriptBridge({
    tabs: { sendMessage: async () => responses.shift() },
    scripting: { executeScript: async () => undefined },
  });
}

function command() {
  return {
    tabId: 1,
    frameId: 0,
    command: "page.getHTML",
    params: {},
    signal: new AbortController().signal,
  };
}

async function waitFor(predicate) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    if (predicate()) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  throw new Error("Condition was not reached");
}
