(() => {
  const BRIDGE_VERSION = "1.3";
  const DEFAULT_MAX_CHARS = 100_000;
  const DEFAULT_MAX_DEPTH = 50;
  const DEFAULT_QUERY_LIMIT = 25;
  const MAX_TEXT_SCAN_CHARS = 2_000_001;
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
        frameId: Number.isInteger(message.frameId) ? message.frameId : 0,
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
      case "page.info":
        return getPageInfo(context);
      case "page.getHTML":
        return getHTML(params, context);
      case "page.getHTMLBySelector":
        return getHTMLBySelector(params, context);
      case "page.getText":
        return getText(params, context);
      case "page.query":
        return queryElements(params, context);
      case "page.getElement":
        return getElement(params, context);
      case "page.snapshot":
        return getSnapshot(params, context);
      case "page.click":
        return click(params, context);
      case "page.fill":
        return fill(params, context);
      default:
        throw commandError("INVALID_COMMAND", `Unknown page command "${command}"`);
    }
  }

  function getPageInfo(context) {
    return {
      url: String(window.location.href).slice(0, 4_096),
      title: String(document.title).slice(0, 1_000),
      readyState: document.readyState,
      contentType: document.contentType || "",
      characterSet: document.characterSet || "",
      viewport: {
        width: window.innerWidth,
        height: window.innerHeight,
        devicePixelRatio: window.devicePixelRatio,
      },
      scroll: {
        x: window.scrollX,
        y: window.scrollY,
        width: document.documentElement.scrollWidth,
        height: document.documentElement.scrollHeight,
      },
      frame: {
        frameId: context.frameId,
        documentId: context.documentId,
        name: String(window.name || "").slice(0, 500),
        isTop: window === window.top,
      },
      timestamp: new Date().toISOString(),
    };
  }

  function getHTML(params) {
    const maxChars = params.maxChars ?? DEFAULT_MAX_CHARS;
    const maxDepth = params.maxDepth ?? DEFAULT_MAX_DEPTH;
    const roots = selectRoots(params.includeSelectors);
    const excluded = selectExcluded(params.excludeSelectors);
    const serialized = serializeElements(roots, { maxChars, maxDepth, excluded });
    return {
      html: serialized.value,
      url: String(window.location.href).slice(0, 4_096),
      title: String(document.title).slice(0, 1_000),
      returnedChars: serialized.value.length,
      truncated: serialized.truncated,
      redacted: serialized.redacted,
      warnings: inspectionWarnings(serialized),
      timestamp: new Date().toISOString(),
    };
  }

  function getHTMLBySelector(params, context) {
    const selector = requiredSelector(params.selector);
    let elements;
    try {
      elements = [...document.querySelectorAll(selector)];
    } catch {
      throw commandError("INVALID_MESSAGE", "The CSS selector is invalid");
    }
    const serialized = serializeElements(elements, {
      maxChars: DEFAULT_MAX_CHARS,
      maxDepth: DEFAULT_MAX_DEPTH,
      excluded: new Set(),
    });
    return {
      selector,
      count: elements.length,
      html: serialized.value,
      returnedChars: serialized.value.length,
      truncated: serialized.truncated,
      redacted: serialized.redacted,
      warnings: [
        ...inspectionWarnings(serialized),
        ...(elements.length > 100 ? ["Element metadata was truncated at 100 entries"] : []),
      ],
      elements: elements.slice(0, 100).map((element, index) =>
        locatorEngine.describeElement(element, index, context.documentId)),
      elementsTruncated: elements.length > 100,
      timestamp: new Date().toISOString(),
    };
  }

  function getText(params) {
    const maxChars = params.maxChars ?? DEFAULT_MAX_CHARS;
    const offset = parseCursor(params.cursor);
    const roots = selectRoots(params.includeSelectors);
    const excluded = selectExcluded(params.excludeSelectors);
    const scanLimit = Math.min(MAX_TEXT_SCAN_CHARS, offset + maxChars + 1);
    const collected = [];
    let scannedChars = 0;
    let scanTruncated = false;
    let redacted = false;

    const append = (value) => {
      if (scanTruncated) return;
      const normalized = normalizeText(value);
      if (!normalized) return;
      const separator = collected.length > 0 ? " " : "";
      const remaining = scanLimit - scannedChars;
      const chunk = `${separator}${normalized}`;
      if (chunk.length > remaining) {
        collected.push(chunk.slice(0, Math.max(0, remaining)));
        scannedChars = scanLimit;
        scanTruncated = true;
        return;
      }
      collected.push(chunk);
      scannedChars += chunk.length;
    };

    const visit = (node) => {
      if (scanTruncated) return;
      if (node.nodeType === Node.TEXT_NODE) {
        append(node.nodeValue);
        return;
      }
      if (node.nodeType !== Node.ELEMENT_NODE) return;
      if (excluded.has(node) || !locatorEngine.isVisible(node)) return;
      if (isSensitiveElement(node)) {
        append("[REDACTED]");
        redacted = true;
        return;
      }
      for (const child of node.childNodes) visit(child);
      if (node.shadowRoot) {
        for (const child of node.shadowRoot.childNodes) visit(child);
      }
    };
    for (const root of roots) visit(root);

    const allText = collected.join("");
    const text = allText.slice(offset, offset + maxChars);
    const hasMore = scanTruncated || allText.length > offset + maxChars;
    return {
      text,
      returnedChars: text.length,
      cursor: String(offset),
      nextCursor: hasMore ? String(offset + text.length) : "",
      truncated: hasMore,
      redacted,
      warnings: [
        ...(hasMore ? ["Visible text was paginated"] : []),
        ...(redacted ? ["Sensitive field values were redacted"] : []),
      ],
      timestamp: new Date().toISOString(),
    };
  }

  function queryElements(params, context) {
    const offset = parseCursor(params.cursor);
    const limit = params.limit ?? DEFAULT_QUERY_LIMIT;
    const result = locatorEngine.query(params.locator, { documentId: context.documentId });
    const page = result.matches.slice(offset, offset + limit);
    const nextOffset = offset + page.length;
    return {
      locator: params.locator,
      matchCount: result.matches.length,
      elements: page.map((element, index) =>
        locatorEngine.describeElement(element, offset + index, context.documentId)),
      cursor: String(offset),
      nextCursor: nextOffset < result.matches.length ? String(nextOffset) : "",
      timestamp: new Date().toISOString(),
    };
  }

  function getElement(params, context) {
    const result = locatorEngine.resolve(params.locator, {
      documentId: context.documentId,
      strictDefault: true,
    });
    const element = result.element;
    const maxHTMLChars = params.maxHTMLChars ?? 20_000;
    const serialized = serializeElements([element], {
      maxChars: maxHTMLChars,
      maxDepth: DEFAULT_MAX_DEPTH,
      excluded: new Set(),
    });
    const rawValue = sensitiveValue(element);
    const value = rawValue === undefined ? undefined : String(rawValue).slice(0, 20_000);
    return {
      matchCount: result.count,
      element: {
        ...locatorEngine.describeElement(element, params.locator.nth ?? 0, context.documentId),
        html: serialized.value,
        value,
        valueTruncated: rawValue !== undefined && String(rawValue).length > 20_000,
        states: {
          checked: booleanState(element.checked, element.getAttribute?.("aria-checked")),
          selected: booleanState(element.selected, element.getAttribute?.("aria-selected")),
          expanded: booleanState(undefined, element.getAttribute?.("aria-expanded")),
          pressed: booleanState(undefined, element.getAttribute?.("aria-pressed")),
          editable: isEditable(element),
        },
      },
      truncated: serialized.truncated,
      redacted: serialized.redacted,
      warnings: inspectionWarnings(serialized),
      timestamp: new Date().toISOString(),
    };
  }

  function getSnapshot(params, context) {
    const interactiveOnly = params.interactiveOnly ?? false;
    const maxDepth = params.maxDepth ?? 20;
    const maxNodes = params.maxNodes ?? 1_000;
    const includeShadowDOM = params.includeShadowDOM ?? true;
    const nodes = [];
    let truncated = false;

    const visit = (element, parentId, depth, inShadowRoot = false) => {
      if (truncated) return;
      if (depth > maxDepth) {
        truncated = true;
        return;
      }
      if (!locatorEngine.isVisible(element)) return;

      const include = !interactiveOnly || isInteractiveElement(element);
      let currentParent = parentId;
      if (include) {
        if (nodes.length >= maxNodes) {
          truncated = true;
          return;
        }
        const described = locatorEngine.describeElement(element, nodes.length, context.documentId);
        const nodeId = nodes.length;
        nodes.push({
          nodeId,
          parentId,
          depth,
          tagName: described.tagName,
          role: described.role,
          name: described.name,
          text: directText(element).slice(0, 300),
          states: {
            visible: described.visible,
            enabled: described.enabled,
            checked: booleanState(element.checked, element.getAttribute?.("aria-checked")),
            selected: booleanState(element.selected, element.getAttribute?.("aria-selected")),
            expanded: booleanState(undefined, element.getAttribute?.("aria-expanded")),
            pressed: booleanState(undefined, element.getAttribute?.("aria-pressed")),
            editable: isEditable(element),
          },
          reference: described.reference,
          shadowRoot: inShadowRoot,
        });
        currentParent = nodeId;
      }

      for (const child of element.children) {
        visit(child, currentParent, depth + 1, inShadowRoot);
      }
      if (includeShadowDOM && element.shadowRoot) {
        for (const child of element.shadowRoot.children) {
          visit(child, currentParent, depth + 1, true);
        }
      }
    };
    visit(document.documentElement, null, 0);
    return {
      frame: {
        frameId: context.frameId,
        documentId: context.documentId,
        url: String(window.location.href).slice(0, 4_096),
      },
      interactiveOnly,
      nodeCount: nodes.length,
      nodes,
      truncated,
      warnings: truncated ? ["Semantic snapshot was truncated by maxDepth or maxNodes"] : [],
      timestamp: new Date().toISOString(),
    };
  }

  function isInteractiveElement(element) {
    const role = element.getAttribute?.("role") || "";
    return [
      "button", "checkbox", "combobox", "link", "listbox", "menuitem", "option",
      "radio", "searchbox", "slider", "spinbutton", "switch", "tab", "textbox",
    ].includes(role)
      || ["A", "BUTTON", "INPUT", "SELECT", "TEXTAREA"].includes(element.tagName)
      || element.isContentEditable
      || element.tabIndex >= 0;
  }

  function directText(element) {
    return normalizeText([...element.childNodes]
      .filter((child) => child.nodeType === Node.TEXT_NODE)
      .map((child) => child.nodeValue)
      .join(" "));
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

  function selectRoots(selectors) {
    if (!selectors || selectors.length === 0) {
      return [document.documentElement];
    }
    try {
      const roots = [...new Set(selectors.flatMap((selector) =>
        [...document.querySelectorAll(selector)]))];
      return roots.filter((candidate) =>
        !roots.some((other) => other !== candidate && other.contains(candidate)));
    } catch {
      throw commandError("INVALID_MESSAGE", "An include CSS selector is invalid");
    }
  }

  function selectExcluded(selectors) {
    if (!selectors || selectors.length === 0) {
      return new Set();
    }
    try {
      return new Set(selectors.flatMap((selector) =>
        [...document.querySelectorAll(selector)]));
    } catch {
      throw commandError("INVALID_MESSAGE", "An exclude CSS selector is invalid");
    }
  }

  function serializeElements(elements, { maxChars, maxDepth, excluded }) {
    let value = "";
    let truncated = false;
    let redacted = false;

    const append = (chunk) => {
      if (truncated || !chunk) return;
      const remaining = maxChars - value.length;
      if (chunk.length > remaining) {
        value += chunk.slice(0, Math.max(0, remaining));
        truncated = true;
        return;
      }
      value += chunk;
    };

    const serialize = (node, depth) => {
      if (truncated) return;
      if (node.nodeType === Node.TEXT_NODE) {
        append(escapeHTML(node.nodeValue || ""));
        return;
      }
      if (node.nodeType !== Node.ELEMENT_NODE || excluded.has(node)) return;

      const tagName = node.tagName.toLowerCase();
      const sensitive = isSensitiveElement(node);
      const attributes = [...node.attributes].map((attribute) => {
        let attributeValue = attribute.value;
        if (
          (sensitive && attribute.name.toLowerCase() === "value")
          || /(?:password|secret|token|authorization|cookie|api[-_]?key)/i.test(attribute.name)
        ) {
          attributeValue = "[REDACTED]";
          redacted = true;
        }
        return ` ${attribute.name}="${escapeAttribute(attributeValue)}"`;
      }).join("");
      append(`<${tagName}${attributes}>`);
      if (VOID_ELEMENTS.has(tagName)) return;

      if (sensitive) {
        append("[REDACTED]");
        redacted = true;
      } else if (depth >= maxDepth) {
        if (node.childNodes.length > 0) truncated = true;
      } else {
        for (const child of node.childNodes) serialize(child, depth + 1);
      }
      if (!truncated) append(`</${tagName}>`);
    };

    elements.forEach((element, index) => {
      if (index > 0) append("\n");
      serialize(element, 0);
    });
    return { value, truncated, redacted };
  }

  function inspectionWarnings(result) {
    const warnings = [];
    if (result.truncated) warnings.push("Output was truncated by the requested limits");
    if (result.redacted) warnings.push("Sensitive field values were redacted");
    return warnings;
  }

  function isSensitiveElement(element) {
    if (!["INPUT", "TEXTAREA", "SELECT"].includes(element.tagName)) {
      return false;
    }
    const identity = [
      element.type,
      element.id,
      element.getAttribute?.("name"),
      element.getAttribute?.("autocomplete"),
    ].filter(Boolean).join(" ");
    return /(?:password|secret|token|credential|authorization|cookie|api[-_]?key)/i.test(identity);
  }

  function sensitiveValue(element) {
    if (isSensitiveElement(element)) return "[REDACTED]";
    if (["INPUT", "TEXTAREA", "SELECT"].includes(element.tagName)) return element.value;
    if (element.isContentEditable) return normalizeText(element.textContent);
    return undefined;
  }

  function booleanState(property, ariaValue) {
    if (typeof property === "boolean") return property;
    if (ariaValue === "true") return true;
    if (ariaValue === "false") return false;
    return null;
  }

  function isEditable(element) {
    return element.isContentEditable
      || ["INPUT", "TEXTAREA", "SELECT"].includes(element.tagName);
  }

  function parseCursor(cursor) {
    return cursor ? Number.parseInt(cursor, 10) : 0;
  }

  function normalizeText(value) {
    return String(value || "").replace(/\s+/g, " ").trim();
  }

  function escapeHTML(value) {
    return String(value)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;");
  }

  function escapeAttribute(value) {
    return escapeHTML(value).replaceAll('"', "&quot;");
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

  const VOID_ELEMENTS = new Set([
    "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta",
    "param", "source", "track", "wbr",
  ]);
})();
