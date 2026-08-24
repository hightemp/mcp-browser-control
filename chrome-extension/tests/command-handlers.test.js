import assert from "node:assert/strict";
import test from "node:test";

import { createBrowserHandlers } from "../src/handlers/browser.js";
import { createConsoleHandlers } from "../src/handlers/console.js";
import { createPageHandlers } from "../src/handlers/page.js";
import { createSessionHandlers } from "../src/handlers/sessions.js";
import { createTabGroupHandlers } from "../src/handlers/tab-groups.js";
import { createTabHandlers } from "../src/handlers/tabs.js";
import { createWindowHandlers } from "../src/handlers/windows.js";
import { ErrorCode } from "../src/protocol.js";

test("browser and tab handlers return stable domain results", async () => {
  const browser = createBrowserHandlers(() => new Date("2026-01-02T03:04:05.000Z"));
  assert.deepEqual(browser.ping(), {
    pong: true,
    time: "2026-01-02T03:04:05.000Z",
  });

  const tabs = createTabHandlers({
    tabs: {
      query: async () => [
        {
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
        },
      ],
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
      query: async () => {
        calls.push(["query"]);
        return [];
      },
      get: async (id) => {
        calls.push(["get", id]);
        return { ...tab, id };
      },
      create: async (params) => {
        calls.push(["create", params]);
        return { ...tab, id: 50 };
      },
      update: async (id, params) => {
        calls.push(["update", id, params]);
        return { ...tab, id, ...params };
      },
      reload: async (id, params) => {
        calls.push(["reload", id, params]);
      },
      goBack: async (id) => {
        calls.push(["back", id]);
      },
      goForward: async (id) => {
        calls.push(["forward", id]);
      },
      move: async (id, params) => {
        calls.push(["move", id, params]);
        return { ...tab, id, ...params };
      },
      duplicate: async (id) => {
        calls.push(["duplicate", id]);
        return { ...tab, id: 51 };
      },
      remove: async (id) => {
        calls.push(["close", id]);
      },
      getZoom: async (id) => {
        calls.push(["getZoom", id]);
        return 1.25;
      },
      setZoom: async (id, factor) => {
        calls.push(["setZoom", id, factor]);
      },
    },
    permissions: { contains: async () => true },
    scripting: {
      executeScript: async (params) => {
        calls.push(["stop", params.target.tabId]);
      },
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
  assert.deepEqual(await handlers.close(targeted()), {
    tabId: 42,
    closed: true,
  });
  await handlers.pin(targeted({ pinned: true }));
  await handlers.mute(targeted({ muted: true }));
  assert.deepEqual(await handlers.getZoom(targeted()), {
    tabId: 42,
    factor: 1.25,
  });
  assert.deepEqual(await handlers.setZoom(targeted({ factor: 1.5 })), {
    tabId: 42,
    factor: 1.5,
  });

  assert.equal(
    calls.some(([operation]) => operation === "query"),
    false,
  );
  for (const operation of [
    "get",
    "update",
    "reload",
    "stop",
    "back",
    "forward",
    "move",
    "duplicate",
    "close",
    "getZoom",
    "setZoom",
  ]) {
    assert.equal(
      calls.some((call) => call[0] === operation && call[1] === 42),
      true,
      `${operation} did not use tab 42`,
    );
  }
});

test("tab handler rejects missing site permission before page access", async () => {
  let scriptExecutions = 0;
  const permissionRequests = [];
  const handlers = createTabHandlers({
    tabs: {
      get: async () => ({ id: 42, url: "http://127.0.0.1:4321/account" }),
    },
    permissions: {
      contains: async (request) => {
        permissionRequests.push(request);
        return false;
      },
    },
    scripting: {
      executeScript: async () => {
        scriptExecutions += 1;
      },
    },
  });

  await assert.rejects(
    handlers.stop({ target: { tabId: 42 }, params: {} }),
    (error) =>
      error.code === ErrorCode.PERMISSION_REQUIRED &&
      error.retryable === false &&
      error.details.origin === "http://127.0.0.1:4321",
  );
  assert.deepEqual(permissionRequests, [{ origins: ["http://127.0.0.1/*"] }]);
  assert.equal(scriptExecutions, 0);
});

test("tab handler rejects restricted URLs before permission or script access", async () => {
  let permissionChecks = 0;
  let scriptExecutions = 0;
  const handlers = createTabHandlers({
    tabs: {
      get: async () => ({ id: 42, url: "chrome://settings/" }),
    },
    permissions: {
      contains: async () => {
        permissionChecks += 1;
        return true;
      },
    },
    scripting: {
      executeScript: async () => {
        scriptExecutions += 1;
      },
    },
  });

  await assert.rejects(
    handlers.stop({ target: { tabId: 42 }, params: {} }),
    (error) => error.code === ErrorCode.RESTRICTED_URL,
  );
  assert.equal(permissionChecks, 0);
  assert.equal(scriptExecutions, 0);
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
      getAll: async (options) => {
        calls.push(["list", options]);
        return [window];
      },
      get: async (id, options) => {
        calls.push(["get", id, options]);
        return window;
      },
      create: async (params) => {
        calls.push(["create", params]);
        return window;
      },
      update: async (id, params) => {
        calls.push(["update", id, params]);
        return window;
      },
      remove: async (id) => {
        calls.push(["close", id]);
      },
    },
  });

  assert.equal((await handlers.list()).totalCount, 1);
  assert.equal((await handlers.get({ target: { windowId: 4 } })).window.id, 4);
  await handlers.create({
    params: { urls: ["https://example.com"], type: "popup", focused: false },
  });
  await handlers.update({
    target: { windowId: 4 },
    params: { state: "minimized" },
  });
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
      group: async (options) => {
        calls.push(["group", options]);
        return 8;
      },
      ungroup: async (tabIds) => {
        calls.push(["ungroup", tabIds]);
      },
    },
    tabGroups: {
      update: async (groupId, update) => {
        calls.push(["update", groupId, update]);
        return {
          id: groupId,
          windowId: 4,
          title: update.title,
          color: "blue",
          collapsed: false,
        };
      },
    },
    sessions: {
      getRecentlyClosed: async (filter) => {
        calls.push(["recentlyClosed", filter]);
        return [
          {
            lastModified: 1_700_000_000,
            tab: {
              sessionId: "tab-session",
              id: 7,
              windowId: 4,
              title: "Example",
            },
          },
        ];
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

  assert.deepEqual(await groups.group({ params: { tabIds: [2, 3], windowId: 4 } }), {
    groupId: 8,
    tabIds: [2, 3],
  });
  assert.deepEqual(await groups.ungroup({ params: { tabIds: [2, 3] } }), {
    tabIds: [2, 3],
    ungrouped: true,
  });
  assert.equal((await groups.update({ params: { groupId: 8, title: "Research" } })).group.id, 8);
  const recent = await sessions.recentlyClosed({ params: { maxResults: 5 } });
  assert.equal(recent.totalCount, 1);
  assert.equal(recent.sessions[0].tab.sessionId, "tab-session");
  const restored = await sessions.restore({
    params: { sessionId: "window-session" },
  });
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
          return { ready: true, bridgeVersion: "1.5" };
        }
        if (args[1].command === "page.info") {
          return { success: true, result: { url: "https://example.com/page" } };
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
    webNavigation: {
      getFrame: async () => ({ documentId: "document-1" }),
      getAllFrames: async () => [
        {
          frameId: 0,
          parentFrameId: -1,
          documentId: "document-1",
          url: "https://example.com/page",
        },
      ],
    },
  };
  const page = createPageHandlers(chromeAPI);
  const request = {
    command: "page.click",
    target: { tabId: 7, frameId: 2, documentId: "document-1" },
    params: { selector: "button" },
  };

  await assert.rejects(
    page.click(request, new AbortController().signal),
    (error) => error.code === ErrorCode.ELEMENT_NOT_FOUND && error.details.matches === 0,
  );
  assert.equal(sent.length, 2);
  assert.equal(sent[1][0], 7);
  assert.equal(sent[1][1].frameId, 2);
  assert.equal(sent[1][1].documentId, "document-1");
  assert.deepEqual(sent[1][2], { frameId: 2, documentId: "document-1" });

  const info = await page.info(
    { command: "page.info", target: { tabId: 7 }, params: {} },
    new AbortController().signal,
  );
  assert.equal(info.frames[0].documentId, "document-1");
  assert.equal(info.frames[0].parentFrameId, -1);
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

test("page interaction optionally waits for the addressed frame navigation", async () => {
  const completed = fakeChromeEvent();
  const history = fakeChromeEvent();
  const fragment = fakeChromeEvent();
  const failed = fakeChromeEvent();
  const chromeAPI = {
    tabs: {
      get: async () => ({ id: 7, url: "https://example.com/start" }),
      sendMessage: async (_tabId, message) => {
        if (message.type === "MCP_BROWSER_BRIDGE_READY") {
          return { ready: true, bridgeVersion: "1.5" };
        }
        queueMicrotask(() =>
          completed.emit({
            tabId: 7,
            frameId: 2,
            documentId: "document-2",
            url: "https://example.com/next",
          }),
        );
        return { success: true, result: { backend: "content" } };
      },
    },
    permissions: { contains: async () => true },
    scripting: { executeScript: async () => undefined },
    webNavigation: {
      getFrame: async () => ({ documentId: "document-1" }),
      onCompleted: completed,
      onHistoryStateUpdated: history,
      onReferenceFragmentUpdated: fragment,
      onErrorOccurred: failed,
    },
  };
  const page = createPageHandlers(chromeAPI);

  const result = await page.click(
    {
      command: "page.click",
      target: { tabId: 7, frameId: 2, documentId: "document-1" },
      params: { locator: { css: "a" }, waitForNavigation: true },
    },
    new AbortController().signal,
  );

  assert.deepEqual(result.navigation, {
    tabId: 7,
    frameId: 2,
    documentId: "document-2",
    url: "https://example.com/next",
    sameDocument: false,
  });
  assert.equal(completed.listenerCount(), 0);
  assert.equal(history.listenerCount(), 0);
  assert.equal(fragment.listenerCount(), 0);
  assert.equal(failed.listenerCount(), 0);
});

test("page navigation wait resolves on same-document history updates", async () => {
  const completed = fakeChromeEvent();
  const history = fakeChromeEvent();
  const failed = fakeChromeEvent();
  const chromeAPI = {
    tabs: {
      get: async () => ({ id: 7, url: "https://example.com/start" }),
      sendMessage: async () => assert.fail("navigation wait must not enter the content bridge"),
    },
    permissions: { contains: async () => true },
    webNavigation: {
      getFrame: async () => ({ documentId: "document-1" }),
      onCompleted: completed,
      onHistoryStateUpdated: history,
      onErrorOccurred: failed,
    },
  };
  const page = createPageHandlers(chromeAPI);
  const waiting = page.wait(
    {
      requestId: "wait-navigation",
      command: "page.wait",
      target: { tabId: 7, frameId: 0, documentId: "document-1" },
      params: { condition: "navigation" },
      timeoutMs: 100,
    },
    new AbortController().signal,
  );
  await waitForCondition(() => history.listenerCount() === 1);
  history.emit({
    tabId: 7,
    frameId: 0,
    documentId: "document-1",
    url: "https://example.com/next",
  });

  const result = await waiting;
  assert.equal(result.condition, "navigation");
  assert.equal(result.matched, true);
  assert.equal(result.navigation.sameDocument, true);
  assert.equal(history.listenerCount(), 0);
});

test("page URL wait survives a cross-document navigation", async () => {
  const committed = fakeChromeEvent();
  const completed = fakeChromeEvent();
  const chromeAPI = {
    tabs: {
      get: async () => ({ id: 7, url: "https://example.com/start" }),
      sendMessage: async () => assert.fail("URL wait must not enter the content bridge"),
    },
    permissions: { contains: async () => true },
    webNavigation: {
      getFrame: async () => ({
        documentId: "document-1",
        url: "https://example.com/start",
      }),
      onCommitted: committed,
      onCompleted: completed,
    },
  };
  const page = createPageHandlers(chromeAPI);
  const waiting = page.wait(
    {
      requestId: "wait-url",
      command: "page.wait",
      target: { tabId: 7 },
      params: {
        condition: "url",
        urlPattern: "https://example.com/result/*",
        mode: "event",
      },
      timeoutMs: 100,
    },
    new AbortController().signal,
  );
  await waitForCondition(() => committed.listenerCount() === 1);
  committed.emit({
    tabId: 7,
    frameId: 0,
    documentId: "document-2",
    url: "https://example.com/result/42",
  });

  const result = await waiting;
  assert.equal(result.matched, true);
  assert.equal(result.documentId, "document-2");
  assert.equal(result.url, "https://example.com/result/42");
  assert.equal(committed.listenerCount(), 0);
  assert.equal(completed.listenerCount(), 0);
});

test("page delay wait is cancelled by the shared command signal", async () => {
  const chromeAPI = {
    tabs: {
      get: async () => ({ id: 7, url: "https://example.com/start" }),
      sendMessage: async () => assert.fail("delay wait must not enter the content bridge"),
    },
    permissions: { contains: async () => true },
    webNavigation: { getFrame: async () => ({ documentId: "document-1" }) },
  };
  const page = createPageHandlers(chromeAPI);
  const controller = new AbortController();
  const waiting = page.wait(
    {
      requestId: "wait-delay",
      command: "page.wait",
      target: { tabId: 7 },
      params: { condition: "delay", delayMs: 1_000 },
    },
    controller.signal,
  );
  await new Promise((resolve) => setTimeout(resolve, 0));
  controller.abort();

  await assert.rejects(waiting, (error) => error.code === ErrorCode.CANCELLED);
});

test("page network-idle wait requires and uses the activity observer", async () => {
  let observed;
  const chromeAPI = {
    tabs: { get: async () => ({ id: 7, url: "https://example.com/start" }) },
    permissions: { contains: async () => true },
    webNavigation: { getFrame: async () => ({ documentId: "document-1" }) },
  };
  const page = createPageHandlers(chromeAPI, {
    networkActivity: {
      available: true,
      waitForIdle: async (options) => {
        observed = options;
        return {
          condition: "networkIdle",
          matched: true,
          idleMs: options.idleMs,
        };
      },
    },
  });
  const result = await page.wait(
    {
      requestId: "wait-network",
      command: "page.wait",
      target: { tabId: 7 },
      params: { condition: "networkIdle", idleMs: 500 },
    },
    new AbortController().signal,
  );

  assert.equal(result.matched, true);
  assert.equal(observed.tabId, 7);
  assert.equal(observed.idleMs, 500);
  assert.equal(observed.signal instanceof AbortSignal, true);
});

test("page viewport screenshot captures and restores the addressed tab", async () => {
  let activeTabId = 9;
  const updates = [];
  const captures = [];
  const pngBase64 =
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=";
  const chromeAPI = {
    tabs: {
      get: async (tabId) => ({
        id: tabId,
        windowId: 3,
        active: tabId === activeTabId,
        url: "https://example.com/page",
      }),
      query: async ({ active, windowId }) => {
        assert.equal(active, true);
        assert.equal(windowId, 3);
        return [{ id: activeTabId, windowId: 3, active: true }];
      },
      update: async (tabId, update) => {
        updates.push([tabId, update]);
        if (update.active) activeTabId = tabId;
        return { id: tabId, windowId: 3, active: true };
      },
      captureVisibleTab: async (windowId, options) => {
        captures.push([windowId, options, activeTabId]);
        return `data:image/png;base64,${pngBase64}`;
      },
    },
    permissions: { contains: async () => true },
  };
  const page = createPageHandlers(chromeAPI);
  const result = await page.screenshot(
    {
      command: "page.screenshot",
      target: { tabId: 7 },
      params: {
        capture: "viewport",
        format: "png",
        maxWidth: 100,
        maxHeight: 100,
        maxBytes: 10_000,
      },
    },
    new AbortController().signal,
  );

  assert.equal(result.width, 1);
  assert.equal(result.height, 1);
  assert.equal(result.byteLength, 68);
  assert.equal(result.dataBase64, pngBase64);
  assert.deepEqual(captures, [[3, { format: "png" }, 7]]);
  assert.deepEqual(updates, [
    [7, { active: true }],
    [9, { active: true }],
  ]);
  assert.equal(activeTabId, 9);
});

test("page JPEG screenshot applies quality and rejects bounded payloads", async () => {
  const jpegBytes = Uint8Array.from([
    0xff, 0xd8, 0xff, 0xc0, 0x00, 0x11, 0x08, 0x00, 0x02, 0x00, 0x03, 0x03, 0x01, 0x11, 0x00, 0x02,
    0x11, 0x00, 0x03, 0x11, 0x00, 0xff, 0xd9,
  ]);
  const jpegBase64 = btoa(String.fromCharCode(...jpegBytes));
  let captureOptions;
  const chromeAPI = {
    tabs: {
      get: async () => ({
        id: 7,
        windowId: 3,
        active: true,
        url: "https://example.com/page",
      }),
      query: async () => [{ id: 7, windowId: 3, active: true }],
      update: async () => assert.fail("the already active tab must not be updated"),
      captureVisibleTab: async (_windowId, options) => {
        captureOptions = options;
        return `data:image/jpeg;base64,${jpegBase64}`;
      },
    },
    permissions: { contains: async () => true },
  };
  const page = createPageHandlers(chromeAPI);
  const request = {
    command: "page.screenshot",
    target: { tabId: 7 },
    params: {
      capture: "viewport",
      format: "jpeg",
      quality: 72,
      maxWidth: 2,
      maxHeight: 2,
      maxBytes: 1_024,
    },
  };

  await assert.rejects(
    page.screenshot(request, new AbortController().signal),
    (error) => error.code === ErrorCode.PAYLOAD_TOO_LARGE,
  );
  assert.deepEqual(captureOptions, { format: "jpeg", quality: 72 });
});

test("page PDF printing uses a managed exact-method CDP lease", async () => {
  const pdfBase64 = btoa("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n%%EOF\n");
  const calls = [];
  const signal = new AbortController().signal;
  const chromeAPI = {
    tabs: {
      get: async (tabId) => ({
        id: tabId,
        windowId: 3,
        url: "https://example.com/report",
      }),
    },
    permissions: {
      contains: async (request) => {
        calls.push(["permission", request]);
        return true;
      },
    },
  };
  const cdpSessions = {
    withSession: async (target, options, operation) => {
      calls.push([
        "session",
        target,
        { ...options, signal: options.signal instanceof AbortSignal },
      ]);
      return operation({
        sendCommand: async (method, params, commandOptions) => {
          calls.push([
            "command",
            method,
            params,
            { signal: commandOptions.signal instanceof AbortSignal },
          ]);
          return { data: pdfBase64 };
        },
      });
    },
  };
  const page = createPageHandlers(chromeAPI, { cdpSessions });
  const result = await page.printToPDF(
    {
      requestId: "pdf-request",
      command: "page.printToPDF",
      target: { tabId: 7 },
      params: {
        landscape: true,
        printBackground: true,
        scale: 0.9,
        paperWidth: 11,
        paperHeight: 8.5,
        marginTop: 0.25,
        marginBottom: 0.25,
        marginLeft: 0.5,
        marginRight: 0.5,
        pageRanges: "1-3,5",
        preferCSSPageSize: false,
        maxBytes: 10_000,
      },
    },
    signal,
  );

  assert.equal(result.mimeType, "application/pdf");
  assert.equal(result.dataBase64, pdfBase64);
  assert.equal(result.tabId, 7);
  assert.deepEqual(calls[1], ["permission", { permissions: ["debugger"] }]);
  assert.deepEqual(calls[2], [
    "session",
    { tabId: 7 },
    {
      consumerId: "pdf:pdf-request",
      domains: ["Page"],
      commands: ["Page.printToPDF"],
      signal: true,
    },
  ]);
  assert.deepEqual(calls.at(-1), [
    "command",
    "Page.printToPDF",
    {
      landscape: true,
      displayHeaderFooter: false,
      printBackground: true,
      scale: 0.9,
      paperWidth: 11,
      paperHeight: 8.5,
      marginTop: 0.25,
      marginBottom: 0.25,
      marginLeft: 0.5,
      marginRight: 0.5,
      pageRanges: "1-3,5",
      preferCSSPageSize: false,
      transferMode: "ReturnAsBase64",
    },
    { signal: true },
  ]);
});

test("page PDF printing rejects missing permission and invalid PDF bytes", async () => {
  const chromeAPI = {
    tabs: {
      get: async (tabId) => ({ id: tabId, windowId: 3, url: "https://example.com/" }),
    },
    permissions: {
      contains: async (request) => !request.permissions?.includes("debugger"),
    },
  };
  let sessions = 0;
  const cdpSessions = {
    withSession: async () => {
      sessions += 1;
    },
  };
  const page = createPageHandlers(chromeAPI, { cdpSessions });
  const request = {
    requestId: "pdf-denied",
    command: "page.printToPDF",
    target: { tabId: 7 },
    params: { maxBytes: 10_000 },
  };
  await assert.rejects(
    page.printToPDF(request, new AbortController().signal),
    (error) => error.code === ErrorCode.PERMISSION_REQUIRED,
  );
  assert.equal(sessions, 0);

  chromeAPI.permissions.contains = async () => true;
  cdpSessions.withSession = async (_target, _options, operation) =>
    operation({ sendCommand: async () => ({ data: btoa("not a PDF") }) });
  await assert.rejects(
    page.printToPDF(request, new AbortController().signal),
    (error) => error.code === ErrorCode.INVALID_MESSAGE,
  );
});

test("console handlers inject packaged bridges and preserve document targeting", async () => {
  let contentReady = false;
  const injections = [];
  const commands = [];
  const chromeAPI = {
    tabs: {
      get: async () => ({
        id: 7,
        windowId: 3,
        url: "https://example.com/page",
      }),
      sendMessage: async (_tabId, message, options) => {
        if (message.type === "MCP_BROWSER_CONSOLE_READY") {
          if (!contentReady) throw new Error("Receiving end does not exist");
          return { ready: true, bridgeVersion: "1.0" };
        }
        commands.push([message.command, message.params, options]);
        return {
          success: true,
          result:
            message.command === "console.read"
              ? { active: true, entries: [], nextCursor: "0" }
              : { active: message.command === "console.start" },
        };
      },
    },
    permissions: { contains: async () => true },
    scripting: {
      executeScript: async (injection) => {
        injections.push(injection);
        if (injection.files.includes("src/console-content.js")) contentReady = true;
      },
    },
    webNavigation: {
      getFrame: async () => ({
        documentId: "document-1",
        url: "https://example.com/page",
      }),
    },
  };
  const handlers = createConsoleHandlers(chromeAPI);
  const signal = new AbortController().signal;
  const target = { tabId: 7, frameId: 2, documentId: "document-1" };

  const started = await handlers.start(
    {
      requestId: "console-start",
      command: "console.start",
      target,
      params: { bufferSize: 250 },
    },
    signal,
  );
  assert.equal(started.active, true);
  assert.equal(started.documentId, "document-1");
  const read = await handlers.read(
    {
      requestId: "console-read",
      command: "console.read",
      target,
      params: { levels: ["error"], limit: 10 },
    },
    signal,
  );
  assert.deepEqual(read.entries, []);

  assert.deepEqual(injections, [
    {
      target: { tabId: 7, documentIds: ["document-1"] },
      files: ["src/console-content.js"],
      world: "ISOLATED",
    },
    {
      target: { tabId: 7, documentIds: ["document-1"] },
      files: ["src/console-main.js"],
      world: "MAIN",
    },
  ]);
  assert.deepEqual(
    commands.map(([command]) => command),
    ["console.start", "console.read"],
  );
  assert.deepEqual(commands[0][2], { frameId: 2, documentId: "document-1" });
});

function fakeChromeEvent() {
  const listeners = new Set();
  return {
    addListener(listener) {
      listeners.add(listener);
    },
    removeListener(listener) {
      listeners.delete(listener);
    },
    emit(details) {
      for (const listener of [...listeners]) listener(details);
    },
    listenerCount() {
      return listeners.size;
    },
  };
}

async function waitForCondition(predicate) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  throw new Error("Condition was not reached");
}
