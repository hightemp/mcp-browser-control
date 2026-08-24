import { ErrorCode, mapChromeError, protocolError } from "./protocol.js";

export const CONSOLE_BRIDGE_VERSION = "1.0";

const READY_MESSAGE = Object.freeze({
  type: "MCP_BROWSER_CONSOLE_READY",
  bridgeVersion: CONSOLE_BRIDGE_VERSION,
});

export class ConsoleCaptureBridge {
  constructor(chromeAPI) {
    this.chromeAPI = chromeAPI;
  }

  async execute({ tabId, frameId, documentId, operationId, command, params, signal }) {
    const options = messageOptions(frameId, documentId);
    await this.ensureContentReady(tabId, frameId, documentId, options, signal);
    if (command === "console.start") {
      await this.ensureMainWorld(tabId, frameId, documentId, signal);
    }
    const response = await abortable(
      this.chromeAPI.tabs.sendMessage(
        tabId,
        {
          type: "MCP_BROWSER_CONSOLE_COMMAND",
          bridgeVersion: CONSOLE_BRIDGE_VERSION,
          operationId: operationId || globalThis.crypto.randomUUID(),
          command,
          params,
          frameId,
          documentId,
        },
        options,
      ),
      signal,
    );
    return unwrapConsoleResponse(response);
  }

  async ingest({ tabId, frameId, documentId, entry, timestamp }) {
    try {
      const response = await this.chromeAPI.tabs.sendMessage(
        tabId,
        {
          type: "MCP_BROWSER_CONSOLE_CDP_EVENT",
          bridgeVersion: CONSOLE_BRIDGE_VERSION,
          frameId,
          documentId,
          entry,
          timestamp,
        },
        messageOptions(frameId, documentId),
      );
      return response?.accepted === true;
    } catch {
      return false;
    }
  }

  async ensureContentReady(tabId, frameId, documentId, options, signal) {
    try {
      const response = await abortable(
        this.chromeAPI.tabs.sendMessage(tabId, READY_MESSAGE, options),
        signal,
      );
      assertReady(response);
      return;
    } catch (error) {
      throwIfAborted(signal);
      if (typeof error?.code === "string") throw error;
    }

    try {
      await abortable(
        this.chromeAPI.scripting.executeScript({
          target: scriptTarget(tabId, frameId, documentId),
          files: ["src/console-content.js"],
          world: "ISOLATED",
        }),
        signal,
      );
      const response = await abortable(
        this.chromeAPI.tabs.sendMessage(tabId, READY_MESSAGE, options),
        signal,
      );
      assertReady(response);
    } catch (error) {
      throwIfAborted(signal);
      if (typeof error?.code === "string") throw error;
      throw mapChromeError(error);
    }
  }

  async ensureMainWorld(tabId, frameId, documentId, signal) {
    try {
      await abortable(
        this.chromeAPI.scripting.executeScript({
          target: scriptTarget(tabId, frameId, documentId),
          files: ["src/console-main.js"],
          world: "MAIN",
        }),
        signal,
      );
    } catch (error) {
      throwIfAborted(signal);
      if (typeof error?.code === "string") throw error;
      throw mapChromeError(error);
    }
  }
}

function scriptTarget(tabId, frameId, documentId) {
  return documentId ? { tabId, documentIds: [documentId] } : { tabId, frameIds: [frameId] };
}

function messageOptions(frameId, documentId) {
  return { frameId, ...(documentId ? { documentId } : {}) };
}

function assertReady(response) {
  if (response?.ready !== true || response.bridgeVersion !== CONSOLE_BRIDGE_VERSION) {
    throw protocolError(
      ErrorCode.CAPABILITY_UNAVAILABLE,
      "The console bridge version is incompatible; reload the tab and try again",
      true,
    );
  }
}

function unwrapConsoleResponse(response) {
  if (!isPlainObject(response) || typeof response.success !== "boolean") {
    throw protocolError(
      ErrorCode.INTERNAL_ERROR,
      "The console bridge returned an invalid response",
    );
  }
  if (response.success) {
    if (!("result" in response)) {
      throw protocolError(ErrorCode.INTERNAL_ERROR, "The console bridge response has no result");
    }
    return response.result;
  }
  const error = response.error;
  if (!isPlainObject(error) || !Object.values(ErrorCode).includes(error.code)) {
    throw protocolError(ErrorCode.INTERNAL_ERROR, "The console bridge returned an invalid error");
  }
  throw protocolError(
    error.code,
    typeof error.message === "string" ? error.message.slice(0, 1_000) : "Console command failed",
    Boolean(error.retryable),
    isPlainObject(error.details) ? error.details : undefined,
  );
}

function abortable(promise, signal) {
  throwIfAborted(signal);
  return new Promise((resolve, reject) => {
    const onAbort = () => reject(abortReason(signal));
    signal.addEventListener("abort", onAbort, { once: true });
    Promise.resolve(promise)
      .then(resolve, reject)
      .finally(() => {
        signal.removeEventListener("abort", onAbort);
      });
  });
}

function throwIfAborted(signal) {
  if (signal.aborted) throw abortReason(signal);
}

function abortReason(signal) {
  return typeof signal.reason?.code === "string"
    ? signal.reason
    : protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
}

function isPlainObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}
