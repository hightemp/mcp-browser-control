export const PROTOCOL_VERSION = "1.0";

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
  TAB_NOT_FOUND: "TAB_NOT_FOUND",
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
  if (
    message.includes("no tab with id") ||
    message.includes("invalid tab id") ||
    message.includes("tab was closed")
  ) {
    return protocolError(ErrorCode.TAB_NOT_FOUND, "The target tab is no longer available", true);
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
    message.includes("cannot access contents of url")
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
  return message;
}
