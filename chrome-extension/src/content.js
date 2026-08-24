(() => {
  const BRIDGE_VERSION = "1.6";
  const DEFAULT_MAX_CHARS = 100_000;
  const DEFAULT_MAX_DEPTH = 50;
  const DEFAULT_QUERY_LIMIT = 25;
  const MAX_TEXT_SCAN_CHARS = 2_000_001;
  const NON_TEXT_INPUT_TYPES = new Set([
    "button",
    "checkbox",
    "file",
    "hidden",
    "image",
    "radio",
    "reset",
    "submit",
  ]);
  const activeOperations = new Map();
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
      message?.type === "MCP_BROWSER_CANCEL" &&
      message.bridgeVersion === BRIDGE_VERSION &&
      typeof message.operationId === "string"
    ) {
      const controller = activeOperations.get(message.operationId);
      controller?.abort();
      sendResponse({ cancelled: Boolean(controller) });
      return false;
    }
    if (
      message?.type !== "MCP_BROWSER_COMMAND" ||
      message.bridgeVersion !== BRIDGE_VERSION ||
      typeof message.operationId !== "string" ||
      message.operationId.length > 200 ||
      !message.params ||
      typeof message.params !== "object" ||
      Array.isArray(message.params)
    ) {
      return false;
    }

    if (activeOperations.has(message.operationId)) {
      sendResponse({
        success: false,
        error: {
          code: "INVALID_MESSAGE",
          message: "The page operation ID is already active",
          retryable: false,
        },
      });
      return false;
    }
    const controller = new AbortController();
    activeOperations.set(message.operationId, controller);

    Promise.resolve()
      .then(() =>
        dispatch(message.command, message.params || {}, {
          frameId: Number.isInteger(message.frameId) ? message.frameId : 0,
          documentId: message.documentId || "",
          signal: controller.signal,
        }),
      )
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
      })
      .finally(() => activeOperations.delete(message.operationId));
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
      case "page.prepareTrustedInput":
        return prepareTrustedInput(params, context);
      case "page.readTrustedInputResult":
        return readTrustedInputResult(params, context);
      case "page.click":
        return click(params, context);
      case "page.fill":
        return fill(params, context);
      case "page.hover":
        return hover(params, context);
      case "page.focus":
        return focusElement(params, context);
      case "page.blur":
        return blurElement(params, context);
      case "page.type":
        return typeText(params, context);
      case "page.clear":
        return clearElement(params, context);
      case "page.press":
        return pressKey(params, context);
      case "page.select":
        return selectOptions(params, context);
      case "page.setChecked":
        return setChecked(params, context);
      case "page.scroll":
        return scrollTarget(params, context);
      case "page.drag":
        return dragAndDrop(params, context);
      case "page.dispatch":
        return dispatchEvent(params, context);
      case "page.submit":
        return submitForm(params, context);
      case "page.wait":
        return waitForCondition(params, context);
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
    const serialized = serializeElements(roots, {
      maxChars,
      maxDepth,
      excluded,
    });
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
      elements: elements
        .slice(0, 100)
        .map((element, index) => locatorEngine.describeElement(element, index, context.documentId)),
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
    const result = locatorEngine.query(params.locator, {
      documentId: context.documentId,
    });
    const page = result.matches.slice(offset, offset + limit);
    const nextOffset = offset + page.length;
    return {
      locator: params.locator,
      matchCount: result.matches.length,
      elements: page.map((element, index) =>
        locatorEngine.describeElement(element, offset + index, context.documentId),
      ),
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
    return (
      [
        "button",
        "checkbox",
        "combobox",
        "link",
        "listbox",
        "menuitem",
        "option",
        "radio",
        "searchbox",
        "slider",
        "spinbutton",
        "switch",
        "tab",
        "textbox",
      ].includes(role) ||
      ["A", "BUTTON", "INPUT", "SELECT", "TEXTAREA"].includes(element.tagName) ||
      element.isContentEditable ||
      element.tabIndex >= 0
    );
  }

  function directText(element) {
    return normalizeText(
      [...element.childNodes]
        .filter((child) => child.nodeType === Node.TEXT_NODE)
        .map((child) => child.nodeValue)
        .join(" "),
    );
  }

  async function click(params, context) {
    assertContentBackend(params.backend);
    const resolved = resolveElement(params, context, true);
    const { element } = resolved;
    await locatorEngine.ensureActionable(element, { pointer: true });
    const button = params.button ?? "left";
    const count = params.clickCount ?? 1;
    for (let iteration = 0; iteration < count; iteration += 1) {
      dispatchMouseSequence(element, button);
    }
    if (count === 2 && button === "left") {
      element.dispatchEvent(mouseEvent("dblclick", element, 0, 2));
    }
    return interactionResult(element, resolved, context, {
      button,
      clickCount: count,
    });
  }

  async function prepareTrustedInput(params, context) {
    const command = params.command;
    const inputParams = params.inputParams || {};
    if (command === "page.scroll" && !hasElementAddress(inputParams)) {
      return {
        target: "page",
        point: trustedInputPoint({
          left: 0,
          top: 0,
          width: window.innerWidth,
          height: window.innerHeight,
        }),
        timestamp: new Date().toISOString(),
      };
    }

    const resolved = resolveElement(inputParams, context, true);
    const element = resolved.element;
    let rect;
    switch (command) {
      case "page.click":
      case "page.hover":
      case "page.scroll":
        rect = await locatorEngine.ensureActionable(element, { pointer: true });
        break;
      case "page.setChecked": {
        if (element.tagName !== "INPUT" || !["checkbox", "radio"].includes(element.type)) {
          throw commandError("INVALID_MESSAGE", "The matched element is not checkable");
        }
        const desired = inputParams.checked ?? !element.checked;
        if (element.type === "radio" && !desired) {
          throw commandError("INVALID_MESSAGE", "A radio input cannot be unchecked directly");
        }
        rect = await locatorEngine.ensureActionable(element, { pointer: true });
        return {
          matchCount: resolved.count,
          element: locatorEngine.describeElement(element, resolved.index, context.documentId),
          point: trustedInputPoint(rect),
          skip: element.checked === desired,
          timestamp: new Date().toISOString(),
        };
      }
      case "page.fill":
        if (element.tagName === "SELECT") {
          throw commandError(
            "CAPABILITY_UNAVAILABLE",
            "Trusted fill does not support select controls; use browser_select_option",
          );
        }
        assertEditable(element);
        rect = await locatorEngine.ensureActionable(element);
        element.focus({ preventScroll: true });
        break;
      case "page.type":
      case "page.clear":
        assertEditable(element);
        rect = await locatorEngine.ensureActionable(element);
        element.focus({ preventScroll: true });
        break;
      case "page.press":
        rect = await locatorEngine.ensureActionable(element);
        element.focus({ preventScroll: true });
        break;
      default:
        throw commandError(
          "CAPABILITY_UNAVAILABLE",
          `Trusted CDP input is unavailable for ${String(command)}`,
        );
    }
    return {
      matchCount: resolved.count,
      element: locatorEngine.describeElement(element, resolved.index, context.documentId),
      point: trustedInputPoint(rect),
      timestamp: new Date().toISOString(),
    };
  }

  async function readTrustedInputResult(params, context) {
    const command = params.command;
    const inputParams = params.inputParams || {};
    await new Promise((resolve) => window.requestAnimationFrame(resolve));
    if (command === "page.scroll" && !hasElementAddress(inputParams)) {
      return {
        target: "page",
        backend: "cdp",
        scroll: { x: window.scrollX, y: window.scrollY },
        timestamp: new Date().toISOString(),
      };
    }
    const resolved = resolveElement(inputParams, context, true);
    const extra = { backend: "cdp" };
    switch (command) {
      case "page.click":
        extra.button = inputParams.button ?? "left";
        extra.clickCount = inputParams.clickCount ?? 1;
        break;
      case "page.fill":
      case "page.type":
      case "page.clear":
        extra.value = sensitiveValue(resolved.element);
        break;
      case "page.press":
        extra.key = inputParams.key;
        extra.modifiers = inputParams.modifiers || [];
        break;
      case "page.setChecked":
        extra.checked = Boolean(resolved.element.checked);
        break;
      case "page.scroll":
        extra.scroll = {
          left: resolved.element.scrollLeft,
          top: resolved.element.scrollTop,
        };
        break;
      case "page.hover":
        break;
      default:
        throw commandError(
          "CAPABILITY_UNAVAILABLE",
          `Trusted CDP input is unavailable for ${String(command)}`,
        );
    }
    return interactionResult(resolved.element, resolved, context, extra);
  }

  function hasElementAddress(params) {
    return Boolean(params.selector || params.coordinates || params.locator);
  }

  function trustedInputPoint(rect) {
    if (
      !rect ||
      ![rect.left, rect.top, rect.width, rect.height].every(Number.isFinite) ||
      rect.width <= 0 ||
      rect.height <= 0
    ) {
      throw commandError("INVALID_MESSAGE", "The element has no trusted input point");
    }
    return {
      x: Math.min(Math.max(rect.left + rect.width / 2, 0), Math.max(window.innerWidth - 1, 0)),
      y: Math.min(Math.max(rect.top + rect.height / 2, 0), Math.max(window.innerHeight - 1, 0)),
    };
  }

  async function fill(params, context) {
    assertContentBackend(params.backend);
    const resolved = resolveElement(params, context, true);
    const { element } = resolved;
    if (element.tagName !== "SELECT" && !acceptsTextInput(element)) {
      throw commandError("INVALID_MESSAGE", `${element.tagName} does not accept input`);
    }
    if (element.readOnly) {
      throw commandError("INVALID_MESSAGE", "Element is read-only");
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
      setNativeValue(element, params.clear === false ? `${element.value}${value}` : value);
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
        value: sensitiveValue(element),
      },
      matchCount: resolved.count,
      backend: "content",
      timestamp: new Date().toISOString(),
    };
  }

  async function hover(params, context) {
    assertContentBackend(params.backend);
    const resolved = resolveElement(params, context, true);
    await locatorEngine.ensureActionable(resolved.element, { pointer: true });
    for (const type of ["mouseover", "mouseenter", "mousemove"]) {
      resolved.element.dispatchEvent(mouseEvent(type, resolved.element, 0, 0));
    }
    return interactionResult(resolved.element, resolved, context);
  }

  async function focusElement(params, context) {
    assertContentBackend(params.backend);
    const resolved = resolveElement(params, context, true);
    await locatorEngine.ensureActionable(resolved.element);
    resolved.element.focus({ preventScroll: false });
    return interactionResult(resolved.element, resolved, context);
  }

  async function blurElement(params, context) {
    assertContentBackend(params.backend);
    const resolved = resolveElement(params, context, true);
    resolved.element.blur();
    return interactionResult(resolved.element, resolved, context);
  }

  async function typeText(params, context) {
    assertContentBackend(params.backend);
    const resolved = resolveElement(params, context, true);
    const element = resolved.element;
    assertEditable(element);
    await locatorEngine.ensureActionable(element);
    element.focus();
    for (const character of params.text) {
      const accepted = element.dispatchEvent(keyboardEvent("keydown", character));
      if (accepted) {
        if (element.isContentEditable) {
          element.textContent += character;
        } else {
          setNativeValue(element, `${element.value}${character}`);
        }
        element.dispatchEvent(
          new InputEvent("input", {
            bubbles: true,
            inputType: "insertText",
            data: character,
          }),
        );
      }
      element.dispatchEvent(keyboardEvent("keyup", character));
      if (params.delayMs) await new Promise((resolve) => setTimeout(resolve, params.delayMs));
    }
    element.dispatchEvent(new Event("change", { bubbles: true }));
    return interactionResult(element, resolved, context, {
      value: sensitiveValue(element),
    });
  }

  async function clearElement(params, context) {
    assertContentBackend(params.backend);
    const resolved = resolveElement(params, context, true);
    const element = resolved.element;
    assertEditable(element);
    await locatorEngine.ensureActionable(element);
    if (element.isContentEditable) element.textContent = "";
    else setNativeValue(element, "");
    element.dispatchEvent(new InputEvent("input", { bubbles: true, inputType: "deleteContent" }));
    element.dispatchEvent(new Event("change", { bubbles: true }));
    return interactionResult(element, resolved, context, {
      value: sensitiveValue(element),
    });
  }

  async function pressKey(params, context) {
    assertContentBackend(params.backend);
    const resolved = resolveElement(params, context, true);
    const element = resolved.element;
    await locatorEngine.ensureActionable(element);
    element.focus();
    const down = keyboardEvent("keydown", params.key, params.modifiers);
    const accepted = element.dispatchEvent(down);
    if (accepted && params.key === "Enter") {
      const form = element.form || (element.tagName === "FORM" ? element : null);
      form?.requestSubmit?.();
    }
    element.dispatchEvent(keyboardEvent("keyup", params.key, params.modifiers));
    return interactionResult(element, resolved, context, {
      key: params.key,
      modifiers: params.modifiers || [],
    });
  }

  async function selectOptions(params, context) {
    assertContentBackend(params.backend);
    const resolved = resolveElement(params, context, true);
    const element = resolved.element;
    if (element.tagName !== "SELECT") {
      throw commandError("INVALID_MESSAGE", "The matched element is not a select control");
    }
    await locatorEngine.ensureActionable(element);
    const requested = new Set(params.values);
    const selected = [];
    for (const option of element.options) {
      const matches = requested.has(option.value) || requested.has(option.text);
      option.selected = matches;
      if (matches) selected.push(option.value);
      if (matches && !element.multiple) break;
    }
    if (selected.length === 0 || (!element.multiple && selected.length !== 1)) {
      throw commandError("ELEMENT_NOT_FOUND", "No requested select option was found");
    }
    element.dispatchEvent(new InputEvent("input", { bubbles: true }));
    element.dispatchEvent(new Event("change", { bubbles: true }));
    return interactionResult(element, resolved, context, {
      selectedValues: selected,
    });
  }

  async function setChecked(params, context) {
    assertContentBackend(params.backend);
    const resolved = resolveElement(params, context, true);
    const element = resolved.element;
    if (element.tagName !== "INPUT" || !["checkbox", "radio"].includes(element.type)) {
      throw commandError("INVALID_MESSAGE", "The matched element is not checkable");
    }
    await locatorEngine.ensureActionable(element, { pointer: true });
    const desired = params.checked ?? !element.checked;
    if (element.type === "radio" && !desired) {
      throw commandError("INVALID_MESSAGE", "A radio input cannot be unchecked directly");
    }
    if (element.checked !== desired) {
      element.click();
      if (element.checked !== desired) {
        element.checked = desired;
        element.dispatchEvent(new InputEvent("input", { bubbles: true }));
        element.dispatchEvent(new Event("change", { bubbles: true }));
      }
    }
    return interactionResult(element, resolved, context, {
      checked: element.checked,
    });
  }

  async function scrollTarget(params, context) {
    assertContentBackend(params.backend);
    const hasAddress = params.selector || params.coordinates || params.locator;
    if (!hasAddress) {
      window.scrollBy({
        left: params.deltaX || 0,
        top: params.deltaY || 0,
        behavior: params.behavior || "auto",
      });
      return {
        target: "page",
        backend: "content",
        scroll: { x: window.scrollX, y: window.scrollY },
        timestamp: new Date().toISOString(),
      };
    }
    const resolved = resolveElement(params, context, true);
    resolved.element.scrollBy({
      left: params.deltaX || 0,
      top: params.deltaY || 0,
      behavior: params.behavior || "auto",
    });
    return interactionResult(resolved.element, resolved, context, {
      scroll: {
        left: resolved.element.scrollLeft,
        top: resolved.element.scrollTop,
      },
    });
  }

  async function dragAndDrop(params, context) {
    assertContentBackend(params.backend);
    const source = locatorEngine.resolve(params.source, {
      documentId: context.documentId,
      strictDefault: true,
    });
    const target = params.targetLocator
      ? locatorEngine.resolve(params.targetLocator, {
          documentId: context.documentId,
          strictDefault: true,
        })
      : locatorEngine.resolve(
          { coordinates: params.targetCoordinates },
          {
            documentId: context.documentId,
            strictDefault: true,
          },
        );
    await locatorEngine.ensureActionable(source.element, { pointer: true });
    await locatorEngine.ensureActionable(target.element, { pointer: true });
    const transfer = typeof DataTransfer === "function" ? new DataTransfer() : undefined;
    for (const [element, type] of [
      [source.element, "dragstart"],
      [target.element, "dragenter"],
      [target.element, "dragover"],
      [target.element, "drop"],
      [source.element, "dragend"],
    ]) {
      element.dispatchEvent(createDragEvent(type, transfer));
    }
    return {
      source: locatorEngine.describeElement(
        source.element,
        params.source.nth ?? 0,
        context.documentId,
      ),
      target: locatorEngine.describeElement(
        target.element,
        params.targetLocator?.nth ?? 0,
        context.documentId,
      ),
      backend: "content",
      timestamp: new Date().toISOString(),
    };
  }

  async function dispatchEvent(params, context) {
    assertContentBackend(params.backend);
    const resolved = resolveElement(params, context, true);
    const accepted = resolved.element.dispatchEvent(
      new CustomEvent(params.eventType, {
        bubbles: true,
        cancelable: true,
        composed: true,
        detail: params.detail || {},
      }),
    );
    return interactionResult(resolved.element, resolved, context, {
      eventType: params.eventType,
      defaultPrevented: !accepted,
    });
  }

  async function submitForm(params, context) {
    assertContentBackend(params.backend);
    const resolved = resolveElement(params, context, true);
    const element = resolved.element;
    const form = element.tagName === "FORM" ? element : element.form;
    if (!form) throw commandError("INVALID_MESSAGE", "The matched element has no form");
    await locatorEngine.ensureActionable(element);
    if (typeof form.requestSubmit === "function") form.requestSubmit();
    else form.submit();
    return interactionResult(element, resolved, context, { submitted: true });
  }

  function waitForCondition(params, context) {
    const startedAt = Date.now();
    const requestedTimeout = Number.isInteger(params.internalTimeoutMs)
      ? params.internalTimeoutMs
      : 30_000;
    const timeoutMs = Math.min(Math.max(requestedTimeout, 1), 120_000);
    const requestedMode = params.mode || "auto";
    const pollIntervalMs = params.pollIntervalMs || 100;
    if (context.signal.aborted) {
      return Promise.reject(waitError("CANCELLED", "Wait was cancelled", true));
    }
    const initial = evaluateWaitCondition(params, context);
    if (initial.matched) {
      return Promise.resolve(waitConditionResult(params, context, initial, startedAt, "immediate"));
    }

    return new Promise((resolve, reject) => {
      let settled = false;
      let checking = false;
      let observer = null;
      let pollTimer = null;
      const eventTargets = [];
      const timeout = setTimeout(
        () =>
          finish(() =>
            reject(
              waitError("TIMEOUT", `Wait condition timed out after ${timeoutMs} ms`, true, {
                condition: params.condition,
                elapsedMs: Date.now() - startedAt,
              }),
            ),
          ),
        timeoutMs,
      );

      const cleanup = () => {
        clearTimeout(timeout);
        if (pollTimer !== null) clearInterval(pollTimer);
        observer?.disconnect();
        for (const [target, event, listener] of eventTargets) {
          target.removeEventListener?.(event, listener);
        }
        context.signal.removeEventListener("abort", onAbort);
      };
      const finish = (operation) => {
        if (settled) return;
        settled = true;
        cleanup();
        operation();
      };
      const check = () => {
        if (settled || checking) return;
        checking = true;
        queueMicrotask(() => {
          try {
            const observation = evaluateWaitCondition(params, context);
            if (observation.matched) {
              finish(() =>
                resolve(
                  waitConditionResult(params, context, observation, startedAt, requestedMode),
                ),
              );
            }
          } catch (error) {
            finish(() => reject(error));
          } finally {
            checking = false;
          }
        });
      };
      const onAbort = () =>
        finish(() => reject(waitError("CANCELLED", "Wait was cancelled", true)));
      context.signal.addEventListener("abort", onAbort, { once: true });

      if (requestedMode !== "polling") {
        if (typeof MutationObserver === "function" && document.documentElement) {
          observer = new MutationObserver(check);
          observer.observe(document.documentElement, {
            subtree: true,
            childList: true,
            attributes: true,
            characterData: true,
          });
        }
        for (const [target, events] of [
          [document, ["readystatechange", "input", "change"]],
          [window, ["popstate", "hashchange"]],
        ]) {
          for (const event of events) {
            target.addEventListener?.(event, check);
            eventTargets.push([target, event, check]);
          }
        }
      }
      if (requestedMode !== "event") {
        pollTimer = setInterval(check, pollIntervalMs);
      }
      check();
    });
  }

  function evaluateWaitCondition(params, context) {
    switch (params.condition) {
      case "loadState": {
        const ranks = { loading: 0, interactive: 1, complete: 2 };
        return {
          matched: (ranks[document.readyState] ?? -1) >= ranks[params.readyState],
          readyState: document.readyState,
        };
      }
      case "url": {
        const url = String(window.location.href);
        return {
          matched:
            params.url !== undefined ? url === params.url : wildcardMatch(url, params.urlPattern),
          url: url.slice(0, 4_096),
        };
      }
      case "element":
        return elementStateObservation(params, context);
      case "text":
        return stringObservation(params, context, "text");
      case "value":
        return stringObservation(params, context, "value");
      case "count": {
        const matches = waitLocatorMatches(params.locator, context);
        const operator = params.countOperator || "equals";
        return {
          matched: compareCount(matches.length, params.count, operator),
          matchCount: matches.length,
        };
      }
      case "attribute":
        return attributeObservation(params, context);
      default:
        throw waitError("INVALID_MESSAGE", "Unsupported content wait condition");
    }
  }

  function elementStateObservation(params, context) {
    let matches;
    try {
      matches = waitLocatorMatches(params.locator, context);
    } catch (error) {
      if (
        ["detached", "hidden"].includes(params.elementState) &&
        ["ELEMENT_NOT_FOUND", "STALE_TARGET"].includes(error.code)
      ) {
        matches = [];
      } else {
        throw error;
      }
    }
    const state = params.elementState;
    let matched = false;
    let element;
    if (state === "attached") {
      matched = matches.length > 0;
      [element] = matches;
    } else if (state === "detached") {
      matched = matches.length === 0;
    } else if (state === "visible") {
      element = matches.find((candidate) => locatorEngine.isVisible(candidate));
      matched = Boolean(element);
    } else if (state === "hidden") {
      matched =
        matches.length === 0 || matches.every((candidate) => !locatorEngine.isVisible(candidate));
      [element] = matches;
    } else if (state === "enabled") {
      element = matches.find((candidate) => locatorEngine.isEnabled(candidate));
      matched = Boolean(element);
    } else if (state === "disabled") {
      element = matches.find((candidate) => !locatorEngine.isEnabled(candidate));
      matched =
        matches.length > 0 && matches.every((candidate) => !locatorEngine.isEnabled(candidate));
    }
    return {
      matched,
      matchCount: matches.length,
      element,
      index: element ? matches.indexOf(element) : undefined,
    };
  }

  function stringObservation(params, context, property) {
    const elements = params.locator
      ? waitLocatorMatches(params.locator, context)
      : [document.body || document.documentElement];
    if (property === "value" && elements.some((element) => isSensitiveElement(element))) {
      throw waitError(
        "INVALID_MESSAGE",
        "Sensitive field values cannot be used in wait conditions",
      );
    }
    const values = elements.map((element) =>
      property === "value" ? String(element.value ?? "") : normalizeText(element.textContent),
    );
    const expected = property === "text" ? normalizeText(params.expected) : params.expected;
    const matchingIndex = values.findIndex((value) =>
      stringMatches(
        value,
        expected,
        params.matchOperator || "contains",
        params.caseSensitive ?? true,
      ),
    );
    return {
      matched: matchingIndex >= 0,
      matchCount: elements.length,
      element: matchingIndex >= 0 && params.locator ? elements[matchingIndex] : undefined,
      index: matchingIndex >= 0 ? matchingIndex : undefined,
    };
  }

  function attributeObservation(params, context) {
    const elements = waitLocatorMatches(params.locator, context);
    if (elements.some((element) => isSensitiveWaitAttribute(element, params.attribute))) {
      throw waitError("INVALID_MESSAGE", "Sensitive attributes cannot be used in wait conditions");
    }
    const values = elements.map((element) => element.getAttribute?.(params.attribute));
    const matchingIndex =
      params.attributeState === "present"
        ? values.findIndex((value) => value !== null && value !== undefined)
        : params.attributeState === "absent"
          ? elements.length > 0 && values.every((value) => value === null || value === undefined)
            ? 0
            : -1
          : values.findIndex(
              (value) =>
                value !== null &&
                value !== undefined &&
                stringMatches(
                  String(value),
                  params.expected,
                  params.attributeState,
                  params.caseSensitive ?? true,
                ),
            );
    return {
      matched: matchingIndex >= 0,
      matchCount: elements.length,
      element: matchingIndex >= 0 ? elements[matchingIndex] : undefined,
      index: matchingIndex >= 0 ? matchingIndex : undefined,
    };
  }

  function waitLocatorMatches(locator, context) {
    return locatorEngine.query(locator, { documentId: context.documentId }).matches;
  }

  function isSensitiveWaitAttribute(element, attribute) {
    return (
      /(?:password|secret|token|credential|authorization|cookie|api[-_]?key)/i.test(attribute) ||
      (attribute.toLowerCase() === "value" && isSensitiveElement(element))
    );
  }

  function waitConditionResult(params, context, observation, startedAt, mode) {
    return {
      condition: params.condition,
      matched: true,
      mode,
      elapsedMs: Math.max(0, Date.now() - startedAt),
      ...(observation.readyState ? { readyState: observation.readyState } : {}),
      ...(observation.url ? { url: observation.url } : {}),
      ...(Number.isInteger(observation.matchCount) ? { matchCount: observation.matchCount } : {}),
      ...(observation.element
        ? {
            element: locatorEngine.describeElement(
              observation.element,
              observation.index ?? 0,
              context.documentId,
            ),
          }
        : {}),
      timestamp: new Date().toISOString(),
    };
  }

  function stringMatches(actual, expected, operator, caseSensitive) {
    const left = caseSensitive ? String(actual) : String(actual).toLowerCase();
    const right = caseSensitive ? String(expected) : String(expected).toLowerCase();
    return operator === "equals" ? left === right : left.includes(right);
  }

  function compareCount(actual, expected, operator) {
    if (operator === "atLeast") return actual >= expected;
    if (operator === "atMost") return actual <= expected;
    return actual === expected;
  }

  function wildcardMatch(value, pattern) {
    const expression = String(pattern)
      .split("*")
      .map((part) => part.replace(/[|\\{}()[\]^$+?.]/g, "\\$&"))
      .join(".*");
    return new RegExp(`^${expression}$`).test(value);
  }

  function waitError(code, message, retryable = false, details = undefined) {
    const error = commandError(code, message);
    error.retryable = retryable;
    error.details = details;
    return error;
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

  function interactionResult(element, resolved, context, extra = {}) {
    return {
      matchCount: resolved.count,
      element: locatorEngine.describeElement(element, resolved.index ?? 0, context.documentId),
      backend: "content",
      ...extra,
      timestamp: new Date().toISOString(),
    };
  }

  function assertContentBackend(backend) {
    if (backend === "cdp") {
      throw commandError(
        "CAPABILITY_UNAVAILABLE",
        "Trusted CDP input requires the debugger backend",
      );
    }
  }

  function assertEditable(element) {
    if (!acceptsTextInput(element)) {
      throw commandError("INVALID_MESSAGE", `${element.tagName} is not text editable`);
    }
    if (element.readOnly) {
      throw commandError("INVALID_MESSAGE", "Element is read-only");
    }
  }

  function dispatchMouseSequence(element, buttonName) {
    const button = { left: 0, middle: 1, right: 2 }[buttonName];
    element.dispatchEvent(mouseEvent("mousedown", element, button, 1));
    element.dispatchEvent(mouseEvent("mouseup", element, button, 1));
    if (buttonName === "left") element.click();
    else if (buttonName === "middle") {
      element.dispatchEvent(mouseEvent("auxclick", element, button, 1));
    } else {
      element.dispatchEvent(mouseEvent("contextmenu", element, button, 1));
    }
  }

  function mouseEvent(type, element, button, detail) {
    const rect = element.getBoundingClientRect();
    return new MouseEvent(type, {
      bubbles: true,
      cancelable: true,
      composed: true,
      button,
      buttons: type === "mousedown" ? { 0: 1, 1: 4, 2: 2 }[button] : 0,
      clientX: rect.left + rect.width / 2,
      clientY: rect.top + rect.height / 2,
      detail,
    });
  }

  function keyboardEvent(type, key, modifiers = []) {
    const active = new Set(modifiers || []);
    return new KeyboardEvent(type, {
      key,
      bubbles: true,
      cancelable: true,
      composed: true,
      altKey: active.has("Alt"),
      ctrlKey: active.has("Control"),
      metaKey: active.has("Meta"),
      shiftKey: active.has("Shift"),
    });
  }

  function createDragEvent(type, dataTransfer) {
    if (typeof DragEvent === "function") {
      return new DragEvent(type, {
        bubbles: true,
        cancelable: true,
        composed: true,
        dataTransfer,
      });
    }
    return new CustomEvent(type, {
      bubbles: true,
      cancelable: true,
      composed: true,
    });
  }

  function selectRoots(selectors) {
    if (!selectors || selectors.length === 0) {
      return [document.documentElement];
    }
    try {
      const roots = [
        ...new Set(selectors.flatMap((selector) => [...document.querySelectorAll(selector)])),
      ];
      return roots.filter(
        (candidate) => !roots.some((other) => other !== candidate && other.contains(candidate)),
      );
    } catch {
      throw commandError("INVALID_MESSAGE", "An include CSS selector is invalid");
    }
  }

  function selectExcluded(selectors) {
    if (!selectors || selectors.length === 0) {
      return new Set();
    }
    try {
      return new Set(selectors.flatMap((selector) => [...document.querySelectorAll(selector)]));
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
      const attributes = [...node.attributes]
        .map((attribute) => {
          let attributeValue = attribute.value;
          if (
            (sensitive && attribute.name.toLowerCase() === "value") ||
            /(?:password|secret|token|authorization|cookie|api[-_]?key)/i.test(attribute.name)
          ) {
            attributeValue = "[REDACTED]";
            redacted = true;
          }
          return ` ${attribute.name}="${escapeAttribute(attributeValue)}"`;
        })
        .join("");
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
    ]
      .filter(Boolean)
      .join(" ");
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
    return acceptsTextInput(element) || element.tagName === "SELECT";
  }

  function acceptsTextInput(element) {
    if (element.isContentEditable || element.tagName === "TEXTAREA") return true;
    if (element.tagName !== "INPUT") return false;
    return !NON_TEXT_INPUT_TYPES.has(String(element.type || "text").toLowerCase());
  }

  function parseCursor(cursor) {
    return cursor ? Number.parseInt(cursor, 10) : 0;
  }

  function normalizeText(value) {
    return String(value || "")
      .replace(/\s+/g, " ")
      .trim();
  }

  function escapeHTML(value) {
    return String(value).replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
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
    "area",
    "base",
    "br",
    "col",
    "embed",
    "hr",
    "img",
    "input",
    "link",
    "meta",
    "param",
    "source",
    "track",
    "wbr",
  ]);
})();
