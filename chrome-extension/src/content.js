(() => {
  const BRIDGE_VERSION = "1.0";
  if (globalThis.__mcpBrowserControlVersion === BRIDGE_VERSION) {
    return;
  }
  if (globalThis.__mcpBrowserControlLoaded) {
    return;
  }
  globalThis.__mcpBrowserControlLoaded = true;
  globalThis.__mcpBrowserControlVersion = BRIDGE_VERSION;

  chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
    if (sender?.id !== chrome.runtime.id) {
      return false;
    }
    if (message?.type === "MCP_BROWSER_BRIDGE_READY") {
      sendResponse({ ready: true, bridgeVersion: BRIDGE_VERSION });
      return false;
    }
    if (
      message?.type !== "MCP_BROWSER_COMMAND"
      || message.bridgeVersion !== BRIDGE_VERSION
      || !message.params
      || typeof message.params !== "object"
      || Array.isArray(message.params)
    ) {
      return false;
    }

    Promise.resolve()
      .then(() => dispatch(message.command, message.params || {}))
      .then((result) => sendResponse({ success: true, result }))
      .catch((error) => {
        sendResponse({
          success: false,
          error: {
            code: error.code || "INTERNAL_ERROR",
            message: error.message || "Page command failed",
            retryable: Boolean(error.retryable),
          },
        });
      });
    return true;
  });

  function dispatch(command, params) {
    switch (command) {
      case "page.getHTML":
        return getHTML();
      case "page.getHTMLBySelector":
        return getHTMLBySelector(params);
      case "page.click":
        return click(params);
      case "page.fill":
        return fill(params);
      default:
        throw commandError("INVALID_COMMAND", `Unknown page command "${command}"`);
    }
  }

  function getHTML() {
    return {
      html: document.documentElement.outerHTML,
      url: window.location.href,
      title: document.title,
      timestamp: new Date().toISOString(),
    };
  }

  function getHTMLBySelector(params) {
    const selector = requiredSelector(params.selector);
    const elements = [...document.querySelectorAll(selector)];
    return {
      selector,
      count: elements.length,
      html: elements.map((element) => element.outerHTML).join("\n"),
      elements: elements.map((element, index) => describeElement(element, index)),
      timestamp: new Date().toISOString(),
    };
  }

  async function click(params) {
    const element = resolveElement(params);
    await ensureActionable(element);
    element.click();
    return {
      element: describeElement(element, params.index || 0),
      timestamp: new Date().toISOString(),
    };
  }

  async function fill(params) {
    const element = resolveElement(params);
    if (!["INPUT", "TEXTAREA", "SELECT"].includes(element.tagName)) {
      throw commandError("INVALID_MESSAGE", `${element.tagName} does not accept input`);
    }
    if (params.value === undefined || params.value === null) {
      throw commandError("INVALID_MESSAGE", "value is required");
    }

    await ensureActionable(element);
    element.focus();
    const value = String(params.value);
    if (element.tagName === "SELECT") {
      const option = [...element.options].find(
        (candidate) => candidate.value === value || candidate.text === value,
      );
      if (!option) {
        throw commandError("ELEMENT_NOT_FOUND", `Select option "${value}" was not found`);
      }
      element.value = option.value;
    } else {
      if (params.clear !== false) {
        setNativeValue(element, "");
      }
      setNativeValue(element, value);
    }
    element.dispatchEvent(new InputEvent("input", { bubbles: true, inputType: "insertText" }));
    element.dispatchEvent(new Event("change", { bubbles: true }));

    return {
      element: {
        ...describeElement(element, params.index || 0),
        value: element.type === "password" ? "[REDACTED]" : element.value,
      },
      timestamp: new Date().toISOString(),
    };
  }

  function resolveElement(params) {
    if (params.coordinates) {
      const { x, y } = params.coordinates;
      const element = document.elementFromPoint(x, y);
      if (!element) {
        throw commandError("ELEMENT_NOT_FOUND", `No element found at (${x}, ${y})`);
      }
      return element;
    }

    const selector = requiredSelector(params.selector);
    const elements = [...document.querySelectorAll(selector)];
    const index = Number.isInteger(params.index) ? params.index : 0;
    if (!elements[index]) {
      throw commandError(
        "ELEMENT_NOT_FOUND",
        `No element found for selector "${selector}" at index ${index}`,
      );
    }
    return elements[index];
  }

  async function ensureActionable(element) {
    if (element.disabled) {
      throw commandError("INVALID_MESSAGE", "Element is disabled");
    }
    let rect = element.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) {
      throw commandError("ELEMENT_NOT_FOUND", "Element is not visible");
    }
    const inViewport =
      rect.bottom >= 0 &&
      rect.right >= 0 &&
      rect.top <= window.innerHeight &&
      rect.left <= window.innerWidth;
    if (!inViewport) {
      element.scrollIntoView({ block: "center", inline: "center" });
      await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      rect = element.getBoundingClientRect();
      if (rect.width <= 0 || rect.height <= 0) {
        throw commandError("ELEMENT_NOT_FOUND", "Element is not visible after scrolling");
      }
    }
  }

  function requiredSelector(value) {
    if (typeof value !== "string" || value.trim() === "") {
      throw commandError("INVALID_MESSAGE", "selector is required");
    }
    return value;
  }

  function describeElement(element, index) {
    const rect = element.getBoundingClientRect();
    return {
      index,
      tagName: element.tagName,
      id: element.id,
      className: String(element.className || ""),
      text: String(element.textContent || "").trim().slice(0, 500),
      attributes: Object.fromEntries(
        [...element.attributes].map((attribute) => [
          attribute.name,
          attribute.name === "value" && element.type === "password"
            ? "[REDACTED]"
            : attribute.value,
        ]),
      ),
      boundingBox: {
        x: rect.x,
        y: rect.y,
        width: rect.width,
        height: rect.height,
      },
    };
  }

  function setNativeValue(element, value) {
    if (element.tagName === "SELECT") {
      element.value = value;
      return;
    }
    const prototype =
      element.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    const setter = Object.getOwnPropertyDescriptor(prototype, "value")?.set;
    if (setter) {
      setter.call(element, value);
    } else {
      element.value = value;
    }
  }

  function commandError(code, message) {
    const error = new Error(message);
    error.code = code;
    return error;
  }
})();
