import {
  ErrorCode,
  normalizeError,
  protocolError,
  validateLocator,
  validateTarget,
} from "./protocol.js";

const COMMANDS = Object.freeze({
  "browser.ping": Object.freeze({ domain: "browser", handler: "ping", validate: validateEmpty }),
  "windows.list": Object.freeze({ domain: "windows", handler: "list", validate: validateEmpty }),
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
  "tabs.list": Object.freeze({ domain: "tabs", handler: "list", validate: validateEmpty }),
  "tabs.get": Object.freeze({ domain: "tabs", handler: "get", validate: validateTabEmpty }),
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
  "tabs.stop": Object.freeze({ domain: "tabs", handler: "stop", validate: validateTabEmpty }),
  "tabs.back": Object.freeze({ domain: "tabs", handler: "back", validate: validateTabEmpty }),
  "tabs.forward": Object.freeze({
    domain: "tabs",
    handler: "forward",
    validate: validateTabEmpty,
  }),
  "tabs.move": Object.freeze({ domain: "tabs", handler: "move", validate: validateTabMove }),
  "tabs.duplicate": Object.freeze({
    domain: "tabs",
    handler: "duplicate",
    validate: validateTabEmpty,
  }),
  "tabs.close": Object.freeze({ domain: "tabs", handler: "close", validate: validateTabEmpty }),
  "tabs.pin": Object.freeze({ domain: "tabs", handler: "pin", validate: validateTabPin }),
  "tabs.mute": Object.freeze({ domain: "tabs", handler: "mute", validate: validateTabMute }),
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
  "page.info": Object.freeze({ domain: "page", handler: "info", validate: validateEmpty }),
  "page.getHTML": Object.freeze({ domain: "page", handler: "getHTML", validate: validateGetHTML }),
  "page.getHTMLBySelector": Object.freeze({
    domain: "page",
    handler: "getHTMLBySelector",
    validate: validateSelector,
  }),
  "page.getText": Object.freeze({ domain: "page", handler: "getText", validate: validateGetText }),
  "page.query": Object.freeze({ domain: "page", handler: "query", validate: validateQuery }),
  "page.getElement": Object.freeze({
    domain: "page",
    handler: "getElement",
    validate: validateGetElement,
  }),
  "page.click": Object.freeze({ domain: "page", handler: "click", validate: validateAction }),
  "page.fill": Object.freeze({ domain: "page", handler: "fill", validate: validateFill }),
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

function validateSelectors(selectors, path) {
  if (selectors === undefined) {
    return;
  }
  if (
    !Array.isArray(selectors)
    || selectors.length > 50
    || selectors.some((selector) => typeof selector !== "string" || selector.trim() === "")
  ) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      `${path} must contain at most 50 non-empty CSS selectors`,
    );
  }
}

function validateCursor(cursor) {
  if (
    cursor !== undefined
    && (
      typeof cursor !== "string"
      || !/^\d+$/.test(cursor)
      || Number.parseInt(cursor, 10) > 1_000_000
    )
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
      !Array.isArray(params.urls)
      || params.urls.length === 0
      || params.urls.length > 50
      || params.urls.some((url) => typeof url !== "string" || url.trim() === "")
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
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.index must be an integer of at least -1");
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
  validateEnum(
    params.color,
    "params.color",
    ["grey", "blue", "red", "yellow", "green", "pink", "purple", "cyan", "orange"],
  );
  validateOptionalBoolean(params.collapsed, "params.collapsed");
}

function validateRecentlyClosed(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["maxResults"]);
  if (
    params.maxResults !== undefined
    && (!Number.isInteger(params.maxResults) || params.maxResults < 1 || params.maxResults > 25)
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
    !Array.isArray(tabIds)
    || tabIds.length === 0
    || tabIds.length > 100
    || tabIds.some((tabId) => !Number.isInteger(tabId) || tabId < 0)
    || new Set(tabIds).size !== tabIds.length
  ) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "params.tabIds must contain between 1 and 100 unique non-negative integers",
    );
  }
}

function validateAction(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["selector", "coordinates", "locator", "index"]);
  validateElementAddress(params, target);
  validateIndex(params.index);
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
  ]);
  validateElementAddress(params, target);
  validateIndex(params.index);
  if (params.value === undefined || params.value === null) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.value is required");
  }
  if (params.clear !== undefined && typeof params.clear !== "boolean") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.clear must be a boolean");
  }
}

function validateElementAddress(params, target) {
  const addresses = [params.selector, params.coordinates, params.locator]
    .filter((value) => value !== undefined);
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
  if (target.tabId !== undefined || target.frameId !== undefined || target.documentId !== undefined) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Window commands require a window-only target");
  }
}

function validateWindowState(state) {
  validateEnum(
    state,
    "params.state",
    ["normal", "minimized", "maximized", "fullscreen"],
  );
}

function validateEnum(value, path, allowed) {
  if (value !== undefined && !allowed.includes(value)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      `${path} must be one of ${allowed.join(", ")}`,
    );
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

function validateWindowBounds(params) {
  for (const property of ["left", "top"]) {
    if (params[property] !== undefined && !Number.isInteger(params[property])) {
      throw protocolError(ErrorCode.INVALID_MESSAGE, `params.${property} must be an integer`);
    }
  }
  for (const property of ["width", "height"]) {
    if (
      params[property] !== undefined
      && (!Number.isInteger(params[property]) || params[property] < 1)
    ) {
      throw protocolError(ErrorCode.INVALID_MESSAGE, `params.${property} must be a positive integer`);
    }
  }
}

function assertStateAndBoundsCompatible(params) {
  const hasBounds = ["left", "top", "width", "height"]
    .some((property) => params[property] !== undefined);
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
