import { ErrorCode, assertFreshDocument, mapChromeError, protocolError } from "../protocol.js";
import { ContentScriptBridge } from "../content-bridge.js";

const DEFAULT_COMMAND_TIMEOUT_MS = 30_000;

export function createPageHandlers(chromeAPI, { networkActivity } = {}) {
  const bridge = new ContentScriptBridge(chromeAPI);
  const captureQueues = new Map();

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
    if (request.command === "page.screenshot") {
      return executeScreenshot(request, tab, signal);
    }
    const frameId = request.target?.frameId ?? 0;
    const documentId = await currentDocument(request, tab.id, frameId);
    throwIfCancelled(signal);
    if (request.command === "page.wait") {
      return executeWait(request, {
        tabId: tab.id,
        frameId,
        documentId,
        signal,
        timeoutMs,
      });
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
        return waitResult("delay", startedAt, "timer", {
          delayMs: request.params.delayMs,
        });
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
        const granted = await chromeAPI.permissions.contains({
          permissions: ["webRequest"],
        });
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

  async function executeScreenshot(request, tab, signal) {
    if (!Number.isInteger(tab.windowId) || tab.windowId < 0) {
      throw protocolError(ErrorCode.INTERNAL_ERROR, "The target tab has no browser window");
    }
    return withCaptureLock(captureQueues, tab.windowId, async () => {
      throwIfCancelled(signal);
      let originalTab;
      let activatedTarget = false;
      const warnings = [];
      try {
        const activeTabs = await chromeAPI.tabs.query({
          active: true,
          windowId: tab.windowId,
        });
        throwIfCancelled(signal);
        originalTab = activeTabs[0];
        if (originalTab?.id !== tab.id) {
          await chromeAPI.tabs.update(tab.id, { active: true });
          activatedTarget = true;
          throwIfCancelled(signal);
        }
        const currentTabs = await chromeAPI.tabs.query({
          active: true,
          windowId: tab.windowId,
        });
        if (currentTabs[0]?.id !== tab.id) {
          throw protocolError(
            ErrorCode.INTERNAL_ERROR,
            "The target tab could not be activated for viewport capture",
            true,
          );
        }
        let currentTab;
        try {
          currentTab = await chromeAPI.tabs.get(tab.id);
        } catch {
          throw protocolError(ErrorCode.TAB_NOT_FOUND, `Tab ${tab.id} was not found`);
        }
        if (currentTab.windowId !== tab.windowId) {
          throw protocolError(
            ErrorCode.STALE_TARGET,
            "The target tab moved to another window before capture",
            true,
          );
        }
        await assertPageAccess(currentTab);
        throwIfCancelled(signal);

        const format = request.params.format || "png";
        const options = { format };
        if (format === "jpeg") options.quality = request.params.quality ?? 90;
        let dataURL;
        try {
          dataURL = await chromeAPI.tabs.captureVisibleTab(tab.windowId, options);
        } catch (error) {
          throw mapChromeError(error);
        }
        throwIfCancelled(signal);
        const image = decodeScreenshotDataURL(
          dataURL,
          format,
          request.params.maxBytes ?? 2_000_000,
        );
        const maxWidth = request.params.maxWidth ?? 16_384;
        const maxHeight = request.params.maxHeight ?? 16_384;
        if (image.width > maxWidth || image.height > maxHeight) {
          throw protocolError(
            ErrorCode.PAYLOAD_TOO_LARGE,
            `Screenshot dimensions ${image.width}x${image.height} exceed the requested limits`,
          );
        }
        return {
          capture: "viewport",
          format,
          mimeType: image.mimeType,
          dataBase64: image.dataBase64,
          byteLength: image.byteLength,
          width: image.width,
          height: image.height,
          tabId: tab.id,
          windowId: tab.windowId,
          warnings,
        };
      } finally {
        if (activatedTarget && Number.isInteger(originalTab?.id)) {
          try {
            await chromeAPI.tabs.update(originalTab.id, { active: true });
          } catch {
            warnings.push("The previously active tab could not be restored after capture");
          }
        }
      }
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
    const tabs = await chromeAPI.tabs.query({
      active: true,
      lastFocusedWindow: true,
    });
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
    const granted = await chromeAPI.permissions.contains({
      origins: [originPattern],
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
    screenshot: execute,
  };
}

async function withCaptureLock(queues, windowId, operation) {
  const previous = queues.get(windowId) || Promise.resolve();
  let release;
  const current = new Promise((resolve) => {
    release = resolve;
  });
  queues.set(windowId, current);
  await previous.catch(() => undefined);
  try {
    return await operation();
  } finally {
    release();
    if (queues.get(windowId) === current) queues.delete(windowId);
  }
}

function decodeScreenshotDataURL(dataURL, format, maxBytes) {
  const mimeType = format === "jpeg" ? "image/jpeg" : "image/png";
  const prefix = `data:${mimeType};base64,`;
  if (typeof dataURL !== "string" || !dataURL.startsWith(prefix)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "The browser returned an invalid screenshot");
  }
  const dataBase64 = dataURL.slice(prefix.length);
  if (
    dataBase64.length === 0 ||
    dataBase64.length % 4 !== 0 ||
    !/^[A-Za-z0-9+/]*={0,2}$/.test(dataBase64)
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "The browser returned invalid image data");
  }
  const padding = dataBase64.endsWith("==") ? 2 : dataBase64.endsWith("=") ? 1 : 0;
  const byteLength = (dataBase64.length / 4) * 3 - padding;
  if (byteLength < 1 || byteLength > maxBytes) {
    throw protocolError(
      ErrorCode.PAYLOAD_TOO_LARGE,
      `Screenshot size ${byteLength} bytes exceeds the ${maxBytes} byte limit`,
    );
  }
  let binary;
  try {
    binary = atob(dataBase64);
  } catch {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "The browser returned invalid image data");
  }
  if (binary.length !== byteLength) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "The browser returned invalid image data");
  }
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
  const { width, height } = format === "jpeg" ? jpegDimensions(bytes) : pngDimensions(bytes);
  return { mimeType, dataBase64, byteLength, width, height };
}

function pngDimensions(bytes) {
  const signature = [137, 80, 78, 71, 13, 10, 26, 10];
  if (bytes.length < 24 || signature.some((value, index) => bytes[index] !== value)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "The browser returned an invalid PNG screenshot",
    );
  }
  const width = readUint32(bytes, 16);
  const height = readUint32(bytes, 20);
  return validImageDimensions(width, height);
}

function jpegDimensions(bytes) {
  if (bytes.length < 4 || bytes[0] !== 0xff || bytes[1] !== 0xd8) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "The browser returned an invalid JPEG screenshot",
    );
  }
  const startOfFrame = new Set([
    0xc0, 0xc1, 0xc2, 0xc3, 0xc5, 0xc6, 0xc7, 0xc9, 0xca, 0xcb, 0xcd, 0xce, 0xcf,
  ]);
  let offset = 2;
  while (offset + 3 < bytes.length) {
    while (offset < bytes.length && bytes[offset] !== 0xff) offset += 1;
    while (offset < bytes.length && bytes[offset] === 0xff) offset += 1;
    if (offset >= bytes.length) break;
    const marker = bytes[offset];
    offset += 1;
    if (marker === 0xd8 || marker === 0xd9 || marker === 0x01 || (marker >= 0xd0 && marker <= 0xd7))
      continue;
    if (offset + 1 >= bytes.length) break;
    const segmentLength = (bytes[offset] << 8) | bytes[offset + 1];
    if (segmentLength < 2 || offset + segmentLength > bytes.length) break;
    if (startOfFrame.has(marker) && segmentLength >= 7) {
      const height = (bytes[offset + 3] << 8) | bytes[offset + 4];
      const width = (bytes[offset + 5] << 8) | bytes[offset + 6];
      return validImageDimensions(width, height);
    }
    offset += segmentLength;
  }
  throw protocolError(ErrorCode.INVALID_MESSAGE, "The browser returned an invalid JPEG screenshot");
}

function readUint32(bytes, offset) {
  return (
    bytes[offset] * 0x1000000 +
    (bytes[offset + 1] << 16) +
    (bytes[offset + 2] << 8) +
    bytes[offset + 3]
  );
}

function validImageDimensions(width, height) {
  if (width < 1 || height < 1 || width > 16_384 || height > 16_384) {
    throw protocolError(ErrorCode.PAYLOAD_TOO_LARGE, "Screenshot dimensions are out of range");
  }
  return { width, height };
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
    return Promise.reject(
      protocolError(
        ErrorCode.CAPABILITY_UNAVAILABLE,
        "Event-driven URL waiting is unavailable in this browser",
      ),
    );
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
      finish(() =>
        resolve(
          waitResult("url", startedAt, mode, {
            url: String(url).slice(0, 4_096),
            documentId,
          }),
        ),
      );
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
        const frame = await chromeAPI.webNavigation.getFrame({
          tabId,
          frameId,
        });
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
      finish(() =>
        rejectPromise(
          protocolError(ErrorCode.INTERNAL_ERROR, "Navigation failed before completion", true),
        ),
      );
    }
  };
  const onAbort = () =>
    finish(() =>
      rejectPromise(
        typeof signal.reason?.code === "string"
          ? signal.reason
          : protocolError(ErrorCode.CANCELLED, "Command was cancelled", true),
      ),
    );
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
