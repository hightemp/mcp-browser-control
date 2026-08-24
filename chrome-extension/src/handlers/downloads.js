import { ErrorCode, mapChromeError, protocolError } from "../protocol.js";

const DEFAULT_TIMEOUT_MS = 30_000;
const MAX_TIMEOUT_MS = 120_000;
const MAX_PAGE_ITEMS = 200;
const MAX_SCAN_ITEMS = 10_000;
const MAX_URL_BYTES = 8_192;
const MAX_FILENAME_BYTES = 1_024;
const MAX_TEXT_BYTES = 256;
const STATES = new Set(["in_progress", "interrupted", "complete"]);
const encoder = new TextEncoder();

export function createDownloadHandlers(chromeAPI, { inIncognitoContext = false } = {}) {
  async function list(request, parentSignal) {
    const params = validateListParams(request.params);
    return run(chromeAPI, request, parentSignal, async (signal) => {
      const offset = params.cursor ? Number(params.cursor) : 0;
      const query = {
        orderBy: ["-startTime"],
        limit: MAX_SCAN_ITEMS + 1,
        ...(params.state ? { state: params.state } : {}),
        ...(params.paused !== undefined ? { paused: params.paused } : {}),
      };
      const found = await callDownloads(chromeAPI, "search", query);
      throwIfCancelled(signal);
      if (!Array.isArray(found) || found.length > MAX_SCAN_ITEMS) {
        throw protocolError(
          ErrorCode.PAYLOAD_TOO_LARGE,
          "The download history exceeds the bounded scan limit",
        );
      }
      const visible = params.allowIncognito
        ? found
        : found.filter((item) => item?.incognito !== true);
      const page = visible.slice(offset, offset + params.limit).map(normalizeDownload);
      return {
        kind: "list",
        downloads: page,
        downloadId: 0,
        totalMatched: visible.length,
        nextCursor: offset + page.length < visible.length ? String(offset + page.length) : "",
        operation: "",
        changed: false,
        erasedIds: [],
        warnings: [],
      };
    });
  }

  async function get(request, parentSignal) {
    const params = validateIDParams(request.params);
    return run(chromeAPI, request, parentSignal, async (signal) => {
      const item = await findDownload(chromeAPI, params.downloadId, params.allowIncognito);
      throwIfCancelled(signal);
      return itemResult("item", "", item, false);
    });
  }

  async function create(request, parentSignal) {
    const params = validateCreateParams(request.params);
    return run(chromeAPI, request, parentSignal, async (signal) => {
      if (inIncognitoContext && !params.allowIncognito) {
        throw protocolError(
          ErrorCode.RESTRICTED_URL,
          "Incognito downloads are disabled by server action policy",
        );
      }
      let downloadId;
      try {
        downloadId = await chromeAPI.downloads.download({
          url: params.url,
          conflictAction: "uniquify",
          saveAs: false,
        });
      } catch (error) {
        throw mapChromeError(error);
      }
      throwIfCancelled(signal);
      if (!validDownloadID(downloadId)) throw invalidDownloadResult();
      return {
        kind: "create",
        downloads: [],
        downloadId,
        totalMatched: 0,
        nextCursor: "",
        operation: "create",
        changed: true,
        erasedIds: [],
        warnings: ["HTTP(S) downloads may include cookies already stored for the destination host"],
      };
    });
  }

  const lifecycle = (operation) => async (request, parentSignal) => {
    const params = validateIDParams(request.params);
    return run(chromeAPI, request, parentSignal, async (signal) => {
      const before = await findDownload(chromeAPI, params.downloadId, params.allowIncognito);
      validateLifecycleState(operation, before);
      await callDownloads(chromeAPI, operation, params.downloadId);
      throwIfCancelled(signal);
      const after = await findDownload(chromeAPI, params.downloadId, params.allowIncognito);
      return itemResult("mutation", operation, after, true);
    });
  };

  async function erase(request, parentSignal) {
    const params = validateEraseParams(request.params);
    return run(chromeAPI, request, parentSignal, async (signal) => {
      const before = await findDownload(chromeAPI, params.downloadId, params.allowIncognito);
      if (before.state === "in_progress") {
        throw protocolError(
          ErrorCode.INVALID_MESSAGE,
          "Cancel an active download before erasing its history entry",
        );
      }
      const erasedIds = await callDownloads(chromeAPI, "erase", { id: params.downloadId });
      throwIfCancelled(signal);
      if (
        !Array.isArray(erasedIds) ||
        erasedIds.length !== 1 ||
        erasedIds[0] !== params.downloadId
      ) {
        throw invalidDownloadResult();
      }
      return {
        kind: "erase",
        downloads: [],
        downloadId: params.downloadId,
        totalMatched: 0,
        nextCursor: "",
        operation: "erase",
        changed: true,
        erasedIds: [params.downloadId],
        warnings: ["The downloaded file was not deleted"],
      };
    });
  }

  return {
    list,
    get,
    create,
    pause: lifecycle("pause"),
    resume: lifecycle("resume"),
    cancel: lifecycle("cancel"),
    erase,
  };
}

async function run(chromeAPI, request, parentSignal, operation) {
  return withTimeout(
    async (signal) => {
      await requireDownloadAccess(chromeAPI);
      const result = await operation(signal);
      await requireDownloadAccess(chromeAPI);
      return result;
    },
    parentSignal,
    validateTimeout(request.timeoutMs),
  );
}

async function requireDownloadAccess(chromeAPI) {
  if (!chromeAPI.downloads?.search || !chromeAPI.permissions?.contains) {
    throw protocolError(ErrorCode.CAPABILITY_UNAVAILABLE, "The downloads API is unavailable");
  }
  if (!(await chromeAPI.permissions.contains({ permissions: ["downloads"] }))) {
    throw protocolError(
      ErrorCode.PERMISSION_REQUIRED,
      "Downloads access is required. Grant Personal data in extension settings.",
    );
  }
}

async function callDownloads(chromeAPI, method, argument) {
  if (typeof chromeAPI.downloads?.[method] !== "function") {
    throw protocolError(ErrorCode.CAPABILITY_UNAVAILABLE, "The downloads API is unavailable");
  }
  try {
    return await chromeAPI.downloads[method](argument);
  } catch (error) {
    throw mapChromeError(error);
  }
}

async function findDownload(chromeAPI, downloadId, allowIncognito) {
  const found = await callDownloads(chromeAPI, "search", { id: downloadId });
  if (!Array.isArray(found) || found.length > 1) throw invalidDownloadResult();
  if (found.length === 0) {
    throw protocolError(ErrorCode.DOWNLOAD_NOT_FOUND, "The download was not found");
  }
  const item = normalizeDownload(found[0]);
  if (item.incognito && !allowIncognito) {
    throw protocolError(
      ErrorCode.RESTRICTED_URL,
      "Incognito downloads are disabled by server action policy",
    );
  }
  return item;
}

function validateLifecycleState(operation, item) {
  if (operation === "pause" && (item.state !== "in_progress" || item.paused)) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Only an active unpaused download can be paused",
    );
  }
  if (operation === "resume" && !item.canResume) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "The download cannot be resumed");
  }
  if (operation === "cancel" && item.state !== "in_progress") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Only an active download can be cancelled");
  }
}

function itemResult(kind, operation, item, changed) {
  return {
    kind,
    downloads: [item],
    downloadId: item.id,
    totalMatched: 1,
    nextCursor: "",
    operation,
    changed,
    erasedIds: [],
    warnings: [],
  };
}

function normalizeDownload(item) {
  if (
    !isObject(item) ||
    !validDownloadID(item.id) ||
    !STATES.has(item.state) ||
    typeof item.paused !== "boolean" ||
    typeof item.canResume !== "boolean" ||
    typeof item.exists !== "boolean" ||
    typeof item.incognito !== "boolean"
  ) {
    throw invalidDownloadResult();
  }
  return {
    id: item.id,
    sourceUrl: safeDownloadURL(item.url),
    finalUrl: safeDownloadURL(item.finalUrl),
    fileName: safeBaseName(item.filename),
    pathRedacted: true,
    state: item.state,
    paused: item.paused,
    canResume: item.canResume,
    danger: boundedText(item.danger, "danger"),
    error: item.error === undefined ? "" : boundedText(item.error, "error"),
    bytesReceived: boundedByteCount(item.bytesReceived),
    totalBytes: boundedByteCount(item.totalBytes, true),
    fileSize: boundedByteCount(item.fileSize, true),
    exists: item.exists,
    incognito: item.incognito,
    mime: boundedText(item.mime, "mime"),
    startTime: normalizedTime(item.startTime, false),
    endTime: normalizedTime(item.endTime, true),
    estimatedEndTime: normalizedTime(item.estimatedEndTime, true),
  };
}

function safeDownloadURL(value) {
  if (typeof value !== "string" || byteLength(value) > MAX_URL_BYTES) {
    throw invalidDownloadResult();
  }
  let parsed;
  try {
    parsed = new URL(value);
  } catch {
    return "[REDACTED]";
  }
  if (!["http:", "https:"].includes(parsed.protocol) || parsed.username || parsed.password) {
    return "[REDACTED]";
  }
  return `${parsed.origin}${parsed.pathname}`;
}

function safeBaseName(value) {
  if (typeof value !== "string") throw invalidDownloadResult();
  const segments = value.split(/[\\/]/);
  const baseName = segments.at(-1) || "[REDACTED]";
  if (byteLength(baseName) > MAX_FILENAME_BYTES || containsControl(baseName)) {
    throw protocolError(ErrorCode.PAYLOAD_TOO_LARGE, "Download filename metadata exceeds limits");
  }
  return baseName;
}

function boundedText(value, name) {
  if (typeof value !== "string" || byteLength(value) > MAX_TEXT_BYTES || containsControl(value)) {
    throw protocolError(ErrorCode.PAYLOAD_TOO_LARGE, `Download ${name} metadata exceeds limits`);
  }
  return value;
}

function boundedByteCount(value, allowUnknown = false) {
  if (!Number.isSafeInteger(value) || value < (allowUnknown ? -1 : 0)) {
    throw invalidDownloadResult();
  }
  return value;
}

function normalizedTime(value, optional) {
  if (optional && value === undefined) return "";
  if (typeof value !== "string" || value.length > 64) throw invalidDownloadResult();
  const timestamp = new Date(value);
  if (Number.isNaN(timestamp.valueOf())) throw invalidDownloadResult();
  return timestamp.toISOString();
}

function validateListParams(params) {
  assertObject(params, ["cursor", "limit", "state", "paused", "allowIncognito"]);
  validateCursor(params.cursor);
  if (!Number.isInteger(params.limit) || params.limit < 1 || params.limit > MAX_PAGE_ITEMS) {
    throw invalidDownloadParams("params.limit is outside the supported range");
  }
  if (params.state !== undefined && !STATES.has(params.state)) {
    throw invalidDownloadParams("params.state is unsupported");
  }
  if (params.paused !== undefined && typeof params.paused !== "boolean") {
    throw invalidDownloadParams("params.paused must be a boolean");
  }
  validateAllowIncognito(params.allowIncognito);
  return params;
}

function validateIDParams(params) {
  assertObject(params, ["downloadId", "allowIncognito"]);
  if (!validDownloadID(params.downloadId)) {
    throw invalidDownloadParams("params.downloadId is invalid");
  }
  validateAllowIncognito(params.allowIncognito);
  return params;
}

function validateEraseParams(params) {
  assertObject(params, ["downloadId", "confirm", "allowIncognito"]);
  if (params.confirm !== true) {
    throw protocolError(
      ErrorCode.CONFIRMATION_REQUIRED,
      "Erasing download history requires confirm: true",
    );
  }
  if (!validDownloadID(params.downloadId)) {
    throw invalidDownloadParams("params.downloadId is invalid");
  }
  validateAllowIncognito(params.allowIncognito);
  return params;
}

function validateCreateParams(params) {
  assertObject(params, ["url", "allowIncognito"]);
  if (typeof params.url !== "string" || byteLength(params.url) > MAX_URL_BYTES) {
    throw invalidDownloadParams("params.url is invalid");
  }
  let parsed;
  try {
    parsed = new URL(params.url);
  } catch {
    throw invalidDownloadParams("params.url must be an HTTP(S) URL");
  }
  if (
    !["http:", "https:"].includes(parsed.protocol) ||
    parsed.username ||
    parsed.password ||
    isBrowserStoreURL(parsed)
  ) {
    throw protocolError(ErrorCode.RESTRICTED_URL, "The download URL is restricted");
  }
  validateAllowIncognito(params.allowIncognito);
  return params;
}

function validateAllowIncognito(value) {
  if (typeof value !== "boolean") {
    throw invalidDownloadParams("params.allowIncognito must be a server boolean");
  }
}

function validateCursor(value) {
  if (
    value !== undefined &&
    (typeof value !== "string" ||
      !/^\d+$/.test(value) ||
      !Number.isSafeInteger(Number(value)) ||
      Number(value) < 1 ||
      Number(value) >= MAX_SCAN_ITEMS)
  ) {
    throw invalidDownloadParams("params.cursor is invalid");
  }
}

function isBrowserStoreURL(parsed) {
  const host = parsed.hostname.toLowerCase();
  const path = parsed.pathname.toLowerCase();
  return (
    host === "chromewebstore.google.com" ||
    (host === "chrome.google.com" && path.startsWith("/webstore")) ||
    (host === "microsoftedge.microsoft.com" && path.startsWith("/addons"))
  );
}

function validDownloadID(value) {
  return Number.isSafeInteger(value) && value >= 0;
}

function containsControl(value) {
  return [...value].some((character) => {
    const codePoint = character.codePointAt(0);
    return codePoint < 32 || codePoint === 127;
  });
}

function assertObject(value, allowedKeys) {
  if (!isObject(value) || Object.keys(value).some((key) => !allowedKeys.includes(key))) {
    throw invalidDownloadParams("Download parameters have an invalid shape");
  }
}

function isObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function validateTimeout(value) {
  const timeout = value || DEFAULT_TIMEOUT_MS;
  if (!Number.isInteger(timeout) || timeout < 1 || timeout > MAX_TIMEOUT_MS) {
    throw invalidDownloadParams("Download timeout is outside the supported range");
  }
  return timeout;
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

function throwIfCancelled(signal) {
  if (signal.aborted) throw signal.reason;
}

function byteLength(value) {
  return encoder.encode(value).byteLength;
}

function invalidDownloadParams(message) {
  return protocolError(ErrorCode.INVALID_MESSAGE, message);
}

function invalidDownloadResult() {
  return protocolError(
    ErrorCode.INVALID_MESSAGE,
    "The browser returned an invalid download result",
  );
}
