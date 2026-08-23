import assert from "node:assert/strict";
import test from "node:test";

import {
  ErrorCode,
  MessageType,
  createMessage,
  mapChromeError,
  normalizeError,
  normalizePairingCode,
  validateIncomingMessage,
  validateServerEndpoint,
} from "../src/protocol.js";

test("validateServerEndpoint accepts loopback WebSockets", () => {
  assert.equal(validateServerEndpoint("ws://127.0.0.1:8090/ws"), "ws://127.0.0.1:8090/ws");
  assert.equal(validateServerEndpoint("ws://localhost:8090/ws"), "ws://localhost:8090/ws");
  assert.equal(validateServerEndpoint("ws://[::1]:8090/ws"), "ws://[::1]:8090/ws");
});

test("validateServerEndpoint rejects remote and HTTP endpoints", () => {
  assert.throws(
    () => validateServerEndpoint("wss://example.com/ws"),
    (error) => error.code === ErrorCode.RESTRICTED_URL,
  );
  assert.throws(
    () => validateServerEndpoint("http://127.0.0.1:8090/ws"),
    (error) => error.code === ErrorCode.INVALID_MESSAGE,
  );
});

test("validateIncomingMessage isolates browser instances", () => {
  const message = createMessage(MessageType.REQUEST, {
    browserId: "browser-a",
    requestId: "request-1",
    command: "tabs.list",
  });
  assert.equal(validateIncomingMessage(message, "browser-a"), message);
  assert.throws(
    () => validateIncomingMessage(message, "browser-b"),
    (error) => error.code === ErrorCode.INVALID_MESSAGE,
  );
});

test("normalizePairingCode accepts readable and compact codes", () => {
  assert.equal(normalizePairingCode("1234-5678"), "1234-5678");
  assert.equal(normalizePairingCode(" 12345678 "), "1234-5678");
  assert.throws(
    () => normalizePairingCode("1234"),
    (error) => error.code === ErrorCode.INVALID_MESSAGE,
  );
});

test("normalizeError does not expose stacks", () => {
  const error = new Error("failed");
  error.code = ErrorCode.TAB_NOT_FOUND;
  error.stack = "sensitive stack";

  assert.deepEqual(normalizeError(error), {
    code: ErrorCode.TAB_NOT_FOUND,
    message: "failed",
    retryable: false,
  });
});

test("normalizeError adds request diagnostics without exposing stacks", () => {
  const error = new Error("failed");
  const target = { tabId: 42, frameId: 0 };
  assert.deepEqual(normalizeError(error, { requestId: "request-1", target }), {
    code: ErrorCode.INTERNAL_ERROR,
    message: "failed",
    retryable: false,
    requestId: "request-1",
    target,
  });
});

test("mapChromeError returns safe stable product errors", () => {
  const cases = [
    ["No tab with id: 42", ErrorCode.TAB_NOT_FOUND, true],
    ["Cannot access a chrome:// URL", ErrorCode.RESTRICTED_URL, false],
    ["Missing host permission for the tab", ErrorCode.PERMISSION_REQUIRED, false],
    ["secret implementation failure", ErrorCode.INTERNAL_ERROR, false],
  ];
  for (const [message, code, retryable] of cases) {
    const mapped = mapChromeError(new Error(message));
    assert.equal(mapped.code, code);
    assert.equal(mapped.retryable, retryable);
    assert.equal(mapped.message.includes("secret implementation failure"), false);
  }
});
