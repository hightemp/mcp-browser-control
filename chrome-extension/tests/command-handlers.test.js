import assert from "node:assert/strict";
import test from "node:test";

import { createBrowserHandlers } from "../src/handlers/browser.js";
import { createPageHandlers } from "../src/handlers/page.js";
import { createTabHandlers } from "../src/handlers/tabs.js";
import { createWindowHandlers } from "../src/handlers/windows.js";
import { ErrorCode } from "../src/protocol.js";

test("browser and tab handlers return stable domain results", async () => {
  const browser = createBrowserHandlers(() => new Date("2026-01-02T03:04:05.000Z"));
  assert.deepEqual(browser.ping(), { pong: true, time: "2026-01-02T03:04:05.000Z" });

  const tabs = createTabHandlers({
    tabs: {
      query: async () => [{
        id: 7,
        windowId: 3,
        index: 1,
        active: true,
        pinned: false,
        mutedInfo: { muted: true },
        status: "complete",
        title: "Example",
        url: "https://example.com/",
        favIconUrl: "https://example.com/favicon.ico",
        incognito: false,
      }],
    },
  });
  const result = await tabs.list();
  assert.equal(result.totalCount, 1);
  assert.equal(result.tabs[0].id, 7);
  assert.equal(result.tabs[0].muted, true);
});

test("window handlers support list, get, create, update, focus, and close", async () => {
  const calls = [];
  const window = {
    id: 4,
    focused: true,
    top: 10,
    left: 20,
    width: 900,
    height: 700,
    incognito: false,
    type: "normal",
    state: "maximized",
    alwaysOnTop: false,
  };
  const handlers = createWindowHandlers({
    windows: {
      getAll: async (options) => { calls.push(["list", options]); return [window]; },
      get: async (id, options) => { calls.push(["get", id, options]); return window; },
      create: async (params) => { calls.push(["create", params]); return window; },
      update: async (id, params) => { calls.push(["update", id, params]); return window; },
      remove: async (id) => { calls.push(["close", id]); },
    },
  });

  assert.equal((await handlers.list()).totalCount, 1);
  assert.equal((await handlers.get({ target: { windowId: 4 } })).window.id, 4);
  await handlers.create({
    params: { urls: ["https://example.com"], type: "popup", focused: false },
  });
  await handlers.update({ target: { windowId: 4 }, params: { state: "minimized" } });
  await handlers.focus({ target: { windowId: 4 } });
  assert.deepEqual(await handlers.close({ target: { windowId: 4 } }), {
    windowId: 4,
    closed: true,
  });
  assert.deepEqual(calls, [
    ["list", { populate: false, windowTypes: ["normal", "popup"] }],
    ["get", 4, { populate: false }],
    ["create", { url: ["https://example.com"], type: "popup", focused: false }],
    ["update", 4, { state: "minimized" }],
    ["update", 4, { focused: true }],
    ["close", 4],
  ]);
});

test("page handlers preserve addressing and structured content errors", async () => {
  const sent = [];
  const chromeAPI = {
    tabs: {
      get: async () => ({ id: 7, url: "https://example.com/page" }),
      sendMessage: async (...args) => {
        sent.push(args);
        if (args[1].type === "MCP_BROWSER_BRIDGE_READY") {
          return { ready: true, bridgeVersion: "1.0" };
        }
        return {
          success: false,
          error: {
            code: ErrorCode.ELEMENT_NOT_FOUND,
            message: "No matching element",
            details: { matches: 0 },
          },
        };
      },
    },
    permissions: { contains: async () => true },
    scripting: { executeScript: async () => undefined },
    webNavigation: { getFrame: async () => ({ documentId: "document-1" }) },
  };
  const page = createPageHandlers(chromeAPI);
  const request = {
    command: "page.click",
    target: { tabId: 7, frameId: 2, documentId: "document-1" },
    params: { selector: "button" },
  };

  await assert.rejects(
    page.click(request, new AbortController().signal),
    (error) => error.code === ErrorCode.ELEMENT_NOT_FOUND
      && error.details.matches === 0,
  );
  assert.equal(sent.length, 2);
  assert.equal(sent[1][0], 7);
  assert.deepEqual(sent[1][2], { frameId: 2, documentId: "document-1" });
});

test("page handler applies a command timeout across browser API calls", async () => {
  const chromeAPI = {
    tabs: {
      get: async () => new Promise(() => {}),
    },
  };
  const page = createPageHandlers(chromeAPI);
  const request = {
    command: "page.getHTML",
    target: { tabId: 7 },
    params: {},
    timeoutMs: 5,
  };

  await assert.rejects(
    page.getHTML(request, new AbortController().signal),
    (error) => error.code === ErrorCode.TIMEOUT && error.retryable === true,
  );
});
