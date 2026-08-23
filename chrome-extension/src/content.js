(() => {
  const BRIDGE_VERSION = "1.1";
  if (globalThis.__mcpBrowserControlVersion === BRIDGE_VERSION) {
    return;
  }
  if (globalThis.__mcpBrowserControlLoaded) {
    return;
  }
  globalThis.__mcpBrowserControlLoaded = true;
  globalThis.__mcpBrowserControlVersion = BRIDGE_VERSION;

  const locatorFactory = globalThis.__mcpBrowserLocatorEngine;
  if (!locatorFactory || typeof locatorFactory.create !== "function") {
    throw new Error("MCP browser locator engine is unavailable");
  }
  const locatorEngine = locatorFactory.create({ document, window });

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
      .then(() => dispatch(message.command, message.params || {}, {
        documentId: message.documentId || "",
      }))
      .then((result) => sendResponse({ success: true, result }))
      .catch((error) => {
        sendResponse({
          success: false,
          error: {
            code: error.code || "INTERNAL_ERROR",
            message: error.message || "Page command failed",
            retryable: Boolean(error.retryable),
            ...(error.details && typeof error.details === "object"
              ? { details: error.details }
              : {}),
          },
        });
      });
    return true;
  });

  function dispatch(command, params, context) {
    switch (command) {
      case "page.getHTML":
        return getHTML();
      case "page.getHTMLBySelector":
        return getHTMLBySelector(params, context);
      case "page.click":
        return click(params, context);
      case "page.fill":
        return fill(params, context);
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

  function getHTMLBySelector(params, context) {
    const selector = requiredSelector(params.selector);
    const elements = [...document.querySelectorAll(selector)];
    return {
      selector,
      count: elements.length,
      html: elements.map((element) => element.outerHTML).join("\n"),
      elements: elements.map((element, index) =>
        locatorEngine.describeElement(element, index, context.documentId)),
      timestamp: new Date().toISOString(),
    };
  }

  async function click(params, context) {
    const resolved = resolveElement(params, context, true);
    const { element } = resolved;
    await locatorEngine.ensureActionable(element, { pointer: true });
    element.click();
    return {
      matchCount: resolved.count,
      element: locatorEngine.describeElement(
        element,
        resolved.index,
        context.documentId,
      ),
      timestamp: new Date().toISOString(),
    };
  }

  async function fill(params, context) {
    const resolved = resolveElement(params, context, true);
    const { element } = resolved;
    if (!["INPUT", "TEXTAREA", "SELECT"].includes(element.tagName) && !element.isContentEditable) {
      throw commandError("INVALID_MESSAGE", `${element.tagName} does not accept input`);
    }
    if (params.value === undefined || params.value === null) {
      throw commandError("INVALID_MESSAGE", "value is required");
    }

    await locatorEngine.ensureActionable(element);
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
    } else if (!element.isContentEditable) {
      if (params.clear !== false) {
        setNativeValue(element, "");
      }
      setNativeValue(element, value);
    } else {
      if (params.clear !== false) {
        element.textContent = "";
      }
      element.textContent += value;
    }
    element.dispatchEvent(new InputEvent("input", { bubbles: true, inputType: "insertText" }));
    element.dispatchEvent(new Event("change", { bubbles: true }));

    return {
      element: {
        ...locatorEngine.describeElement(element, resolved.index, context.documentId),
        value: element.type === "password"
          ? "[REDACTED]"
          : element.isContentEditable
            ? element.textContent
            : element.value,
      },
      matchCount: resolved.count,
      timestamp: new Date().toISOString(),
    };
  }

  function resolveElement(params, context, strictDefault) {
    let locator = params.locator;
    if (!locator && params.coordinates) {
      locator = { coordinates: params.coordinates };
    }
    if (!locator) {
      locator = {
        css: requiredSelector(params.selector),
        ...(Number.isInteger(params.index) ? { nth: params.index } : {}),
      };
    }
    const resolved = locatorEngine.resolve(locator, {
      documentId: context.documentId,
      strictDefault,
    });
    return {
      ...resolved,
      index: locator.nth ?? 0,
    };
  }

  function requiredSelector(value) {
    if (typeof value !== "string" || value.trim() === "") {
      throw commandError("INVALID_MESSAGE", "selector is required");
    }
    return value;
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
