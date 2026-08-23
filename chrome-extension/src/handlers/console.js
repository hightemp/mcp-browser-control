import { ErrorCode, assertFreshDocument, mapChromeError, protocolError } from "../protocol.js";
import { ConsoleCaptureBridge } from "../console-bridge.js";

const DEFAULT_TIMEOUT_MS = 30_000;

export function createConsoleHandlers(chromeAPI) {
  const bridge = new ConsoleCaptureBridge(chromeAPI);

  async function execute(request, parentSignal) {
    const timeoutMs = request.timeoutMs || DEFAULT_TIMEOUT_MS;
    return withTimeout(
      async (signal) => {
        const tab = await resolveTab(chromeAPI, request.target?.tabId);
        throwIfCancelled(signal);
        await assertPageAccess(chromeAPI, tab);
        const frameId = request.target?.frameId ?? 0;
        const frame = await currentFrame(chromeAPI, request, tab.id, frameId);
        throwIfCancelled(signal);
        const result = await bridge.execute({
          tabId: tab.id,
          frameId,
          documentId: frame.documentId,
          operationId: request.requestId,
          command: request.command,
          params: request.params,
          signal,
        });
        return {
          ...result,
          tabId: tab.id,
          frameId,
          documentId: frame.documentId,
        };
      },
      parentSignal,
      timeoutMs,
    );
  }

  return {
    start: execute,
    stop: execute,
    clear: execute,
    read: execute,
  };
}

async function resolveTab(chromeAPI, explicitTabId) {
  if (Number.isInteger(explicitTabId)) {
    try {
      return await chromeAPI.tabs.get(explicitTabId);
    } catch {
      throw protocolError(ErrorCode.TAB_NOT_FOUND, `Tab ${explicitTabId} was not found`);
    }
  }
  const tabs = await chromeAPI.tabs.query({
    active: true,
    lastFocusedWindow: true,
  });
  if (!tabs[0]) throw protocolError(ErrorCode.TAB_NOT_FOUND, "No active tab was found");
  return tabs[0];
}

async function assertPageAccess(chromeAPI, tab) {
  let parsed;
  try {
    parsed = new URL(tab.url);
  } catch {
    throw protocolError(ErrorCode.RESTRICTED_URL, "The tab URL cannot be accessed");
  }
  if (!new Set(["http:", "https:"]).has(parsed.protocol)) {
    throw protocolError(ErrorCode.RESTRICTED_URL, `Cannot access ${parsed.protocol} pages`);
  }
  const granted = await chromeAPI.permissions.contains({
    origins: [`${parsed.protocol}//${parsed.hostname}/*`],
  });
  if (!granted) {
    throw protocolError(
      ErrorCode.PERMISSION_REQUIRED,
      "Site access is required. Grant it from the extension popup.",
      false,
      { origin: parsed.origin },
    );
  }
}

async function currentFrame(chromeAPI, request, tabId, frameId) {
  let frame;
  try {
    frame = await chromeAPI.webNavigation.getFrame({ tabId, frameId });
  } catch (error) {
    throw mapChromeError(error);
  }
  if (!frame?.documentId) {
    throw protocolError(ErrorCode.FRAME_NOT_FOUND, "The target frame is unavailable", true);
  }
  if (request.target?.documentId) {
    assertFreshDocument(request.target.documentId, frame.documentId);
  }
  return frame;
}

function withTimeout(operation, parentSignal, timeoutMs) {
  if (parentSignal.aborted) {
    return Promise.reject(protocolError(ErrorCode.CANCELLED, "Command was cancelled", true));
  }
  const controller = new AbortController();
  const onParentAbort = () =>
    controller.abort(protocolError(ErrorCode.CANCELLED, "Command was cancelled", true));
  parentSignal.addEventListener("abort", onParentAbort, { once: true });
  const timeout = setTimeout(
    () =>
      controller.abort(
        protocolError(ErrorCode.TIMEOUT, `Command timed out after ${timeoutMs} ms`, true),
      ),
    timeoutMs,
  );
  return Promise.race([
    operation(controller.signal),
    new Promise((_, reject) =>
      controller.signal.addEventListener("abort", () => reject(controller.signal.reason), {
        once: true,
      }),
    ),
  ]).finally(() => {
    clearTimeout(timeout);
    parentSignal.removeEventListener("abort", onParentAbort);
  });
}

function throwIfCancelled(signal) {
  if (signal.aborted) throw signal.reason;
}
