import assert from "node:assert/strict";
import test from "node:test";

import {
  ErrorCode,
  MessageType,
  assertFreshDocument,
  createMessage,
  mapChromeError,
  normalizeError,
  normalizePairingCode,
  validateLocator,
  validateIncomingMessage,
  validateTarget,
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
  assert.throws(
    () => validateIncomingMessage({ ...message, timeoutMs: 120_001 }, "browser-a"),
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
    ["No window with id: 4", ErrorCode.WINDOW_NOT_FOUND, true],
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

test("validateTarget requires browser-scoped non-negative identifiers", () => {
  assert.doesNotThrow(() =>
    validateTarget({ browserId: "browser-a", tabId: 42, frameId: 0 }, "browser-a"),
  );
  for (const target of [
    { tabId: 42 },
    { browserId: "browser-b", tabId: 42 },
    { browserId: "browser-a", tabId: -1 },
    { browserId: "browser-a", frameId: 0 },
  ]) {
    assert.throws(
      () => validateTarget(target, "browser-a"),
      (error) => error.code === ErrorCode.INVALID_MESSAGE,
    );
  }
});

test("validateLocator accepts every primary strategy", () => {
  const locators = [
    { css: "#submit" },
    { xpath: "//button" },
    { text: "Submit" },
    { role: "button", name: "Submit" },
    { label: "Email" },
    { placeholder: "name@example.com" },
    { alt: "Company logo" },
    { title: "Close" },
    { testId: "submit" },
    { coordinates: { x: 12.5, y: 24 } },
    { element: { elementId: "element-1", documentId: "document-1" } },
  ];
  for (const locator of locators) {
    assert.equal(validateLocator(locator), locator);
  }
});

test("validateLocator enforces strategy and modifier bounds", () => {
  for (const locator of [
    {},
    { css: "a", text: "link" },
    { css: "a", text: " " },
    { css: "a", name: "link" },
    { css: "a", nth: -1 },
    { coordinates: { x: -1, y: 0 } },
  ]) {
    assert.throws(
      () => validateLocator(locator),
      (error) => error.code === ErrorCode.INVALID_MESSAGE,
    );
  }
});

test("document-scoped targets and element references reject stale documents", () => {
  assert.doesNotThrow(() => assertFreshDocument("document-1", "document-1"));
  assert.throws(
    () => assertFreshDocument("document-1", "document-2"),
    (error) => error.code === ErrorCode.STALE_TARGET,
  );
  assert.throws(
    () =>
      validateLocator(
        { element: { elementId: "element-1", documentId: "document-1" } },
        { documentId: "document-2" },
      ),
    (error) => error.code === ErrorCode.STALE_TARGET,
  );
});
