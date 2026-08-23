import {
  ErrorCode,
  assertFreshDocument,
  mapChromeError,
  protocolError,
} from "../protocol.js";
import { ContentScriptBridge } from "../content-bridge.js";

const DEFAULT_COMMAND_TIMEOUT_MS = 30_000;

export function createPageHandlers(chromeAPI, { networkActivity } = {}) {
  const bridge = new ContentScriptBridge(chromeAPI);

  function execute(request, signal) {
    const timeoutMs = request.timeoutMs || DEFAULT_COMMAND_TIMEOUT_MS;
    return withTimeout(
      (commandSignal) => executeWithinDeadline(request, commandSignal, timeoutMs),
      signal,
      timeoutMs,
    );
  }

  async function executeWithinDeadline(request, signal, timeoutMs) {
    const tab = await resolveTab(request.target?.tabId);
    throwIfCancelled(signal);
    await assertPageAccess(tab);
    throwIfCancelled(signal);
    const frameId = request.target?.frameId ?? 0;
    const documentId = await currentDocument(request, tab.id, frameId);
    throwIfCancelled(signal);
    if (request.command === "page.wait") {
      return executeWait(request, { tabId: tab.id, frameId, documentId, signal, timeoutMs });
    }
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

  async function executeWait(request, context) {
    const startedAt = Date.now();
    switch (request.params.condition) {
      case "delay":
        await waitForDelay(request.params.delayMs, context.signal);
        return waitResult("delay", startedAt, "timer", { delayMs: request.params.delayMs });
      case "navigation": {
        const navigation = createNavigationWaiter(
          chromeAPI,
          context.tabId,
          context.frameId,
          context.signal,
        );
        return waitResult("navigation", startedAt, "event", {
          navigation: await navigation.promise,
        });
      }
      case "url":
        return waitForURL(
          chromeAPI,
          context.tabId,
          context.frameId,
          request.params,
          context.signal,
        );
      case "networkIdle": {
        const granted = await chromeAPI.permissions.contains({ permissions: ["webRequest"] });
        if (!granted || !networkActivity?.available) {
          throw protocolError(
            ErrorCode.CAPABILITY_UNAVAILABLE,
            "Network idle requires the Observe network activity permission",
          );
        }
        return networkActivity.waitForIdle({
          tabId: context.tabId,
          idleMs: request.params.idleMs,
          signal: context.signal,
        });
      }
      default:
        return bridge.execute({
          tabId: context.tabId,
          frameId: context.frameId,
          documentId: context.documentId,
          operationId: request.requestId,
          command: request.command,
          params: { ...request.params, internalTimeoutMs: context.timeoutMs },
          signal: context.signal,
        });
    }
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
    wait: execute,
  };
}

function waitForDelay(delayMs, signal) {
  if (signal.aborted) return Promise.reject(abortReason(signal));
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve();
    }, delayMs);
    const onAbort = () => {
      clearTimeout(timeout);
      signal.removeEventListener("abort", onAbort);
      reject(abortReason(signal));
    };
    signal.addEventListener("abort", onAbort, { once: true });
  });
}

function waitResult(condition, startedAt, mode, extra = {}) {
  return {
    condition,
    matched: true,
    mode,
    elapsedMs: Math.max(0, Date.now() - startedAt),
    ...extra,
    timestamp: new Date().toISOString(),
  };
}

function waitForURL(chromeAPI, tabId, frameId, params, signal) {
  if (signal.aborted) return Promise.reject(abortReason(signal));
  const startedAt = Date.now();
  const mode = params.mode || "auto";
  const pollIntervalMs = params.pollIntervalMs || 100;
  const events = [
    chromeAPI.webNavigation?.onCommitted,
    chromeAPI.webNavigation?.onCompleted,
    chromeAPI.webNavigation?.onHistoryStateUpdated,
    chromeAPI.webNavigation?.onReferenceFragmentUpdated,
  ].filter((event) => event?.addListener && event?.removeListener);
  if (mode === "event" && events.length === 0) {
    return Promise.reject(protocolError(
      ErrorCode.CAPABILITY_UNAVAILABLE,
      "Event-driven URL waiting is unavailable in this browser",
    ));
  }

  return new Promise((resolve, reject) => {
    let settled = false;
    let checking = false;
    let pollTimer = null;
    const cleanup = () => {
      if (pollTimer !== null) clearInterval(pollTimer);
      for (const event of events) event.removeListener(onNavigation);
      signal.removeEventListener("abort", onAbort);
    };
    const finish = (operation) => {
      if (settled) return;
      settled = true;
      cleanup();
      operation();
    };
    const match = (url, documentId = "") => {
      if (!urlMatches(url, params)) return false;
      finish(() => resolve(waitResult("url", startedAt, mode, {
        url: String(url).slice(0, 4_096),
        documentId,
      })));
      return true;
    };
    const onNavigation = (details) => {
      if (details.tabId === tabId && details.frameId === frameId) {
        match(details.url, details.documentId || "");
      }
    };
    const checkCurrent = async () => {
      if (settled || checking) return;
      checking = true;
      try {
        const frame = await chromeAPI.webNavigation.getFrame({ tabId, frameId });
        if (frame) match(frame.url, frame.documentId || "");
      } catch (error) {
        const mapped = mapChromeError(error);
        if (![ErrorCode.FRAME_NOT_FOUND, ErrorCode.TAB_NOT_FOUND].includes(mapped.code)) {
          finish(() => reject(mapped));
        }
      } finally {
        checking = false;
      }
    };
    const onAbort = () => finish(() => reject(abortReason(signal)));

    if (mode !== "polling") {
      for (const event of events) event.addListener(onNavigation);
    }
    if (mode !== "event") pollTimer = setInterval(checkCurrent, pollIntervalMs);
    signal.addEventListener("abort", onAbort, { once: true });
    void checkCurrent();
  });
}

function urlMatches(url, params) {
  if (params.url !== undefined) return url === params.url;
  const expression = String(params.urlPattern)
    .split("*")
    .map((part) => part.replace(/[|\\{}()[\]^$+?.]/g, "\\$&"))
    .join(".*");
  return new RegExp(`^${expression}$`).test(url);
}

function throwIfCancelled(signal) {
  if (signal.aborted) {
    throw typeof signal.reason?.code === "string"
      ? signal.reason
      : protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
  }
}

function abortReason(signal) {
  return typeof signal.reason?.code === "string"
    ? signal.reason
    : protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
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
