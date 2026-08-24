import assert from "node:assert/strict";
import test from "node:test";

import { COMMAND_NAMES, CommandRouter } from "../src/command-router.js";
import { ErrorCode, protocolError } from "../src/protocol.js";

const browserId = "11111111-1111-4111-8111-111111111111";

test("router dispatches every allowlisted command to its domain handler", async () => {
  const calls = [];
  const handler = (request) => {
    calls.push(request.command);
    return { command: request.command };
  };
  const router = createRouter({
    handlers: {
      accessibility: { getTree: handler },
      browser: { ping: handler },
      emulation: { set: handler, get: handler, reset: handler },
      evaluation: { evaluate: handler },
      rawCDP: { sendReadOnly: handler },
      performance: { metrics: handler, capture: handler },
      network: {
        start: handler,
        stop: handler,
        clear: handler,
        read: handler,
        getBody: handler,
        exportHAR: handler,
      },
      cookies: {
        list: handler,
        listSensitive: handler,
        get: handler,
        getSensitive: handler,
        set: handler,
        remove: handler,
      },
      storageData: {
        list: handler,
        listSensitive: handler,
        get: handler,
        getSensitive: handler,
        set: handler,
        remove: handler,
        cacheMetadata: handler,
        indexedDBMetadata: handler,
        clear: handler,
      },
      downloads: {
        list: handler,
        get: handler,
        create: handler,
        pause: handler,
        resume: handler,
        cancel: handler,
        erase: handler,
      },
      history: {
        search: handler,
        getVisits: handler,
        deleteUrl: handler,
        deleteRange: handler,
        deleteAll: handler,
      },
      bookmarks: {
        list: handler,
        create: handler,
        update: handler,
        move: handler,
        remove: handler,
      },
      readingList: {
        list: handler,
        add: handler,
        update: handler,
        remove: handler,
      },
      windows: {
        list: handler,
        get: handler,
        create: handler,
        update: handler,
        focus: handler,
        close: handler,
      },
      tabs: tabHandlers(handler),
      tabGroups: {
        group: handler,
        ungroup: handler,
        update: handler,
      },
      sessions: {
        recentlyClosed: handler,
        restore: handler,
      },
      console: {
        start: handler,
        stop: handler,
        clear: handler,
        read: handler,
      },
      page: {
        info: handler,
        getHTML: handler,
        getHTMLBySelector: handler,
        getText: handler,
        query: handler,
        getElement: handler,
        snapshot: handler,
        click: handler,
        fill: handler,
        hover: handler,
        focus: handler,
        blur: handler,
        type: handler,
        clear: handler,
        press: handler,
        select: handler,
        setChecked: handler,
        scroll: handler,
        drag: handler,
        dispatch: handler,
        submit: handler,
        wait: handler,
        screenshot: handler,
        printToPDF: handler,
      },
    },
  });
  const paramsByCommand = {
    "browser.ping": {},
    "windows.list": {},
    "windows.get": {},
    "windows.create": {
      urls: ["https://example.com"],
      type: "popup",
      state: "normal",
      width: 900,
      height: 700,
    },
    "windows.update": { state: "normal", left: -100, width: 1_000 },
    "windows.focus": {},
    "windows.close": {},
    "tabs.list": {},
    "tabs.get": {},
    "tabs.create": { url: "https://example.com", active: false },
    "tabs.activate": {},
    "tabs.navigate": { url: "https://example.org" },
    "tabs.reload": { bypassCache: true },
    "tabs.stop": {},
    "tabs.back": {},
    "tabs.forward": {},
    "tabs.move": { windowId: 3, index: -1 },
    "tabs.duplicate": {},
    "tabs.close": {},
    "tabs.pin": { pinned: true },
    "tabs.mute": { muted: false },
    "tabs.getZoom": {},
    "tabs.setZoom": { factor: 1.25 },
    "tabs.group": { tabIds: [1, 2] },
    "tabs.ungroup": { tabIds: [1, 2] },
    "tabGroups.update": { groupId: 3, title: "Work", color: "blue" },
    "sessions.recentlyClosed": { maxResults: 10 },
    "sessions.restore": { sessionId: "session-1" },
    "cookies.list": { url: "https://example.com/", limit: 50 },
    "cookies.get": { url: "https://example.com/", name: "session" },
    "cookies.set": { url: "https://example.com/", name: "session", value: "value" },
    "cookies.remove": { url: "https://example.com/", name: "session" },
    "cookies.listSensitive": { url: "https://example.com/", limit: 50 },
    "cookies.getSensitive": { url: "https://example.com/", name: "session" },
    "storage.list": { origin: "https://example.com", storageType: "localStorage", limit: 50 },
    "storage.get": { origin: "https://example.com", storageType: "localStorage", key: "theme" },
    "storage.set": {
      origin: "https://example.com",
      storageType: "localStorage",
      key: "theme",
      value: "dark",
    },
    "storage.remove": {
      origin: "https://example.com",
      storageType: "localStorage",
      key: "theme",
    },
    "storage.cacheMetadata": { origin: "https://example.com", limit: 50 },
    "storage.indexedDBMetadata": { origin: "https://example.com", limit: 50 },
    "storage.clear": {
      origin: "https://example.com",
      types: ["sessionStorage"],
      confirm: true,
    },
    "storage.listSensitive": {
      origin: "https://example.com",
      storageType: "sessionStorage",
      limit: 50,
    },
    "storage.getSensitive": {
      origin: "https://example.com",
      storageType: "sessionStorage",
      key: "token",
    },
    "downloads.list": { limit: 50, allowIncognito: false },
    "downloads.get": { downloadId: 7, allowIncognito: false },
    "downloads.create": { url: "https://example.com/file.zip", allowIncognito: false },
    "downloads.pause": { downloadId: 7, allowIncognito: false },
    "downloads.resume": { downloadId: 7, allowIncognito: false },
    "downloads.cancel": { downloadId: 7, allowIncognito: false },
    "downloads.erase": { downloadId: 7, confirm: true, allowIncognito: false },
    "history.search": { text: "docs", limit: 50 },
    "history.getVisits": { url: "https://example.com/", limit: 50 },
    "history.deleteUrl": { url: "https://example.com/", confirm: true },
    "history.deleteRange": { startTime: 10, endTime: 20, confirm: true },
    "history.deleteAll": { confirm: true },
    "bookmarks.list": { query: "docs", limit: 50 },
    "bookmarks.create": { title: "Docs", url: "https://example.com/" },
    "bookmarks.update": { bookmarkId: "42", title: "Updated" },
    "bookmarks.move": { bookmarkId: "42", parentId: "1" },
    "bookmarks.remove": { bookmarkId: "42", recursive: false },
    "readingList.list": { hasBeenRead: false, limit: 50 },
    "readingList.add": {
      url: "https://example.com/article",
      title: "Article",
      hasBeenRead: false,
    },
    "readingList.update": { url: "https://example.com/article", hasBeenRead: true },
    "readingList.remove": { url: "https://example.com/article" },
    "page.info": {},
    "page.getHTML": {},
    "page.getHTMLBySelector": { selector: "main" },
    "page.getText": { maxChars: 1_000, cursor: "0" },
    "page.query": { locator: { role: "button", name: "Save" }, limit: 10 },
    "page.getElement": { locator: { css: "#save" }, maxHTMLChars: 2_000 },
    "page.snapshot": { interactiveOnly: true, maxDepth: 10, maxNodes: 500 },
    "page.click": { coordinates: { x: 20, y: 40 } },
    "page.fill": { selector: "#email", value: "user@example.com", clear: true },
    "page.hover": { locator: { css: "#save" } },
    "page.focus": { locator: { css: "#save" } },
    "page.blur": { locator: { css: "#save" } },
    "page.type": { locator: { css: "#email" }, text: "hello", delayMs: 0 },
    "page.clear": { locator: { css: "#email" } },
    "page.press": {
      locator: { css: "#email" },
      key: "Enter",
      modifiers: ["Control"],
    },
    "page.select": { locator: { css: "#country" }, values: ["US"] },
    "page.setChecked": { locator: { css: "#terms" }, checked: true },
    "page.scroll": { deltaY: 500, behavior: "auto" },
    "page.drag": {
      source: { css: "#card" },
      targetLocator: { css: "#column" },
    },
    "page.dispatch": {
      locator: { css: "#save" },
      eventType: "app:save",
      detail: {},
    },
    "page.submit": { locator: { css: "#form" }, waitForNavigation: false },
    "page.wait": {
      condition: "element",
      locator: { css: "#save" },
      elementState: "visible",
      mode: "event",
    },
    "page.screenshot": {
      capture: "viewport",
      format: "jpeg",
      quality: 80,
      maxWidth: 1_920,
      maxHeight: 1_080,
      maxBytes: 1_000_000,
    },
    "page.printToPDF": {
      landscape: true,
      printBackground: true,
      scale: 1,
      paperWidth: 11,
      paperHeight: 8.5,
      marginTop: 0.4,
      marginBottom: 0.4,
      marginLeft: 0.4,
      marginRight: 0.4,
      pageRanges: "1-3, 5",
      preferCSSPageSize: false,
      maxBytes: 1_000_000,
    },
    "accessibility.getTree": {
      mode: "full",
      roles: ["button", "link"],
      nameContains: "",
      includeIgnored: false,
      includeLocators: true,
      includeElementReferences: true,
      maxDepth: 20,
      maxNodes: 1_000,
      maxProperties: 20,
      maxValueChars: 500,
      maxElementReferences: 50,
      maxBytes: 1_000_000,
    },
    "emulation.set": {
      viewport: {
        width: 390,
        height: 844,
        deviceScaleFactor: 3,
        mobile: true,
        orientation: "portraitPrimary",
      },
      touch: { enabled: true, maxTouchPoints: 5 },
      network: {
        offline: false,
        latencyMs: 80,
        downloadKbps: 2_000,
        uploadKbps: 1_000,
        connectionType: "cellular4g",
      },
      userAgent: {
        value: "ExampleBrowser/1.0",
        acceptLanguage: "en-US",
        platform: "Linux armv8l",
      },
      locale: "en_US",
      timezoneId: "America/New_York",
      geolocation: { latitude: 40.7, longitude: -74, accuracy: 20 },
      media: { type: "screen", colorScheme: "dark", reducedMotion: "reduce" },
    },
    "emulation.get": {},
    "emulation.reset": {},
    "runtime.evaluateIsolated": {
      expression: "document.title",
      awaitPromise: true,
      maxDepth: 6,
      maxNodes: 1_000,
      maxStringChars: 10_000,
      maxBytes: 524_288,
    },
    "cdp.sendReadOnly": {
      method: "Performance.getMetrics",
      params: {},
      maxDepth: 12,
      maxNodes: 2_000,
      maxStringChars: 2_000,
      maxBytes: 524_288,
    },
    "performance.metrics": {},
    "performance.capture": {
      kind: "trace",
      durationMs: 1_000,
      maxBytes: 1_000_000,
    },
    "network.start": { maxEntries: 1_000 },
    "network.stop": {},
    "network.clear": {},
    "network.read": { limit: 50, maxBytes: 524_288 },
    "network.getBody": { entryId: "1", direction: "response", maxBytes: 262_144 },
    "network.exportHAR": { maxBytes: 2_000_000 },
    "console.start": {
      bufferSize: 500,
      captureConsole: true,
      captureErrors: true,
    },
    "console.stop": {},
    "console.clear": {},
    "console.read": {
      levels: ["warn", "error"],
      kinds: ["console", "exception"],
      cursor: "12",
      limit: 50,
      since: "2026-08-24T10:00:00Z",
    },
  };

  for (const [index, command] of COMMAND_NAMES.entries()) {
    const outcomes = [];
    const request = createRequest(command, paramsByCommand[command], `request-${index}`);
    if (["windows.get", "windows.update", "windows.focus", "windows.close"].includes(command)) {
      request.target = { browserId, windowId: 3 };
    }
    if (
      command.startsWith("downloads.") ||
      command.startsWith("history.") ||
      command.startsWith("bookmarks.") ||
      command.startsWith("readingList.")
    ) {
      delete request.target;
    }
    const accepted = await router.execute(request, (outcome) => outcomes.push(outcome));
    assert.equal(accepted, true);
    assert.deepEqual(outcomes, [{ success: true, result: { command } }]);
  }
  assert.deepEqual(calls, COMMAND_NAMES);
});

test("router rejects unknown and currently unavailable commands", async () => {
  const router = createRouter({ capabilities: ["browser.ping"] });

  assert.equal(
    (await execute(router, createRequest("browser.unknown", {}))).error.code,
    ErrorCode.INVALID_COMMAND,
  );
  assert.equal(
    (await execute(router, createRequest("tabs.list", {}))).error.code,
    ErrorCode.CAPABILITY_UNAVAILABLE,
  );
  assert.equal(
    (
      await execute(
        createRouter(),
        createRequest("cdp.sendReadOnly", {
          method: "Runtime.evaluate",
          params: {},
          maxDepth: 12,
          maxNodes: 2_000,
          maxStringChars: 2_000,
          maxBytes: 524_288,
        }),
      )
    ).error.code,
    ErrorCode.INVALID_COMMAND,
  );
});

test("router validates target and command params before invoking handlers", async () => {
  let calls = 0;
  const router = createRouter({
    handlers: {
      accessibility: {
        getTree: () => {
          calls += 1;
        },
      },
      browser: {
        ping: () => {
          calls += 1;
        },
      },
      evaluation: {
        evaluate: () => {
          calls += 1;
        },
      },
      performance: {
        metrics: () => {
          calls += 1;
        },
        capture: () => {
          calls += 1;
        },
      },
      network: {
        start: () => {
          calls += 1;
        },
        stop: () => {
          calls += 1;
        },
        clear: () => {
          calls += 1;
        },
        read: () => {
          calls += 1;
        },
        getBody: () => {
          calls += 1;
        },
        exportHAR: () => {
          calls += 1;
        },
      },
      cookies: {
        list: () => {
          calls += 1;
        },
        listSensitive: () => {
          calls += 1;
        },
        get: () => {
          calls += 1;
        },
        getSensitive: () => {
          calls += 1;
        },
        set: () => {
          calls += 1;
        },
        remove: () => {
          calls += 1;
        },
      },
      storageData: {
        list: () => {
          calls += 1;
        },
        listSensitive: () => {
          calls += 1;
        },
        get: () => {
          calls += 1;
        },
        getSensitive: () => {
          calls += 1;
        },
        set: () => {
          calls += 1;
        },
        remove: () => {
          calls += 1;
        },
        cacheMetadata: () => {
          calls += 1;
        },
        indexedDBMetadata: () => {
          calls += 1;
        },
        clear: () => {
          calls += 1;
        },
      },
      downloads: {
        list: () => {
          calls += 1;
        },
        get: () => {
          calls += 1;
        },
        create: () => {
          calls += 1;
        },
        pause: () => {
          calls += 1;
        },
        resume: () => {
          calls += 1;
        },
        cancel: () => {
          calls += 1;
        },
        erase: () => {
          calls += 1;
        },
      },
      windows: {
        list: () => {
          calls += 1;
        },
        get: () => {
          calls += 1;
        },
        create: () => {
          calls += 1;
        },
        update: () => {
          calls += 1;
        },
        focus: () => {
          calls += 1;
        },
        close: () => {
          calls += 1;
        },
      },
      tabs: tabHandlers(() => {
        calls += 1;
      }),
      tabGroups: {
        group: () => {
          calls += 1;
        },
        ungroup: () => {
          calls += 1;
        },
        update: () => {
          calls += 1;
        },
      },
      sessions: {
        recentlyClosed: () => {
          calls += 1;
        },
        restore: () => {
          calls += 1;
        },
      },
      console: {
        start: () => {
          calls += 1;
        },
        stop: () => {
          calls += 1;
        },
        clear: () => {
          calls += 1;
        },
        read: () => {
          calls += 1;
        },
      },
      page: {
        info: () => {
          calls += 1;
        },
        getHTML: () => {
          calls += 1;
        },
        getHTMLBySelector: () => {
          calls += 1;
        },
        getText: () => {
          calls += 1;
        },
        query: () => {
          calls += 1;
        },
        getElement: () => {
          calls += 1;
        },
        snapshot: () => {
          calls += 1;
        },
        click: () => {
          calls += 1;
        },
        fill: () => {
          calls += 1;
        },
        hover: () => {
          calls += 1;
        },
        focus: () => {
          calls += 1;
        },
        blur: () => {
          calls += 1;
        },
        type: () => {
          calls += 1;
        },
        clear: () => {
          calls += 1;
        },
        press: () => {
          calls += 1;
        },
        select: () => {
          calls += 1;
        },
        setChecked: () => {
          calls += 1;
        },
        scroll: () => {
          calls += 1;
        },
        drag: () => {
          calls += 1;
        },
        dispatch: () => {
          calls += 1;
        },
        submit: () => {
          calls += 1;
        },
        wait: () => {
          calls += 1;
        },
        screenshot: () => {
          calls += 1;
        },
        printToPDF: () => {
          calls += 1;
        },
      },
    },
  });
  const invalidRequests = [
    createRequest("browser.ping", { unexpected: true }),
    createRequest("page.getHTMLBySelector", { selector: "" }),
    createRequest("page.getHTML", { maxChars: 0 }),
    createRequest("page.getHTML", { includeSelectors: [""] }),
    createRequest("page.getText", { cursor: "next" }),
    createRequest("page.query", { locator: { css: "button" }, limit: 101 }),
    createRequest("page.getElement", { locator: {}, maxHTMLChars: 1_000 }),
    createRequest("page.snapshot", { maxNodes: 0 }),
    createRequest("page.snapshot", { interactiveOnly: "yes" }),
    createRequest("page.click", {
      selector: "button",
      coordinates: { x: 1, y: 2 },
    }),
    createRequest("page.click", { coordinates: { x: -1, y: 2 } }),
    createRequest("page.click", { coordinates: { x: 1, y: 2 }, index: 0 }),
    createRequest("page.click", {
      locator: { css: "button", unexpected: true },
    }),
    createRequest("page.fill", { selector: "input" }),
    createRequest("page.fill", { selector: "input", value: "x", clear: "yes" }),
    createRequest("page.type", { locator: { css: "input" }, text: "" }),
    createRequest("page.type", {
      locator: { css: "input" },
      text: "x".repeat(10_001),
      delayMs: 1,
    }),
    createRequest("page.press", { locator: { css: "input" }, key: "x".repeat(101) }),
    createRequest("page.press", {
      locator: { css: "input" },
      key: "A",
      modifiers: ["Bad"],
    }),
    createRequest("page.select", { locator: { css: "select" }, values: [] }),
    createRequest("page.scroll", { deltaY: 0 }),
    createRequest("page.drag", { source: { css: "#a" } }),
    createRequest("page.dispatch", {
      locator: { css: "#a" },
      eventType: "bad event",
    }),
    createRequest("page.hover", { locator: { css: "#a" }, backend: "native" }),
    createRequest("page.focus", { locator: { css: "#a" }, backend: "cdp" }),
    createRequest("page.select", {
      locator: { css: "select" },
      values: ["US"],
      backend: "cdp",
    }),
    createRequest("page.scroll", { deltaY: 100, behavior: "smooth", backend: "cdp" }),
    createRequest("page.wait", { condition: "delay" }),
    createRequest("page.wait", { condition: "url", url: "a", urlPattern: "*" }),
    createRequest("page.wait", {
      condition: "element",
      locator: { css: "#a" },
    }),
    createRequest("page.wait", {
      condition: "attribute",
      locator: { css: "#a" },
      attribute: "bad name",
      attributeState: "present",
    }),
    createRequest("page.wait", {
      condition: "attribute",
      locator: { css: "#a" },
      attribute: "data-token",
      attributeState: "present",
    }),
    createRequest("page.screenshot", { format: "png", quality: 80 }),
    createRequest("page.screenshot", { format: "jpeg", maxBytes: 2_000_001 }),
    createRequest("page.screenshot", { capture: "element" }),
    createRequest("page.screenshot", { capture: "fullPage", locator: { css: "main" } }),
    createRequest("page.printToPDF", { pageRanges: "5-2" }),
    createRequest("page.printToPDF", { paperWidth: 2, marginLeft: 1, marginRight: 1 }),
    createRequest("accessibility.getTree", {
      mode: "partial",
      roles: [],
      nameContains: "",
      includeIgnored: false,
      includeLocators: true,
      includeElementReferences: false,
      maxNodes: 100,
      maxProperties: 20,
      maxValueChars: 500,
      maxElementReferences: 0,
      maxBytes: 100_000,
    }),
    createRequest("emulation.set", {}),
    createRequest("emulation.set", {
      viewport: { width: 390, deviceScaleFactor: 3, mobile: true },
    }),
    createRequest("emulation.set", {
      touch: { enabled: false, maxTouchPoints: 2 },
    }),
    createRequest("emulation.set", { network: { latencyMs: -1 } }),
    createRequest("emulation.set", { userAgent: { value: "Bad\nAgent" } }),
    createRequest("emulation.set", { locale: "not a locale" }),
    createRequest("emulation.set", {
      geolocation: { latitude: 91, longitude: 0, accuracy: 10 },
    }),
    createRequest("emulation.set", { media: {} }),
    createRequest("runtime.evaluateIsolated", {
      expression: " ",
      awaitPromise: true,
      maxDepth: 6,
      maxNodes: 1_000,
      maxStringChars: 10_000,
      maxBytes: 524_288,
    }),
    createRequest("cdp.sendReadOnly", {
      method: "DOM.describeNode",
      params: { backendNodeId: 7, objectId: "forbidden" },
      maxDepth: 12,
      maxNodes: 2_000,
      maxStringChars: 2_000,
      maxBytes: 524_288,
    }),
    createRequest("performance.capture", {
      kind: "trace",
      durationMs: 99,
      maxBytes: 1_000_000,
    }),
    createRequest("network.start", { maxEntries: 0 }),
    createRequest("network.stop", { unexpected: true }),
    createRequest("network.read", { limit: 0, maxBytes: 524_288 }),
    createRequest("network.read", {
      limit: 50,
      maxBytes: 524_288,
      resourceTypes: ["Document", "Document"],
    }),
    createRequest("network.getBody", {
      entryId: "request-id",
      direction: "response",
      maxBytes: 262_144,
    }),
    createRequest("network.exportHAR", { maxBytes: 1 }),
    createRequest("cookies.list", { url: "https://example.com/", limit: 0 }),
    createRequest("cookies.get", { url: "chrome://settings", name: "session" }),
    createRequest("cookies.set", {
      url: "https://example.com/",
      name: "session",
      value: "secret",
      sameSite: "no_restriction",
    }),
    createRequest("cookies.remove", {
      url: "https://example.com/",
      name: "bad name",
    }),
    createRequest("storage.list", {
      origin: "https://example.com/path",
      storageType: "localStorage",
      limit: 50,
    }),
    createRequest("storage.get", {
      origin: "https://example.com",
      storageType: "unknown",
      key: "theme",
    }),
    createRequest("storage.set", {
      origin: "https://example.com",
      storageType: "localStorage",
      key: "theme",
      value: "x".repeat(65_537),
    }),
    createRequest("storage.clear", {
      origin: "https://example.com",
      types: ["localStorage", "localStorage"],
      confirm: true,
    }),
    createRequest("downloads.list", { limit: 0, allowIncognito: false }),
    createRequest("downloads.get", { downloadId: -1, allowIncognito: false }),
    createRequest("downloads.create", {
      url: "https://example.com/file.zip",
      allowIncognito: "yes",
    }),
    createRequest("runtime.evaluateIsolated", {
      expression: "document.title",
      awaitPromise: true,
      maxDepth: 11,
      maxNodes: 1_000,
      maxStringChars: 10_000,
      maxBytes: 524_288,
    }),
    createRequest("runtime.evaluateIsolated", {
      expression: "document.title",
      world: "main",
      awaitPromise: true,
      maxDepth: 6,
      maxNodes: 1_000,
      maxStringChars: 10_000,
      maxBytes: 524_288,
    }),
    createRequest("accessibility.getTree", {
      mode: "full",
      backendNodeId: 7,
      roles: [],
      nameContains: "",
      includeIgnored: false,
      includeLocators: true,
      includeElementReferences: false,
      maxDepth: 20,
      maxNodes: 100,
      maxProperties: 20,
      maxValueChars: 500,
      maxElementReferences: 0,
      maxBytes: 100_000,
    }),
    createRequest("console.start", {
      captureConsole: false,
      captureErrors: false,
    }),
    createRequest("console.read", { levels: ["error", "error"] }),
    createRequest("console.read", { kinds: ["network"] }),
    createRequest("console.read", { cursor: "-1" }),
    createRequest("console.read", { since: "yesterday" }),
    createRequest("windows.get", {}),
    createRequest("windows.create", { urls: [] }),
    createRequest("tabs.create", { index: -1 }),
    createRequest("tabs.navigate", { url: "" }),
    createRequest("tabs.reload", { bypassCache: "yes" }),
    createRequest("tabs.move", { index: -2 }),
    createRequest("tabs.pin", {}),
    createRequest("tabs.setZoom", { factor: 6 }),
    createRequest("tabs.group", { tabIds: [] }),
    createRequest("tabs.group", { tabIds: [1], groupId: 2, windowId: 3 }),
    createRequest("tabs.ungroup", { tabIds: [1, 1] }),
    createRequest("tabGroups.update", { groupId: 2 }),
    createRequest("tabGroups.update", { groupId: 2, color: "black" }),
    createRequest("sessions.recentlyClosed", { maxResults: 26 }),
    createRequest("sessions.restore", { sessionId: " " }),
    {
      ...createRequest("windows.update", { state: "fullscreen", width: 800 }),
      target: { browserId, windowId: 3 },
    },
    {
      ...createRequest("windows.update", {}),
      target: { browserId, windowId: 3 },
    },
    {
      ...createRequest("page.getHTML", {}),
      target: { browserId: "22222222-2222-4222-8222-222222222222", tabId: 1 },
    },
  ];

  const prohibitedCapture = await execute(
    router,
    createRequest("performance.capture", {
      kind: "heapSnapshot",
      durationMs: 1_000,
      maxBytes: 1_000_000,
    }),
  );
  assert.equal(prohibitedCapture.success, false);
  assert.equal(prohibitedCapture.error.code, ErrorCode.INVALID_COMMAND);

  for (const [index, request] of invalidRequests.entries()) {
    request.requestId = `invalid-${index}`;
    const outcome = await execute(router, request);
    assert.equal(outcome.success, false);
    assert.equal(outcome.error.code, ErrorCode.INVALID_MESSAGE);
  }
  assert.equal(calls, 0);
});

test("router enforces personal-data scope, confirmation, and URL boundaries", async () => {
  const noTarget = (command, params, requestId) => {
    const request = createRequest(command, params, requestId);
    delete request.target;
    return request;
  };
  assert.equal(
    (await execute(createRouter(), noTarget("history.deleteAll", { confirm: false }, "pd-1"))).error
      .code,
    ErrorCode.CONFIRMATION_REQUIRED,
  );
  assert.equal(
    (
      await execute(
        createRouter(),
        noTarget("bookmarks.remove", { bookmarkId: "folder", recursive: true }, "pd-2"),
      )
    ).error.code,
    ErrorCode.CONFIRMATION_REQUIRED,
  );
  assert.equal(
    (
      await execute(
        createRouter(),
        noTarget(
          "readingList.add",
          { url: "file:///private/note", title: "Note", hasBeenRead: false },
          "pd-3",
        ),
      )
    ).error.code,
    ErrorCode.INVALID_MESSAGE,
  );
  assert.equal(
    (await execute(createRouter(), createRequest("history.search", { limit: 50 }, "pd-4"))).error
      .code,
    ErrorCode.INVALID_MESSAGE,
  );
});

test("router accepts every bounded wait condition shape", async () => {
  const router = createRouter();
  const conditions = [
    { condition: "delay", delayMs: 0 },
    { condition: "loadState", readyState: "complete", mode: "event" },
    { condition: "url", url: "https://example.com/" },
    {
      condition: "url",
      urlPattern: "https://*.example.com/*",
      mode: "polling",
      pollIntervalMs: 50,
    },
    {
      condition: "element",
      locator: { css: "#save" },
      elementState: "visible",
    },
    {
      condition: "text",
      expected: "Saved",
      matchOperator: "contains",
      caseSensitive: false,
    },
    {
      condition: "value",
      locator: { css: "input" },
      expected: "",
      matchOperator: "equals",
    },
    {
      condition: "count",
      locator: { role: "button" },
      count: 2,
      countOperator: "atLeast",
    },
    { condition: "navigation" },
    { condition: "networkIdle", idleMs: 500 },
    {
      condition: "attribute",
      locator: { css: "#save" },
      attribute: "aria-busy",
      attributeState: "absent",
    },
  ];

  for (const [index, params] of conditions.entries()) {
    const outcome = await execute(
      router,
      createRequest("page.wait", params, `wait-condition-${index}`),
    );
    assert.equal(outcome.success, true, JSON.stringify(outcome.error));
  }
});

test("router emits one cancellation response and suppresses duplicate request IDs", async () => {
  let resolveHandler;
  const router = createRouter({
    handlers: {
      browser: {
        ping: () =>
          new Promise((resolve) => {
            resolveHandler = resolve;
          }),
      },
      windows: {},
      tabs: {},
      page: {},
    },
  });
  const request = createRequest("browser.ping", {}, "same-request");
  const outcomes = [];
  const first = router.execute(request, (outcome) => outcomes.push(outcome));
  await waitFor(() => resolveHandler !== undefined);

  assert.equal(await router.execute(request, (outcome) => outcomes.push(outcome)), false);
  assert.equal(router.cancel(request.requestId), true);
  resolveHandler({ pong: true });
  assert.equal(await first, true);
  assert.equal(router.cancel(request.requestId), false);
  assert.equal(outcomes.length, 1);
  assert.equal(outcomes[0].success, false);
  assert.equal(outcomes[0].error.code, ErrorCode.CANCELLED);
  assert.equal(outcomes[0].error.requestId, request.requestId);
});

test("router maps handler failures to structured protocol errors", async () => {
  const router = createRouter({
    handlers: {
      browser: {
        ping: () => {
          throw protocolError(ErrorCode.PERMISSION_REQUIRED, "Access required", false, {
            origin: "https://example.com",
          });
        },
      },
      tabs: {},
      page: {},
    },
  });

  const outcome = await execute(router, createRequest("browser.ping", {}));
  assert.deepEqual(outcome.error.details, { origin: "https://example.com" });
  assert.equal(outcome.error.code, ErrorCode.PERMISSION_REQUIRED);
  assert.equal("stack" in outcome.error, false);
});

function createRouter({ handlers = defaultHandlers(), capabilities = COMMAND_NAMES } = {}) {
  return new CommandRouter({
    getBrowserId: async () => browserId,
    getCapabilities: async () => capabilities,
    handlers,
  });
}

function defaultHandlers() {
  return {
    browser: { ping: () => ({ pong: true }) },
    windows: {
      list: () => ({ windows: [] }),
      get: () => ({ window: {} }),
      create: () => ({ window: {} }),
      update: () => ({ window: {} }),
      focus: () => ({ window: {} }),
      close: () => ({ closed: true }),
    },
    tabs: tabHandlers(() => ({ tab: {} })),
    tabGroups: {
      group: () => ({ groupId: 1 }),
      ungroup: () => ({ ungrouped: true }),
      update: () => ({ group: {} }),
    },
    sessions: {
      recentlyClosed: () => ({ sessions: [] }),
      restore: () => ({ session: {} }),
    },
    console: {
      start: () => ({ active: true }),
      stop: () => ({ active: false }),
      clear: () => ({ cleared: true }),
      read: () => ({ entries: [] }),
    },
    evaluation: {
      evaluate: () => ({ completed: true }),
    },
    rawCDP: {
      sendReadOnly: () => ({ result: {} }),
    },
    performance: {
      metrics: () => ({ metrics: [] }),
      capture: () => ({ dataBase64: "artifact" }),
    },
    network: {
      start: () => ({ active: true }),
      stop: () => ({ active: false }),
      clear: () => ({ entries: [] }),
      read: () => ({ entries: [] }),
      getBody: () => ({ dataBase64: "artifact" }),
      exportHAR: () => ({ dataBase64: "artifact" }),
    },
    history: {
      search: () => ({ items: [] }),
      getVisits: () => ({ visits: [] }),
      deleteUrl: () => ({ changed: true }),
      deleteRange: () => ({ changed: true }),
      deleteAll: () => ({ changed: true }),
    },
    bookmarks: {
      list: () => ({ bookmarks: [] }),
      create: () => ({ bookmark: {} }),
      update: () => ({ bookmark: {} }),
      move: () => ({ bookmark: {} }),
      remove: () => ({ changed: true }),
    },
    readingList: {
      list: () => ({ entries: [] }),
      add: () => ({ entry: {} }),
      update: () => ({ entry: {} }),
      remove: () => ({ changed: true }),
    },
    page: {
      info: () => ({ url: "" }),
      getHTML: () => ({ html: "" }),
      getHTMLBySelector: () => ({ html: "" }),
      getText: () => ({ text: "" }),
      query: () => ({ elements: [] }),
      getElement: () => ({ element: {} }),
      snapshot: () => ({ nodes: [] }),
      click: () => ({ clicked: true }),
      fill: () => ({ filled: true }),
      hover: () => ({ hovered: true }),
      focus: () => ({ focused: true }),
      blur: () => ({ blurred: true }),
      type: () => ({ typed: true }),
      clear: () => ({ cleared: true }),
      press: () => ({ pressed: true }),
      select: () => ({ selected: true }),
      setChecked: () => ({ checked: true }),
      scroll: () => ({ scrolled: true }),
      drag: () => ({ dragged: true }),
      dispatch: () => ({ dispatched: true }),
      submit: () => ({ submitted: true }),
      wait: () => ({ matched: true }),
      screenshot: () => ({ dataBase64: "image" }),
      printToPDF: () => ({ dataBase64: "pdf" }),
    },
  };
}

function tabHandlers(handler) {
  return {
    list: handler,
    get: handler,
    create: handler,
    activate: handler,
    navigate: handler,
    reload: handler,
    stop: handler,
    back: handler,
    forward: handler,
    move: handler,
    duplicate: handler,
    close: handler,
    pin: handler,
    mute: handler,
    getZoom: handler,
    setZoom: handler,
  };
}

function createRequest(command, params, requestId = "request-1") {
  return {
    requestId,
    browserId,
    command,
    params,
    target: { browserId, tabId: 1 },
  };
}

async function execute(router, request) {
  let received;
  await router.execute(request, (outcome) => {
    received = outcome;
  });
  return received;
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
