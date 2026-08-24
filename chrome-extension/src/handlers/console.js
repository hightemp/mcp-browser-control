import { ErrorCode, assertFreshDocument, mapChromeError, protocolError } from "../protocol.js";
import { ConsoleCaptureBridge } from "../console-bridge.js";

const DEFAULT_TIMEOUT_MS = 30_000;
const CDP_COMMANDS = Object.freeze([
  "Page.getFrameTree",
  "Runtime.enable",
  "Log.enable",
  "Network.enable",
]);
const CDP_EVENTS = Object.freeze([
  "Runtime.consoleAPICalled",
  "Runtime.exceptionThrown",
  "Log.entryAdded",
  "Network.loadingFailed",
]);

export function createConsoleHandlers(chromeAPI, { cdpSessions } = {}) {
  const bridge = new ConsoleCaptureBridge(chromeAPI);
  const cdpCaptures = new Map();

  chromeAPI.webNavigation?.onCommitted?.addListener?.((details) => {
    for (const capture of cdpCaptures.values()) {
      if (capture.tabId === details.tabId && capture.frameId === details.frameId) {
        void releaseCDPCapture(cdpCaptures, capture).catch(() => undefined);
      }
    }
  });

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
        let result;
        try {
          result = await bridge.execute({
            tabId: tab.id,
            frameId,
            documentId: frame.documentId,
            operationId: request.requestId,
            command: request.command,
            params: request.params,
            signal,
          });
        } finally {
          if (request.command === "console.stop") {
            await releaseMatchingCapture(cdpCaptures, tab.id, frameId, frame.documentId);
          }
        }

        let enrichmentWarning = "";
        if (request.command === "console.start") {
          enrichmentWarning = await startCDPCapture({
            chromeAPI,
            cdpSessions,
            cdpCaptures,
            bridge,
            tabId: tab.id,
            frameId,
            documentId: frame.documentId,
            signal,
          });
        }
        const cdpEnriched = cdpCaptures.has(captureKey(tab.id, frameId, frame.documentId));
        return {
          ...result,
          tabId: tab.id,
          frameId,
          documentId: frame.documentId,
          cdpEnriched,
          backends: cdpEnriched ? ["bridge", "cdp"] : ["bridge"],
          warnings: uniqueWarnings([
            ...(Array.isArray(result.warnings) ? result.warnings : []),
            enrichmentWarning,
          ]),
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

async function startCDPCapture({
  chromeAPI,
  cdpSessions,
  cdpCaptures,
  bridge,
  tabId,
  frameId,
  documentId,
  signal,
}) {
  await releaseStaleCaptures(cdpCaptures, tabId, frameId, documentId);
  if (!cdpSessions || frameId !== 0) return "";
  let debuggerGranted;
  try {
    debuggerGranted = await chromeAPI.permissions.contains({ permissions: ["debugger"] });
  } catch {
    return "CDP console enrichment could not verify Debug permission";
  }
  if (!debuggerGranted) return "";

  const key = captureKey(tabId, frameId, documentId);
  let capture;
  try {
    const lease = await cdpSessions.acquire(
      { tabId },
      {
        consumerId: `console:${tabId}:${documentId}`.slice(0, 128),
        domains: ["Runtime", "Log", "Network", "Page"],
        commands: CDP_COMMANDS,
        events: CDP_EVENTS,
        signal,
        onEvent: async (event) => {
          if (!capture?.active) return;
          const entry = normalizeCDPEvent(capture, event);
          if (!entry) return;
          await bridge.ingest({
            tabId,
            frameId,
            documentId,
            entry,
            timestamp: entry.timestamp,
          });
        },
        onDetach: () => {
          if (!capture) return;
          capture.active = false;
          if (cdpCaptures.get(key) === capture) cdpCaptures.delete(key);
        },
      },
    );
    capture = { key, tabId, frameId, documentId, lease, rootFrameId: "", active: true };
    cdpCaptures.set(key, capture);
    const frameTree = await lease.sendCommand("Page.getFrameTree", {}, { signal });
    capture.rootFrameId = String(frameTree?.frameTree?.frame?.id || "").slice(0, 256);
    if (!capture.rootFrameId) {
      throw protocolError(ErrorCode.INVALID_MESSAGE, "The browser returned invalid frame metadata");
    }
    await lease.sendCommand("Runtime.enable", {}, { signal });
    await lease.sendCommand("Log.enable", {}, { signal });
    await lease.sendCommand("Network.enable", {}, { signal });
    return "";
  } catch {
    if (capture) await releaseCDPCapture(cdpCaptures, capture);
    if (signal.aborted) throw signal.reason;
    return "CDP console enrichment was unavailable; bridge capture remains active";
  }
}

function normalizeCDPEvent(capture, event) {
  const params = event?.params || {};
  switch (event?.method) {
    case "Runtime.consoleAPICalled": {
      if (!runtimeEventBelongsToRoot(capture, params.executionContextId)) return null;
      return {
        backend: "cdp",
        scope: "frame",
        timestamp: cdpTimestamp(params.timestamp),
        kind: "console",
        level: consoleLevel(params.type),
        method: boundedString(params.type, 100),
        args: (Array.isArray(params.args) ? params.args : [])
          .slice(0, 20)
          .map(serializeRemoteObject),
        stack: stackTraceText(params.stackTrace),
      };
    }
    case "Runtime.exceptionThrown": {
      const details = params.exceptionDetails || {};
      if (!runtimeEventBelongsToRoot(capture, details.executionContextId)) return null;
      return {
        backend: "cdp",
        scope: "frame",
        timestamp: cdpTimestamp(params.timestamp),
        kind: "exception",
        level: "error",
        method: "exceptionThrown",
        args: [boundedString(details.text, 2_000), serializeRemoteObject(details.exception)],
        stack:
          stackTraceText(details.stackTrace) ||
          boundedString(details.exception?.description, 8_000),
        source: boundedString(details.url, 4_000),
        line: nonNegativeInteger(details.lineNumber),
        column: nonNegativeInteger(details.columnNumber),
      };
    }
    case "Log.entryAdded": {
      const entry = params.entry || {};
      return {
        backend: "cdp",
        scope: "tab",
        timestamp: cdpTimestamp(entry.timestamp),
        kind: entry.source === "network" ? "resourceError" : "exception",
        level: logLevel(entry.level),
        method: boundedString(entry.source, 100) || "log",
        args: [boundedString(entry.text, 4_000)],
        stack: stackTraceText(entry.stackTrace),
        source: boundedString(entry.url, 4_000),
        line: nonNegativeInteger(entry.lineNumber),
      };
    }
    case "Network.loadingFailed":
      return {
        backend: "cdp",
        scope: "tab",
        timestamp: new Date().toISOString(),
        kind: "resourceError",
        level: "error",
        method: "loadingFailed",
        args: [
          {
            requestId: boundedString(params.requestId, 256),
            resourceType: boundedString(params.type, 100),
            errorText: boundedString(params.errorText, 2_000),
            canceled: Boolean(params.canceled),
            blockedReason: boundedString(params.blockedReason, 100),
          },
        ],
      };
    default:
      return null;
  }
}

function runtimeEventBelongsToRoot(capture, executionContextId) {
  if (!Number.isInteger(executionContextId)) return false;
  return capture.lease
    .frameContexts(capture.rootFrameId)
    .some((context) => context.contextId === executionContextId && !context.sessionId);
}

function serializeRemoteObject(remoteObject) {
  if (!remoteObject || typeof remoteObject !== "object") return null;
  if (["string", "number", "boolean"].includes(typeof remoteObject.value)) {
    return remoteObject.value;
  }
  if (remoteObject.value === null || remoteObject.subtype === "null") return null;
  if (remoteObject.unserializableValue !== undefined) {
    return boundedString(String(remoteObject.unserializableValue), 500);
  }
  const preview = remoteObject.preview;
  return {
    type: boundedString(remoteObject.type, 50),
    subtype: boundedString(remoteObject.subtype, 50),
    description: boundedString(remoteObject.description, 4_000),
    ...(preview && Array.isArray(preview.properties)
      ? {
          preview: Object.fromEntries(
            preview.properties
              .slice(0, 20)
              .map((property) => [
                boundedString(property?.name, 100) || "property",
                boundedString(property?.value, 1_000),
              ]),
          ),
        }
      : {}),
  };
}

function stackTraceText(stackTrace, depth = 0) {
  if (!stackTrace || depth > 2) return "";
  const lines = (Array.isArray(stackTrace.callFrames) ? stackTrace.callFrames : [])
    .slice(0, 20)
    .map(
      (frame) =>
        `${boundedString(frame?.functionName, 200) || "anonymous"} (${boundedString(frame?.url, 2_000)}:${nonNegativeInteger(frame?.lineNumber) + 1}:${nonNegativeInteger(frame?.columnNumber) + 1})`,
    );
  const parent = stackTrace.parent ? stackTraceText(stackTrace.parent, depth + 1) : "";
  return boundedString([...lines, parent].filter(Boolean).join("\n"), 8_000);
}

function consoleLevel(type) {
  if (type === "debug") return "debug";
  if (type === "info") return "info";
  if (["warning", "warn"].includes(type)) return "warn";
  if (["error", "assert"].includes(type)) return "error";
  return "log";
}

function logLevel(level) {
  return ["debug", "info", "warning", "error"].includes(level)
    ? level === "warning"
      ? "warn"
      : level
    : "log";
}

function cdpTimestamp(value) {
  if (!Number.isFinite(value) || value < 1_000_000_000) return new Date().toISOString();
  const date = new Date(value * 1_000);
  return Number.isFinite(date.getTime()) ? date.toISOString() : new Date().toISOString();
}

function nonNegativeInteger(value) {
  return Number.isInteger(value) ? Math.max(0, value) : 0;
}

function boundedString(value, maximum) {
  return typeof value === "string" ? value.slice(0, maximum) : "";
}

function captureKey(tabId, frameId, documentId) {
  return `${tabId}:${frameId}:${documentId}`;
}

async function releaseMatchingCapture(captures, tabId, frameId, documentId) {
  const capture = captures.get(captureKey(tabId, frameId, documentId));
  if (capture) await releaseCDPCapture(captures, capture);
}

async function releaseStaleCaptures(captures, tabId, frameId, documentId) {
  for (const capture of [...captures.values()]) {
    if (capture.tabId === tabId && capture.frameId === frameId) {
      await releaseCDPCapture(captures, capture);
    }
  }
  captures.delete(captureKey(tabId, frameId, documentId));
}

async function releaseCDPCapture(captures, capture) {
  if (!capture.active) return;
  capture.active = false;
  if (captures.get(capture.key) === capture) captures.delete(capture.key);
  await capture.lease.release();
}

function uniqueWarnings(values) {
  return [...new Set(values.filter(Boolean))].slice(0, 10);
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
