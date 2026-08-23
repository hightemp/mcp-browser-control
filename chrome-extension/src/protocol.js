export const PROTOCOL_VERSION = "1.0";
export const MAX_TIMEOUT_MS = 120_000;
export const MAX_LOCATOR_NTH = 10_000;
export const MAX_COORDINATE = 1_000_000;

export const MessageType = Object.freeze({
  HELLO: "hello",
  WELCOME: "welcome",
  AUTH_ERROR: "auth_error",
  REVOKE: "revoke",
  REQUEST: "request",
  RESPONSE: "response",
  CANCEL: "cancel",
  EVENT: "event",
  PING: "ping",
  PONG: "pong",
  CAPABILITIES_CHANGED: "capabilities_changed",
});

export const ErrorCode = Object.freeze({
  NO_BROWSER_CONNECTED: "NO_BROWSER_CONNECTED",
  AMBIGUOUS_BROWSER: "AMBIGUOUS_BROWSER",
  BROWSER_NOT_FOUND: "BROWSER_NOT_FOUND",
  BROWSER_DISCONNECTED: "BROWSER_DISCONNECTED",
  WINDOW_NOT_FOUND: "WINDOW_NOT_FOUND",
  TAB_NOT_FOUND: "TAB_NOT_FOUND",
  TAB_GROUP_NOT_FOUND: "TAB_GROUP_NOT_FOUND",
  SESSION_NOT_FOUND: "SESSION_NOT_FOUND",
  FRAME_NOT_FOUND: "FRAME_NOT_FOUND",
  STALE_TARGET: "STALE_TARGET",
  ELEMENT_NOT_FOUND: "ELEMENT_NOT_FOUND",
  STRICT_MODE_VIOLATION: "STRICT_MODE_VIOLATION",
  PERMISSION_REQUIRED: "PERMISSION_REQUIRED",
  CAPABILITY_UNAVAILABLE: "CAPABILITY_UNAVAILABLE",
  PAIRING_REQUIRED: "PAIRING_REQUIRED",
  UNSUPPORTED_PROTOCOL_VERSION: "UNSUPPORTED_PROTOCOL_VERSION",
  INVALID_MESSAGE: "INVALID_MESSAGE",
  INVALID_COMMAND: "INVALID_COMMAND",
  TIMEOUT: "TIMEOUT",
  CANCELLED: "CANCELLED",
  PAYLOAD_TOO_LARGE: "PAYLOAD_TOO_LARGE",
  RESTRICTED_URL: "RESTRICTED_URL",
  CONFIRMATION_REQUIRED: "CONFIRMATION_REQUIRED",
  BACKPRESSURE: "BACKPRESSURE",
  INTERNAL_ERROR: "INTERNAL_ERROR",
});

export function createMessage(type, fields = {}) {
  return {
    protocolVersion: PROTOCOL_VERSION,
    type,
    ...fields,
    timestamp: new Date().toISOString(),
  };
}

export function validateServerEndpoint(value) {
  let endpoint;
  try {
    endpoint = new URL(value);
  } catch {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Server endpoint is not a valid URL");
  }

  if (endpoint.protocol !== "ws:" && endpoint.protocol !== "wss:") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Server endpoint must use ws:// or wss://");
  }
  const host = endpoint.hostname.replace(/^\[|\]$/g, "").toLowerCase();
  if (host !== "localhost" && host !== "127.0.0.1" && host !== "::1") {
    throw protocolError(
      ErrorCode.RESTRICTED_URL,
      "The first release only allows loopback WebSocket endpoints",
    );
  }
  return endpoint.toString();
}

export function normalizePairingCode(value) {
  const digits = String(value || "").replace(/[\s-]/g, "");
  if (!/^\d{8}$/.test(digits)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Pairing code must contain eight digits");
  }
  return `${digits.slice(0, 4)}-${digits.slice(4)}`;
}

export function protocolError(code, message, retryable = false, details = undefined) {
  const error = new Error(message);
  error.code = code;
  error.retryable = retryable;
  error.details = details;
  return error;
}

export function normalizeError(error, context = {}) {
  return {
    code: error?.code || ErrorCode.INTERNAL_ERROR,
    message: error?.message || "An unexpected extension error occurred",
    retryable: Boolean(error?.retryable),
    ...(error?.requestId || context.requestId
      ? { requestId: error?.requestId || context.requestId }
      : {}),
    ...(error?.target || context.target
      ? { target: error?.target || context.target }
      : {}),
    ...(error?.details ? { details: error.details } : {}),
  };
}

export function mapChromeError(error) {
  const message = String(error?.message || error || "").toLowerCase();
  if (message.includes("no window with id") || message.includes("invalid window id")) {
    return protocolError(
      ErrorCode.WINDOW_NOT_FOUND,
      "The target window is no longer available",
      true,
    );
  }
  if (message.includes("no frame with id") || message.includes("frame was removed")) {
    return protocolError(ErrorCode.FRAME_NOT_FOUND, "The target frame is no longer available", true);
  }
  if (
    message.includes("no tab with id") ||
    message.includes("invalid tab id") ||
    message.includes("tab was closed")
  ) {
    return protocolError(ErrorCode.TAB_NOT_FOUND, "The target tab is no longer available", true);
  }
  if (message.includes("no group with id") || message.includes("tab group not found")) {
    return protocolError(
      ErrorCode.TAB_GROUP_NOT_FOUND,
      "The target tab group is no longer available",
      true,
    );
  }
  if (
    message.includes("session")
    && (message.includes("not found") || message.includes("could not restore"))
  ) {
    return protocolError(
      ErrorCode.SESSION_NOT_FOUND,
      "The recently closed session is no longer available",
      true,
    );
  }
  if (
    message.includes("chrome://") ||
    message.includes("edge://") ||
    message.includes("extensions gallery cannot be scripted")
  ) {
    return protocolError(
      ErrorCode.RESTRICTED_URL,
      "The browser does not allow access to this page",
    );
  }
  if (
    message.includes("missing host permission") ||
    message.includes("cannot access contents of url") ||
    message.includes("permission is required") ||
    message.includes("requires permission")
  ) {
    return protocolError(
      ErrorCode.PERMISSION_REQUIRED,
      "Website access is required. Grant it from the extension popup.",
    );
  }
  return protocolError(ErrorCode.INTERNAL_ERROR, "A browser API operation failed");
}

export function validateIncomingMessage(message, browserId) {
  if (!message || typeof message !== "object") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Message must be an object");
  }
  if (message.protocolVersion !== PROTOCOL_VERSION) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      `Unsupported protocol version "${message.protocolVersion}"`,
    );
  }
  if (!Object.values(MessageType).includes(message.type)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `Unknown message type "${message.type}"`);
  }
  if (message.browserId && message.browserId !== browserId) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Message targets another browser instance");
  }
  if (
    message.timeoutMs !== undefined &&
    (!Number.isInteger(message.timeoutMs) || message.timeoutMs < 1 || message.timeoutMs > MAX_TIMEOUT_MS)
  ) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      `timeoutMs must be between 1 and ${MAX_TIMEOUT_MS}`,
    );
  }
  validateTarget(message.target, browserId);
  validateTarget(message.error?.target, browserId);
  if (message.params?.locator !== undefined) {
    validateLocator(message.params.locator, message.target);
  }
  return message;
}

export function validateTarget(target, browserId) {
  if (target === undefined || target === null) {
    return;
  }
  if (typeof target !== "object" || Array.isArray(target)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "target must be an object");
  }
  if (!target.browserId || target.browserId !== browserId) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "target.browserId must match the resolved browser",
    );
  }
  for (const field of ["windowId", "tabId", "frameId"]) {
    if (target[field] !== undefined && (!Number.isInteger(target[field]) || target[field] < 0)) {
      throw protocolError(ErrorCode.INVALID_MESSAGE, `target.${field} must not be negative`);
    }
  }
  if (
    target.tabId === undefined &&
    (target.frameId !== undefined || target.documentId !== undefined)
  ) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "target.frameId and documentId require tabId",
    );
  }
  if (target.documentId !== undefined && !isNonEmptyString(target.documentId)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "target.documentId must not be empty");
  }
}

export function validateLocator(locator, target = undefined) {
  if (!locator || typeof locator !== "object" || Array.isArray(locator)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "locator is required");
  }
  const strategyFields = [
    "css",
    "xpath",
    "text",
    "role",
    "label",
    "placeholder",
    "alt",
    "title",
    "testId",
  ];
  for (const field of strategyFields) {
    if (locator[field] !== undefined && !isNonEmptyString(locator[field])) {
      throw protocolError(ErrorCode.INVALID_MESSAGE, `locator.${field} must not be empty`);
    }
  }
  let strategyCount = strategyFields.filter((field) => isNonEmptyString(locator[field])).length;
  strategyCount += locator.coordinates !== undefined ? 1 : 0;
  strategyCount += locator.element !== undefined ? 1 : 0;
  if (strategyCount !== 1) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "locator must contain exactly one primary strategy",
    );
  }
  if (locator.name !== undefined && !isNonEmptyString(locator.name)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "locator.name must not be empty");
  }
  if (isNonEmptyString(locator.name) && !isNonEmptyString(locator.role)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "locator.name requires locator.role");
  }
  if (
    locator.nth !== undefined &&
    (!Number.isInteger(locator.nth) || locator.nth < 0 || locator.nth > MAX_LOCATOR_NTH)
  ) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      `locator.nth must be between 0 and ${MAX_LOCATOR_NTH}`,
    );
  }
  if (locator.strict !== undefined && typeof locator.strict !== "boolean") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "locator.strict must be a boolean");
  }
  if (locator.coordinates !== undefined) {
    validateCoordinates(locator.coordinates);
  }
  if (locator.element !== undefined) {
    validateElementReference(locator.element);
    if (target?.documentId) {
      assertFreshDocument(locator.element.documentId, target.documentId);
    }
  }
  return locator;
}

export function assertFreshDocument(expectedDocumentId, actualDocumentId) {
  if (expectedDocumentId && expectedDocumentId === actualDocumentId) {
    return;
  }
  throw protocolError(
    ErrorCode.STALE_TARGET,
    "The referenced document is no longer current",
    false,
    { expectedDocumentId },
  );
}

function validateCoordinates(coordinates) {
  if (!coordinates || typeof coordinates !== "object" || Array.isArray(coordinates)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "locator.coordinates must be an object");
  }
  if (![coordinates.x, coordinates.y].every(isValidCoordinate)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      `locator coordinates must be between 0 and ${MAX_COORDINATE}`,
    );
  }
}

function validateElementReference(reference) {
  if (
    !reference ||
    typeof reference !== "object" ||
    !isNonEmptyString(reference.elementId) ||
    !isNonEmptyString(reference.documentId)
  ) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "element reference requires elementId and documentId",
    );
  }
}

function isValidCoordinate(value) {
  return Number.isFinite(value) && value >= 0 && value <= MAX_COORDINATE;
}

function isNonEmptyString(value) {
  return typeof value === "string" && value.trim() !== "";
}
