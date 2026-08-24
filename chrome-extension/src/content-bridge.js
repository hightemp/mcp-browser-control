import { ErrorCode, mapChromeError, protocolError } from "./protocol.js";

export const CONTENT_BRIDGE_VERSION = "1.6";

const READY_MESSAGE = Object.freeze({
  type: "MCP_BROWSER_BRIDGE_READY",
  bridgeVersion: CONTENT_BRIDGE_VERSION,
});

const CONTENT_ERROR_CODES = new Set([
  ErrorCode.ELEMENT_NOT_FOUND,
  ErrorCode.STRICT_MODE_VIOLATION,
  ErrorCode.INVALID_MESSAGE,
  ErrorCode.STALE_TARGET,
  ErrorCode.RESTRICTED_URL,
  ErrorCode.CAPABILITY_UNAVAILABLE,
  ErrorCode.TIMEOUT,
  ErrorCode.CANCELLED,
  ErrorCode.INTERNAL_ERROR,
]);

export class ContentScriptBridge {
  constructor(chromeAPI) {
    this.chromeAPI = chromeAPI;
  }

  async execute({ tabId, frameId, documentId, operationId, command, params, signal }) {
    const options = messageOptions(frameId, documentId);
    const currentOperationId = operationId || globalThis.crypto.randomUUID();
    await this.ensureReady(tabId, frameId, options, signal);
    const response = await abortable(
      this.chromeAPI.tabs.sendMessage(
        tabId,
        {
          type: "MCP_BROWSER_COMMAND",
          bridgeVersion: CONTENT_BRIDGE_VERSION,
          operationId: currentOperationId,
          command,
          params,
          frameId,
          documentId,
        },
        options,
      ),
      signal,
      () => this.cancel(tabId, currentOperationId, options),
    );
    return unwrapResponse(response);
  }

  cancel(tabId, operationId, options) {
    void this.chromeAPI.tabs
      .sendMessage(
        tabId,
        {
          type: "MCP_BROWSER_CANCEL",
          bridgeVersion: CONTENT_BRIDGE_VERSION,
          operationId,
        },
        options,
      )
      .catch(() => undefined);
  }

  async ensureReady(tabId, frameId, options, signal) {
    try {
      const response = await abortable(
        this.chromeAPI.tabs.sendMessage(tabId, READY_MESSAGE, options),
        signal,
      );
      assertReadyResponse(response);
      return;
    } catch (error) {
      throwIfAborted(signal);
      if (typeof error?.code === "string") {
        throw error;
      }
    }

    try {
      await abortable(
        this.chromeAPI.scripting.executeScript({
          target: { tabId, frameIds: [frameId] },
          files: ["src/locator-engine.js", "src/content.js"],
        }),
        signal,
      );
      const response = await abortable(
        this.chromeAPI.tabs.sendMessage(tabId, READY_MESSAGE, options),
        signal,
      );
      assertReadyResponse(response);
    } catch (error) {
      throwIfAborted(signal);
      if (typeof error?.code === "string") {
        throw error;
      }
      throw mapChromeError(error);
    }
  }
}

function messageOptions(frameId, documentId) {
  return {
    frameId,
    ...(documentId ? { documentId } : {}),
  };
}

function assertReadyResponse(response) {
  if (response?.ready !== true || response.bridgeVersion !== CONTENT_BRIDGE_VERSION) {
    throw protocolError(
      ErrorCode.CAPABILITY_UNAVAILABLE,
      "The page bridge version is incompatible; reload the tab and try again",
      true,
    );
  }
}

function unwrapResponse(response) {
  if (!isPlainObject(response) || typeof response.success !== "boolean") {
    throw protocolError(ErrorCode.INTERNAL_ERROR, "The page bridge returned an invalid response");
  }
  if (response.success) {
    if (!("result" in response)) {
      throw protocolError(ErrorCode.INTERNAL_ERROR, "The page bridge response has no result");
    }
    return response.result;
  }

  const error = response.error;
  if (!isPlainObject(error) || !CONTENT_ERROR_CODES.has(error.code)) {
    throw protocolError(ErrorCode.INTERNAL_ERROR, "The page bridge returned an invalid error");
  }
  const message =
    typeof error.message === "string" && error.message.trim()
      ? error.message.slice(0, 1_000)
      : "Page command failed";
  throw protocolError(
    error.code,
    message,
    Boolean(error.retryable),
    isPlainObject(error.details) ? error.details : undefined,
  );
}

function abortable(promise, signal, onAbort = undefined) {
  throwIfAborted(signal);
  return new Promise((resolve, reject) => {
    const handleAbort = () => {
      onAbort?.();
      reject(abortReason(signal));
    };
    signal.addEventListener("abort", handleAbort, { once: true });
    Promise.resolve(promise)
      .then(resolve, reject)
      .finally(() => {
        signal.removeEventListener("abort", handleAbort);
      });
  });
}

function throwIfAborted(signal) {
  if (signal.aborted) {
    throw abortReason(signal);
  }
}

function abortReason(signal) {
  return typeof signal.reason?.code === "string"
    ? signal.reason
    : protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
}

function isPlainObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
