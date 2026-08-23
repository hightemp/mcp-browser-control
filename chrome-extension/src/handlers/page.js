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
    const documentId = await currentDocument(request, tab.id, frameId);
    throwIfCancelled(signal);
    const navigation = request.params.waitForNavigation
      ? createNavigationWaiter(chromeAPI, tab.id, frameId, signal)
      : null;
    let result;
    try {
      result = await bridge.execute({
        tabId: tab.id,
        frameId,
        documentId,
        command: request.command,
        params: request.params,
        signal,
      });
    } catch (error) {
      navigation?.cancel();
      throw error;
    }
    if (navigation) {
      const completed = await navigation.promise;
      result = { ...result, navigation: completed };
    }
    if (request.command !== "page.info") {
      return result;
    }
    throwIfCancelled(signal);
    let frames;
    try {
      frames = await chromeAPI.webNavigation.getAllFrames({ tabId: tab.id });
    } catch (error) {
      throw mapChromeError(error);
    }
    const normalizedFrames = (frames || []).slice(0, 500).map((frame) => ({
      frameId: frame.frameId,
      parentFrameId: frame.parentFrameId,
      documentId: frame.documentId || "",
      url: String(frame.url || "").slice(0, 4_096),
      errorOccurred: Boolean(frame.errorOccurred),
    }));
    return {
      ...result,
      frameCount: (frames || []).length,
      frames: normalizedFrames,
      warnings: [
        ...(Array.isArray(result.warnings) ? result.warnings : []),
        ...((frames || []).length > normalizedFrames.length
          ? ["Frame metadata was truncated at 500 entries"]
          : []),
      ],
    };
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

  async function currentDocument(request, tabId, frameId) {
    const expectedDocumentId =
      request.target?.documentId || request.params.locator?.element?.documentId;
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
    if (expectedDocumentId) {
      assertFreshDocument(expectedDocumentId, frame.documentId);
    }
    if (typeof frame.documentId !== "string" || !frame.documentId) {
      throw protocolError(
        ErrorCode.CAPABILITY_UNAVAILABLE,
        "The browser did not provide a document identity for this frame",
        true,
      );
    }
    return frame.documentId;
  }

  return {
    info: execute,
    getHTML: execute,
    getHTMLBySelector: execute,
    getText: execute,
    query: execute,
    getElement: execute,
    snapshot: execute,
    click: execute,
    fill: execute,
    hover: execute,
    focus: execute,
    blur: execute,
    type: execute,
    clear: execute,
    press: execute,
    select: execute,
    setChecked: execute,
    scroll: execute,
    drag: execute,
    dispatch: execute,
    submit: execute,
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

function createNavigationWaiter(chromeAPI, tabId, frameId, signal) {
  const completedEvent = chromeAPI.webNavigation?.onCompleted;
  const historyEvent = chromeAPI.webNavigation?.onHistoryStateUpdated;
  const fragmentEvent = chromeAPI.webNavigation?.onReferenceFragmentUpdated;
  const errorEvent = chromeAPI.webNavigation?.onErrorOccurred;
  if (!completedEvent?.addListener || !errorEvent?.addListener) {
    throw protocolError(
      ErrorCode.CAPABILITY_UNAVAILABLE,
      "Navigation waiting is unavailable in this browser",
    );
  }

  let settled = false;
  let resolvePromise;
  let rejectPromise;
  const matches = (details) => details.tabId === tabId && details.frameId === frameId;
  const cleanup = () => {
    completedEvent.removeListener(onCompleted);
    historyEvent?.removeListener?.(onHistory);
    fragmentEvent?.removeListener?.(onHistory);
    errorEvent.removeListener(onError);
    signal.removeEventListener("abort", onAbort);
  };
  const finish = (operation) => {
    if (settled) return;
    settled = true;
    cleanup();
    operation();
  };
  const navigationResult = (details, sameDocument) => ({
    tabId,
    frameId,
    documentId: details.documentId || "",
    url: String(details.url || "").slice(0, 4_096),
    sameDocument,
  });
  const onCompleted = (details) => {
    if (matches(details)) finish(() => resolvePromise(navigationResult(details, false)));
  };
  const onHistory = (details) => {
    if (matches(details)) finish(() => resolvePromise(navigationResult(details, true)));
  };
  const onError = (details) => {
    if (matches(details)) {
      finish(() => rejectPromise(protocolError(
        ErrorCode.INTERNAL_ERROR,
        "Navigation failed before completion",
        true,
      )));
    }
  };
  const onAbort = () => finish(() => rejectPromise(
    typeof signal.reason?.code === "string"
      ? signal.reason
      : protocolError(ErrorCode.CANCELLED, "Command was cancelled", true),
  ));
  const promise = new Promise((resolve, reject) => {
    resolvePromise = resolve;
    rejectPromise = reject;
  });
  completedEvent.addListener(onCompleted);
  historyEvent?.addListener?.(onHistory);
  fragmentEvent?.addListener?.(onHistory);
  errorEvent.addListener(onError);
  signal.addEventListener("abort", onAbort, { once: true });
  return {
    promise,
    cancel: () => {
      if (!settled) {
        settled = true;
        cleanup();
      }
    },
  };
}
