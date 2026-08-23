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
  BROWSER_DISCONNECTED: "BROWSER_DISCONNECTED",
  TAB_NOT_FOUND: "TAB_NOT_FOUND",
  FRAME_NOT_FOUND: "FRAME_NOT_FOUND",
  ELEMENT_NOT_FOUND: "ELEMENT_NOT_FOUND",
  STRICT_MODE_VIOLATION: "STRICT_MODE_VIOLATION",
  PERMISSION_REQUIRED: "PERMISSION_REQUIRED",
  CAPABILITY_UNAVAILABLE: "CAPABILITY_UNAVAILABLE",
  PAIRING_REQUIRED: "PAIRING_REQUIRED",
  INVALID_MESSAGE: "INVALID_MESSAGE",
  INVALID_COMMAND: "INVALID_COMMAND",
  CANCELLED: "CANCELLED",
  RESTRICTED_URL: "RESTRICTED_URL",
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

export function normalizeError(error) {
  return {
    code: error?.code || ErrorCode.INTERNAL_ERROR,
    message: error?.message || "An unexpected extension error occurred",
    retryable: Boolean(error?.retryable),
    ...(error?.details ? { details: error.details } : {}),
  };
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
