import assert from "node:assert/strict";
import test from "node:test";

await import("../src/locator-engine.js");

const createEngine = globalThis.__mcpBrowserLocatorEngine.create;

test("locator engine resolves every strategy and open shadow roots", () => {
  const save = element("button", { id: "save", text: "Save" });
  const email = element("input", {
    id: "email",
    attributes: {
      placeholder: "name@example.com",
      "data-testid": "email-input",
    },
  });
  const label = element("label", { text: "Email", attributes: { for: "email" } });
  label.control = email;
  const logo = element("img", { attributes: { alt: "Company logo" } });
  const close = element("button", { text: "Close", attributes: { title: "Dismiss" } });
  const shadowButton = element("button", { id: "shadow-save", text: "Shadow Save" });
  const shadow = new FakeRoot([shadowButton]);
  const host = element("section");
  host.shadowRoot = shadow;
  const document = new FakeDocument([save, email, label, logo, close, host]);
  const engine = createEngine({ document, window: fakeWindow() });

  const cases = [
    [{ css: "#save" }, save],
    [{ xpath: "//button", nth: 0 }, save],
    [{ text: "Save" }, save],
    [{ role: "button", name: "Save" }, save],
    [{ label: "Email" }, email],
    [{ placeholder: "name@example.com" }, email],
    [{ alt: "Company logo" }, logo],
    [{ title: "Dismiss" }, close],
    [{ testId: "email-input" }, email],
    [{ coordinates: { x: 10, y: 10 } }, save],
  ];
  document.hit = save;
  for (const [locator, expected] of cases) {
    assert.equal(engine.resolve(locator).element, expected, JSON.stringify(locator));
  }

  assert.equal(engine.query({ css: "#shadow-save" }).matches.length, 0);
  assert.equal(
    engine.resolve({ css: "#shadow-save", includeShadowDOM: true }).element,
    shadowButton,
  );
});

test("strict resolution reports zero, ambiguous, and nth diagnostics", () => {
  const first = element("button", { text: "Save" });
  const second = element("button", { text: "Save" });
  const document = new FakeDocument([first, second]);
  const engine = createEngine({ document, window: fakeWindow() });

  assert.throws(
    () => engine.resolve({ role: "button", name: "Save" }, { strictDefault: true }),
    (error) => error.code === "STRICT_MODE_VIOLATION"
      && error.details.matchCount === 2
      && error.details.candidates.length === 2,
  );
  assert.throws(
    () => engine.resolve({ css: "#missing" }, { strictDefault: true }),
    (error) => error.code === "ELEMENT_NOT_FOUND" && error.details.matchCount === 0,
  );
  assert.equal(
    engine.resolve({ role: "button", name: "Save", nth: 1 }, { strictDefault: true }).element,
    second,
  );
  assert.equal(
    engine.resolve({ role: "button", name: "Save", strict: false }, { strictDefault: true }).element,
    first,
  );
});

test("element references are document scoped, stable, and expire", () => {
  let currentTime = 1_000;
  let nextID = 0;
  const save = element("button", { text: "Save" });
  const document = new FakeDocument([save]);
  const engine = createEngine({
    document,
    window: fakeWindow(),
    now: () => currentTime,
    createID: () => `element-${++nextID}`,
    referenceTTLMS: 50,
  });

  const first = engine.createReference(save, "document-1");
  const second = engine.createReference(save, "document-1");
  assert.deepEqual(first, second);
  const described = engine.describeElement(save, 0, "document-1");
  assert.deepEqual(described.reference, first);
  assert.equal(described.visible, true);
  assert.equal(described.enabled, true);
  assert.equal(engine.resolve({ element: first }, { documentId: "document-1" }).element, save);
  assert.throws(
    () => engine.resolve({ element: first }, { documentId: "document-2" }),
    (error) => error.code === "STALE_TARGET",
  );

  currentTime += 51;
  assert.throws(
    () => engine.resolve({ element: first }, { documentId: "document-1" }),
    (error) => error.code === "STALE_TARGET",
  );
});

test("actionability rejects hidden, disabled, detached, and obscured elements", async () => {
  const target = element("button", { text: "Save" });
  const covering = element("div", { text: "Overlay" });
  const document = new FakeDocument([target, covering]);
  const engine = createEngine({ document, window: fakeWindow() });

  target.style.display = "none";
  await assert.rejects(
    engine.ensureActionable(target),
    (error) => error.code === "ELEMENT_NOT_FOUND" && error.details.reason === "not-visible",
  );
  target.style.display = "block";
  target.disabled = true;
  await assert.rejects(
    engine.ensureActionable(target),
    (error) => error.code === "INVALID_MESSAGE" && error.details.reason === "disabled",
  );
  target.disabled = false;
  target.isConnected = false;
  await assert.rejects(
    engine.ensureActionable(target),
    (error) => error.code === "STALE_TARGET",
  );
  target.isConnected = true;
  document.hit = covering;
  await assert.rejects(
    engine.ensureActionable(target, { pointer: true }),
    (error) => error.code === "INVALID_MESSAGE" && error.details.reason === "obscured",
  );
  document.hit = target;
  await engine.ensureActionable(target, { pointer: true });
});

class FakeRoot {
  constructor(children = []) {
    this.children = children;
    for (const child of children) {
      child.parentNode = this;
      child.root = this;
    }
  }

  querySelectorAll(selector) {
    return descendants(this.children).filter((candidate) => matches(candidate, selector));
  }

  querySelector(selector) {
    return this.querySelectorAll(selector)[0] || null;
  }

  getElementById(id) {
    return this.querySelectorAll("*").find((candidate) => candidate.id === id) || null;
  }

  elementFromPoint() {
    return this.hit || null;
  }
}

class FakeDocument extends FakeRoot {
  constructor(children) {
    super(children);
    this.nodeType = 9;
    this.hit = null;
  }

  evaluate(expression, root) {
    if (expression !== "//button") {
      throw new Error("unsupported test XPath");
    }
    const nodes = root.querySelectorAll("button");
    return {
      snapshotLength: nodes.length,
      snapshotItem: (index) => nodes[index],
    };
  }
}

function element(tagName, { id = "", text = "", attributes = {}, children = [] } = {}) {
  const attributeMap = new Map(Object.entries(attributes));
  if (id) {
    attributeMap.set("id", id);
  }
  const candidate = {
    nodeType: 1,
    tagName: tagName.toUpperCase(),
    id,
    className: "",
    textContent: text,
    children,
    attributes: [...attributeMap].map(([name, value]) => ({ name, value })),
    style: { display: "block", visibility: "visible", opacity: "1", pointerEvents: "auto" },
    disabled: false,
    hidden: false,
    isConnected: true,
    isContentEditable: false,
    type: attributes.type || "",
    multiple: false,
    size: 0,
    getAttribute(name) {
      return attributeMap.has(name) ? attributeMap.get(name) : null;
    },
    hasAttribute(name) {
      return attributeMap.has(name);
    },
    getBoundingClientRect() {
      return { x: 0, y: 0, left: 0, top: 0, right: 100, bottom: 30, width: 100, height: 30 };
    },
    scrollIntoView() {},
    contains(other) {
      return this === other || descendants(this.children).includes(other);
    },
    querySelectorAll(selector) {
      return descendants(this.children).filter((child) => matches(child, selector));
    },
    querySelector(selector) {
      return this.querySelectorAll(selector)[0] || null;
    },
    getRootNode() {
      return this.root;
    },
  };
  for (const child of children) {
    child.parentNode = candidate;
    child.root = candidate.root;
  }
  return candidate;
}

function descendants(children) {
  return children.flatMap((child) => [child, ...descendants(child.children || [])]);
}

function matches(candidate, selector) {
  if (selector === "*") return true;
  if (selector.startsWith("#")) return candidate.id === selector.slice(1);
  if (selector.includes(",")) {
    return selector.split(",").some((part) => matches(candidate, part.trim()));
  }
  return candidate.tagName === selector.toUpperCase();
}

function fakeWindow() {
  return {
    innerWidth: 1280,
    innerHeight: 720,
    getComputedStyle: (candidate) => candidate.style,
    requestAnimationFrame: (callback) => callback(),
  };
}
