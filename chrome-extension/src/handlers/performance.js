import { ErrorCode, assertFreshDocument, mapChromeError, protocolError } from "../protocol.js";

const DEFAULT_TIMEOUT_MS = 30_000;
const MAX_TIMEOUT_MS = 120_000;
const MIN_DURATION_MS = 100;
const MAX_DURATION_MS = 10_000;
const MIN_ARTIFACT_BYTES = 64 * 1024;
const MAX_ARTIFACT_BYTES = 2_000_000;
const MAX_METRICS = 200;
const MAX_METRIC_NAME_CHARS = 200;
const MAX_AUDIT_ISSUES = 500;
const TRACE_CHUNK_BYTES = 64 * 1024;
const TRACE_CATEGORIES = ["blink.user_timing", "devtools.timeline", "loading", "v8.execute"].join(
  ",",
);

const CAPTURE_CONFIG = Object.freeze({
  trace: Object.freeze({
    domains: Object.freeze(["Tracing", "IO"]),
    commands: Object.freeze(["Tracing.start", "Tracing.end", "IO.read", "IO.close"]),
    events: Object.freeze(["Tracing.tracingComplete"]),
  }),
  coverage: Object.freeze({
    domains: Object.freeze(["Profiler"]),
    commands: Object.freeze([
      "Profiler.enable",
      "Profiler.startPreciseCoverage",
      "Profiler.takePreciseCoverage",
      "Profiler.stopPreciseCoverage",
      "Profiler.disable",
    ]),
    events: Object.freeze([]),
  }),
  cpuProfile: Object.freeze({
    domains: Object.freeze(["Profiler"]),
    commands: Object.freeze([
      "Profiler.enable",
      "Profiler.setSamplingInterval",
      "Profiler.start",
      "Profiler.stop",
      "Profiler.disable",
    ]),
    events: Object.freeze([]),
  }),
  audits: Object.freeze({
    domains: Object.freeze(["Audits"]),
    commands: Object.freeze(["Audits.enable", "Audits.disable"]),
    events: Object.freeze(["Audits.issueAdded"]),
  }),
});

export function createPerformanceHandlers(chromeAPI, { cdpSessions } = {}) {
  let sequence = 0;

  async function metrics(request, parentSignal) {
    assertEmptyParams(request.params);
    const timeoutMs = validateTimeout(request.timeoutMs);
    return withTimeout(
      async (signal) => {
        const { tab, document } = await prepareTarget(chromeAPI, cdpSessions, request, signal);
        sequence += 1;
        const raw = await cdpSessions.withSession(
          { tabId: tab.id },
          {
            consumerId: `performance-metrics:${tab.id}:${sequence}`,
            domains: ["Performance"],
            commands: ["Performance.getMetrics"],
            signal,
          },
          async (lease) => {
            await recheckTarget(chromeAPI, tab.id, document.documentId);
            const value = await lease.sendCommand("Performance.getMetrics", {}, { signal });
            await recheckTarget(chromeAPI, tab.id, document.documentId);
            return value;
          },
        );
        return {
          tabId: tab.id,
          documentId: document.documentId,
          metrics: normalizeMetrics(raw?.metrics),
          warnings: [],
        };
      },
      parentSignal,
      timeoutMs,
    );
  }

  async function capture(request, parentSignal) {
    const captureParams = assertCaptureParams(request.params);
    const timeoutMs = validateTimeout(request.timeoutMs);
    if (timeoutMs < captureParams.durationMs + 1_000) {
      throw protocolError(
        ErrorCode.INVALID_MESSAGE,
        "Performance timeout must exceed durationMs by at least 1000 ms",
      );
    }
    return withTimeout(
      async (signal) => {
        const { tab, document } = await prepareTarget(chromeAPI, cdpSessions, request, signal);
        const config = CAPTURE_CONFIG[captureParams.kind];
        const state = createCaptureState(captureParams.kind, captureParams.maxBytes);
        sequence += 1;
        const bytes = await cdpSessions.withSession(
          { tabId: tab.id },
          {
            consumerId: `performance-${captureParams.kind}:${tab.id}:${sequence}`,
            domains: [...config.domains],
            commands: [...config.commands],
            ...(config.events.length > 0 ? { events: [...config.events] } : {}),
            signal,
            ...(config.events.length > 0
              ? { onEvent: (event) => handleCaptureEvent(state, event) }
              : {}),
          },
          async (lease) => {
            await recheckTarget(chromeAPI, tab.id, document.documentId);
            const value = await runCapture(
              lease,
              state,
              captureParams,
              signal,
              document.documentId,
              tab.id,
              chromeAPI,
            );
            await recheckTarget(chromeAPI, tab.id, document.documentId);
            return value;
          },
        );
        return {
          kind: captureParams.kind,
          mimeType: "application/json",
          dataBase64: bytesToBase64(bytes),
          byteLength: bytes.byteLength,
          tabId: tab.id,
          documentId: document.documentId,
          durationMs: captureParams.durationMs,
          warnings: state.truncated
            ? ["Audit issues were truncated to the configured count or byte limit"]
            : [],
        };
      },
      parentSignal,
      timeoutMs,
    );
  }

  return { metrics, capture };
}

function createCaptureState(kind, maxBytes) {
  const state = {
    kind,
    issues: [],
    issueBytes: 0,
    maxBytes,
    truncated: false,
    traceComplete: null,
    resolveTrace: null,
  };
  if (kind === "trace") {
    state.traceComplete = new Promise((resolve) => {
      state.resolveTrace = resolve;
    });
  }
  return state;
}

function handleCaptureEvent(state, event) {
  if (event?.droppedBefore > 0) state.truncated = true;
  if (state.kind === "trace" && event?.method === "Tracing.tracingComplete") {
    state.resolveTrace?.(event.params || {});
    return;
  }
  if (state.kind === "audits" && event?.method === "Audits.issueAdded") {
    if (state.issues.length >= MAX_AUDIT_ISSUES) {
      state.truncated = true;
      return;
    }
    if (
      !event.params?.issue ||
      typeof event.params.issue !== "object" ||
      Array.isArray(event.params.issue)
    ) {
      state.truncated = true;
      return;
    }
    try {
      const json = JSON.stringify(event.params.issue);
      const bytes = new TextEncoder().encode(json).byteLength;
      if (state.issueBytes + bytes > state.maxBytes) {
        state.truncated = true;
        return;
      }
      state.issues.push(JSON.parse(json));
      state.issueBytes += bytes;
    } catch {
      state.truncated = true;
    }
  }
}

async function runCapture(lease, state, params, signal, documentId, tabId, chromeAPI) {
  switch (params.kind) {
    case "trace":
      return captureTrace(lease, state, params, signal);
    case "coverage":
      return captureCoverage(lease, params, signal);
    case "cpuProfile":
      return captureCPUProfile(lease, params, signal);
    case "audits":
      return captureAudits(lease, state, params, signal, documentId, tabId, chromeAPI);
    default:
      throw protocolError(ErrorCode.INVALID_COMMAND, "Performance capture kind is not allowlisted");
  }
}

async function captureTrace(lease, state, params, signal) {
  let started = false;
  let ended = false;
  let stream = "";
  try {
    await lease.sendCommand(
      "Tracing.start",
      {
        categories: TRACE_CATEGORIES,
        transferMode: "ReturnAsStream",
        streamFormat: "json",
        streamCompression: "none",
      },
      { signal },
    );
    started = true;
    await delay(params.durationMs, signal);
    await lease.sendCommand("Tracing.end", {}, { signal });
    ended = true;
    const completed = await waitWithSignal(state.traceComplete, signal);
    if (
      typeof completed?.stream !== "string" ||
      completed.stream.length === 0 ||
      completed.stream.length > 1_000 ||
      (completed.traceFormat !== undefined && completed.traceFormat !== "json") ||
      (completed.streamCompression !== undefined && completed.streamCompression !== "none")
    ) {
      throw invalidCaptureResult();
    }
    stream = completed.stream;
    const bytes = await readTraceStream(lease, stream, params.maxBytes, signal);
    stream = "";
    assertJSONObjectBytes(bytes);
    return bytes;
  } finally {
    if (started && !ended) await bestEffortCommand(lease, "Tracing.end", {});
    if (stream) await bestEffortCommand(lease, "IO.close", { handle: stream });
  }
}

async function readTraceStream(lease, stream, maxBytes, signal) {
  const chunks = [];
  let total = 0;
  try {
    for (let reads = 0; reads < 100_000; reads += 1) {
      const value = await lease.sendCommand(
        "IO.read",
        { handle: stream, size: Math.min(TRACE_CHUNK_BYTES, maxBytes + 1 - total) },
        { signal },
      );
      if (typeof value?.data !== "string" || typeof value?.eof !== "boolean") {
        throw invalidCaptureResult();
      }
      const chunk = value.base64Encoded
        ? decodeBase64(value.data)
        : new TextEncoder().encode(value.data);
      total += chunk.byteLength;
      if (total > maxBytes) {
        throw protocolError(ErrorCode.PAYLOAD_TOO_LARGE, `The trace exceeds ${maxBytes} bytes`);
      }
      chunks.push(chunk);
      if (value.eof) return joinBytes(chunks, total);
    }
    throw invalidCaptureResult();
  } finally {
    await bestEffortCommand(lease, "IO.close", { handle: stream });
  }
}

async function captureCoverage(lease, params, signal) {
  let enabled = false;
  let started = false;
  try {
    await lease.sendCommand("Profiler.enable", {}, { signal });
    enabled = true;
    await lease.sendCommand(
      "Profiler.startPreciseCoverage",
      { callCount: true, detailed: true, allowTriggeredUpdates: false },
      { signal },
    );
    started = true;
    await delay(params.durationMs, signal);
    const coverage = await lease.sendCommand("Profiler.takePreciseCoverage", {}, { signal });
    await lease.sendCommand("Profiler.stopPreciseCoverage", {}, { signal });
    started = false;
    await lease.sendCommand("Profiler.disable", {}, { signal });
    enabled = false;
    if (!Array.isArray(coverage?.result) || !Number.isFinite(coverage?.timestamp)) {
      throw invalidCaptureResult();
    }
    return encodeJSONObject(
      { kind: "coverage", timestamp: coverage.timestamp, result: coverage.result },
      params.maxBytes,
    );
  } finally {
    if (started) await bestEffortCommand(lease, "Profiler.stopPreciseCoverage", {});
    if (enabled) await bestEffortCommand(lease, "Profiler.disable", {});
  }
}

async function captureCPUProfile(lease, params, signal) {
  let enabled = false;
  let started = false;
  try {
    await lease.sendCommand("Profiler.enable", {}, { signal });
    enabled = true;
    await lease.sendCommand("Profiler.setSamplingInterval", { interval: 1_000 }, { signal });
    await lease.sendCommand("Profiler.start", {}, { signal });
    started = true;
    await delay(params.durationMs, signal);
    const result = await lease.sendCommand("Profiler.stop", {}, { signal });
    started = false;
    await lease.sendCommand("Profiler.disable", {}, { signal });
    enabled = false;
    if (!result?.profile || typeof result.profile !== "object" || Array.isArray(result.profile)) {
      throw invalidCaptureResult();
    }
    return encodeJSONObject({ kind: "cpuProfile", profile: result.profile }, params.maxBytes);
  } finally {
    if (started) await bestEffortCommand(lease, "Profiler.stop", {});
    if (enabled) await bestEffortCommand(lease, "Profiler.disable", {});
  }
}

async function captureAudits(lease, state, params, signal, documentId, tabId, chromeAPI) {
  let enabled = false;
  try {
    await lease.sendCommand("Audits.enable", {}, { signal });
    enabled = true;
    await delay(params.durationMs, signal);
    await recheckTarget(chromeAPI, tabId, documentId);
    await lease.sendCommand("Audits.disable", {}, { signal });
    enabled = false;
    let value = {
      kind: "audits",
      issueCount: state.issues.length,
      issues: state.issues,
      truncated: state.truncated,
    };
    let bytes = encodeJSONObject(value, params.maxBytes, false);
    while (bytes.byteLength > params.maxBytes && state.issues.length > 0) {
      state.issues.pop();
      state.truncated = true;
      value = {
        kind: "audits",
        issueCount: state.issues.length,
        issues: state.issues,
        truncated: true,
      };
      bytes = encodeJSONObject(value, params.maxBytes, false);
    }
    if (bytes.byteLength > params.maxBytes) throw payloadTooLarge(params.maxBytes);
    return bytes;
  } finally {
    if (enabled) await bestEffortCommand(lease, "Audits.disable", {});
  }
}

function normalizeMetrics(value) {
  if (!Array.isArray(value) || value.length > MAX_METRICS) throw invalidCaptureResult();
  const seen = new Set();
  return value.map((metric) => {
    if (
      !metric ||
      typeof metric !== "object" ||
      Array.isArray(metric) ||
      Object.keys(metric).some((key) => !["name", "value"].includes(key)) ||
      typeof metric.name !== "string" ||
      metric.name.trim() === "" ||
      [...metric.name].length > MAX_METRIC_NAME_CHARS ||
      !Number.isFinite(metric.value) ||
      seen.has(metric.name)
    ) {
      throw invalidCaptureResult();
    }
    seen.add(metric.name);
    return { name: metric.name, value: metric.value };
  });
}

function assertEmptyParams(params) {
  if (
    !params ||
    typeof params !== "object" ||
    Array.isArray(params) ||
    Object.keys(params).length > 0
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Performance metrics parameters must be empty");
  }
}

function assertCaptureParams(params) {
  if (!params || typeof params !== "object" || Array.isArray(params)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Performance capture parameters must be an object",
    );
  }
  const allowed = new Set(["kind", "durationMs", "maxBytes"]);
  if (Object.keys(params).some((key) => !allowed.has(key))) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Performance capture parameters contain an unknown field",
    );
  }
  if (!CAPTURE_CONFIG[params.kind]) {
    throw protocolError(ErrorCode.INVALID_COMMAND, "Performance capture kind is not allowlisted");
  }
  if (!integerBetween(params.durationMs, MIN_DURATION_MS, MAX_DURATION_MS)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "durationMs is outside the supported range");
  }
  if (!integerBetween(params.maxBytes, MIN_ARTIFACT_BYTES, MAX_ARTIFACT_BYTES)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "maxBytes is outside the supported range");
  }
  return { kind: params.kind, durationMs: params.durationMs, maxBytes: params.maxBytes };
}

function validateTimeout(value) {
  const timeoutMs = value || DEFAULT_TIMEOUT_MS;
  if (!integerBetween(timeoutMs, 1, MAX_TIMEOUT_MS)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Performance timeout is outside the supported range",
    );
  }
  return timeoutMs;
}

async function prepareTarget(chromeAPI, cdpSessions, request, signal) {
  if (!cdpSessions) {
    throw protocolError(ErrorCode.CAPABILITY_UNAVAILABLE, "Managed CDP sessions are unavailable");
  }
  const tab = await resolveTab(chromeAPI, request.target?.tabId);
  await assertPageAccess(chromeAPI, tab);
  const debuggerGranted = await chromeAPI.permissions.contains({ permissions: ["debugger"] });
  if (!debuggerGranted) {
    throw protocolError(
      ErrorCode.PERMISSION_REQUIRED,
      "Debug permission is required. Grant it from the extension settings page.",
    );
  }
  const document = await resolveRootDocument(chromeAPI, request, tab.id);
  throwIfCancelled(signal);
  return { tab, document };
}

function encodeJSONObject(value, maxBytes, enforce = true) {
  let json;
  try {
    json = JSON.stringify(value);
  } catch {
    throw invalidCaptureResult();
  }
  const bytes = new TextEncoder().encode(json);
  if (enforce && bytes.byteLength > maxBytes) throw payloadTooLarge(maxBytes);
  assertJSONObjectBytes(bytes);
  return bytes;
}

function assertJSONObjectBytes(bytes) {
  try {
    const value = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes));
    if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("shape");
  } catch {
    throw invalidCaptureResult();
  }
}

function payloadTooLarge(maxBytes) {
  return protocolError(
    ErrorCode.PAYLOAD_TOO_LARGE,
    `The performance artifact exceeds ${maxBytes} bytes`,
  );
}

function decodeBase64(value) {
  try {
    const binary = atob(value);
    const bytes = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
    return bytes;
  } catch {
    throw invalidCaptureResult();
  }
}

function bytesToBase64(bytes) {
  let binary = "";
  const chunkSize = 32 * 1024;
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + chunkSize));
  }
  return btoa(binary);
}

function joinBytes(chunks, total) {
  const result = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    result.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return result;
}

async function bestEffortCommand(lease, method, params) {
  try {
    await lease.sendCommand(method, params);
  } catch {
    // Releasing the exact-method lease is the final cleanup boundary.
  }
}

function delay(durationMs, signal) {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(signal.reason || protocolError(ErrorCode.CANCELLED, "Command was cancelled", true));
      return;
    }
    const timeout = setTimeout(finish, durationMs);
    const onAbort = () =>
      finish(signal.reason || protocolError(ErrorCode.CANCELLED, "Command was cancelled", true));
    function finish(error) {
      clearTimeout(timeout);
      signal.removeEventListener("abort", onAbort);
      if (error) reject(error);
      else resolve();
    }
    signal.addEventListener("abort", onAbort, { once: true });
  });
}

function waitWithSignal(promise, signal) {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(signal.reason || protocolError(ErrorCode.CANCELLED, "Command was cancelled", true));
      return;
    }
    const onAbort = () =>
      reject(signal.reason || protocolError(ErrorCode.CANCELLED, "Command was cancelled", true));
    signal.addEventListener("abort", onAbort, { once: true });
    promise.then(
      (value) => {
        signal.removeEventListener("abort", onAbort);
        resolve(value);
      },
      (error) => {
        signal.removeEventListener("abort", onAbort);
        reject(error);
      },
    );
  });
}

async function recheckTarget(chromeAPI, tabId, documentId) {
  const tab = await resolveTab(chromeAPI, tabId);
  await assertPageAccess(chromeAPI, tab);
  const document = await currentRootDocument(chromeAPI, tabId);
  assertFreshDocument(documentId, document.documentId);
}

async function resolveTab(chromeAPI, explicitTabId) {
  if (Number.isInteger(explicitTabId)) {
    try {
      return await chromeAPI.tabs.get(explicitTabId);
    } catch {
      throw protocolError(ErrorCode.TAB_NOT_FOUND, `Tab ${explicitTabId} was not found`);
    }
  }
  const tabs = await chromeAPI.tabs.query({ active: true, lastFocusedWindow: true });
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
  if (!["http:", "https:"].includes(parsed.protocol)) {
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

async function resolveRootDocument(chromeAPI, request, tabId) {
  const frame = await currentRootDocument(chromeAPI, tabId);
  if (request.target?.documentId) assertFreshDocument(request.target.documentId, frame.documentId);
  return frame;
}

async function currentRootDocument(chromeAPI, tabId) {
  let frame;
  try {
    frame = await chromeAPI.webNavigation.getFrame({ tabId, frameId: 0 });
  } catch (error) {
    throw mapChromeError(error);
  }
  if (!frame?.documentId) {
    throw protocolError(ErrorCode.FRAME_NOT_FOUND, "The target document is unavailable", true);
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
  return operation(controller.signal).finally(() => {
    clearTimeout(timeout);
    parentSignal.removeEventListener("abort", onParentAbort);
  });
}

function throwIfCancelled(signal) {
  if (signal.aborted) {
    throw signal.reason || protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
  }
}

function integerBetween(value, minimum, maximum) {
  return Number.isSafeInteger(value) && value >= minimum && value <= maximum;
}

function invalidCaptureResult() {
  return protocolError(ErrorCode.INVALID_MESSAGE, "CDP returned an invalid performance result");
}
