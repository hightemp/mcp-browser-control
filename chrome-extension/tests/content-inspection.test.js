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
  const script = element("script", { children: [textNode("window.secret = true")] });
  const main = element("main", { children: [button, password, script] });
  const body = element("body", { children: [main] });
  const html = element("html", { children: [body] });
  const document = new FakeDocument(html);
  const window = fakeWindow();
  let listener;

  globalThis.Node = { ELEMENT_NODE: 1, TEXT_NODE: 3 };
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
  assert.equal(snapshot.result.nodeCount, 2);
  assert.deepEqual(snapshot.result.nodes.map((node) => node.tagName), ["BUTTON", "INPUT"]);
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
});

function command(listener, name, params) {
  return new Promise((resolve, reject) => {
    const handled = listener({
      type: "MCP_BROWSER_COMMAND",
      bridgeVersion: "1.3",
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
    return this.querySelector("button");
  }

  querySelector(selector) {
    return this.querySelectorAll(selector)[0] || null;
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
  };
  window.top = window;
  return window;
}
