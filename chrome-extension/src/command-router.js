import {
  ErrorCode,
  normalizeError,
  protocolError,
  validateLocator,
  validateTarget,
} from "./protocol.js";

const COMMANDS = Object.freeze({
  "browser.ping": Object.freeze({
    domain: "browser",
    handler: "ping",
    validate: validateEmpty,
  }),
  "windows.list": Object.freeze({
    domain: "windows",
    handler: "list",
    validate: validateEmpty,
  }),
  "windows.get": Object.freeze({
    domain: "windows",
    handler: "get",
    validate: validateWindowTarget,
  }),
  "windows.create": Object.freeze({
    domain: "windows",
    handler: "create",
    validate: validateWindowCreate,
  }),
  "windows.update": Object.freeze({
    domain: "windows",
    handler: "update",
    validate: validateWindowUpdate,
  }),
  "windows.focus": Object.freeze({
    domain: "windows",
    handler: "focus",
    validate: validateWindowTarget,
  }),
  "windows.close": Object.freeze({
    domain: "windows",
    handler: "close",
    validate: validateWindowTarget,
  }),
  "tabs.list": Object.freeze({
    domain: "tabs",
    handler: "list",
    validate: validateEmpty,
  }),
  "tabs.get": Object.freeze({
    domain: "tabs",
    handler: "get",
    validate: validateTabEmpty,
  }),
  "tabs.create": Object.freeze({
    domain: "tabs",
    handler: "create",
    validate: validateTabCreate,
  }),
  "tabs.activate": Object.freeze({
    domain: "tabs",
    handler: "activate",
    validate: validateTabEmpty,
  }),
  "tabs.navigate": Object.freeze({
    domain: "tabs",
    handler: "navigate",
    validate: validateTabNavigate,
  }),
  "tabs.reload": Object.freeze({
    domain: "tabs",
    handler: "reload",
    validate: validateTabReload,
  }),
  "tabs.stop": Object.freeze({
    domain: "tabs",
    handler: "stop",
    validate: validateTabEmpty,
  }),
  "tabs.back": Object.freeze({
    domain: "tabs",
    handler: "back",
    validate: validateTabEmpty,
  }),
  "tabs.forward": Object.freeze({
    domain: "tabs",
    handler: "forward",
    validate: validateTabEmpty,
  }),
  "tabs.move": Object.freeze({
    domain: "tabs",
    handler: "move",
    validate: validateTabMove,
  }),
  "tabs.duplicate": Object.freeze({
    domain: "tabs",
    handler: "duplicate",
    validate: validateTabEmpty,
  }),
  "tabs.close": Object.freeze({
    domain: "tabs",
    handler: "close",
    validate: validateTabEmpty,
  }),
  "tabs.pin": Object.freeze({
    domain: "tabs",
    handler: "pin",
    validate: validateTabPin,
  }),
  "tabs.mute": Object.freeze({
    domain: "tabs",
    handler: "mute",
    validate: validateTabMute,
  }),
  "tabs.getZoom": Object.freeze({
    domain: "tabs",
    handler: "getZoom",
    validate: validateTabEmpty,
  }),
  "tabs.setZoom": Object.freeze({
    domain: "tabs",
    handler: "setZoom",
    validate: validateTabSetZoom,
  }),
  "tabs.group": Object.freeze({
    domain: "tabGroups",
    handler: "group",
    validate: validateTabGroup,
  }),
  "tabs.ungroup": Object.freeze({
    domain: "tabGroups",
    handler: "ungroup",
    validate: validateTabUngroup,
  }),
  "tabGroups.update": Object.freeze({
    domain: "tabGroups",
    handler: "update",
    validate: validateTabGroupUpdate,
  }),
  "sessions.recentlyClosed": Object.freeze({
    domain: "sessions",
    handler: "recentlyClosed",
    validate: validateRecentlyClosed,
  }),
  "sessions.restore": Object.freeze({
    domain: "sessions",
    handler: "restore",
    validate: validateSessionRestore,
  }),
  "cookies.list": Object.freeze({
    domain: "cookies",
    handler: "list",
    validate: validateCookieList,
  }),
  "cookies.get": Object.freeze({
    domain: "cookies",
    handler: "get",
    validate: validateCookieIdentity,
  }),
  "cookies.set": Object.freeze({
    domain: "cookies",
    handler: "set",
    validate: validateCookieSet,
  }),
  "cookies.remove": Object.freeze({
    domain: "cookies",
    handler: "remove",
    validate: validateCookieIdentity,
  }),
  "cookies.listSensitive": Object.freeze({
    domain: "cookies",
    handler: "listSensitive",
    validate: validateCookieList,
  }),
  "cookies.getSensitive": Object.freeze({
    domain: "cookies",
    handler: "getSensitive",
    validate: validateCookieIdentity,
  }),
  "page.info": Object.freeze({
    domain: "page",
    handler: "info",
    validate: validateEmpty,
  }),
  "page.getHTML": Object.freeze({
    domain: "page",
    handler: "getHTML",
    validate: validateGetHTML,
  }),
  "page.getHTMLBySelector": Object.freeze({
    domain: "page",
    handler: "getHTMLBySelector",
    validate: validateSelector,
  }),
  "page.getText": Object.freeze({
    domain: "page",
    handler: "getText",
    validate: validateGetText,
  }),
  "page.query": Object.freeze({
    domain: "page",
    handler: "query",
    validate: validateQuery,
  }),
  "page.getElement": Object.freeze({
    domain: "page",
    handler: "getElement",
    validate: validateGetElement,
  }),
  "page.snapshot": Object.freeze({
    domain: "page",
    handler: "snapshot",
    validate: validateSnapshot,
  }),
  "page.click": Object.freeze({
    domain: "page",
    handler: "click",
    validate: validateAction,
  }),
  "page.fill": Object.freeze({
    domain: "page",
    handler: "fill",
    validate: validateFill,
  }),
  "page.hover": Object.freeze({
    domain: "page",
    handler: "hover",
    validate: validateSimpleAction,
  }),
  "page.focus": Object.freeze({
    domain: "page",
    handler: "focus",
    validate: validateSimpleAction,
  }),
  "page.blur": Object.freeze({
    domain: "page",
    handler: "blur",
    validate: validateSimpleAction,
  }),
  "page.type": Object.freeze({
    domain: "page",
    handler: "type",
    validate: validateType,
  }),
  "page.clear": Object.freeze({
    domain: "page",
    handler: "clear",
    validate: validateSimpleAction,
  }),
  "page.press": Object.freeze({
    domain: "page",
    handler: "press",
    validate: validatePress,
  }),
  "page.select": Object.freeze({
    domain: "page",
    handler: "select",
    validate: validateSelect,
  }),
  "page.setChecked": Object.freeze({
    domain: "page",
    handler: "setChecked",
    validate: validateSetChecked,
  }),
  "page.scroll": Object.freeze({
    domain: "page",
    handler: "scroll",
    validate: validateScroll,
  }),
  "page.drag": Object.freeze({
    domain: "page",
    handler: "drag",
    validate: validateDrag,
  }),
  "page.dispatch": Object.freeze({
    domain: "page",
    handler: "dispatch",
    validate: validateDispatch,
  }),
  "page.submit": Object.freeze({
    domain: "page",
    handler: "submit",
    validate: validateSimpleAction,
  }),
  "page.wait": Object.freeze({
    domain: "page",
    handler: "wait",
    validate: validateWait,
  }),
  "page.screenshot": Object.freeze({
    domain: "page",
    handler: "screenshot",
    validate: validateScreenshot,
  }),
  "page.printToPDF": Object.freeze({
    domain: "page",
    handler: "printToPDF",
    validate: validatePrintToPDF,
  }),
  "accessibility.getTree": Object.freeze({
    domain: "accessibility",
    handler: "getTree",
    validate: validateAccessibilityTree,
  }),
  "emulation.set": Object.freeze({
    domain: "emulation",
    handler: "set",
    validate: validateEmulationSet,
  }),
  "emulation.get": Object.freeze({
    domain: "emulation",
    handler: "get",
    validate: validateEmulationEmpty,
  }),
  "emulation.reset": Object.freeze({
    domain: "emulation",
    handler: "reset",
    validate: validateEmulationReset,
  }),
  "runtime.evaluateIsolated": Object.freeze({
    domain: "evaluation",
    handler: "evaluate",
    validate: validateEvaluation,
  }),
  "cdp.sendReadOnly": Object.freeze({
    domain: "rawCDP",
    handler: "sendReadOnly",
    validate: validateRawCDP,
  }),
  "performance.metrics": Object.freeze({
    domain: "performance",
    handler: "metrics",
    validate: validatePerformanceMetrics,
  }),
  "performance.capture": Object.freeze({
    domain: "performance",
    handler: "capture",
    validate: validatePerformanceCapture,
  }),
  "network.start": Object.freeze({
    domain: "network",
    handler: "start",
    validate: validateNetworkStart,
  }),
  "network.stop": Object.freeze({
    domain: "network",
    handler: "stop",
    validate: validateNetworkEmpty,
  }),
  "network.clear": Object.freeze({
    domain: "network",
    handler: "clear",
    validate: validateNetworkEmpty,
  }),
  "network.read": Object.freeze({
    domain: "network",
    handler: "read",
    validate: validateNetworkRead,
  }),
  "network.getBody": Object.freeze({
    domain: "network",
    handler: "getBody",
    validate: validateNetworkBody,
  }),
  "network.exportHAR": Object.freeze({
    domain: "network",
    handler: "exportHAR",
    validate: validateNetworkHAR,
  }),
  "console.start": Object.freeze({
    domain: "console",
    handler: "start",
    validate: validateConsoleStart,
  }),
  "console.stop": Object.freeze({
    domain: "console",
    handler: "stop",
    validate: validateConsoleEmpty,
  }),
  "console.clear": Object.freeze({
    domain: "console",
    handler: "clear",
    validate: validateConsoleEmpty,
  }),
  "console.read": Object.freeze({
    domain: "console",
    handler: "read",
    validate: validateConsoleRead,
  }),
});

export const COMMAND_NAMES = Object.freeze(Object.keys(COMMANDS));

export class CommandRouter {
  constructor({ getBrowserId, getCapabilities, handlers }) {
    this.getBrowserId = getBrowserId;
    this.getCapabilities = getCapabilities;
    this.handlers = handlers;
    this.activeRequests = new Map();
  }

  async execute(request, respond) {
    if (this.activeRequests.has(request.requestId)) {
      return false;
    }

    const controller = new AbortController();
    this.activeRequests.set(request.requestId, controller);
    let outcome;
    try {
      const result = await this.dispatch(request, controller.signal);
      throwIfCancelled(controller.signal);
      outcome = { success: true, result };
    } catch (error) {
      const normalized = controller.signal.aborted
        ? protocolError(ErrorCode.CANCELLED, "Command was cancelled", true)
        : error;
      outcome = {
        success: false,
        error: normalizeError(normalized, {
          requestId: request.requestId,
          target: request.target,
        }),
      };
    }

    try {
      await respond(outcome);
      return true;
    } finally {
      this.activeRequests.delete(request.requestId);
    }
  }

  cancel(requestId) {
    const controller = this.activeRequests.get(requestId);
    if (!controller) {
      return false;
    }
    controller.abort();
    return true;
  }

  async dispatch(request, signal) {
    throwIfCancelled(signal);
    const definition = COMMANDS[request.command];
    if (!definition) {
      throw protocolError(
        ErrorCode.INVALID_COMMAND,
        `The extension does not support command "${request.command}"`,
      );
    }

    const browserId = await this.getBrowserId();
    validateTarget(request.target, browserId);
    definition.validate(request.params, request.target);

    const capabilities = new Set(await this.getCapabilities());
    if (!capabilities.has(request.command)) {
      throw protocolError(
        ErrorCode.CAPABILITY_UNAVAILABLE,
        `Command "${request.command}" is not available with the current browser permissions`,
      );
    }

    const handler = this.handlers?.[definition.domain]?.[definition.handler];
    if (typeof handler !== "function") {
      throw protocolError(ErrorCode.INTERNAL_ERROR, "The command handler is unavailable");
    }
    throwIfCancelled(signal);
    return handler(request, signal);
  }
}

function validateEmpty(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, []);
}

function validateSelector(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["selector"]);
  assertNonEmptyString(params.selector, "params.selector");
}

function validateGetHTML(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["maxChars", "maxDepth", "includeSelectors", "excludeSelectors"]);
  validateIntegerRange(params.maxChars, "params.maxChars", 1, 1_000_000);
  validateIntegerRange(params.maxDepth, "params.maxDepth", 0, 200);
  validateSelectors(params.includeSelectors, "params.includeSelectors");
  validateSelectors(params.excludeSelectors, "params.excludeSelectors");
}

function validateGetText(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["maxChars", "cursor", "includeSelectors", "excludeSelectors"]);
  validateIntegerRange(params.maxChars, "params.maxChars", 1, 1_000_000);
  validateCursor(params.cursor);
  validateSelectors(params.includeSelectors, "params.includeSelectors");
  validateSelectors(params.excludeSelectors, "params.excludeSelectors");
}

function validateQuery(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["locator", "cursor", "limit"]);
  validateLocator(params.locator, target);
  validateCursor(params.cursor);
  validateIntegerRange(params.limit, "params.limit", 1, 100);
}

function validateGetElement(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["locator", "maxHTMLChars"]);
  validateLocator(params.locator, target);
  validateIntegerRange(params.maxHTMLChars, "params.maxHTMLChars", 1, 100_000);
}

function validateSnapshot(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["interactiveOnly", "maxDepth", "maxNodes", "includeShadowDOM"]);
  validateOptionalBoolean(params.interactiveOnly, "params.interactiveOnly");
  validateIntegerRange(params.maxDepth, "params.maxDepth", 0, 50);
  validateIntegerRange(params.maxNodes, "params.maxNodes", 1, 5_000);
  validateOptionalBoolean(params.includeShadowDOM, "params.includeShadowDOM");
}

function validateSelectors(selectors, path) {
  if (selectors === undefined) {
    return;
  }
  if (
    !Array.isArray(selectors) ||
    selectors.length > 50 ||
    selectors.some((selector) => typeof selector !== "string" || selector.trim() === "")
  ) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      `${path} must contain at most 50 non-empty CSS selectors`,
    );
  }
}

function validateCursor(cursor) {
  if (
    cursor !== undefined &&
    (typeof cursor !== "string" || !/^\d+$/.test(cursor) || Number.parseInt(cursor, 10) > 1_000_000)
  ) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "params.cursor must be a numeric string no greater than 1000000",
    );
  }
}

function validateIntegerRange(value, path, minimum, maximum) {
  if (value !== undefined && (!Number.isInteger(value) || value < minimum || value > maximum)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      `${path} must be between ${minimum} and ${maximum}`,
    );
  }
}

function validateNumberRange(value, path, minimum, maximum) {
  if (value !== undefined && (!Number.isFinite(value) || value < minimum || value > maximum)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      `${path} must be between ${minimum} and ${maximum}`,
    );
  }
}

function validateWindowTarget(params, target) {
  validateEmpty(params);
  requireWindowTarget(target);
}

function validateWindowCreate(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, [
    "urls",
    "type",
    "state",
    "focused",
    "incognito",
    "left",
    "top",
    "width",
    "height",
  ]);
  if (params.urls !== undefined) {
    if (
      !Array.isArray(params.urls) ||
      params.urls.length === 0 ||
      params.urls.length > 50 ||
      params.urls.some((url) => typeof url !== "string" || url.trim() === "")
    ) {
      throw protocolError(
        ErrorCode.INVALID_MESSAGE,
        "params.urls must contain between 1 and 50 non-empty URLs",
      );
    }
  }
  validateEnum(params.type, "params.type", ["normal", "popup"]);
  validateWindowState(params.state);
  validateOptionalBoolean(params.focused, "params.focused");
  validateOptionalBoolean(params.incognito, "params.incognito");
  validateWindowBounds(params);
  assertStateAndBoundsCompatible(params);
}

function validateWindowUpdate(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, [
    "state",
    "focused",
    "drawAttention",
    "left",
    "top",
    "width",
    "height",
  ]);
  if (Object.keys(params).length === 0) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "At least one window update is required");
  }
  requireWindowTarget(target);
  validateWindowState(params.state);
  validateOptionalBoolean(params.focused, "params.focused");
  validateOptionalBoolean(params.drawAttention, "params.drawAttention");
  validateWindowBounds(params);
  assertStateAndBoundsCompatible(params);
}

function validateTabEmpty(params, target) {
  validateEmpty(params);
  validateOptionalTabTarget(target);
}

function validateTabCreate(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["windowId", "url", "index", "active", "pinned"]);
  validateOptionalIdentifier(params.windowId, "params.windowId");
  if (params.url !== undefined) {
    assertNonEmptyString(params.url, "params.url");
  }
  validateOptionalIdentifier(params.index, "params.index");
  validateOptionalBoolean(params.active, "params.active");
  validateOptionalBoolean(params.pinned, "params.pinned");
}

function validateTabNavigate(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["url"]);
  assertNonEmptyString(params.url, "params.url");
  validateOptionalTabTarget(target);
}

function validateTabReload(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["bypassCache"]);
  validateOptionalBoolean(params.bypassCache, "params.bypassCache");
  validateOptionalTabTarget(target);
}

function validateTabMove(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["windowId", "index"]);
  validateOptionalIdentifier(params.windowId, "params.windowId");
  if (!Number.isInteger(params.index) || params.index < -1) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "params.index must be an integer of at least -1",
    );
  }
  validateOptionalTabTarget(target);
}

function validateTabPin(params, target) {
  validateRequiredBoolean(params, target, "pinned");
}

function validateTabMute(params, target) {
  validateRequiredBoolean(params, target, "muted");
}

function validateRequiredBoolean(params, target, property) {
  validateParamsObject(params);
  assertAllowedProperties(params, [property]);
  if (typeof params[property] !== "boolean") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `params.${property} must be a boolean`);
  }
  validateOptionalTabTarget(target);
}

function validateTabSetZoom(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["factor"]);
  if (!Number.isFinite(params.factor) || params.factor < 0.25 || params.factor > 5) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.factor must be between 0.25 and 5");
  }
  validateOptionalTabTarget(target);
}

function validateTabGroup(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["tabIds", "groupId", "windowId"]);
  validateTabIDs(params.tabIds);
  validateOptionalIdentifier(params.groupId, "params.groupId");
  validateOptionalIdentifier(params.windowId, "params.windowId");
  if (params.groupId !== undefined && params.windowId !== undefined) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "params.groupId and params.windowId cannot be used together",
    );
  }
}

function validateTabUngroup(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["tabIds"]);
  validateTabIDs(params.tabIds);
}

function validateTabGroupUpdate(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["groupId", "title", "color", "collapsed"]);
  validateOptionalIdentifier(params.groupId, "params.groupId");
  if (params.groupId === undefined) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.groupId is required");
  }
  if (Object.keys(params).length === 1) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "At least one tab group update is required");
  }
  if (params.title !== undefined && typeof params.title !== "string") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.title must be a string");
  }
  validateEnum(params.color, "params.color", [
    "grey",
    "blue",
    "red",
    "yellow",
    "green",
    "pink",
    "purple",
    "cyan",
    "orange",
  ]);
  validateOptionalBoolean(params.collapsed, "params.collapsed");
}

function validateRecentlyClosed(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["maxResults"]);
  if (
    params.maxResults !== undefined &&
    (!Number.isInteger(params.maxResults) || params.maxResults < 1 || params.maxResults > 25)
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.maxResults must be between 1 and 25");
  }
}

function validateSessionRestore(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["sessionId"]);
  if (params.sessionId !== undefined) {
    assertNonEmptyString(params.sessionId, "params.sessionId");
  }
}

function validateTabIDs(tabIds) {
  if (
    !Array.isArray(tabIds) ||
    tabIds.length === 0 ||
    tabIds.length > 100 ||
    tabIds.some((tabId) => !Number.isInteger(tabId) || tabId < 0) ||
    new Set(tabIds).size !== tabIds.length
  ) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "params.tabIds must contain between 1 and 100 unique non-negative integers",
    );
  }
}

function validateAction(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, [
    "selector",
    "coordinates",
    "locator",
    "index",
    "button",
    "clickCount",
    "backend",
    "waitForNavigation",
  ]);
  validateElementAddress(params, target);
  validateIndex(params.index);
  validateEnum(params.button, "params.button", ["left", "middle", "right"]);
  validateIntegerRange(params.clickCount, "params.clickCount", 1, 2);
  validateInteractionOptions(params);
}

function validateFill(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, [
    "selector",
    "coordinates",
    "locator",
    "index",
    "value",
    "clear",
    "backend",
    "waitForNavigation",
  ]);
  validateElementAddress(params, target);
  validateIndex(params.index);
  if (params.value === undefined || params.value === null) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.value is required");
  }
  if (params.clear !== undefined && typeof params.clear !== "boolean") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.clear must be a boolean");
  }
  validateInteractionOptions(params);
}

function validateSimpleAction(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, [
    "selector",
    "coordinates",
    "locator",
    "index",
    "backend",
    "waitForNavigation",
  ]);
  validateElementAddress(params, target);
  validateIndex(params.index);
  validateInteractionOptions(params);
}

function validateType(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, [
    "selector",
    "coordinates",
    "locator",
    "index",
    "text",
    "delayMs",
    "backend",
    "waitForNavigation",
  ]);
  validateElementAddress(params, target);
  validateIndex(params.index);
  assertNonEmptyString(params.text, "params.text");
  validateIntegerRange(params.delayMs, "params.delayMs", 0, 1_000);
  validateInteractionOptions(params);
}

function validatePress(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, [
    "selector",
    "coordinates",
    "locator",
    "index",
    "key",
    "modifiers",
    "backend",
    "waitForNavigation",
  ]);
  validateElementAddress(params, target);
  validateIndex(params.index);
  assertNonEmptyString(params.key, "params.key");
  if (
    params.modifiers !== undefined &&
    (!Array.isArray(params.modifiers) ||
      params.modifiers.length > 4 ||
      new Set(params.modifiers).size !== params.modifiers.length ||
      params.modifiers.some((modifier) => !["Alt", "Control", "Meta", "Shift"].includes(modifier)))
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.modifiers contains invalid keys");
  }
  validateInteractionOptions(params);
}

function validateSelect(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, [
    "selector",
    "coordinates",
    "locator",
    "index",
    "values",
    "backend",
    "waitForNavigation",
  ]);
  validateElementAddress(params, target);
  validateIndex(params.index);
  if (
    !Array.isArray(params.values) ||
    params.values.length === 0 ||
    params.values.length > 100 ||
    params.values.some((value) => typeof value !== "string")
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.values must contain 1 to 100 strings");
  }
  validateInteractionOptions(params);
}

function validateSetChecked(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, [
    "selector",
    "coordinates",
    "locator",
    "index",
    "checked",
    "backend",
    "waitForNavigation",
  ]);
  validateElementAddress(params, target);
  validateIndex(params.index);
  validateOptionalBoolean(params.checked, "params.checked");
  validateInteractionOptions(params);
}

function validateScroll(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, [
    "selector",
    "coordinates",
    "locator",
    "index",
    "deltaX",
    "deltaY",
    "behavior",
    "backend",
    "waitForNavigation",
  ]);
  validateOptionalElementAddress(params, target);
  validateIndex(params.index);
  for (const property of ["deltaX", "deltaY"]) {
    if (
      params[property] !== undefined &&
      (!Number.isFinite(params[property]) || Math.abs(params[property]) > 1_000_000)
    ) {
      throw protocolError(ErrorCode.INVALID_MESSAGE, `params.${property} is out of range`);
    }
  }
  if (!params.deltaX && !params.deltaY) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "A non-zero scroll delta is required");
  }
  validateEnum(params.behavior, "params.behavior", ["auto", "smooth"]);
  validateInteractionOptions(params);
}

function validateDrag(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, [
    "source",
    "targetLocator",
    "targetCoordinates",
    "backend",
    "waitForNavigation",
  ]);
  validateLocator(params.source, target);
  const targets = [params.targetLocator, params.targetCoordinates].filter(
    (value) => value !== undefined,
  );
  if (targets.length !== 1) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Exactly one drag target locator or coordinates is required",
    );
  }
  if (params.targetLocator !== undefined) validateLocator(params.targetLocator, target);
  if (params.targetCoordinates !== undefined) {
    validateLocator({ coordinates: params.targetCoordinates }, target);
  }
  validateInteractionOptions(params);
}

function validateDispatch(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, [
    "selector",
    "coordinates",
    "locator",
    "index",
    "eventType",
    "detail",
    "backend",
    "waitForNavigation",
  ]);
  validateElementAddress(params, target);
  validateIndex(params.index);
  if (
    typeof params.eventType !== "string" ||
    !/^[A-Za-z][A-Za-z0-9:_-]{0,99}$/.test(params.eventType)
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.eventType is invalid");
  }
  if (
    params.detail !== undefined &&
    (!params.detail || typeof params.detail !== "object" || Array.isArray(params.detail))
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.detail must be an object");
  }
  validateInteractionOptions(params);
}

function validateInteractionOptions(params) {
  validateEnum(params.backend, "params.backend", ["auto", "content", "cdp"]);
  validateOptionalBoolean(params.waitForNavigation, "params.waitForNavigation");
}

function validateWait(params, target) {
  validateParamsObject(params);
  const common = ["condition", "mode", "pollIntervalMs"];
  validateEnum(params.condition, "params.condition", [
    "delay",
    "loadState",
    "url",
    "element",
    "text",
    "value",
    "count",
    "navigation",
    "networkIdle",
    "attribute",
  ]);
  if (typeof params.condition !== "string") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.condition is required");
  }
  validateEnum(params.mode, "params.mode", ["auto", "polling", "event"]);
  validateIntegerRange(params.pollIntervalMs, "params.pollIntervalMs", 25, 1_000);

  switch (params.condition) {
    case "delay":
      assertAllowedProperties(params, [...common, "delayMs"]);
      requireIntegerRange(params.delayMs, "params.delayMs", 0, 120_000);
      break;
    case "loadState":
      assertAllowedProperties(params, [...common, "readyState"]);
      validateEnum(params.readyState, "params.readyState", ["interactive", "complete"]);
      if (params.readyState === undefined) {
        throw protocolError(ErrorCode.INVALID_MESSAGE, "params.readyState is required");
      }
      break;
    case "url": {
      assertAllowedProperties(params, [...common, "url", "urlPattern"]);
      const addresses = [params.url, params.urlPattern].filter((value) => value !== undefined);
      if (addresses.length !== 1) {
        throw protocolError(
          ErrorCode.INVALID_MESSAGE,
          "Exactly one of params.url or params.urlPattern is required",
        );
      }
      assertBoundedString(addresses[0], "URL wait value", 4_096);
      break;
    }
    case "element":
      assertAllowedProperties(params, [...common, "locator", "elementState"]);
      validateLocator(params.locator, target);
      validateEnum(params.elementState, "params.elementState", [
        "attached",
        "detached",
        "visible",
        "hidden",
        "enabled",
        "disabled",
      ]);
      if (params.elementState === undefined) {
        throw protocolError(ErrorCode.INVALID_MESSAGE, "params.elementState is required");
      }
      break;
    case "text":
      assertAllowedProperties(params, [
        ...common,
        "locator",
        "expected",
        "matchOperator",
        "caseSensitive",
      ]);
      if (params.locator !== undefined) validateLocator(params.locator, target);
      validateStringWait(params);
      break;
    case "value":
      assertAllowedProperties(params, [
        ...common,
        "locator",
        "expected",
        "matchOperator",
        "caseSensitive",
      ]);
      validateLocator(params.locator, target);
      validateStringWait(params);
      break;
    case "count":
      assertAllowedProperties(params, [...common, "locator", "count", "countOperator"]);
      validateLocator(params.locator, target);
      requireIntegerRange(params.count, "params.count", 0, 1_000_000);
      validateEnum(params.countOperator, "params.countOperator", ["equals", "atLeast", "atMost"]);
      break;
    case "navigation":
      assertAllowedProperties(params, common);
      break;
    case "networkIdle":
      assertAllowedProperties(params, [...common, "idleMs"]);
      requireIntegerRange(params.idleMs, "params.idleMs", 100, 30_000);
      break;
    case "attribute":
      assertAllowedProperties(params, [
        ...common,
        "locator",
        "attribute",
        "attributeState",
        "expected",
        "caseSensitive",
      ]);
      validateLocator(params.locator, target);
      if (
        typeof params.attribute !== "string" ||
        !/^[A-Za-z0-9:_-]{1,200}$/.test(params.attribute)
      ) {
        throw protocolError(ErrorCode.INVALID_MESSAGE, "params.attribute is invalid");
      }
      if (
        /(?:password|secret|token|credential|authorization|cookie|api[-_]?key)/i.test(
          params.attribute,
        )
      ) {
        throw protocolError(ErrorCode.INVALID_MESSAGE, "params.attribute is sensitive");
      }
      validateEnum(params.attributeState, "params.attributeState", [
        "present",
        "absent",
        "equals",
        "contains",
      ]);
      if (params.attributeState === undefined) {
        throw protocolError(ErrorCode.INVALID_MESSAGE, "params.attributeState is required");
      }
      if (["equals", "contains"].includes(params.attributeState)) {
        assertBoundedString(params.expected, "params.expected", 100_000, true);
      } else if (params.expected !== undefined) {
        throw protocolError(
          ErrorCode.INVALID_MESSAGE,
          "params.expected is only valid for equals or contains attribute waits",
        );
      }
      validateOptionalBoolean(params.caseSensitive, "params.caseSensitive");
      break;
    default:
      throw protocolError(ErrorCode.INVALID_MESSAGE, "params.condition is invalid");
  }
}

function validateScreenshot(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, [
    "capture",
    "format",
    "quality",
    "maxWidth",
    "maxHeight",
    "maxBytes",
  ]);
  validateOptionalTabTarget(target);
  validateEnum(params.capture, "params.capture", ["viewport"]);
  validateEnum(params.format, "params.format", ["png", "jpeg"]);
  validateIntegerRange(params.quality, "params.quality", 0, 100);
  if (params.quality !== undefined && params.format !== "jpeg") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.quality is only valid for JPEG");
  }
  validateIntegerRange(params.maxWidth, "params.maxWidth", 1, 16_384);
  validateIntegerRange(params.maxHeight, "params.maxHeight", 1, 16_384);
  validateIntegerRange(params.maxBytes, "params.maxBytes", 1_024, 2_000_000);
}

function validatePrintToPDF(params, target) {
  validateParamsObject(params);
  validateOptionalTabTarget(target);
  assertAllowedProperties(params, [
    "landscape",
    "printBackground",
    "scale",
    "paperWidth",
    "paperHeight",
    "marginTop",
    "marginBottom",
    "marginLeft",
    "marginRight",
    "pageRanges",
    "preferCSSPageSize",
    "maxBytes",
  ]);
  validateOptionalBoolean(params.landscape, "params.landscape");
  validateOptionalBoolean(params.printBackground, "params.printBackground");
  validateOptionalBoolean(params.preferCSSPageSize, "params.preferCSSPageSize");
  validateNumberRange(params.scale, "params.scale", 0.1, 2);
  validateNumberRange(params.paperWidth, "params.paperWidth", 1, 200);
  validateNumberRange(params.paperHeight, "params.paperHeight", 1, 200);
  for (const field of ["marginTop", "marginBottom", "marginLeft", "marginRight"]) {
    validateNumberRange(params[field], `params.${field}`, 0, 10);
  }
  validatePageRanges(params.pageRanges);
  validateIntegerRange(params.maxBytes, "params.maxBytes", 1_024, 2_000_000);

  const paperWidth = params.paperWidth ?? 8.5;
  const paperHeight = params.paperHeight ?? 11;
  if ((params.marginLeft ?? 0.4) + (params.marginRight ?? 0.4) >= paperWidth) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Horizontal PDF margins exceed paper width");
  }
  if ((params.marginTop ?? 0.4) + (params.marginBottom ?? 0.4) >= paperHeight) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Vertical PDF margins exceed paper height");
  }
}

function validateAccessibilityTree(params, target) {
  validateParamsObject(params);
  validateAccessibilityTarget(target);
  assertAllowedProperties(params, [
    "mode",
    "backendNodeId",
    "fetchRelatives",
    "roles",
    "nameContains",
    "includeIgnored",
    "includeLocators",
    "includeElementReferences",
    "maxDepth",
    "maxNodes",
    "maxProperties",
    "maxValueChars",
    "maxElementReferences",
    "maxBytes",
  ]);
  if (!["full", "partial"].includes(params.mode)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.mode must be full or partial");
  }
  if (params.mode === "full") {
    requireIntegerRange(params.maxDepth, "params.maxDepth", 0, 50);
    if (params.backendNodeId !== undefined || params.fetchRelatives !== undefined) {
      throw protocolError(
        ErrorCode.INVALID_MESSAGE,
        "backendNodeId and fetchRelatives require partial mode",
      );
    }
  } else if (params.mode === "partial") {
    requireIntegerRange(params.backendNodeId, "params.backendNodeId", 1, Number.MAX_SAFE_INTEGER);
    if (typeof params.fetchRelatives !== "boolean" || params.maxDepth !== undefined) {
      throw protocolError(
        ErrorCode.INVALID_MESSAGE,
        "partial mode requires fetchRelatives and does not accept maxDepth",
      );
    }
  }
  if (
    !Array.isArray(params.roles) ||
    params.roles.length > 50 ||
    params.roles.some(
      (role) =>
        typeof role !== "string" ||
        role.length > 100 ||
        role.trim() === "" ||
        role !== role.trim().toLocaleLowerCase(),
    ) ||
    new Set(params.roles).size !== params.roles.length
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.roles contains invalid values");
  }
  assertBoundedString(params.nameContains, "params.nameContains", 500, true);
  for (const field of ["includeIgnored", "includeLocators", "includeElementReferences"]) {
    if (typeof params[field] !== "boolean") {
      throw protocolError(ErrorCode.INVALID_MESSAGE, `params.${field} must be a boolean`);
    }
  }
  requireIntegerRange(params.maxNodes, "params.maxNodes", 1, 5_000);
  requireIntegerRange(params.maxProperties, "params.maxProperties", 0, 50);
  requireIntegerRange(params.maxValueChars, "params.maxValueChars", 1, 2_000);
  requireIntegerRange(params.maxElementReferences, "params.maxElementReferences", 0, 100);
  requireIntegerRange(params.maxBytes, "params.maxBytes", 64 * 1_024, 1_500_000);
  if (!params.includeElementReferences && params.maxElementReferences !== 0) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "maxElementReferences must be zero when element references are disabled",
    );
  }
}

function validateEmulationSet(params, target) {
  validateParamsObject(params);
  validateEmulationTarget(target);
  const fields = [
    "viewport",
    "touch",
    "network",
    "userAgent",
    "locale",
    "timezoneId",
    "geolocation",
    "media",
  ];
  assertAllowedProperties(params, fields);
  if (!fields.some((field) => params[field] !== undefined)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "At least one emulation setting is required");
  }

  if (params.viewport !== undefined) {
    validateParamsObject(params.viewport);
    assertAllowedProperties(params.viewport, [
      "width",
      "height",
      "deviceScaleFactor",
      "mobile",
      "orientation",
    ]);
    requireIntegerRange(params.viewport.width, "params.viewport.width", 1, 10_000);
    requireIntegerRange(params.viewport.height, "params.viewport.height", 1, 10_000);
    requireNumberRange(
      params.viewport.deviceScaleFactor,
      "params.viewport.deviceScaleFactor",
      0.1,
      10,
    );
    if (typeof params.viewport.mobile !== "boolean") {
      throw protocolError(ErrorCode.INVALID_MESSAGE, "params.viewport.mobile must be a boolean");
    }
    validateEnum(params.viewport.orientation, "params.viewport.orientation", [
      "portraitPrimary",
      "portraitSecondary",
      "landscapePrimary",
      "landscapeSecondary",
    ]);
  }

  if (params.touch !== undefined) {
    validateParamsObject(params.touch);
    assertAllowedProperties(params.touch, ["enabled", "maxTouchPoints"]);
    if (typeof params.touch.enabled !== "boolean") {
      throw protocolError(ErrorCode.INVALID_MESSAGE, "params.touch.enabled must be a boolean");
    }
    validateIntegerRange(params.touch.maxTouchPoints, "params.touch.maxTouchPoints", 1, 10);
    if (!params.touch.enabled && params.touch.maxTouchPoints !== undefined) {
      throw protocolError(
        ErrorCode.INVALID_MESSAGE,
        "params.touch.maxTouchPoints requires enabled touch",
      );
    }
  }

  if (params.network !== undefined) {
    validateNonEmptyEmulationObject(params.network, "network", [
      "offline",
      "latencyMs",
      "downloadKbps",
      "uploadKbps",
      "connectionType",
    ]);
    validateOptionalBoolean(params.network.offline, "params.network.offline");
    validateNumberRange(params.network.latencyMs, "params.network.latencyMs", 0, 300_000);
    validateNumberRange(params.network.downloadKbps, "params.network.downloadKbps", 0, 10_000_000);
    validateNumberRange(params.network.uploadKbps, "params.network.uploadKbps", 0, 10_000_000);
    validateEnum(params.network.connectionType, "params.network.connectionType", [
      "none",
      "cellular2g",
      "cellular3g",
      "cellular4g",
      "bluetooth",
      "ethernet",
      "wifi",
      "wimax",
      "other",
    ]);
  }

  if (params.userAgent !== undefined) {
    validateParamsObject(params.userAgent);
    assertAllowedProperties(params.userAgent, ["value", "acceptLanguage", "platform"]);
    validateSafeEmulationString(params.userAgent.value, "params.userAgent.value", 1, 1_000);
    validateSafeEmulationString(
      params.userAgent.acceptLanguage,
      "params.userAgent.acceptLanguage",
      0,
      200,
    );
    validateSafeEmulationString(params.userAgent.platform, "params.userAgent.platform", 0, 100);
  }

  if (params.locale !== undefined) {
    if (
      typeof params.locale !== "string" ||
      params.locale.length > 100 ||
      params.locale !== params.locale.trim() ||
      !/^[A-Za-z]{2,8}(?:[_-][A-Za-z0-9]{1,8})*$/.test(params.locale)
    ) {
      throw protocolError(ErrorCode.INVALID_MESSAGE, "params.locale is invalid");
    }
  }
  if (params.timezoneId !== undefined) {
    if (
      typeof params.timezoneId !== "string" ||
      params.timezoneId.length > 100 ||
      params.timezoneId !== params.timezoneId.trim() ||
      !/^[A-Za-z0-9_+./-]+$/.test(params.timezoneId)
    ) {
      throw protocolError(ErrorCode.INVALID_MESSAGE, "params.timezoneId is invalid");
    }
  }

  if (params.geolocation !== undefined) {
    validateParamsObject(params.geolocation);
    assertAllowedProperties(params.geolocation, [
      "latitude",
      "longitude",
      "accuracy",
      "altitude",
      "heading",
      "speed",
    ]);
    requireNumberRange(params.geolocation.latitude, "params.geolocation.latitude", -90, 90);
    requireNumberRange(params.geolocation.longitude, "params.geolocation.longitude", -180, 180);
    requireNumberRange(params.geolocation.accuracy, "params.geolocation.accuracy", 0, 1_000_000);
    validateNumberRange(
      params.geolocation.altitude,
      "params.geolocation.altitude",
      -10_000,
      100_000,
    );
    validateNumberRange(params.geolocation.heading, "params.geolocation.heading", 0, 360);
    validateNumberRange(params.geolocation.speed, "params.geolocation.speed", 0, 1_000_000);
  }

  if (params.media !== undefined) {
    validateNonEmptyEmulationObject(params.media, "media", [
      "type",
      "colorScheme",
      "reducedMotion",
      "forcedColors",
      "contrast",
    ]);
    validateEnum(params.media.type, "params.media.type", ["screen", "print"]);
    validateEnum(params.media.colorScheme, "params.media.colorScheme", [
      "light",
      "dark",
      "no-preference",
    ]);
    validateEnum(params.media.reducedMotion, "params.media.reducedMotion", [
      "reduce",
      "no-preference",
    ]);
    validateEnum(params.media.forcedColors, "params.media.forcedColors", ["active", "none"]);
    validateEnum(params.media.contrast, "params.media.contrast", [
      "more",
      "less",
      "custom",
      "no-preference",
    ]);
  }
}

function validateEmulationEmpty(params, target) {
  validateEmpty(params);
  validateEmulationTarget(target);
}

function validateEmulationReset(params, target) {
  validateEmpty(params);
  validateEmulationTarget(target);
  if (target?.documentId !== undefined) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Emulation reset is tab-scoped");
  }
}

function validateEmulationTarget(target) {
  if (target === undefined || target === null) return;
  if (!Number.isInteger(target.tabId) || target.tabId < 0) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "target.tabId is required when target is set");
  }
  if (target.windowId !== undefined || (target.frameId !== undefined && target.frameId !== 0)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Emulation commands accept only the root frame of a tab",
    );
  }
}

function validateEvaluation(params, target) {
  validateParamsObject(params);
  validateEvaluationTarget(target);
  assertAllowedProperties(params, [
    "expression",
    "awaitPromise",
    "maxDepth",
    "maxNodes",
    "maxStringChars",
    "maxBytes",
  ]);
  assertBoundedString(params.expression, "params.expression", 32_768);
  if (typeof params.awaitPromise !== "boolean") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.awaitPromise must be a boolean");
  }
  requireIntegerRange(params.maxDepth, "params.maxDepth", 0, 10);
  requireIntegerRange(params.maxNodes, "params.maxNodes", 1, 5_000);
  requireIntegerRange(params.maxStringChars, "params.maxStringChars", 1, 100_000);
  requireIntegerRange(params.maxBytes, "params.maxBytes", 64 * 1_024, 1_000_000);
}

function validateEvaluationTarget(target) {
  if (target === undefined || target === null) return;
  if (!Number.isInteger(target.tabId) || target.tabId < 0) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "target.tabId is required when target is set");
  }
  if (target.windowId !== undefined || (target.frameId !== undefined && target.frameId !== 0)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "JavaScript evaluation accepts only the root frame of a tab",
    );
  }
}

const RAW_CDP_METHODS = Object.freeze([
  "Accessibility.getFullAXTree",
  "Accessibility.getPartialAXTree",
  "Accessibility.queryAXTree",
  "DOM.describeNode",
  "DOM.getBoxModel",
  "Page.getLayoutMetrics",
  "Performance.getMetrics",
]);

function validateRawCDP(params, target) {
  validateParamsObject(params);
  validateRawCDPTarget(target);
  assertAllowedProperties(params, [
    "method",
    "params",
    "maxDepth",
    "maxNodes",
    "maxStringChars",
    "maxBytes",
  ]);
  if (!RAW_CDP_METHODS.includes(params.method)) {
    throw protocolError(ErrorCode.INVALID_COMMAND, "params.method is not allowlisted");
  }
  requireIntegerRange(params.maxDepth, "params.maxDepth", 1, 20);
  requireIntegerRange(params.maxNodes, "params.maxNodes", 2, 5_000);
  requireIntegerRange(params.maxStringChars, "params.maxStringChars", 1, 10_000);
  requireIntegerRange(params.maxBytes, "params.maxBytes", 64 * 1_024, 1_000_000);
  validateParamsObject(params.params);

  const allowed = {
    "Accessibility.getFullAXTree": ["depth"],
    "Accessibility.getPartialAXTree": ["backendNodeId", "fetchRelatives"],
    "Accessibility.queryAXTree": ["backendNodeId", "accessibleName", "role"],
    "DOM.describeNode": ["backendNodeId", "depth"],
    "DOM.getBoxModel": ["backendNodeId"],
    "Page.getLayoutMetrics": [],
    "Performance.getMetrics": [],
  }[params.method];
  assertAllowedProperties(params.params, allowed);
  if (
    [
      "Accessibility.getPartialAXTree",
      "Accessibility.queryAXTree",
      "DOM.describeNode",
      "DOM.getBoxModel",
    ].includes(params.method)
  ) {
    requireIntegerRange(
      params.params.backendNodeId,
      "params.params.backendNodeId",
      1,
      Number.MAX_SAFE_INTEGER,
    );
  }
  if (params.method === "Accessibility.getPartialAXTree") {
    validateOptionalBoolean(params.params.fetchRelatives, "params.params.fetchRelatives");
  }
  if (params.method === "Accessibility.queryAXTree") {
    assertOptionalBoundedString(params.params.accessibleName, "params.params.accessibleName", 500);
    assertOptionalBoundedString(params.params.role, "params.params.role", 100);
  }
  if (params.method === "Accessibility.getFullAXTree") {
    validateIntegerRange(params.params.depth, "params.params.depth", 0, 50);
  }
  if (params.method === "DOM.describeNode") {
    validateIntegerRange(params.params.depth, "params.params.depth", 0, 10);
  }
}

function validateRawCDPTarget(target) {
  if (target === undefined || target === null) return;
  if (!Number.isInteger(target.tabId) || target.tabId < 0) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "target.tabId is required when target is set");
  }
  if (target.windowId !== undefined || (target.frameId !== undefined && target.frameId !== 0)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Raw CDP commands accept only the root frame of a tab",
    );
  }
}

function assertOptionalBoundedString(value, path, maximum) {
  if (value !== undefined && (typeof value !== "string" || [...value].length > maximum)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} exceeds its string limit`);
  }
}

function validatePerformanceMetrics(params, target) {
  validateEmpty(params);
  validatePerformanceTarget(target);
}

function validatePerformanceCapture(params, target) {
  validateParamsObject(params);
  validatePerformanceTarget(target);
  assertAllowedProperties(params, ["kind", "durationMs", "maxBytes"]);
  if (!["trace", "coverage", "cpuProfile", "audits"].includes(params.kind)) {
    throw protocolError(ErrorCode.INVALID_COMMAND, "params.kind is not allowlisted");
  }
  requireIntegerRange(params.durationMs, "params.durationMs", 100, 10_000);
  requireIntegerRange(params.maxBytes, "params.maxBytes", 64 * 1_024, 2_000_000);
}

function validatePerformanceTarget(target) {
  if (target === undefined || target === null) return;
  if (!Number.isInteger(target.tabId) || target.tabId < 0) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "target.tabId is required when target is set");
  }
  if (target.windowId !== undefined || (target.frameId !== undefined && target.frameId !== 0)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Performance commands accept only the root frame of a tab",
    );
  }
}

const NETWORK_RESOURCE_TYPES = Object.freeze([
  "Document",
  "Stylesheet",
  "Image",
  "Media",
  "Font",
  "Script",
  "TextTrack",
  "XHR",
  "Fetch",
  "Prefetch",
  "EventSource",
  "WebSocket",
  "Manifest",
  "SignedExchange",
  "Ping",
  "CSPViolationReport",
  "Preflight",
  "Other",
]);

function validateNetworkStart(params, target) {
  validateParamsObject(params);
  validateNetworkTarget(target);
  assertAllowedProperties(params, ["maxEntries"]);
  requireIntegerRange(params.maxEntries, "params.maxEntries", 1, 5_000);
}

function validateNetworkEmpty(params, target) {
  validateEmpty(params);
  validateNetworkTarget(target);
}

function validateNetworkRead(params, target) {
  validateParamsObject(params);
  validateNetworkTarget(target);
  assertAllowedProperties(params, [
    "cursor",
    "limit",
    "resourceTypes",
    "failedOnly",
    "statusMin",
    "statusMax",
    "since",
    "maxBytes",
  ]);
  requireIntegerRange(params.limit, "params.limit", 1, 200);
  requireIntegerRange(params.maxBytes, "params.maxBytes", 64 * 1_024, 1_000_000);
  if (
    params.cursor !== undefined &&
    (typeof params.cursor !== "string" ||
      !/^\d+$/.test(params.cursor) ||
      Number(params.cursor) < 1 ||
      !Number.isSafeInteger(Number(params.cursor)))
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.cursor is invalid");
  }
  if (
    params.resourceTypes !== undefined &&
    (!Array.isArray(params.resourceTypes) ||
      params.resourceTypes.length > NETWORK_RESOURCE_TYPES.length ||
      new Set(params.resourceTypes).size !== params.resourceTypes.length ||
      params.resourceTypes.some((value) => !NETWORK_RESOURCE_TYPES.includes(value)))
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.resourceTypes is invalid");
  }
  validateOptionalBoolean(params.failedOnly, "params.failedOnly");
  validateIntegerRange(params.statusMin, "params.statusMin", 100, 599);
  validateIntegerRange(params.statusMax, "params.statusMax", 100, 599);
  if (
    params.statusMin !== undefined &&
    params.statusMax !== undefined &&
    params.statusMin > params.statusMax
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Network status bounds are invalid");
  }
  if (
    params.since !== undefined &&
    (typeof params.since !== "string" ||
      !/^\d{4}-\d{2}-\d{2}T/.test(params.since) ||
      !Number.isFinite(Date.parse(params.since)))
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.since must be an RFC 3339 timestamp");
  }
}

function validateNetworkBody(params, target) {
  validateParamsObject(params);
  validateNetworkTarget(target);
  assertAllowedProperties(params, ["entryId", "direction", "maxBytes"]);
  if (
    typeof params.entryId !== "string" ||
    !/^\d+$/.test(params.entryId) ||
    Number(params.entryId) < 1 ||
    !Number.isSafeInteger(Number(params.entryId))
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.entryId is invalid");
  }
  validateEnum(params.direction, "params.direction", ["request", "response"]);
  requireIntegerRange(params.maxBytes, "params.maxBytes", 1_024, 1_000_000);
}

function validateNetworkHAR(params, target) {
  validateParamsObject(params);
  validateNetworkTarget(target);
  assertAllowedProperties(params, ["maxBytes"]);
  requireIntegerRange(params.maxBytes, "params.maxBytes", 64 * 1_024, 2_000_000);
}

function validateNetworkTarget(target) {
  if (target === undefined || target === null) return;
  if (!Number.isInteger(target.tabId) || target.tabId < 0) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "target.tabId is required when target is set");
  }
  if (target.windowId !== undefined || (target.frameId !== undefined && target.frameId !== 0)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Network commands accept only the root frame of a tab",
    );
  }
}

function validateCookieList(params, target) {
  validateParamsObject(params);
  validateCookieTarget(target);
  assertAllowedProperties(params, [
    "url",
    "domain",
    "name",
    "path",
    "secure",
    "session",
    "storeId",
    "partitionKey",
    "cursor",
    "limit",
  ]);
  validateCookieURL(params.url, "params.url");
  validateOptionalBoundedString(params.domain, "params.domain", 253);
  validateOptionalCookieName(params.name, "params.name");
  validateOptionalCookiePath(params.path, "params.path");
  validateOptionalBoolean(params.secure, "params.secure");
  validateOptionalBoolean(params.session, "params.session");
  validateOptionalBoundedString(params.storeId, "params.storeId", 256);
  validateCookiePartitionKey(params.partitionKey);
  if (
    params.cursor !== undefined &&
    (typeof params.cursor !== "string" ||
      !/^\d+$/.test(params.cursor) ||
      !Number.isSafeInteger(Number(params.cursor)) ||
      Number(params.cursor) < 1)
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.cursor is invalid");
  }
  requireIntegerRange(params.limit, "params.limit", 1, 200);
}

function validateCookieIdentity(params, target) {
  validateParamsObject(params);
  validateCookieTarget(target);
  assertAllowedProperties(params, ["url", "name", "storeId", "partitionKey"]);
  validateCookieURL(params.url, "params.url");
  validateCookieName(params.name, "params.name");
  validateOptionalBoundedString(params.storeId, "params.storeId", 256);
  validateCookiePartitionKey(params.partitionKey);
}

function validateCookieSet(params, target) {
  validateParamsObject(params);
  validateCookieTarget(target);
  assertAllowedProperties(params, [
    "url",
    "name",
    "value",
    "domain",
    "path",
    "secure",
    "httpOnly",
    "sameSite",
    "expirationDate",
    "storeId",
    "partitionKey",
  ]);
  validateCookieURL(params.url, "params.url");
  validateCookieName(params.name, "params.name");
  if (
    typeof params.value !== "string" ||
    new TextEncoder().encode(params.value).byteLength > 4_096 ||
    params.value.includes(";") ||
    hasCookieControl(params.value)
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.value is invalid");
  }
  validateOptionalBoundedString(params.domain, "params.domain", 253);
  validateOptionalCookiePath(params.path, "params.path");
  validateOptionalBoolean(params.secure, "params.secure");
  validateOptionalBoolean(params.httpOnly, "params.httpOnly");
  if (
    params.sameSite !== undefined &&
    !["no_restriction", "lax", "strict", "unspecified"].includes(params.sameSite)
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.sameSite is unsupported");
  }
  if (params.sameSite === "no_restriction" && params.secure !== true) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "SameSite=None requires secure=true");
  }
  if (
    params.expirationDate !== undefined &&
    (!Number.isFinite(params.expirationDate) || params.expirationDate <= 0)
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.expirationDate is invalid");
  }
  validateOptionalBoundedString(params.storeId, "params.storeId", 256);
  validateCookiePartitionKey(params.partitionKey);
}

function validateCookieTarget(target) {
  if (target === undefined || target === null) return;
  if (!Number.isInteger(target.tabId) || target.tabId < 0) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "target.tabId is required when target is set");
  }
  if (target.windowId !== undefined || (target.frameId !== undefined && target.frameId !== 0)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Cookie commands accept only the root frame of a tab",
    );
  }
}

function validateCookieURL(value, path) {
  let parsed;
  try {
    parsed = new URL(value);
  } catch {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} is invalid`);
  }
  if (
    typeof value !== "string" ||
    value.length > 8192 ||
    !["http:", "https:"].includes(parsed.protocol) ||
    parsed.username ||
    parsed.password ||
    parsed.hash
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} must be a safe HTTP(S) URL`);
  }
}

function validateCookieName(value, path) {
  if (
    typeof value !== "string" ||
    value.length < 1 ||
    value.length > 256 ||
    !/^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/.test(value)
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} is invalid`);
  }
}

function validateOptionalCookieName(value, path) {
  if (value !== undefined) validateCookieName(value, path);
}

function validateOptionalCookiePath(value, path) {
  if (
    value !== undefined &&
    (typeof value !== "string" ||
      !value.startsWith("/") ||
      value.length > 2_048 ||
      value.includes(";") ||
      hasCookieControl(value))
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} is invalid`);
  }
}

function validateOptionalBoundedString(value, path, maximum) {
  if (
    value !== undefined &&
    (typeof value !== "string" || value.length > maximum || hasCookieControl(value))
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} is invalid`);
  }
}

function hasCookieControl(value) {
  return [...value].some((character) => {
    const code = character.codePointAt(0);
    return code < 32 || code === 127;
  });
}

function validateCookiePartitionKey(value) {
  if (value === undefined) return;
  validateParamsObject(value);
  assertAllowedProperties(value, ["topLevelSite", "hasCrossSiteAncestor"]);
  validateCookieURL(value.topLevelSite, "params.partitionKey.topLevelSite");
  validateOptionalBoolean(value.hasCrossSiteAncestor, "params.partitionKey.hasCrossSiteAncestor");
}

function validateNonEmptyEmulationObject(value, name, allowed) {
  validateParamsObject(value);
  assertAllowedProperties(value, allowed);
  if (Object.keys(value).length === 0) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `params.${name} must not be empty`);
  }
}

function validateSafeEmulationString(value, path, minimum, maximum) {
  if (value === undefined && minimum === 0) return;
  if (
    typeof value !== "string" ||
    value.length < minimum ||
    value.length > maximum ||
    value !== value.trim() ||
    [...value].some((character) => {
      const code = character.charCodeAt(0);
      return code < 32 || code === 127;
    })
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} contains invalid characters or length`);
  }
}

function requireNumberRange(value, path, minimum, maximum) {
  if (!Number.isFinite(value) || value < minimum || value > maximum) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      `${path} must be between ${minimum} and ${maximum}`,
    );
  }
}

function validatePageRanges(pageRanges) {
  if (pageRanges === undefined || pageRanges === "") return;
  if (typeof pageRanges !== "string" || pageRanges.length > 256) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.pageRanges is too long");
  }
  const ranges = pageRanges.split(",");
  if (ranges.length > 50) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.pageRanges contains too many ranges");
  }
  for (const range of ranges) {
    const match = range.trim().match(/^(\d+)(?:\s*-\s*(\d+))?$/);
    const start = Number.parseInt(match?.[1] || "", 10);
    const end = Number.parseInt(match?.[2] || match?.[1] || "", 10);
    if (!match || start < 1 || end < start || end > 100_000) {
      throw protocolError(ErrorCode.INVALID_MESSAGE, "params.pageRanges is invalid");
    }
  }
}

function validateConsoleStart(params, target) {
  validateParamsObject(params);
  validateConsoleTarget(target);
  assertAllowedProperties(params, ["bufferSize", "captureConsole", "captureErrors"]);
  validateIntegerRange(params.bufferSize, "params.bufferSize", 1, 5_000);
  validateOptionalBoolean(params.captureConsole, "params.captureConsole");
  validateOptionalBoolean(params.captureErrors, "params.captureErrors");
  if (params.captureConsole === false && params.captureErrors === false) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "At least one capture source must be enabled");
  }
}

function validateConsoleEmpty(params, target) {
  validateEmpty(params);
  validateConsoleTarget(target);
}

function validateConsoleRead(params, target) {
  validateParamsObject(params);
  validateConsoleTarget(target);
  assertAllowedProperties(params, ["levels", "kinds", "cursor", "limit", "since"]);
  validateEnumArray(params.levels, "params.levels", ["debug", "log", "info", "warn", "error"]);
  validateEnumArray(params.kinds, "params.kinds", [
    "console",
    "exception",
    "unhandledRejection",
    "resourceError",
  ]);
  validateConsoleCursor(params.cursor);
  validateIntegerRange(params.limit, "params.limit", 1, 200);
  if (
    params.since !== undefined &&
    (typeof params.since !== "string" ||
      params.since.length > 100 ||
      !Number.isFinite(Date.parse(params.since)))
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.since must be an RFC 3339 timestamp");
  }
}

function validateConsoleTarget(target) {
  if (target?.windowId !== undefined) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Console commands do not accept windowId");
  }
}

function validateConsoleCursor(cursor) {
  if (cursor === undefined) return;
  if (
    typeof cursor !== "string" ||
    !/^\d+$/.test(cursor) ||
    !Number.isSafeInteger(Number.parseInt(cursor, 10))
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.cursor is out of range");
  }
}

function validateEnumArray(values, path, allowed) {
  if (values === undefined) return;
  if (
    !Array.isArray(values) ||
    values.length > allowed.length ||
    values.some((value) => !allowed.includes(value)) ||
    new Set(values).size !== values.length
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} contains invalid values`);
  }
}

function validateStringWait(params) {
  assertBoundedString(params.expected, "params.expected", 100_000, true);
  validateEnum(params.matchOperator, "params.matchOperator", ["equals", "contains"]);
  validateOptionalBoolean(params.caseSensitive, "params.caseSensitive");
}

function requireIntegerRange(value, path, minimum, maximum) {
  if (!Number.isInteger(value) || value < minimum || value > maximum) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      `${path} must be between ${minimum} and ${maximum}`,
    );
  }
}

function assertBoundedString(value, path, maximum, allowEmpty = false) {
  if (typeof value !== "string" || value.length > maximum || (!allowEmpty && value.trim() === "")) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      `${path} must be a${allowEmpty ? "" : " non-empty"} string no longer than ${maximum}`,
    );
  }
}

function validateOptionalElementAddress(params, target) {
  const hasAddress = [params.selector, params.coordinates, params.locator].some(
    (value) => value !== undefined,
  );
  if (hasAddress) validateElementAddress(params, target);
}

function validateElementAddress(params, target) {
  const addresses = [params.selector, params.coordinates, params.locator].filter(
    (value) => value !== undefined,
  );
  if (addresses.length !== 1) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Exactly one of params.selector, params.coordinates, or params.locator is required",
    );
  }
  if (params.selector !== undefined) {
    assertNonEmptyString(params.selector, "params.selector");
  }
  if (params.coordinates !== undefined) {
    validateLocator({ coordinates: params.coordinates }, target);
  }
  if (params.locator !== undefined) {
    validateLocator(params.locator, target);
  }
  if (params.index !== undefined && params.selector === undefined) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "params.index can only be used with params.selector",
    );
  }
}

function validateParamsObject(params) {
  if (!params || typeof params !== "object" || Array.isArray(params)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params must be an object");
  }
}

function assertAllowedProperties(params, allowed) {
  const unexpected = Object.keys(params).find((property) => !allowed.includes(property));
  if (unexpected) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `Unexpected parameter "${unexpected}"`);
  }
}

function assertNonEmptyString(value, path) {
  if (typeof value !== "string" || value.trim() === "") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} must not be empty`);
  }
}

function validateIndex(index) {
  if (index !== undefined && (!Number.isInteger(index) || index < 0)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.index must be a non-negative integer");
  }
}

function requireWindowTarget(target) {
  if (!Number.isInteger(target?.windowId) || target.windowId < 0) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "target.windowId is required");
  }
  if (
    target.tabId !== undefined ||
    target.frameId !== undefined ||
    target.documentId !== undefined
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Window commands require a window-only target");
  }
}

function validateWindowState(state) {
  validateEnum(state, "params.state", ["normal", "minimized", "maximized", "fullscreen"]);
}

function validateEnum(value, path, allowed) {
  if (value !== undefined && !allowed.includes(value)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} must be one of ${allowed.join(", ")}`);
  }
}

function validateOptionalBoolean(value, path) {
  if (value !== undefined && typeof value !== "boolean") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} must be a boolean`);
  }
}

function validateOptionalIdentifier(value, path) {
  if (value !== undefined && (!Number.isInteger(value) || value < 0)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} must be a non-negative integer`);
  }
}

function validateOptionalTabTarget(target) {
  if (target === undefined || target === null) {
    return;
  }
  if (!Number.isInteger(target.tabId) || target.tabId < 0) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "target.tabId is required when target is set");
  }
  if (target.frameId !== undefined || target.documentId !== undefined) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Tab commands require a tab-only target");
  }
}

function validateAccessibilityTarget(target) {
  if (target === undefined || target === null) return;
  if (!Number.isInteger(target.tabId) || target.tabId < 0) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "target.tabId is required when target is set");
  }
  if (target.windowId !== undefined || (target.frameId !== undefined && target.frameId !== 0)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Accessibility commands accept only the root frame of a tab",
    );
  }
}

function validateWindowBounds(params) {
  for (const property of ["left", "top"]) {
    if (params[property] !== undefined && !Number.isInteger(params[property])) {
      throw protocolError(ErrorCode.INVALID_MESSAGE, `params.${property} must be an integer`);
    }
  }
  for (const property of ["width", "height"]) {
    if (
      params[property] !== undefined &&
      (!Number.isInteger(params[property]) || params[property] < 1)
    ) {
      throw protocolError(
        ErrorCode.INVALID_MESSAGE,
        `params.${property} must be a positive integer`,
      );
    }
  }
}

function assertStateAndBoundsCompatible(params) {
  const hasBounds = ["left", "top", "width", "height"].some(
    (property) => params[property] !== undefined,
  );
  if (hasBounds && params.state !== undefined && params.state !== "normal") {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Window bounds can only be combined with the normal state",
    );
  }
}

function throwIfCancelled(signal) {
  if (signal.aborted) {
    throw protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
  }
}
