import { ErrorCode, assertFreshDocument, mapChromeError, protocolError } from "../protocol.js";

const DEFAULT_TIMEOUT_MS = 30_000;
const MAX_TIMEOUT_MS = 120_000;
const MAX_CAPTURES = 8;
const MAX_ENTRIES = 5_000;
const MAX_BUFFER_BYTES = 2_000_000;
const MAX_ENTRY_BYTES = 32 * 1024;
const MAX_READ_ENTRIES = 200;
const MIN_READ_BYTES = 64 * 1024;
const MAX_READ_BYTES = 1_000_000;
const MIN_BODY_BYTES = 1_024;
const MAX_BODY_BYTES = 1_000_000;
const MIN_HAR_BYTES = 64 * 1024;
const MAX_HAR_BYTES = 2_000_000;
const RETAINED_TTL_MS = 10 * 60 * 1_000;
const MAX_HEADERS = 64;
const MAX_PENDING_EXTRA_EVENTS = 64;
const MAX_PENDING_EXTRA_BYTES = 256 * 1024;
const NETWORK_COMMANDS = Object.freeze([
  "Network.enable",
  "Network.getRequestPostData",
  "Network.getResponseBody",
]);
const NETWORK_EVENTS = Object.freeze([
  "Network.requestWillBeSent",
  "Network.requestWillBeSentExtraInfo",
  "Network.responseReceived",
  "Network.responseReceivedExtraInfo",
  "Network.loadingFinished",
  "Network.loadingFailed",
  "Network.requestServedFromCache",
]);
const RESOURCE_TYPES = new Set([
  "Document",
  "Stylesheet",
  "Image",
  "Media",
  "Font",
  "Script",
  "TextTrack",
  "XHR",
  "Fetch",
  "Prefetch",
  "EventSource",
  "WebSocket",
  "Manifest",
  "SignedExchange",
  "Ping",
  "CSPViolationReport",
  "Preflight",
  "Other",
]);
const SENSITIVE_NAME =
  /password|passwd|passphrase|secret|token|credential|authorization|cookie|api[-_ ]?key|access[-_ ]?token|refresh[-_ ]?token/i;
const SENSITIVE_HEADER = /^(?:authorization|proxy-authorization|cookie|set-cookie|set-cookie2)$/i;

export function createNetworkHandlers(chromeAPI, { cdpSessions, now = () => Date.now() } = {}) {
  const captures = new Map();
  let consumerSequence = 0;

  chromeAPI.webNavigation?.onCommitted?.addListener?.((details) => {
    if (details.frameId !== 0) return;
    const capture = captures.get(details.tabId);
    if (!capture) return;
    captures.delete(details.tabId);
    void releaseCapture(capture, now).catch(() => undefined);
  });

  async function start(request, parentSignal) {
    const params = validateStartParams(request.params);
    return withTimeout(
      async (signal) => {
        cleanupExpired(captures, now);
        const { tab, document, origin } = await prepareTarget(
          chromeAPI,
          cdpSessions,
          request,
          signal,
        );
        const previous = captures.get(tab.id);
        let previousRelease = Promise.resolve();
        if (previous) {
          captures.delete(tab.id);
          previousRelease = releaseCapture(previous, now);
        }
        reserveCaptureSlot(captures);

        consumerSequence += 1;
        const capture = createCapture({
          tabId: tab.id,
          documentId: document.documentId,
          origin,
          maxEntries: params.maxEntries,
          now,
        });
        captures.set(tab.id, capture);
        let lease;
        try {
          await previousRelease;
          assertCurrentCapture(captures, capture);
          lease = await cdpSessions.acquire(
            { tabId: tab.id },
            {
              consumerId: `network:${tab.id}:${consumerSequence}`,
              domains: ["Network"],
              commands: [...NETWORK_COMMANDS],
              events: [...NETWORK_EVENTS],
              signal,
              onEvent: (event) => {
                if (capture.active) handleNetworkEvent(capture, event);
              },
              onDetach: () => {
                capture.active = false;
                capture.lease = null;
                disableBodyAccess(capture);
                capture.expiresAt = now() + RETAINED_TTL_MS;
              },
            },
          );
          capture.lease = lease;
          assertCurrentCapture(captures, capture);
          await lease.sendCommand(
            "Network.enable",
            {
              maxTotalBufferSize: MAX_BUFFER_BYTES,
              maxResourceBufferSize: MAX_BODY_BYTES,
              maxPostDataSize: MAX_BODY_BYTES,
            },
            { signal },
          );
          assertCurrentCapture(captures, capture);
          await recheckTarget(chromeAPI, tab.id, document.documentId);
          assertCurrentCapture(captures, capture);
        } catch (error) {
          if (captures.get(tab.id) === capture) captures.delete(tab.id);
          capture.active = false;
          if (lease && capture.lease === lease) {
            capture.lease = null;
            await bestEffortRelease(lease);
          }
          throw error;
        }
        return captureStatus(capture, []);
      },
      parentSignal,
      validateTimeout(request.timeoutMs),
    );
  }

  async function stop(request, parentSignal) {
    assertEmptyParams(request.params);
    return withCaptureCommand(request, parentSignal, async (capture) => {
      await releaseCapture(capture, now);
      return captureStatus(capture, []);
    });
  }

  async function clear(request, parentSignal) {
    assertEmptyParams(request.params);
    return withCaptureCommand(request, parentSignal, async (capture) => {
      clearCapture(capture);
      return captureStatus(capture, []);
    });
  }

  async function read(request, parentSignal) {
    const params = validateReadParams(request.params);
    return withCaptureCommand(request, parentSignal, async (capture) =>
      readCapture(capture, params),
    );
  }

  async function getBody(request, parentSignal) {
    const params = validateBodyParams(request.params);
    return withCaptureCommand(request, parentSignal, async (capture, signal) => {
      if (!capture.active || !capture.lease) {
        throw protocolError(
          ErrorCode.CAPABILITY_UNAVAILABLE,
          "Network bodies are available only while capture is active",
        );
      }
      const record = capture.byEntryId.get(params.entryId);
      if (!record) {
        throw protocolError(
          ErrorCode.INVALID_MESSAGE,
          "The network entry is unavailable or expired",
        );
      }
      if (!record.sameOrigin) {
        throw protocolError(
          ErrorCode.RESTRICTED_URL,
          "Network bodies are limited to the captured root-document origin",
        );
      }
      const mimeType = params.direction === "request" ? record.requestMIME : record.responseMIME;
      const available =
        params.direction === "request"
          ? record.public.requestBodyAvailable
          : record.public.responseBodyAvailable;
      if (!available || !allowedBodyMIME(mimeType)) {
        throw protocolError(
          ErrorCode.CAPABILITY_UNAVAILABLE,
          "The requested textual network body is unavailable",
        );
      }
      await recheckTarget(chromeAPI, capture.tabId, capture.documentId);
      const command =
        params.direction === "request" ? "Network.getRequestPostData" : "Network.getResponseBody";
      const result = await capture.lease.sendCommand(
        command,
        { requestId: record.requestId },
        { signal },
      );
      const rawBody = params.direction === "request" ? result?.postData : result?.body;
      if (
        typeof rawBody !== "string" ||
        (result?.base64Encoded !== undefined && typeof result.base64Encoded !== "boolean")
      ) {
        throw invalidNetworkResult();
      }
      const rawBytes = result?.base64Encoded
        ? decodeBase64(rawBody)
        : new TextEncoder().encode(rawBody);
      if (rawBytes.byteLength > params.maxBytes) {
        throw payloadTooLarge(params.maxBytes);
      }
      const redacted = redactBody(rawBytes, mimeType, params.maxBytes);
      await recheckTarget(chromeAPI, capture.tabId, capture.documentId);
      return {
        kind: `${params.direction}Body`,
        mimeType: normalizeMIME(mimeType),
        dataBase64: bytesToBase64(redacted.bytes),
        byteLength: redacted.bytes.byteLength,
        tabId: capture.tabId,
        documentId: capture.documentId,
        entryId: params.entryId,
        entryCount: 0,
        truncated: redacted.truncated,
        redactionApplied: redacted.rules.length > 0,
        redactionRules: redacted.rules,
        warnings: redacted.truncated ? ["The network body was truncated during redaction"] : [],
      };
    });
  }

  async function exportHAR(request, parentSignal) {
    const params = validateHARParams(request.params);
    return withCaptureCommand(request, parentSignal, async (capture) => {
      const exported = encodeHAR(capture, params.maxBytes);
      return {
        kind: "har",
        mimeType: "application/json",
        dataBase64: bytesToBase64(exported.bytes),
        byteLength: exported.bytes.byteLength,
        tabId: capture.tabId,
        documentId: capture.documentId,
        entryCount: exported.entryCount,
        truncated: exported.truncated,
        redactionApplied: capture.redactionRules.size > 0,
        redactionRules: [...capture.redactionRules].sort(),
        warnings: exported.truncated ? ["Older HAR entries were omitted to fit maxBytes"] : [],
      };
    });
  }

  async function withCaptureCommand(request, parentSignal, operation) {
    return withTimeout(
      async (signal) => {
        cleanupExpired(captures, now);
        const { tab, document } = await prepareTarget(chromeAPI, cdpSessions, request, signal);
        const capture = captures.get(tab.id);
        if (!capture || capture.documentId !== document.documentId) {
          throw protocolError(
            ErrorCode.CAPABILITY_UNAVAILABLE,
            "Network capture has not been started for this document",
          );
        }
        const result = await operation(capture, signal);
        await recheckTarget(chromeAPI, tab.id, document.documentId);
        return result;
      },
      parentSignal,
      validateTimeout(request.timeoutMs),
    );
  }

  return { start, stop, clear, read, getBody, exportHAR };
}

function createCapture({ tabId, documentId, origin, maxEntries, now }) {
  return {
    tabId,
    documentId,
    origin,
    maxEntries,
    createdAt: now(),
    expiresAt: Number.POSITIVE_INFINITY,
    active: true,
    lease: null,
    entries: [],
    byEntryId: new Map(),
    currentByRequest: new Map(),
    recordsByRequest: new Map(),
    pendingRequestExtra: new Map(),
    pendingResponseExtra: new Map(),
    pendingExtraEvents: 0,
    pendingExtraBytes: 0,
    nextSequence: 1,
    bufferedBytes: 0,
    evicted: 0,
    droppedEvents: 0,
    redactionRules: new Set(),
  };
}

function handleNetworkEvent(capture, event) {
  try {
    const params = event?.params || {};
    const requestId = boundedString(params.requestId, 512);
    if (!requestId) return;
    switch (event.method) {
      case "Network.requestWillBeSent":
        handleRequest(capture, requestId, params);
        break;
      case "Network.requestWillBeSentExtraInfo":
        applyExtraInfo(capture, requestId, "request", params);
        break;
      case "Network.responseReceived":
        updateCurrent(capture, requestId, (record) => {
          if (params.hasExtraInfo === false) record.responseExtraApplied = true;
          applyResponse(record, params.response, params.type, capture);
        });
        break;
      case "Network.responseReceivedExtraInfo":
        applyExtraInfo(capture, requestId, "response", params);
        break;
      case "Network.loadingFinished":
        updateCurrent(capture, requestId, (record) => finishRecord(record, params));
        break;
      case "Network.loadingFailed":
        updateCurrent(capture, requestId, (record) => failRecord(record, params, capture));
        break;
      case "Network.requestServedFromCache":
        updateCurrent(capture, requestId, (record) => {
          record.public.fromCache = true;
        });
        break;
      default:
        break;
    }
  } catch {
    capture.droppedEvents += 1;
  }
}

function handleRequest(capture, requestId, params) {
  const previous = capture.currentByRequest.get(requestId);
  let redirectFrom = "";
  if (previous && params.redirectResponse) {
    if (params.redirectHasExtraInfo === false) previous.responseExtraApplied = true;
    applyResponse(previous, params.redirectResponse, previous.public.resourceType, capture);
    finishRecord(previous, params, true);
    redirectFrom = previous.public.entryId;
  }
  const request = isObject(params.request) ? params.request : {};
  const rawURL = boundedString(request.url, 16_000);
  if (!rawURL) return;
  const entryId = String(capture.nextSequence);
  const requestHeaders = normalizeHeaders(request.headers, capture.redactionRules);
  const requestMIME = contentType(request.headers);
  const isSameOrigin = sameOrigin(rawURL, capture.origin);
  const hasPostData = request.hasPostData === true;
  const publicEntry = {
    entryId,
    sequence: capture.nextSequence,
    startedAt: wallTimeISO(params.wallTime),
    method: boundedString(request.method, 32) || "GET",
    url: redactURL(rawURL, capture.redactionRules),
    documentUrl: redactURL(boundedString(params.documentURL, 16_000), capture.redactionRules),
    resourceType: normalizeResourceType(params.type),
    initiator: normalizeInitiator(params.initiator, capture.redactionRules),
    requestHeaders,
    requestBodyAvailable: hasPostData && isSameOrigin && allowedBodyMIME(requestMIME),
    responseBodyAvailable: false,
    completed: false,
    failed: false,
    fromCache: false,
    ...(redirectFrom ? { redirectFrom } : {}),
  };
  const record = {
    requestId,
    sameOrigin: isSameOrigin,
    hasPostData,
    redirect: false,
    requestExtraApplied: false,
    responseExtraApplied: false,
    requestMIME,
    responseMIME: "",
    monotonicStart: finiteNumber(params.timestamp, 0),
    public: publicEntry,
    bytes: 0,
  };
  if (previous && redirectFrom) {
    previous.public.redirectTo = entryId;
    updateRecord(capture, previous);
  }
  capture.nextSequence += 1;
  capture.entries.push(record);
  capture.byEntryId.set(entryId, record);
  capture.currentByRequest.set(requestId, record);
  const records = capture.recordsByRequest.get(requestId) || [];
  records.push(record);
  capture.recordsByRequest.set(requestId, records);
  drainPendingExtra(capture, requestId, "request");
  drainPendingExtra(capture, requestId, "response");
  updateRecord(capture, record);
}

function applyExtraInfo(capture, requestId, kind, params) {
  const extra = normalizeExtraInfo(capture, kind, params);
  const record = nextExtraRecord(capture, requestId, kind);
  if (!record) {
    queuePendingExtra(capture, requestId, kind, extra);
    return;
  }
  applyNormalizedExtra(capture, record, kind, extra);
}

function nextExtraRecord(capture, requestId, kind) {
  const flag = kind === "request" ? "requestExtraApplied" : "responseExtraApplied";
  return (capture.recordsByRequest.get(requestId) || []).find((record) => !record[flag]);
}

function normalizeExtraInfo(capture, kind, params) {
  let headers = normalizeHeaders(params.headers, capture.redactionRules);
  let truncated = false;
  if (byteLength(headers) > MAX_ENTRY_BYTES) {
    headers = {};
    truncated = true;
  }
  return {
    headers,
    mimeType: contentType(params.headers),
    status: kind === "response" && validStatus(params.statusCode) ? params.statusCode : 0,
    truncated,
  };
}

function queuePendingExtra(capture, requestId, kind, extra) {
  const pending = kind === "request" ? capture.pendingRequestExtra : capture.pendingResponseExtra;
  const extras = pending.get(requestId) || [];
  const bytes = byteLength(extra);
  if (
    capture.pendingExtraEvents >= MAX_PENDING_EXTRA_EVENTS ||
    capture.pendingExtraBytes + bytes > MAX_PENDING_EXTRA_BYTES
  ) {
    capture.droppedEvents += 1;
    return;
  }
  extras.push({ value: extra, bytes });
  pending.set(requestId, extras);
  capture.pendingExtraEvents += 1;
  capture.pendingExtraBytes += bytes;
}

function drainPendingExtra(capture, requestId, kind) {
  const pending = kind === "request" ? capture.pendingRequestExtra : capture.pendingResponseExtra;
  const extras = pending.get(requestId);
  if (!extras) return;
  while (extras.length > 0) {
    const record = nextExtraRecord(capture, requestId, kind);
    if (!record) break;
    const extra = extras.shift();
    capture.pendingExtraEvents -= 1;
    capture.pendingExtraBytes -= extra.bytes;
    applyNormalizedExtra(capture, record, kind, extra.value);
  }
  if (extras.length === 0) pending.delete(requestId);
}

function applyNormalizedExtra(capture, record, kind, extra) {
  if (kind === "request") {
    record.requestExtraApplied = true;
    record.public.requestHeaders = extra.headers;
    record.requestMIME = extra.mimeType || record.requestMIME;
  } else {
    record.responseExtraApplied = true;
    record.public.responseHeaders = extra.headers;
    record.responseMIME = extra.mimeType || record.responseMIME;
    record.public.mimeType = record.responseMIME;
    if (extra.status) record.public.status = extra.status;
  }
  if (extra.truncated) record.public.truncated = true;
  updateBodyAvailability(record);
  updateRecord(capture, record);
}

function applyResponse(record, response, resourceType, capture) {
  if (!isObject(response)) return;
  record.public.resourceType = normalizeResourceType(resourceType || record.public.resourceType);
  if (validStatus(response.status)) record.public.status = response.status;
  record.public.statusText = boundedString(response.statusText, 500);
  record.public.protocol = boundedString(response.protocol, 50);
  record.public.responseHeaders = normalizeHeaders(response.headers, capture.redactionRules);
  record.responseMIME = normalizeMIME(
    contentType(response.headers) || boundedString(response.mimeType, 256),
  );
  record.public.mimeType = record.responseMIME;
  record.public.remoteIPAddress = boundedString(response.remoteIPAddress, 100);
  record.public.remotePort = integerBetween(response.remotePort, 0, 65_535)
    ? response.remotePort
    : 0;
  record.public.fromCache = Boolean(
    response.fromDiskCache || response.fromPrefetchCache || response.fromServiceWorker,
  );
  record.public.encodedDataLength = boundedNumber(response.encodedDataLength, 0, 1e15);
  record.public.timing = normalizeTiming(response.timing);
  updateBodyAvailability(record);
}

function finishRecord(record, params, redirect = false) {
  record.public.completed = true;
  record.redirect = redirect;
  record.public.finishedAt = new Date().toISOString();
  record.public.durationMs = durationMS(record.monotonicStart, params.timestamp);
  if (Number.isFinite(params.encodedDataLength)) {
    record.public.encodedDataLength = boundedNumber(params.encodedDataLength, 0, 1e15);
  }
  updateBodyAvailability(record);
}

function failRecord(record, params, capture) {
  record.public.completed = true;
  record.public.failed = true;
  record.public.finishedAt = new Date().toISOString();
  record.public.durationMs = durationMS(record.monotonicStart, params.timestamp);
  record.public.errorText = redactText(
    boundedString(params.errorText, 2_000),
    capture.redactionRules,
  );
  record.public.canceled = Boolean(params.canceled);
  record.public.blockedReason = boundedString(params.blockedReason, 100);
  updateBodyAvailability(record);
}

function updateBodyAvailability(record) {
  record.public.requestBodyAvailable =
    !record.redirect &&
    record.hasPostData &&
    record.sameOrigin &&
    allowedBodyMIME(record.requestMIME);
  record.public.responseBodyAvailable =
    record.public.completed &&
    !record.redirect &&
    !record.public.failed &&
    record.sameOrigin &&
    allowedBodyMIME(record.responseMIME);
}

function updateCurrent(capture, requestId, mutate) {
  const record = capture.currentByRequest.get(requestId);
  if (!record) return;
  mutate(record);
  updateRecord(capture, record);
}

function updateRecord(capture, record) {
  capture.bufferedBytes -= record.bytes;
  boundPublicEntry(record.public);
  record.bytes = byteLength(record.public);
  capture.bufferedBytes += record.bytes;
  evictCapture(capture);
}

function boundPublicEntry(entry) {
  if (byteLength(entry) <= MAX_ENTRY_BYTES) return;
  entry.requestHeaders = {};
  entry.responseHeaders = {};
  if (entry.initiator?.stack) delete entry.initiator.stack;
  entry.truncated = true;
  entry.url = boundedString(entry.url, 4_000);
  entry.documentUrl = boundedString(entry.documentUrl, 4_000);
  if (byteLength(entry) > MAX_ENTRY_BYTES) {
    entry.initiator = { type: boundedString(entry.initiator?.type, 100) };
    entry.errorText = boundedString(entry.errorText, 1_000);
  }
}

function evictCapture(capture) {
  while (capture.entries.length > capture.maxEntries || capture.bufferedBytes > MAX_BUFFER_BYTES) {
    const removed = capture.entries.shift();
    if (!removed) break;
    capture.bufferedBytes -= removed.bytes;
    capture.byEntryId.delete(removed.public.entryId);
    if (capture.currentByRequest.get(removed.requestId) === removed) {
      capture.currentByRequest.delete(removed.requestId);
    }
    const records = capture.recordsByRequest.get(removed.requestId) || [];
    const remaining = records.filter((record) => record !== removed);
    if (remaining.length > 0) capture.recordsByRequest.set(removed.requestId, remaining);
    else capture.recordsByRequest.delete(removed.requestId);
    capture.evicted += 1;
  }
}

function readCapture(capture, params) {
  const cursor = params.cursor ? Number(params.cursor) : 0;
  const sinceMS = params.since ? Date.parse(params.since) : Number.NEGATIVE_INFINITY;
  const typeSet = params.resourceTypes.length > 0 ? new Set(params.resourceTypes) : null;
  const matching = capture.entries.filter((record) => {
    const entry = record.public;
    if (entry.sequence <= cursor) return false;
    if (typeSet && !typeSet.has(entry.resourceType)) return false;
    if (params.failedOnly && !entry.failed) return false;
    if (
      params.statusMin !== null &&
      (!validStatus(entry.status) || entry.status < params.statusMin)
    ) {
      return false;
    }
    if (
      params.statusMax !== null &&
      (!validStatus(entry.status) || entry.status > params.statusMax)
    ) {
      return false;
    }
    return Date.parse(entry.startedAt) >= sinceMS;
  });
  const entries = [];
  let more = false;
  for (const record of matching) {
    if (entries.length >= params.limit) {
      more = true;
      break;
    }
    const candidate = [...entries, record.public];
    const envelope = networkReadResult(capture, candidate, "", []);
    if (byteLength(envelope) > params.maxBytes) {
      more = true;
      break;
    }
    entries.push(record.public);
  }
  const warnings = [];
  const firstSequence = capture.entries[0]?.public.sequence || capture.nextSequence;
  if (capture.evicted > 0) warnings.push(`${capture.evicted} older network entries were evicted`);
  if (capture.droppedEvents > 0) {
    warnings.push(`${capture.droppedEvents} network events were dropped by safety limits`);
  }
  if (cursor > 0 && cursor < firstSequence - 1) {
    warnings.push("The network cursor expired after ring-buffer eviction");
  }
  const nextCursor = more && entries.length > 0 ? String(entries.at(-1).sequence) : "";
  const result = networkReadResult(capture, entries, nextCursor, warnings);
  if (byteLength(result) > params.maxBytes) throw payloadTooLarge(params.maxBytes);
  return result;
}

function networkReadResult(capture, entries, nextCursor, warnings) {
  return {
    tabId: capture.tabId,
    documentId: capture.documentId,
    active: capture.active,
    entries,
    nextCursor,
    retainedEntries: capture.entries.length,
    evictedEntries: capture.evicted,
    droppedEvents: capture.droppedEvents,
    bufferedBytes: capture.bufferedBytes,
    expiresAt: Number.isFinite(capture.expiresAt) ? new Date(capture.expiresAt).toISOString() : "",
    warnings,
  };
}

function captureStatus(capture, warnings) {
  return {
    tabId: capture.tabId,
    documentId: capture.documentId,
    active: capture.active,
    maxEntries: capture.maxEntries,
    retainedEntries: capture.entries.length,
    evictedEntries: capture.evicted,
    droppedEvents: capture.droppedEvents,
    bufferedBytes: capture.bufferedBytes,
    expiresAt: Number.isFinite(capture.expiresAt) ? new Date(capture.expiresAt).toISOString() : "",
    warnings,
  };
}

function clearCapture(capture) {
  capture.entries = [];
  capture.byEntryId.clear();
  capture.currentByRequest.clear();
  capture.recordsByRequest.clear();
  capture.pendingRequestExtra.clear();
  capture.pendingResponseExtra.clear();
  capture.pendingExtraEvents = 0;
  capture.pendingExtraBytes = 0;
  capture.bufferedBytes = 0;
  capture.evicted = 0;
  capture.droppedEvents = 0;
  capture.redactionRules.clear();
}

async function releaseCapture(capture, now) {
  if (capture.active) {
    capture.active = false;
    const lease = capture.lease;
    capture.lease = null;
    if (lease) await bestEffortRelease(lease);
  }
  disableBodyAccess(capture);
  capture.expiresAt = now() + RETAINED_TTL_MS;
}

function disableBodyAccess(capture) {
  capture.byEntryId.clear();
  capture.currentByRequest.clear();
  capture.recordsByRequest.clear();
  capture.pendingRequestExtra.clear();
  capture.pendingResponseExtra.clear();
  capture.pendingExtraEvents = 0;
  capture.pendingExtraBytes = 0;
  for (const record of capture.entries) record.requestId = "";
}

async function bestEffortRelease(lease) {
  try {
    await lease.release();
  } catch {
    // The shared manager owns final detach cleanup.
  }
}

function cleanupExpired(captures, now) {
  const timestamp = now();
  for (const [tabId, capture] of captures) {
    if (!capture.active && capture.expiresAt <= timestamp) captures.delete(tabId);
  }
}

function reserveCaptureSlot(captures) {
  if (captures.size < MAX_CAPTURES) return;
  const inactive = [...captures.entries()]
    .filter(([, capture]) => !capture.active)
    .sort((left, right) => left[1].expiresAt - right[1].expiresAt)[0];
  if (inactive) {
    captures.delete(inactive[0]);
    disableBodyAccess(inactive[1]);
    return;
  }
  throw protocolError(
    ErrorCode.BACKPRESSURE,
    `Network capture is limited to ${MAX_CAPTURES} tabs per browser`,
    true,
  );
}

function assertCurrentCapture(captures, capture) {
  if (capture.active && captures.get(capture.tabId) === capture) return;
  throw protocolError(
    ErrorCode.CANCELLED,
    "Network capture start was superseded by a newer request",
    true,
  );
}

function encodeHAR(capture, maxBytes) {
  const candidates = capture.entries.map((record) => harEntry(record.public, capture.documentId));
  let truncated = false;
  let bytes;
  while (true) {
    const value = {
      log: {
        version: "1.2",
        creator: { name: "MCP Browser Control", version: "0.3.0" },
        pages: [
          {
            startedDateTime: new Date(capture.createdAt).toISOString(),
            id: `document-${capture.documentId}`,
            title: "Captured root document",
            pageTimings: {},
          },
        ],
        entries: candidates,
        _mcp: {
          tabId: capture.tabId,
          documentId: capture.documentId,
          active: capture.active,
          evictedEntries: capture.evicted,
          droppedEvents: capture.droppedEvents,
          truncated,
          bodiesIncluded: false,
        },
      },
    };
    bytes = new TextEncoder().encode(JSON.stringify(value));
    if (bytes.byteLength <= maxBytes) {
      return { bytes, entryCount: candidates.length, truncated };
    }
    if (candidates.length === 0) throw payloadTooLarge(maxBytes);
    candidates.shift();
    truncated = true;
  }
}

function harEntry(entry, documentId) {
  const requestHeaders = headerArray(entry.requestHeaders);
  const responseHeaders = headerArray(entry.responseHeaders);
  return {
    pageref: `document-${documentId}`,
    startedDateTime: entry.startedAt,
    time: finiteNumber(entry.durationMs, 0),
    request: {
      method: entry.method,
      url: entry.url,
      httpVersion: "",
      cookies: [],
      headers: requestHeaders,
      queryString: queryArray(entry.url),
      headersSize: -1,
      bodySize: -1,
    },
    response: {
      status: validStatus(entry.status) ? entry.status : 0,
      statusText: entry.statusText || "",
      httpVersion: entry.protocol || "",
      cookies: [],
      headers: responseHeaders,
      content: {
        size: finiteNumber(entry.encodedDataLength, 0),
        mimeType: entry.mimeType || "",
      },
      redirectURL: entry.responseHeaders?.location || "",
      headersSize: -1,
      bodySize: finiteNumber(entry.encodedDataLength, -1),
    },
    cache: {},
    timings: harTimings(entry),
    _mcp: {
      entryId: entry.entryId,
      resourceType: entry.resourceType,
      completed: entry.completed,
      failed: entry.failed,
      fromCache: entry.fromCache,
      ...(entry.errorText ? { errorText: entry.errorText } : {}),
      ...(entry.redirectFrom ? { redirectFrom: entry.redirectFrom } : {}),
      ...(entry.redirectTo ? { redirectTo: entry.redirectTo } : {}),
    },
  };
}

function harTimings(entry) {
  const timing = entry.timing || {};
  const interval = (start, end) =>
    Number.isFinite(start) && Number.isFinite(end) && start >= 0 && end >= start ? end - start : -1;
  const send = interval(timing.sendStart, timing.sendEnd);
  const wait =
    Number.isFinite(timing.receiveHeadersEnd) && Number.isFinite(timing.sendEnd)
      ? Math.max(0, timing.receiveHeadersEnd - timing.sendEnd)
      : -1;
  const receive =
    Number.isFinite(entry.durationMs) && Number.isFinite(timing.receiveHeadersEnd)
      ? Math.max(0, entry.durationMs - timing.receiveHeadersEnd)
      : -1;
  return {
    blocked: -1,
    dns: interval(timing.dnsStart, timing.dnsEnd),
    connect: interval(timing.connectStart, timing.connectEnd),
    ssl: interval(timing.sslStart, timing.sslEnd),
    send,
    wait,
    receive,
  };
}

function normalizeHeaders(value, redactionRules) {
  if (!isObject(value)) return {};
  const result = {};
  for (const name of Object.keys(value).sort().slice(0, MAX_HEADERS)) {
    const safeName = boundedString(name, 256);
    if (!safeName) continue;
    const rawValue = typeof value[name] === "string" ? value[name] : String(value[name] ?? "");
    if (SENSITIVE_HEADER.test(safeName) || SENSITIVE_NAME.test(safeName)) {
      result[safeName] = "[REDACTED]";
      redactionRules.add(
        SENSITIVE_HEADER.test(safeName) ? "authorization-cookies" : "header-fields",
      );
    } else {
      result[safeName] = redactText(boundedString(rawValue, 4_000), redactionRules);
    }
  }
  return result;
}

function normalizeInitiator(value, redactionRules) {
  if (!isObject(value)) return { type: "other" };
  const initiator = {
    type: boundedString(value.type, 100) || "other",
    ...(value.url ? { url: redactURL(boundedString(value.url, 4_000), redactionRules) } : {}),
    ...(integerBetween(value.lineNumber, 0, 1e9) ? { line: value.lineNumber } : {}),
    ...(integerBetween(value.columnNumber, 0, 1e9) ? { column: value.columnNumber } : {}),
  };
  const callFrames = Array.isArray(value.stack?.callFrames) ? value.stack.callFrames : [];
  if (callFrames.length > 0) {
    initiator.stack = callFrames.slice(0, 10).map((frame) => ({
      functionName: boundedString(frame?.functionName, 200),
      url: redactURL(boundedString(frame?.url, 4_000), redactionRules),
      line: integerBetween(frame?.lineNumber, 0, 1e9) ? frame.lineNumber : 0,
      column: integerBetween(frame?.columnNumber, 0, 1e9) ? frame.columnNumber : 0,
    }));
  }
  return initiator;
}

function normalizeTiming(value) {
  if (!isObject(value)) return {};
  const result = {};
  for (const name of [
    "proxyStart",
    "proxyEnd",
    "dnsStart",
    "dnsEnd",
    "connectStart",
    "connectEnd",
    "sslStart",
    "sslEnd",
    "workerStart",
    "workerReady",
    "sendStart",
    "sendEnd",
    "pushStart",
    "pushEnd",
    "receiveHeadersStart",
    "receiveHeadersEnd",
  ]) {
    if (Number.isFinite(value[name]) && value[name] >= -1 && value[name] <= 86_400_000) {
      result[name] = value[name];
    }
  }
  return result;
}

function redactURL(value, rules) {
  if (!value) return "";
  try {
    const parsed = new URL(value);
    if (parsed.username || parsed.password) {
      parsed.username = "[REDACTED]";
      parsed.password = "";
      rules.add("authorization");
    }
    for (const name of [...parsed.searchParams.keys()]) {
      if (SENSITIVE_NAME.test(name)) {
        parsed.searchParams.set(name, "[REDACTED]");
        rules.add("query-tokens");
      }
    }
    return boundedString(parsed.toString(), 8_000);
  } catch {
    return redactText(boundedString(value, 8_000), rules);
  }
}

function redactText(value, rules) {
  let result = value;
  const replacements = [
    [/\bBearer\s+[A-Za-z0-9._~+/=-]+/gi, "Bearer [REDACTED]", "authorization"],
    [/(\b(?:set-)?cookie\s*:\s*)[^\r\n]+/gi, "$1[REDACTED]", "authorization-cookies"],
    [
      /(\b(?:password|passwd|passphrase|secret|token|credential|api[-_]?key|access[-_]?token|refresh[-_]?token)\b\s*[:=]\s*)[^,;\s&]+/gi,
      "$1[REDACTED]",
      "body-fields",
    ],
    [
      /([?&#](?:password|passwd|passphrase|secret|token|credential|api[-_]?key|access[-_]?token|refresh[-_]?token)=)[^&#\s]*/gi,
      "$1[REDACTED]",
      "query-tokens",
    ],
  ];
  for (const [pattern, replacement, rule] of replacements) {
    pattern.lastIndex = 0;
    if (pattern.test(result)) {
      pattern.lastIndex = 0;
      result = result.replace(pattern, replacement);
      rules.add(rule);
    }
  }
  return result;
}

function redactBody(bytes, mimeType, maxBytes) {
  let text;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "The network body is not valid UTF-8 text");
  }
  const rules = new Set();
  let truncated = false;
  let output;
  if (isJSONMIME(mimeType)) {
    try {
      const state = { nodes: 0, truncated: false, rules };
      output = JSON.stringify(redactJSONValue(JSON.parse(text), state, 0));
      truncated = state.truncated;
    } catch {
      output = redactText(text, rules);
    }
  } else {
    output = redactText(text, rules);
  }
  let result = new TextEncoder().encode(output);
  if (result.byteLength > maxBytes) {
    throw payloadTooLarge(maxBytes);
  }
  return { bytes: result, rules: [...rules].sort(), truncated };
}

function redactJSONValue(value, state, depth) {
  if (depth > 64 || state.nodes >= 50_000) {
    state.truncated = true;
    return "[TRUNCATED]";
  }
  state.nodes += 1;
  if (Array.isArray(value)) return value.map((item) => redactJSONValue(item, state, depth + 1));
  if (isObject(value)) {
    const result = {};
    for (const [key, item] of Object.entries(value)) {
      if (SENSITIVE_NAME.test(key)) {
        result[key] = "[REDACTED]";
        state.rules.add("body-fields");
      } else {
        result[key] = redactJSONValue(item, state, depth + 1);
      }
    }
    return result;
  }
  return typeof value === "string" ? redactText(value, state.rules) : value;
}

function validateStartParams(params) {
  assertObjectWithKeys(params, ["maxEntries"]);
  requireInteger(params.maxEntries, 1, MAX_ENTRIES, "params.maxEntries");
  return { maxEntries: params.maxEntries };
}

function validateReadParams(params) {
  assertObjectWithKeys(params, [
    "cursor",
    "limit",
    "resourceTypes",
    "failedOnly",
    "statusMin",
    "statusMax",
    "since",
    "maxBytes",
  ]);
  requireInteger(params.limit, 1, MAX_READ_ENTRIES, "params.limit");
  requireInteger(params.maxBytes, MIN_READ_BYTES, MAX_READ_BYTES, "params.maxBytes");
  const cursor = params.cursor || "";
  if (
    cursor &&
    (!/^\d+$/.test(cursor) || Number(cursor) < 1 || !Number.isSafeInteger(Number(cursor)))
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.cursor is invalid");
  }
  const resourceTypes = params.resourceTypes || [];
  if (
    !Array.isArray(resourceTypes) ||
    resourceTypes.length > RESOURCE_TYPES.size ||
    new Set(resourceTypes).size !== resourceTypes.length ||
    resourceTypes.some((value) => !RESOURCE_TYPES.has(value))
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.resourceTypes is invalid");
  }
  if (params.failedOnly !== undefined && typeof params.failedOnly !== "boolean") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.failedOnly must be a boolean");
  }
  for (const name of ["statusMin", "statusMax"]) {
    if (params[name] !== undefined) requireInteger(params[name], 100, 599, `params.${name}`);
  }
  if (
    params.statusMin !== undefined &&
    params.statusMax !== undefined &&
    params.statusMin > params.statusMax
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Network status bounds are invalid");
  }
  if (params.since !== undefined && !validTimestamp(params.since)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.since must be an RFC 3339 timestamp");
  }
  return {
    cursor,
    limit: params.limit,
    resourceTypes,
    failedOnly: params.failedOnly === true,
    statusMin: params.statusMin ?? null,
    statusMax: params.statusMax ?? null,
    since: params.since || "",
    maxBytes: params.maxBytes,
  };
}

function validateBodyParams(params) {
  assertObjectWithKeys(params, ["entryId", "direction", "maxBytes"]);
  if (
    typeof params.entryId !== "string" ||
    !/^\d+$/.test(params.entryId) ||
    Number(params.entryId) < 1 ||
    !Number.isSafeInteger(Number(params.entryId))
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.entryId is invalid");
  }
  if (!["request", "response"].includes(params.direction)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.direction is invalid");
  }
  requireInteger(params.maxBytes, MIN_BODY_BYTES, MAX_BODY_BYTES, "params.maxBytes");
  return params;
}

function validateHARParams(params) {
  assertObjectWithKeys(params, ["maxBytes"]);
  requireInteger(params.maxBytes, MIN_HAR_BYTES, MAX_HAR_BYTES, "params.maxBytes");
  return params;
}

function assertEmptyParams(params) {
  assertObjectWithKeys(params, []);
}

function assertObjectWithKeys(params, allowed) {
  if (!isObject(params) || Object.keys(params).some((key) => !allowed.includes(key))) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Network parameters have an invalid shape");
  }
}

function requireInteger(value, minimum, maximum, name) {
  if (!integerBetween(value, minimum, maximum)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${name} is outside the supported range`);
  }
}

function validateTimeout(value) {
  const timeout = value || DEFAULT_TIMEOUT_MS;
  if (!integerBetween(timeout, 1, MAX_TIMEOUT_MS)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Network timeout is outside the supported range",
    );
  }
  return timeout;
}

async function prepareTarget(chromeAPI, cdpSessions, request, signal) {
  if (!cdpSessions) {
    throw protocolError(ErrorCode.CAPABILITY_UNAVAILABLE, "Managed CDP sessions are unavailable");
  }
  const tab = await resolveTab(chromeAPI, request.target?.tabId);
  const origin = await assertPageAccess(chromeAPI, tab);
  const debuggerGranted = await chromeAPI.permissions.contains({ permissions: ["debugger"] });
  if (!debuggerGranted) {
    throw protocolError(
      ErrorCode.PERMISSION_REQUIRED,
      "Debug permission is required. Grant it from the extension settings page.",
    );
  }
  const document = await currentRootDocument(chromeAPI, tab.id);
  if (request.target?.documentId) {
    assertFreshDocument(request.target.documentId, document.documentId);
  }
  throwIfCancelled(signal);
  return { tab, document, origin };
}

async function recheckTarget(chromeAPI, tabId, documentId) {
  const tab = await resolveTab(chromeAPI, tabId);
  await assertPageAccess(chromeAPI, tab);
  const current = await currentRootDocument(chromeAPI, tabId);
  assertFreshDocument(documentId, current.documentId);
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
  return parsed.origin;
}

async function currentRootDocument(chromeAPI, tabId) {
  try {
    const frame = await chromeAPI.webNavigation.getFrame({ tabId, frameId: 0 });
    if (frame?.documentId) return frame;
  } catch (error) {
    throw mapChromeError(error);
  }
  throw protocolError(ErrorCode.FRAME_NOT_FOUND, "The root document is unavailable", true);
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

function contentType(headers) {
  if (!isObject(headers)) return "";
  for (const [name, value] of Object.entries(headers)) {
    if (name.toLowerCase() === "content-type" && typeof value === "string") {
      return normalizeMIME(value);
    }
  }
  return "";
}

function normalizeMIME(value) {
  const mediaType = boundedString(value, 256).split(";", 1)[0].trim().toLowerCase();
  return /^[a-z0-9!#$&^_.+-]+\/[a-z0-9!#$&^_.+-]+$/.test(mediaType) ? mediaType : "";
}

function allowedBodyMIME(value) {
  const mediaType = normalizeMIME(value);
  return (
    mediaType.startsWith("text/") ||
    isJSONMIME(mediaType) ||
    [
      "application/xml",
      "application/xhtml+xml",
      "application/javascript",
      "application/x-www-form-urlencoded",
      "image/svg+xml",
    ].includes(mediaType)
  );
}

function isJSONMIME(value) {
  const mediaType = normalizeMIME(value);
  return mediaType === "application/json" || mediaType.endsWith("+json");
}

function sameOrigin(value, origin) {
  try {
    return new URL(value).origin === origin;
  } catch {
    return false;
  }
}

function normalizeResourceType(value) {
  return RESOURCE_TYPES.has(value) ? value : "Other";
}

function validStatus(value) {
  return integerBetween(value, 100, 599);
}

function validTimestamp(value) {
  return (
    typeof value === "string" &&
    /^\d{4}-\d{2}-\d{2}T/.test(value) &&
    Number.isFinite(Date.parse(value))
  );
}

function wallTimeISO(value) {
  const date = Number.isFinite(value) ? new Date(value * 1_000) : new Date();
  return Number.isFinite(date.getTime()) ? date.toISOString() : new Date().toISOString();
}

function durationMS(start, end) {
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return 0;
  return Math.min(86_400_000, Math.round((end - start) * 1_000 * 1_000) / 1_000);
}

function boundedNumber(value, minimum, maximum) {
  return Number.isFinite(value) ? Math.min(maximum, Math.max(minimum, value)) : minimum;
}

function finiteNumber(value, fallback) {
  return Number.isFinite(value) ? value : fallback;
}

function boundedString(value, maximum) {
  return typeof value === "string" ? [...value].slice(0, maximum).join("") : "";
}

function integerBetween(value, minimum, maximum) {
  return Number.isSafeInteger(value) && value >= minimum && value <= maximum;
}

function isObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function byteLength(value) {
  return new TextEncoder().encode(JSON.stringify(value)).byteLength;
}

function headerArray(headers) {
  return Object.entries(headers || {}).map(([name, value]) => ({ name, value }));
}

function queryArray(value) {
  try {
    return [...new URL(value).searchParams.entries()].map(([name, item]) => ({
      name,
      value: item,
    }));
  } catch {
    return [];
  }
}

function decodeBase64(value) {
  try {
    const binary = atob(value);
    const bytes = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
    return bytes;
  } catch {
    throw invalidNetworkResult();
  }
}

function bytesToBase64(bytes) {
  let binary = "";
  for (let offset = 0; offset < bytes.length; offset += 32 * 1024) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 32 * 1024));
  }
  return btoa(binary);
}

function payloadTooLarge(maxBytes) {
  return protocolError(ErrorCode.PAYLOAD_TOO_LARGE, `The network result exceeds ${maxBytes} bytes`);
}

function invalidNetworkResult() {
  return protocolError(ErrorCode.INVALID_MESSAGE, "CDP returned an invalid network result");
}
