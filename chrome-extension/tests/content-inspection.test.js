import assert from "node:assert/strict";
import test from "node:test";

test("content inspection bounds output, redacts secrets, paginates, and describes elements", async () => {
  const text = textNode("Save settings");
  const button = element("button", { id: "save", children: [text] });
  const password = element("input", {
    id: "password",
    attributes: { type: "password", name: "password", value: "top-secret" },
  });
  password.type = "password";
  password.value = "top-secret";
  const country = element("select", { id: "country" });
  country.options = [
    { value: "US", text: "United States", selected: false },
    { value: "CA", text: "Canada", selected: false },
  ];
  country.multiple = false;
  const checkbox = element("input", {
    id: "terms",
    attributes: { type: "checkbox" },
  });
  checkbox.type = "checkbox";
  checkbox.checked = false;
  const scrollBox = element("div", { id: "scroll-box" });
  const dragSource = element("div", { id: "drag-source" });
  const dragTarget = element("div", { id: "drag-target" });
  const form = element("form", { id: "settings-form" });
  form.requestSubmit = () => { form.submitted = true; };
  const script = element("script", { children: [textNode("window.secret = true")] });
  const main = element("main", {
    children: [
      button, password, country, checkbox, scrollBox, dragSource, dragTarget, form, script,
    ],
  });
  const body = element("body", { children: [main] });
  const html = element("html", { children: [body] });
  const document = new FakeDocument(html);
  const window = fakeWindow();
  let listener;

  globalThis.Node = { ELEMENT_NODE: 1, TEXT_NODE: 3 };
  globalThis.Event = FakeEvent;
  globalThis.CustomEvent = FakeEvent;
  globalThis.MouseEvent = FakeEvent;
  globalThis.KeyboardEvent = FakeEvent;
  globalThis.InputEvent = FakeEvent;
  globalThis.DragEvent = FakeEvent;
  globalThis.MutationObserver = FakeMutationObserver;
  globalThis.HTMLInputElement = class {};
  globalThis.HTMLTextAreaElement = class {};
  Object.defineProperty(globalThis.HTMLInputElement.prototype, "value", {
    set(value) { this.value = value; },
  });
  Object.defineProperty(globalThis.HTMLTextAreaElement.prototype, "value", {
    set(value) { this.value = value; },
  });
  globalThis.document = document;
  globalThis.window = window;
  globalThis.chrome = {
    runtime: {
      id: "extension-id",
      onMessage: { addListener: (registered) => { listener = registered; } },
    },
  };
  await import(`../src/locator-engine.js?inspection=${Date.now()}`);
  await import(`../src/content.js?inspection=${Date.now()}`);

  const htmlResult = await command(listener, "page.getHTML", {
    maxChars: 10_000,
    maxDepth: 10,
    excludeSelectors: ["script"],
  });
  assert.equal(htmlResult.success, true);
  assert.equal(htmlResult.result.html.includes("top-secret"), false);
  assert.equal(htmlResult.result.html.includes("[REDACTED]"), true);
  assert.equal(htmlResult.result.html.includes("window.secret"), false);
  assert.equal(htmlResult.result.redacted, true);

  const textFirst = await command(listener, "page.getText", { maxChars: 8 });
  assert.equal(textFirst.result.text.length, 8);
  assert.notEqual(textFirst.result.nextCursor, "");
  const textSecond = await command(listener, "page.getText", {
    maxChars: 100,
    cursor: textFirst.result.nextCursor,
  });
  assert.equal(textSecond.result.text.includes("top-secret"), false);

  const queried = await command(listener, "page.query", {
    locator: { role: "button", name: "Save settings" },
    limit: 10,
  });
  assert.equal(queried.result.matchCount, 1);
  assert.equal(queried.result.elements[0].reference.documentId, "document-1");

  const detailed = await command(listener, "page.getElement", {
    locator: { element: queried.result.elements[0].reference },
    maxHTMLChars: 1_000,
  });
  assert.equal(detailed.result.element.id, "save");
  assert.equal(detailed.result.element.states.editable, false);

  const secret = await command(listener, "page.getElement", {
    locator: { css: "#password" },
    maxHTMLChars: 1_000,
  });
  assert.equal(secret.result.element.value, "[REDACTED]");
  assert.equal(secret.result.element.attributes.value, "[REDACTED]");

  const snapshot = await command(listener, "page.snapshot", {
    interactiveOnly: true,
    maxDepth: 10,
    maxNodes: 10,
    includeShadowDOM: true,
  });
  assert.equal(snapshot.result.nodeCount, 4);
  assert.deepEqual(
    snapshot.result.nodes.map((node) => node.tagName),
    ["BUTTON", "INPUT", "SELECT", "INPUT"],
  );
  assert.equal(snapshot.result.nodes[0].reference.documentId, "document-1");
  assert.equal(snapshot.result.truncated, false);

  const truncatedSnapshot = await command(listener, "page.snapshot", {
    interactiveOnly: true,
    maxDepth: 10,
    maxNodes: 1,
  });
  assert.equal(truncatedSnapshot.result.nodeCount, 1);
  assert.equal(truncatedSnapshot.result.truncated, true);
  assert.equal(truncatedSnapshot.result.warnings.length, 1);

  const immediateWaits = [
    { condition: "loadState", readyState: "complete" },
    { condition: "url", urlPattern: "https://example.com/*" },
    { condition: "element", locator: { css: "#save" }, elementState: "visible" },
    { condition: "text", expected: "Save settings", matchOperator: "contains" },
    {
      condition: "value",
      locator: { css: "#country" },
      expected: "",
      matchOperator: "equals",
    },
    { condition: "count", locator: { css: "input" }, count: 2, countOperator: "equals" },
    {
      condition: "attribute",
      locator: { css: "#save" },
      attribute: "id",
      attributeState: "equals",
      expected: "save",
    },
  ];
  for (const params of immediateWaits) {
    const waited = await command(listener, "page.wait", { ...params, internalTimeoutMs: 100 });
    assert.equal(waited.success, true, JSON.stringify(waited.error));
    assert.equal(waited.result.matched, true);
    assert.equal(waited.result.mode, "immediate");
    assert.equal(JSON.stringify(waited.result).includes("top-secret"), false);
  }
  const sensitiveWait = await command(listener, "page.wait", {
    condition: "value",
    locator: { css: "#password" },
    expected: "top-secret",
    matchOperator: "equals",
    internalTimeoutMs: 100,
  });
  assert.equal(sensitiveWait.success, false);
  assert.equal(sensitiveWait.error.code, "INVALID_MESSAGE");

  const typed = await command(listener, "page.type", {
    locator: { css: "#password" },
    text: "abc",
    backend: "content",
  });
  assert.equal(typed.success, true);
  assert.equal(typed.result.value, "[REDACTED]");
  assert.equal(password.value, "top-secretabc");
  const appended = await command(listener, "page.fill", {
    locator: { css: "#password" },
    value: "xyz",
    clear: false,
  });
  assert.equal(appended.result.element.value, "[REDACTED]");
  assert.equal(password.value, "top-secretabcxyz");

  const hovered = await command(listener, "page.hover", {
    locator: { css: "#save" },
  });
  assert.equal(hovered.result.element.id, "save");
  assert.equal(button.events.includes("mousemove"), true);

  await command(listener, "page.focus", { locator: { css: "#save" } });
  assert.equal(button.focused, true);
  await command(listener, "page.blur", { locator: { css: "#save" } });
  assert.equal(button.focused, false);

  await command(listener, "page.click", {
    locator: { css: "#save" },
    button: "right",
  });
  assert.equal(button.events.includes("contextmenu"), true);
  await command(listener, "page.click", {
    locator: { css: "#save" },
    clickCount: 2,
  });
  assert.equal(button.events.includes("dblclick"), true);

  await command(listener, "page.press", {
    locator: { css: "#password" },
    key: "A",
    modifiers: ["Control"],
  });
  assert.equal(password.events.includes("keydown"), true);
  const cleared = await command(listener, "page.clear", { locator: { css: "#password" } });
  assert.equal(cleared.result.value, "[REDACTED]");
  assert.equal(password.value, "");

  const selected = await command(listener, "page.select", {
    locator: { css: "#country" },
    values: ["Canada"],
  });
  assert.deepEqual(selected.result.selectedValues, ["CA"]);
  assert.equal(country.options[1].selected, true);

  document.pointTargets = [checkbox];
  const checkedResult = await command(listener, "page.setChecked", {
    locator: { css: "#terms" },
    checked: true,
  });
  assert.equal(checkedResult.result.checked, true);

  const pageScroll = await command(listener, "page.scroll", { deltaY: 200 });
  assert.equal(pageScroll.result.target, "page");
  assert.equal(window.scrollY, 320);
  const elementScroll = await command(listener, "page.scroll", {
    locator: { css: "#scroll-box" },
    deltaX: 25,
    deltaY: 50,
  });
  assert.deepEqual(elementScroll.result.scroll, { left: 25, top: 50 });

  document.pointTargets = [dragSource, dragTarget];
  const dragged = await command(listener, "page.drag", {
    source: { css: "#drag-source" },
    targetLocator: { css: "#drag-target" },
  });
  assert.equal(dragged.result.source.id, "drag-source");
  assert.equal(dragTarget.events.includes("drop"), true);

  const disappearing = command(listener, "page.wait", {
    condition: "element",
    locator: { css: "#drag-target" },
    elementState: "detached",
    mode: "event",
    internalTimeoutMs: 100,
  });
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(FakeMutationObserver.activeCount(), 1);
  main.children = main.children.filter((child) => child !== dragTarget);
  main.childNodes = main.childNodes.filter((child) => child !== dragTarget);
  dragTarget.isConnected = false;
  FakeMutationObserver.flush();
  const disappeared = await disappearing;
  assert.equal(disappeared.success, true);
  assert.equal(disappeared.result.mode, "event");
  assert.equal(disappeared.result.matchCount, 0);

  const timedOut = await command(listener, "page.wait", {
    condition: "element",
    locator: { css: "#missing" },
    elementState: "attached",
    mode: "event",
    internalTimeoutMs: 5,
  });
  assert.equal(timedOut.success, false);
  assert.equal(timedOut.error.code, "TIMEOUT");
  assert.equal(timedOut.error.retryable, true);

  const cancelledOperation = "cancelled-wait";
  const cancelledWait = command(listener, "page.wait", {
    condition: "element",
    locator: { css: "#missing" },
    elementState: "attached",
    mode: "event",
    internalTimeoutMs: 1_000,
  }, cancelledOperation);
  await new Promise((resolve) => setTimeout(resolve, 0));
  listener({
    type: "MCP_BROWSER_CANCEL",
    bridgeVersion: "1.5",
    operationId: cancelledOperation,
  }, { id: "extension-id" }, () => undefined);
  const cancelled = await cancelledWait;
  assert.equal(cancelled.success, false);
  assert.equal(cancelled.error.code, "CANCELLED");
  assert.equal(FakeMutationObserver.activeCount(), 0);

  const dispatched = await command(listener, "page.dispatch", {
    locator: { css: "#save" },
    eventType: "app:save",
    detail: { source: "test" },
  });
  assert.equal(dispatched.result.eventType, "app:save");
  assert.equal(button.events.includes("app:save"), true);

  const submitted = await command(listener, "page.submit", {
    locator: { css: "#settings-form" },
  });
  assert.equal(submitted.result.submitted, true);
  assert.equal(form.submitted, true);

  const unavailable = await command(listener, "page.focus", {
    locator: { css: "#save" },
    backend: "cdp",
  });
  assert.equal(unavailable.success, false);
  assert.equal(unavailable.error.code, "CAPABILITY_UNAVAILABLE");
});

function command(listener, name, params, operationId = `${name}-${Date.now()}-${Math.random()}`) {
  return new Promise((resolve, reject) => {
    const handled = listener({
      type: "MCP_BROWSER_COMMAND",
      bridgeVersion: "1.5",
      operationId,
      command: name,
      params,
      frameId: 0,
      documentId: "document-1",
    }, { id: "extension-id" }, resolve);
    if (!handled) reject(new Error(`Command ${name} was not handled`));
  });
}

class FakeDocument {
  constructor(documentElement) {
    this.nodeType = 9;
    this.documentElement = documentElement;
    this.body = documentElement.querySelector("body");
    this.title = "Settings";
    this.readyState = "complete";
    this.contentType = "text/html";
    this.characterSet = "UTF-8";
    this.listeners = new Map();
    documentElement.scrollWidth = 1_280;
    documentElement.scrollHeight = 2_000;
    setRoot(documentElement, this);
  }

  querySelectorAll(selector) {
    return [this.documentElement, ...descendants(this.documentElement)]
      .filter((candidate) => matches(candidate, selector));
  }

  getElementById(id) {
    return this.querySelectorAll("*").find((candidate) => candidate.id === id) || null;
  }

  elementFromPoint() {
    if (this.pointTargets?.length) return this.pointTargets.shift();
    return this.querySelector("button");
  }

  querySelector(selector) {
    return this.querySelectorAll(selector)[0] || null;
  }

  addEventListener(event, listener) {
    const listeners = this.listeners.get(event) || new Set();
    listeners.add(listener);
    this.listeners.set(event, listeners);
  }

  removeEventListener(event, listener) {
    this.listeners.get(event)?.delete(listener);
  }
}

function element(tagName, { id = "", attributes = {}, children = [] } = {}) {
  const attributeMap = new Map(Object.entries(attributes));
  if (id) attributeMap.set("id", id);
  const candidate = {
    nodeType: 1,
    tagName: tagName.toUpperCase(),
    id,
    className: "",
    attributes: [...attributeMap].map(([name, value]) => ({ name, value })),
    childNodes: children,
    children: children.filter((child) => child.nodeType === 1),
    style: { display: "block", visibility: "visible", opacity: "1", pointerEvents: "auto" },
    hidden: false,
    disabled: false,
    isConnected: true,
    isContentEditable: false,
    value: "",
    type: attributes.type || "",
    events: [],
    get textContent() {
      return this.childNodes.map((child) => child.textContent || child.nodeValue || "").join("");
    },
    set textContent(value) {
      this.childNodes = [textNode(value)];
      this.children = [];
    },
    getAttribute(name) {
      return attributeMap.has(name) ? attributeMap.get(name) : null;
    },
    hasAttribute(name) {
      return attributeMap.has(name);
    },
    getBoundingClientRect() {
      return { x: 0, y: 0, left: 0, top: 0, right: 100, bottom: 30, width: 100, height: 30 };
    },
    querySelectorAll(selector) {
      return descendants(this).filter((child) => matches(child, selector));
    },
    querySelector(selector) {
      return this.querySelectorAll(selector)[0] || null;
    },
    contains(other) {
      return this === other || descendants(this).includes(other);
    },
    getRootNode() {
      return this.root;
    },
    scrollIntoView() {},
    scrollBy({ left = 0, top = 0 }) {
      this.scrollLeft = (this.scrollLeft || 0) + left;
      this.scrollTop = (this.scrollTop || 0) + top;
    },
    dispatchEvent(event) {
      this.events.push(event.type);
      return !event.defaultPrevented;
    },
    click() {
      if (["checkbox", "radio"].includes(this.type)) this.checked = true;
      this.events.push("click");
    },
    focus() { this.focused = true; },
    blur() { this.focused = false; },
  };
  for (const child of children) child.parentNode = candidate;
  return candidate;
}

function textNode(value) {
  return { nodeType: 3, nodeValue: value, textContent: value };
}

function descendants(root) {
  return root.children.flatMap((child) => [child, ...descendants(child)]);
}

function matches(candidate, selector) {
  if (selector === "*") return true;
  if (selector.startsWith("#")) return candidate.id === selector.slice(1);
  return candidate.tagName === selector.toUpperCase();
}

function setRoot(element, root) {
  element.root = root;
  for (const child of element.children) setRoot(child, root);
}

function fakeWindow() {
  const listeners = new Map();
  const window = {
    location: { href: "https://example.com/settings" },
    name: "",
    innerWidth: 1_280,
    innerHeight: 720,
    devicePixelRatio: 2,
    scrollX: 0,
    scrollY: 120,
    getComputedStyle: (candidate) => candidate.style,
    requestAnimationFrame: (callback) => callback(),
    scrollBy({ left = 0, top = 0 }) {
      this.scrollX += left;
      this.scrollY += top;
    },
    addEventListener(event, listener) {
      const current = listeners.get(event) || new Set();
      current.add(listener);
      listeners.set(event, current);
    },
    removeEventListener(event, listener) {
      listeners.get(event)?.delete(listener);
    },
  };
  window.top = window;
  return window;
}

class FakeEvent {
  constructor(type, init = {}) {
    this.type = type;
    this.defaultPrevented = false;
    Object.assign(this, init);
  }
}

class FakeMutationObserver {
  static active = new Set();

  constructor(callback) {
    this.callback = callback;
  }

  observe() {
    FakeMutationObserver.active.add(this);
  }

  disconnect() {
    FakeMutationObserver.active.delete(this);
  }

  static flush() {
    for (const observer of [...FakeMutationObserver.active]) observer.callback([]);
  }

  static activeCount() {
    return FakeMutationObserver.active.size;
  }
}
