import assert from "node:assert/strict";
import test from "node:test";

import { createBrowserHandlers } from "../src/handlers/browser.js";
import { createPageHandlers } from "../src/handlers/page.js";
import { createSessionHandlers } from "../src/handlers/sessions.js";
import { createTabGroupHandlers } from "../src/handlers/tab-groups.js";
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

test("tab handlers keep every operation scoped to the addressed tab", async () => {
  const calls = [];
  const tab = {
    id: 42,
    windowId: 3,
    index: 1,
    active: false,
    pinned: false,
    mutedInfo: { muted: false },
    url: "https://example.com/",
  };
  const chromeAPI = {
    tabs: {
      query: async () => { calls.push(["query"]); return []; },
      get: async (id) => { calls.push(["get", id]); return { ...tab, id }; },
      create: async (params) => { calls.push(["create", params]); return { ...tab, id: 50 }; },
      update: async (id, params) => { calls.push(["update", id, params]); return { ...tab, id, ...params }; },
      reload: async (id, params) => { calls.push(["reload", id, params]); },
      goBack: async (id) => { calls.push(["back", id]); },
      goForward: async (id) => { calls.push(["forward", id]); },
      move: async (id, params) => { calls.push(["move", id, params]); return { ...tab, id, ...params }; },
      duplicate: async (id) => { calls.push(["duplicate", id]); return { ...tab, id: 51 }; },
      remove: async (id) => { calls.push(["close", id]); },
      getZoom: async (id) => { calls.push(["getZoom", id]); return 1.25; },
      setZoom: async (id, factor) => { calls.push(["setZoom", id, factor]); },
    },
    permissions: { contains: async () => true },
    scripting: {
      executeScript: async (params) => { calls.push(["stop", params.target.tabId]); },
    },
  };
  const handlers = createTabHandlers(chromeAPI);
  const targeted = (params = {}) => ({ target: { tabId: 42 }, params });

  assert.equal((await handlers.get(targeted())).tab.id, 42);
  assert.equal((await handlers.create({ params: { url: "https://example.org" } })).tab.id, 50);
  await handlers.activate(targeted());
  await handlers.navigate(targeted({ url: "https://example.org" }));
  await handlers.reload(targeted({ bypassCache: true }));
  await handlers.stop(targeted());
  await handlers.back(targeted());
  await handlers.forward(targeted());
  await handlers.move(targeted({ windowId: 4, index: -1 }));
  assert.equal((await handlers.duplicate(targeted())).tab.id, 51);
  assert.deepEqual(await handlers.close(targeted()), { tabId: 42, closed: true });
  await handlers.pin(targeted({ pinned: true }));
  await handlers.mute(targeted({ muted: true }));
  assert.deepEqual(await handlers.getZoom(targeted()), { tabId: 42, factor: 1.25 });
  assert.deepEqual(await handlers.setZoom(targeted({ factor: 1.5 })), {
    tabId: 42,
    factor: 1.5,
  });

  assert.equal(calls.some(([operation]) => operation === "query"), false);
  for (const operation of [
    "get", "update", "reload", "stop", "back", "forward", "move", "duplicate",
    "close", "getZoom", "setZoom",
  ]) {
    assert.equal(
      calls.some((call) => call[0] === operation && call[1] === 42),
      true,
      `${operation} did not use tab 42`,
    );
  }
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

test("tab group and session handlers normalize Chrome API results", async () => {
  const calls = [];
  const chromeAPI = {
    tabs: {
      group: async (options) => { calls.push(["group", options]); return 8; },
      ungroup: async (tabIds) => { calls.push(["ungroup", tabIds]); },
    },
    tabGroups: {
      update: async (groupId, update) => {
        calls.push(["update", groupId, update]);
        return { id: groupId, windowId: 4, title: update.title, color: "blue", collapsed: false };
      },
    },
    sessions: {
      getRecentlyClosed: async (filter) => {
        calls.push(["recentlyClosed", filter]);
        return [{
          lastModified: 1_700_000_000,
          tab: { sessionId: "tab-session", id: 7, windowId: 4, title: "Example" },
        }];
      },
      restore: async (sessionId) => {
        calls.push(["restore", sessionId]);
        return {
          lastModified: 1_700_000_001,
          window: {
            sessionId: "window-session",
            id: 5,
            state: "normal",
            tabs: [{ sessionId: "restored-tab", id: 9, windowId: 5 }],
          },
        };
      },
    },
  };
  const groups = createTabGroupHandlers(chromeAPI);
  const sessions = createSessionHandlers(chromeAPI);

  assert.deepEqual(
    await groups.group({ params: { tabIds: [2, 3], windowId: 4 } }),
    { groupId: 8, tabIds: [2, 3] },
  );
  assert.deepEqual(
    await groups.ungroup({ params: { tabIds: [2, 3] } }),
    { tabIds: [2, 3], ungrouped: true },
  );
  assert.equal(
    (await groups.update({ params: { groupId: 8, title: "Research" } })).group.id,
    8,
  );
  const recent = await sessions.recentlyClosed({ params: { maxResults: 5 } });
  assert.equal(recent.totalCount, 1);
  assert.equal(recent.sessions[0].tab.sessionId, "tab-session");
  const restored = await sessions.restore({ params: { sessionId: "window-session" } });
  assert.equal(restored.session.window.tabs[0].sessionId, "restored-tab");
  assert.deepEqual(calls, [
    ["group", { tabIds: [2, 3], createProperties: { windowId: 4 } }],
    ["ungroup", [2, 3]],
    ["update", 8, { title: "Research" }],
    ["recentlyClosed", { maxResults: 5 }],
    ["restore", "window-session"],
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
          return { ready: true, bridgeVersion: "1.1" };
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
  assert.equal(sent[1][1].documentId, "document-1");
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
