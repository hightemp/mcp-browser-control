import { ErrorCode, assertFreshDocument, mapChromeError, protocolError } from "../protocol.js";

const DEFAULT_TIMEOUT_MS = 10_000;
const MAX_TIMEOUT_MS = 30_000;
const MAX_DEPTH = 20;
const MAX_NODES = 5_000;
const MAX_STRING_CHARS = 10_000;
const MIN_RESULT_BYTES = 64 * 1024;
const MAX_RESULT_BYTES = 1_000_000;
const MAX_KEY_CHARS = 256;
const MAX_ACCESSIBILITY_DEPTH = 50;
const MAX_DESCRIBE_DEPTH = 10;
const REDACTED = "[REDACTED]";
const SENSITIVE_IDENTITY =
  /(?:password|passwd|passphrase|secret|token|credential|authorization|cookie|api[-_ ]?key|access[-_ ]?token|refresh[-_ ]?token)/i;

const METHOD_CONFIG = Object.freeze({
  "Accessibility.getFullAXTree": Object.freeze({
    domain: "Accessibility",
    fields: Object.freeze(["depth"]),
    shape: "ax",
  }),
  "Accessibility.getPartialAXTree": Object.freeze({
    domain: "Accessibility",
    fields: Object.freeze(["backendNodeId", "fetchRelatives"]),
    shape: "ax",
  }),
  "Accessibility.queryAXTree": Object.freeze({
    domain: "Accessibility",
    fields: Object.freeze(["backendNodeId", "accessibleName", "role"]),
    shape: "ax",
  }),
  "DOM.describeNode": Object.freeze({
    domain: "DOM",
    fields: Object.freeze(["backendNodeId", "depth"]),
    shape: "node",
  }),
  "DOM.getBoxModel": Object.freeze({
    domain: "DOM",
    fields: Object.freeze(["backendNodeId"]),
    shape: "model",
  }),
  "Page.getLayoutMetrics": Object.freeze({
    domain: "Page",
    fields: Object.freeze([]),
    shape: "layout",
  }),
  "Performance.getMetrics": Object.freeze({
    domain: "Performance",
    fields: Object.freeze([]),
    shape: "metrics",
  }),
});

const DENIED_METHODS = new Set([
  "Browser.close",
  "DOM.setFileInputFiles",
  "Network.clearBrowserCache",
  "Network.clearBrowserCookies",
  "Network.setCookie",
  "Network.setCookies",
  "Page.addScriptToEvaluateOnNewDocument",
  "Runtime.callFunctionOn",
  "Runtime.evaluate",
  "Storage.clearDataForOrigin",
]);
const DENIED_DOMAINS = new Set([
  "Browser",
  "Fetch",
  "HeapProfiler",
  "IO",
  "Network",
  "Runtime",
  "Security",
  "Storage",
  "SystemInfo",
  "Target",
]);
const PROHIBITED_RESULT_KEYS = new Set(["executionContextId", "objectId", "scriptId", "stream"]);
const LAYOUT_KEYS = new Set([
  "layoutViewport",
  "visualViewport",
  "contentSize",
  "cssLayoutViewport",
  "cssVisualViewport",
  "cssContentSize",
]);

export const RAW_CDP_METHODS = Object.freeze(Object.keys(METHOD_CONFIG));

export function createRawCDPHandlers(chromeAPI, { cdpSessions } = {}) {
  let sequence = 0;

  async function sendReadOnly(request, parentSignal) {
    const methodParams = assertRawCDPParams(request.params);
    const timeoutMs = request.timeoutMs || DEFAULT_TIMEOUT_MS;
    if (!integerBetween(timeoutMs, 1, MAX_TIMEOUT_MS)) {
      throw protocolError(
        ErrorCode.INVALID_MESSAGE,
        `Raw CDP timeout must be between 1 and ${MAX_TIMEOUT_MS} ms`,
      );
    }

    return withTimeout(
      async (signal) => {
        if (!cdpSessions) {
          throw protocolError(
            ErrorCode.CAPABILITY_UNAVAILABLE,
            "Managed CDP sessions are unavailable",
          );
        }
        const tab = await resolveTab(chromeAPI, request.target?.tabId);
        await assertPageAccess(chromeAPI, tab);
        const debuggerGranted = await chromeAPI.permissions.contains({
          permissions: ["debugger"],
        });
        if (!debuggerGranted) {
          throw protocolError(
            ErrorCode.PERMISSION_REQUIRED,
            "Debug permission is required. Grant it from the extension settings page.",
          );
        }
        const document = await resolveRootDocument(chromeAPI, request, tab.id);
        throwIfCancelled(signal);

        const config = METHOD_CONFIG[request.params.method];
        sequence += 1;
        const rawResult = await cdpSessions.withSession(
          { tabId: tab.id },
          {
            consumerId: `raw-cdp:${String(request.requestId || sequence).slice(0, 100)}`,
            domains: [config.domain],
            commands: [request.params.method],
            signal,
          },
          async (lease) => {
            await recheckTarget(chromeAPI, tab.id, document.documentId);
            const value = await lease.sendCommand(request.params.method, methodParams, { signal });
            await recheckTarget(chromeAPI, tab.id, document.documentId);
            return value;
          },
        );

        assertResultShape(request.params.method, rawResult);
        const state = {
          maxDepth: request.params.maxDepth,
          maxNodes: request.params.maxNodes,
          maxStringChars: request.params.maxStringChars,
          visitedNodes: 0,
          truncated: false,
          redacted: false,
          seen: new WeakSet(),
        };
        const normalized = normalizeJSONValue(rawResult, 0, state);
        if (normalized === OMITTED) {
          throw invalidResult();
        }
        normalizeTruncatedShape(request.params.method, normalized, state);
        assertResultShape(request.params.method, normalized);
        const nodeCount = countJSONNodes(normalized);
        if (nodeCount < 1 || nodeCount > request.params.maxNodes) {
          throw invalidResult();
        }
        const warnings = [];
        if (state.truncated) {
          warnings.push(
            "CDP result was truncated to the configured depth, node, string, or key limits",
          );
        }
        if (state.redacted) {
          warnings.push("Sensitive CDP result values were redacted by the extension");
        }
        const result = {
          method: request.params.method,
          tabId: tab.id,
          documentId: document.documentId,
          result: normalized,
          truncated: state.truncated,
          nodeCount,
          warnings,
        };
        ensureResultBytes(result, request.params.maxBytes);
        return result;
      },
      parentSignal,
      timeoutMs,
    );
  }

  return { sendReadOnly };
}

function assertRawCDPParams(params) {
  if (!params || typeof params !== "object" || Array.isArray(params)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Raw CDP parameters must be an object");
  }
  const allowed = new Set([
    "method",
    "params",
    "maxDepth",
    "maxNodes",
    "maxStringChars",
    "maxBytes",
  ]);
  if (Object.keys(params).some((key) => !allowed.has(key))) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Raw CDP parameters contain an unknown field");
  }
  const config = validateMethod(params.method);
  if (
    !integerBetween(params.maxDepth, 1, MAX_DEPTH) ||
    !integerBetween(params.maxNodes, 2, MAX_NODES) ||
    !integerBetween(params.maxStringChars, 1, MAX_STRING_CHARS) ||
    !integerBetween(params.maxBytes, MIN_RESULT_BYTES, MAX_RESULT_BYTES)
  ) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Raw CDP limits are outside the supported bounds",
    );
  }
  const methodParams = params.params ?? {};
  if (!methodParams || typeof methodParams !== "object" || Array.isArray(methodParams)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.params must be an object");
  }
  const unexpected = Object.keys(methodParams).find((key) => !config.fields.includes(key));
  if (unexpected) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      `params.params.${unexpected} is not allowed for ${params.method}`,
    );
  }

  switch (params.method) {
    case "Accessibility.getFullAXTree":
      optionalInteger(methodParams.depth, "params.params.depth", 0, MAX_ACCESSIBILITY_DEPTH);
      break;
    case "Accessibility.getPartialAXTree":
      requireInteger(
        methodParams.backendNodeId,
        "params.params.backendNodeId",
        1,
        Number.MAX_SAFE_INTEGER,
      );
      if (
        methodParams.fetchRelatives !== undefined &&
        typeof methodParams.fetchRelatives !== "boolean"
      ) {
        throw protocolError(
          ErrorCode.INVALID_MESSAGE,
          "params.params.fetchRelatives must be a boolean",
        );
      }
      break;
    case "Accessibility.queryAXTree":
      requireInteger(
        methodParams.backendNodeId,
        "params.params.backendNodeId",
        1,
        Number.MAX_SAFE_INTEGER,
      );
      optionalString(methodParams.accessibleName, "params.params.accessibleName", 500);
      optionalString(methodParams.role, "params.params.role", 100);
      break;
    case "DOM.describeNode":
      requireInteger(
        methodParams.backendNodeId,
        "params.params.backendNodeId",
        1,
        Number.MAX_SAFE_INTEGER,
      );
      optionalInteger(methodParams.depth, "params.params.depth", 0, MAX_DESCRIBE_DEPTH);
      break;
    case "DOM.getBoxModel":
      requireInteger(
        methodParams.backendNodeId,
        "params.params.backendNodeId",
        1,
        Number.MAX_SAFE_INTEGER,
      );
      break;
    case "Page.getLayoutMetrics":
    case "Performance.getMetrics":
      break;
    default:
      throw protocolError(ErrorCode.INVALID_COMMAND, "The CDP method is not allowlisted");
  }
  return copyObject(methodParams);
}

function validateMethod(method) {
  if (typeof method !== "string") {
    throw protocolError(ErrorCode.INVALID_COMMAND, "The CDP method is not allowlisted");
  }
  const config = METHOD_CONFIG[method];
  if (config) return config;
  if (DENIED_METHODS.has(method) || DENIED_DOMAINS.has(method.split(".", 1)[0])) {
    throw protocolError(ErrorCode.INVALID_COMMAND, "The CDP method is explicitly prohibited");
  }
  throw protocolError(ErrorCode.INVALID_COMMAND, "The CDP method is not allowlisted");
}

function copyObject(value) {
  const result = Object.create(null);
  for (const key of Object.keys(value)) {
    Object.defineProperty(result, key, {
      value: value[key],
      enumerable: true,
      configurable: true,
      writable: true,
    });
  }
  return result;
}

function assertResultShape(method, value) {
  if (!isObject(value)) throw invalidResult();
  const config = METHOD_CONFIG[method];
  switch (config?.shape) {
    case "ax":
      if (!hasExactKeys(value, ["nodes"]) || !Array.isArray(value.nodes)) throw invalidResult();
      break;
    case "node":
      if (!hasExactKeys(value, ["node"]) || !isObject(value.node)) throw invalidResult();
      break;
    case "model":
      if (!hasExactKeys(value, ["model"]) || !isObject(value.model)) throw invalidResult();
      break;
    case "layout": {
      const keys = Object.keys(value);
      if (keys.length === 0 || keys.some((key) => !LAYOUT_KEYS.has(key) || !isObject(value[key]))) {
        throw invalidResult();
      }
      break;
    }
    case "metrics":
      if (
        !hasExactKeys(value, ["metrics"]) ||
        !Array.isArray(value.metrics) ||
        value.metrics.length > 1_000 ||
        value.metrics.some(
          (metric) =>
            !isObject(metric) ||
            !hasExactKeys(metric, ["name", "value"]) ||
            typeof metric.name !== "string" ||
            metric.name.trim() === "" ||
            metric.name.length > 200 ||
            !Number.isFinite(metric.value),
        )
      ) {
        throw invalidResult();
      }
      break;
    default:
      throw invalidResult();
  }
}

function normalizeTruncatedShape(method, result, state) {
  if (method !== "Performance.getMetrics") return;
  const metrics = result.metrics.filter(
    (metric) =>
      isObject(metric) &&
      hasExactKeys(metric, ["name", "value"]) &&
      typeof metric.name === "string" &&
      metric.name.trim() !== "" &&
      metric.name.length <= 200 &&
      Number.isFinite(metric.value),
  );
  if (metrics.length !== result.metrics.length) state.truncated = true;
  result.metrics = metrics;
}

const OMITTED = Symbol("omitted");
function normalizeJSONValue(value, depth, state) {
  if (state.visitedNodes >= state.maxNodes) {
    state.truncated = true;
    return OMITTED;
  }
  state.visitedNodes += 1;
  if (value === null || typeof value === "boolean") return value;
  if (typeof value === "number") {
    if (Number.isFinite(value)) return value;
    throw invalidResult();
  }
  if (typeof value === "string") return normalizeString(value, state);
  if (!isObject(value) && !Array.isArray(value)) throw invalidResult();
  if (state.seen.has(value)) throw invalidResult();
  state.seen.add(value);

  const result = Array.isArray(value) ? [] : Object.create(null);
  if (depth >= state.maxDepth) {
    if (Object.keys(value).length > 0) state.truncated = true;
    return result;
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      const normalized = normalizeJSONValue(item, depth + 1, state);
      if (normalized === OMITTED) break;
      result.push(normalized);
    }
    return result;
  }

  for (const key of Object.keys(value).sort()) {
    if ([...key].length > MAX_KEY_CHARS) {
      state.truncated = true;
      continue;
    }
    if (PROHIBITED_RESULT_KEYS.has(key)) throw invalidResult();
    const sourceValue = key === "attributes" ? redactDOMAttributes(value[key], state) : value[key];
    const normalized = normalizeJSONValue(sourceValue, depth + 1, state);
    if (normalized === OMITTED) break;
    Object.defineProperty(result, key, {
      value: normalized,
      enumerable: true,
      configurable: true,
      writable: true,
    });
  }
  return result;
}

function redactDOMAttributes(value, state) {
  if (
    !Array.isArray(value) ||
    value.length % 2 !== 0 ||
    value.some((item) => typeof item !== "string")
  ) {
    throw invalidResult();
  }
  const result = [...value];
  let passwordInput = false;
  for (let index = 0; index < result.length; index += 2) {
    if (
      result[index].toLocaleLowerCase() === "type" &&
      result[index + 1].toLocaleLowerCase() === "password"
    ) {
      passwordInput = true;
    }
  }
  for (let index = 0; index < result.length; index += 2) {
    const name = result[index];
    if (SENSITIVE_IDENTITY.test(name) || (passwordInput && name.toLocaleLowerCase() === "value")) {
      result[index + 1] = REDACTED;
      state.redacted = true;
    }
  }
  return result;
}

function normalizeString(value, state) {
  let result = value;
  const replacements = [
    [/(https?:\/\/)[^/@\s:]+:[^/@\s]+@/gi, "$1[REDACTED]@"],
    [/\bBearer\s+[A-Za-z0-9._~+/=-]+/gi, "Bearer [REDACTED]"],
    [/\b((?:proxy-)?authorization\s*:\s*)(?:basic|bearer)?\s*[^\r\n,;]+/gi, "$1[REDACTED]"],
    [/\b((?:set-)?cookie\s*:\s*)[^\r\n]+/gi, "$1[REDACTED]"],
    [
      /([?&#](?:password|passwd|passphrase|secret|token|credential|authorization|cookie|api[-_]?key|access[-_]?token|refresh[-_]?token)=)[^&#\s]*/gi,
      "$1[REDACTED]",
    ],
    [
      /(\b(?:password|passwd|passphrase|secret|token|credential|authorization|cookie|api[-_]?key|access[-_]?token|refresh[-_]?token)\b\s*[:=]\s*)[^,;\s&]+/gi,
      "$1[REDACTED]",
    ],
  ];
  for (const [pattern, replacement] of replacements) {
    const replaced = result.replace(pattern, replacement);
    if (replaced !== result) state.redacted = true;
    result = replaced;
  }
  const characters = [...result];
  if (characters.length > state.maxStringChars) {
    state.truncated = true;
    result = characters.slice(0, state.maxStringChars).join("");
  }
  return result;
}

function countJSONNodes(value) {
  let count = 1;
  if (Array.isArray(value)) {
    for (const item of value) count += countJSONNodes(item);
  } else if (isObject(value)) {
    for (const item of Object.values(value)) count += countJSONNodes(item);
  }
  return count;
}

function hasExactKeys(value, expected) {
  const keys = Object.keys(value).sort();
  return keys.length === expected.length && expected.every((key) => keys.includes(key));
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function requireInteger(value, path, minimum, maximum) {
  if (!integerBetween(value, minimum, maximum)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} is outside the supported range`);
  }
}

function optionalInteger(value, path, minimum, maximum) {
  if (value !== undefined) requireInteger(value, path, minimum, maximum);
}

function optionalString(value, path, maximum) {
  if (value !== undefined && (typeof value !== "string" || [...value].length > maximum)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} exceeds its string limit`);
  }
}

function integerBetween(value, minimum, maximum) {
  return Number.isSafeInteger(value) && value >= minimum && value <= maximum;
}

function ensureResultBytes(result, maxBytes) {
  let bytes;
  try {
    bytes = new TextEncoder().encode(JSON.stringify(result)).byteLength;
  } catch {
    throw invalidResult();
  }
  if (bytes > maxBytes) {
    throw protocolError(
      ErrorCode.PAYLOAD_TOO_LARGE,
      `The normalized raw CDP result exceeds ${maxBytes} bytes`,
    );
  }
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
  if (request.target?.documentId) {
    assertFreshDocument(request.target.documentId, frame.documentId);
  }
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

function invalidResult() {
  return protocolError(ErrorCode.INVALID_MESSAGE, "CDP returned an invalid read-only result");
}
