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
      browser: { ping: handler },
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
    "page.press": { locator: { css: "#email" }, key: "Enter", modifiers: ["Control"] },
    "page.select": { locator: { css: "#country" }, values: ["US"] },
    "page.setChecked": { locator: { css: "#terms" }, checked: true },
    "page.scroll": { deltaY: 500, behavior: "auto" },
    "page.drag": { source: { css: "#card" }, targetLocator: { css: "#column" } },
    "page.dispatch": { locator: { css: "#save" }, eventType: "app:save", detail: {} },
    "page.submit": { locator: { css: "#form" }, waitForNavigation: false },
    "page.wait": {
      condition: "element",
      locator: { css: "#save" },
      elementState: "visible",
      mode: "event",
    },
  };

  for (const [index, command] of COMMAND_NAMES.entries()) {
    const outcomes = [];
    const request = createRequest(command, paramsByCommand[command], `request-${index}`);
    if (["windows.get", "windows.update", "windows.focus", "windows.close"].includes(command)) {
      request.target = { browserId, windowId: 3 };
    }
    const accepted = await router.execute(
      request,
      (outcome) => outcomes.push(outcome),
    );
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
});

test("router validates target and command params before invoking handlers", async () => {
  let calls = 0;
  const router = createRouter({
    handlers: {
      browser: { ping: () => { calls += 1; } },
      windows: {
        list: () => { calls += 1; },
        get: () => { calls += 1; },
        create: () => { calls += 1; },
        update: () => { calls += 1; },
        focus: () => { calls += 1; },
        close: () => { calls += 1; },
      },
      tabs: tabHandlers(() => { calls += 1; }),
      tabGroups: {
        group: () => { calls += 1; },
        ungroup: () => { calls += 1; },
        update: () => { calls += 1; },
      },
      sessions: {
        recentlyClosed: () => { calls += 1; },
        restore: () => { calls += 1; },
      },
      page: {
        info: () => { calls += 1; },
        getHTML: () => { calls += 1; },
        getHTMLBySelector: () => { calls += 1; },
        getText: () => { calls += 1; },
        query: () => { calls += 1; },
        getElement: () => { calls += 1; },
        snapshot: () => { calls += 1; },
        click: () => { calls += 1; },
        fill: () => { calls += 1; },
        hover: () => { calls += 1; },
        focus: () => { calls += 1; },
        blur: () => { calls += 1; },
        type: () => { calls += 1; },
        clear: () => { calls += 1; },
        press: () => { calls += 1; },
        select: () => { calls += 1; },
        setChecked: () => { calls += 1; },
        scroll: () => { calls += 1; },
        drag: () => { calls += 1; },
        dispatch: () => { calls += 1; },
        submit: () => { calls += 1; },
        wait: () => { calls += 1; },
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
    createRequest("page.click", { selector: "button", coordinates: { x: 1, y: 2 } }),
    createRequest("page.click", { coordinates: { x: -1, y: 2 } }),
    createRequest("page.click", { coordinates: { x: 1, y: 2 }, index: 0 }),
    createRequest("page.click", { locator: { css: "button", unexpected: true } }),
    createRequest("page.fill", { selector: "input" }),
    createRequest("page.fill", { selector: "input", value: "x", clear: "yes" }),
    createRequest("page.type", { locator: { css: "input" }, text: "" }),
    createRequest("page.press", { locator: { css: "input" }, key: "A", modifiers: ["Bad"] }),
    createRequest("page.select", { locator: { css: "select" }, values: [] }),
    createRequest("page.scroll", { deltaY: 0 }),
    createRequest("page.drag", { source: { css: "#a" } }),
    createRequest("page.dispatch", { locator: { css: "#a" }, eventType: "bad event" }),
    createRequest("page.hover", { locator: { css: "#a" }, backend: "native" }),
    createRequest("page.wait", { condition: "delay" }),
    createRequest("page.wait", { condition: "url", url: "a", urlPattern: "*" }),
    createRequest("page.wait", { condition: "element", locator: { css: "#a" } }),
    createRequest("page.wait", { condition: "attribute", locator: { css: "#a" }, attribute: "bad name", attributeState: "present" }),
    createRequest("page.wait", { condition: "attribute", locator: { css: "#a" }, attribute: "data-token", attributeState: "present" }),
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

  for (const [index, request] of invalidRequests.entries()) {
    request.requestId = `invalid-${index}`;
    const outcome = await execute(router, request);
    assert.equal(outcome.success, false);
    assert.equal(outcome.error.code, ErrorCode.INVALID_MESSAGE);
  }
  assert.equal(calls, 0);
});

test("router accepts every bounded wait condition shape", async () => {
  const router = createRouter();
  const conditions = [
    { condition: "delay", delayMs: 0 },
    { condition: "loadState", readyState: "complete", mode: "event" },
    { condition: "url", url: "https://example.com/" },
    { condition: "url", urlPattern: "https://*.example.com/*", mode: "polling", pollIntervalMs: 50 },
    { condition: "element", locator: { css: "#save" }, elementState: "visible" },
    { condition: "text", expected: "Saved", matchOperator: "contains", caseSensitive: false },
    { condition: "value", locator: { css: "input" }, expected: "", matchOperator: "equals" },
    { condition: "count", locator: { role: "button" }, count: 2, countOperator: "atLeast" },
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
        ping: () => new Promise((resolve) => { resolveHandler = resolve; }),
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
  await router.execute(request, (outcome) => { received = outcome; });
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
