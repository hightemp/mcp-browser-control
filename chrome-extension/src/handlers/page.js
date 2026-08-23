import {
  ErrorCode,
  assertFreshDocument,
  mapChromeError,
  protocolError,
} from "../protocol.js";
import { ContentScriptBridge } from "../content-bridge.js";

const DEFAULT_COMMAND_TIMEOUT_MS = 30_000;

export function createPageHandlers(chromeAPI) {
  const bridge = new ContentScriptBridge(chromeAPI);

  function execute(request, signal) {
    return withTimeout(
      (commandSignal) => executeWithinDeadline(request, commandSignal),
      signal,
      request.timeoutMs || DEFAULT_COMMAND_TIMEOUT_MS,
    );
  }

  async function executeWithinDeadline(request, signal) {
    const tab = await resolveTab(request.target?.tabId);
    throwIfCancelled(signal);
    await assertPageAccess(tab);
    throwIfCancelled(signal);
    const frameId = request.target?.frameId ?? 0;
    await assertCurrentDocument(request, tab.id, frameId);
    throwIfCancelled(signal);
    return bridge.execute({
      tabId: tab.id,
      frameId,
      documentId: request.target?.documentId,
      command: request.command,
      params: request.params,
      signal,
    });
  }

  async function resolveTab(explicitTabId) {
    if (Number.isInteger(explicitTabId)) {
      try {
        return await chromeAPI.tabs.get(explicitTabId);
      } catch {
        throw protocolError(ErrorCode.TAB_NOT_FOUND, `Tab ${explicitTabId} was not found`);
      }
    }
    const tabs = await chromeAPI.tabs.query({ active: true, lastFocusedWindow: true });
    if (!tabs[0]) {
      throw protocolError(ErrorCode.TAB_NOT_FOUND, "No active tab was found");
    }
    return tabs[0];
  }

  async function assertPageAccess(tab) {
    let parsed;
    try {
      parsed = new URL(tab.url);
    } catch {
      throw protocolError(ErrorCode.RESTRICTED_URL, "The tab URL cannot be accessed");
    }
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      throw protocolError(ErrorCode.RESTRICTED_URL, `Cannot access ${parsed.protocol} pages`);
    }
    const originPattern = `${parsed.protocol}//${parsed.host}/*`;
    const granted = await chromeAPI.permissions.contains({ origins: [originPattern] });
    if (!granted) {
      throw protocolError(
        ErrorCode.PERMISSION_REQUIRED,
        "Site access is required. Grant it from the extension popup.",
        false,
        { origin: parsed.origin },
      );
    }
  }

  async function assertCurrentDocument(request, tabId, frameId) {
    const expectedDocumentId =
      request.target?.documentId || request.params.locator?.element?.documentId;
    if (!expectedDocumentId) {
      return;
    }

    let frame;
    try {
      frame = await chromeAPI.webNavigation.getFrame({ tabId, frameId });
    } catch (error) {
      throw mapChromeError(error);
    }
    if (!frame) {
      throw protocolError(
        ErrorCode.FRAME_NOT_FOUND,
        "The target frame is no longer available",
        true,
      );
    }
    assertFreshDocument(expectedDocumentId, frame.documentId);
  }

  return {
    getHTML: execute,
    getHTMLBySelector: execute,
    click: execute,
    fill: execute,
  };
}

function throwIfCancelled(signal) {
  if (signal.aborted) {
    throw typeof signal.reason?.code === "string"
      ? signal.reason
      : protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
  }
}

function withTimeout(operation, parentSignal, timeoutMs) {
  if (parentSignal.aborted) {
    return Promise.reject(protocolError(ErrorCode.CANCELLED, "Command was cancelled", true));
  }
  const controller = new AbortController();
  const onParentAbort = () => controller.abort(
    protocolError(ErrorCode.CANCELLED, "Command was cancelled", true),
  );
  parentSignal.addEventListener("abort", onParentAbort, { once: true });
  const timeout = setTimeout(() => controller.abort(
    protocolError(ErrorCode.TIMEOUT, `Command timed out after ${timeoutMs} ms`, true),
  ), timeoutMs);

  return Promise.race([
    operation(controller.signal),
    new Promise((_, reject) => {
      controller.signal.addEventListener("abort", () => reject(controller.signal.reason), {
        once: true,
      });
    }),
  ]).finally(() => {
    clearTimeout(timeout);
    parentSignal.removeEventListener("abort", onParentAbort);
  });
}
