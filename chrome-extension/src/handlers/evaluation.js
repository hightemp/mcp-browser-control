import { ErrorCode, assertFreshDocument, mapChromeError, protocolError } from "../protocol.js";

const DEFAULT_TIMEOUT_MS = 5_000;
const MAX_TIMEOUT_MS = 10_000;
const MAX_EXPRESSION_CHARS = 32_768;
const MAX_DEPTH = 10;
const MAX_NODES = 5_000;
const MAX_STRING_CHARS = 100_000;
const MIN_RESULT_BYTES = 64 * 1_024;
const MAX_RESULT_BYTES = 1_000_000;
const MAX_KEY_CHARS = 256;
const MAX_EXCEPTION_CHARS = 2_000;
const WORLD_NAME = "mcp-browser-control-isolated";
const WORLD_CSP = [
  "default-src 'none'",
  "connect-src 'none'",
  "img-src 'none'",
  "media-src 'none'",
  "object-src 'none'",
  "frame-src 'none'",
  "child-src 'none'",
  "worker-src 'none'",
  "style-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
  "script-src 'unsafe-eval'",
].join("; ");
const CDP_COMMANDS = Object.freeze([
  "Page.createIsolatedWorld",
  "Page.getFrameTree",
  "Runtime.evaluate",
  "Runtime.releaseObjectGroup",
]);

export function createEvaluationHandlers(chromeAPI, { cdpSessions } = {}) {
  let sequence = 0;

  async function evaluate(request, parentSignal) {
    assertEvaluationParams(request.params);
    const timeoutMs = request.timeoutMs || DEFAULT_TIMEOUT_MS;
    if (timeoutMs < 1 || timeoutMs > MAX_TIMEOUT_MS) {
      throw protocolError(
        ErrorCode.INVALID_MESSAGE,
        `Evaluation timeout must be between 1 and ${MAX_TIMEOUT_MS} ms`,
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
        const debuggerGranted = await chromeAPI.permissions.contains({
          permissions: ["debugger"],
        });
        if (!debuggerGranted) {
          throw protocolError(
            ErrorCode.PERMISSION_REQUIRED,
            "Debug permission is required. Grant it from the extension settings page.",
          );
        }
        await assertPageAccess(chromeAPI, tab);
        const document = await resolveRootDocument(chromeAPI, request, tab.id);
        throwIfCancelled(signal);

        const objectGroup = `mcp-isolated-evaluation-${tab.id}-${++sequence}`;
        const evaluation = await cdpSessions.withSession(
          { tabId: tab.id },
          {
            consumerId: objectGroup,
            domains: ["Page", "Runtime"],
            commands: CDP_COMMANDS,
            signal,
          },
          async (lease) => {
            const frameTree = await lease.sendCommand("Page.getFrameTree", {}, { signal });
            const frameId = frameTree?.frameTree?.frame?.id;
            if (typeof frameId !== "string" || frameId.length === 0 || frameId.length > 256) {
              throw protocolError(ErrorCode.INVALID_MESSAGE, "CDP returned an invalid root frame");
            }
            await recheckTarget(chromeAPI, tab.id, document.documentId);
            const world = await lease.sendCommand(
              "Page.createIsolatedWorld",
              {
                frameId,
                worldName: WORLD_NAME,
                grantUniveralAccess: false,
                contentSecurityPolicy: WORLD_CSP,
              },
              { signal },
            );
            if (!Number.isInteger(world?.executionContextId) || world.executionContextId < 1) {
              throw protocolError(
                ErrorCode.INVALID_MESSAGE,
                "CDP returned an invalid isolated execution context",
              );
            }
            await recheckTarget(chromeAPI, tab.id, document.documentId);
            try {
              return await lease.sendCommand(
                "Runtime.evaluate",
                {
                  expression: request.params.expression,
                  objectGroup,
                  includeCommandLineAPI: false,
                  silent: true,
                  contextId: world.executionContextId,
                  returnByValue: true,
                  generatePreview: false,
                  userGesture: false,
                  awaitPromise: request.params.awaitPromise,
                  timeout: timeoutMs,
                  disableBreaks: true,
                  allowUnsafeEvalBlockedByCSP: false,
                },
                { signal },
              );
            } finally {
              try {
                await lease.sendCommand("Runtime.releaseObjectGroup", { objectGroup });
              } catch {
                // Releasing the request-scoped CDP lease is the final cleanup boundary.
              }
            }
          },
        );

        await recheckTarget(chromeAPI, tab.id, document.documentId);
        const result = normalizeEvaluation(evaluation, request.params, tab.id, document.documentId);
        ensureResultBytes(result, request.params.maxBytes);
        return result;
      },
      parentSignal,
      timeoutMs,
    );
  }

  return { evaluate };
}

function assertEvaluationParams(params) {
  if (!params || typeof params !== "object" || Array.isArray(params)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Evaluation parameters must be an object");
  }
  const allowed = new Set([
    "expression",
    "awaitPromise",
    "maxDepth",
    "maxNodes",
    "maxStringChars",
    "maxBytes",
  ]);
  if (Object.keys(params).some((key) => !allowed.has(key))) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Evaluation parameters contain an unknown field",
    );
  }
  if (
    typeof params.expression !== "string" ||
    params.expression.trim() === "" ||
    params.expression.length > MAX_EXPRESSION_CHARS ||
    typeof params.awaitPromise !== "boolean" ||
    !integerBetween(params.maxDepth, 0, MAX_DEPTH) ||
    !integerBetween(params.maxNodes, 1, MAX_NODES) ||
    !integerBetween(params.maxStringChars, 1, MAX_STRING_CHARS) ||
    !integerBetween(params.maxBytes, MIN_RESULT_BYTES, MAX_RESULT_BYTES)
  ) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "Evaluation parameters are outside the limits");
  }
}

function integerBetween(value, minimum, maximum) {
  return Number.isInteger(value) && value >= minimum && value <= maximum;
}

function normalizeEvaluation(evaluation, limits, tabId, documentId) {
  if (!evaluation || typeof evaluation !== "object" || Array.isArray(evaluation)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "CDP returned an invalid evaluation result");
  }
  if (evaluation.exceptionDetails) {
    return {
      completed: false,
      tabId,
      documentId,
      world: "isolated",
      valueType: "undefined",
      exception: normalizeException(evaluation.exceptionDetails),
      truncated: false,
      nodeCount: 0,
      warnings: [],
    };
  }
  const remote = evaluation.result;
  if (!remote || typeof remote !== "object" || Array.isArray(remote)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "CDP returned an invalid remote value");
  }
  const normalized = normalizeRemoteValue(remote, limits);
  return {
    completed: true,
    tabId,
    documentId,
    world: "isolated",
    valueType: normalized.valueType,
    ...(normalized.hasValue ? { value: normalized.value } : {}),
    ...(normalized.unserializableValue
      ? { unserializableValue: normalized.unserializableValue }
      : {}),
    truncated: normalized.truncated,
    nodeCount: normalized.nodeCount,
    warnings: normalized.truncated
      ? ["Result was truncated to the configured depth, node, string, or key limits"]
      : [],
  };
}

function normalizeRemoteValue(remote, limits) {
  if (remote.unserializableValue !== undefined) {
    const value = String(remote.unserializableValue);
    if (!/^(?:NaN|Infinity|-Infinity|-0|-?[0-9]+n)$/.test(value) || value.length > 1_000) {
      return scalarResult("unsupported");
    }
    return {
      valueType: "unserializable",
      unserializableValue: value,
      hasValue: false,
      truncated: false,
      nodeCount: 1,
    };
  }
  if (remote.type === "undefined") return scalarResult("undefined");
  if (["function", "symbol", "bigint"].includes(remote.type)) {
    return scalarResult("unsupported");
  }
  if (remote.type === "object") {
    if (remote.subtype === "null") {
      return { ...scalarResult("null"), hasValue: true, value: null };
    }
    const permittedObject =
      remote.subtype === "array" ||
      (remote.subtype === undefined && [undefined, "Object"].includes(remote.className));
    if (!permittedObject || remote.value === undefined) return scalarResult("unsupported");
  } else if (!["boolean", "number", "string"].includes(remote.type)) {
    return scalarResult("unsupported");
  }

  const state = {
    maxDepth: limits.maxDepth,
    maxNodes: limits.maxNodes,
    maxStringChars: limits.maxStringChars,
    nodeCount: 0,
    truncated: false,
    seen: new WeakSet(),
  };
  const value = normalizeJSONValue(remote.value, 0, state);
  if (value === omitted) return scalarResult("unsupported");
  const valueType = jsonValueType(value);
  if (
    (remote.type === "object" && !["array", "object", "null"].includes(valueType)) ||
    (remote.type !== "object" && remote.type !== valueType)
  ) {
    return scalarResult("unsupported");
  }
  return {
    valueType,
    value,
    hasValue: true,
    truncated: state.truncated,
    nodeCount: state.nodeCount,
  };
}

const omitted = Symbol("omitted");
function normalizeJSONValue(value, depth, state) {
  if (state.nodeCount >= state.maxNodes) {
    state.truncated = true;
    return omitted;
  }
  state.nodeCount += 1;
  if (value === null || typeof value === "boolean") return value;
  if (typeof value === "number") {
    if (Number.isFinite(value)) return value;
    state.truncated = true;
    return null;
  }
  if (typeof value === "string") {
    const characters = [...value];
    if (characters.length <= state.maxStringChars) return value;
    state.truncated = true;
    return characters.slice(0, state.maxStringChars).join("");
  }
  if (typeof value !== "object") {
    state.truncated = true;
    return null;
  }
  if (state.seen.has(value)) {
    state.truncated = true;
    return null;
  }
  state.seen.add(value);
  const result = Array.isArray(value) ? [] : Object.create(null);
  if (depth >= state.maxDepth) {
    state.truncated = Object.keys(value).length > 0 || state.truncated;
    return result;
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      const normalized = normalizeJSONValue(item, depth + 1, state);
      if (normalized === omitted) break;
      result.push(normalized);
    }
    return result;
  }
  for (const key of Object.keys(value)) {
    if ([...key].length > MAX_KEY_CHARS) {
      state.truncated = true;
      continue;
    }
    const normalized = normalizeJSONValue(value[key], depth + 1, state);
    if (normalized === omitted) break;
    Object.defineProperty(result, key, {
      value: normalized,
      enumerable: true,
      configurable: true,
      writable: true,
    });
  }
  return result;
}

function scalarResult(valueType) {
  return {
    valueType,
    hasValue: false,
    truncated: false,
    nodeCount: 1,
  };
}

function jsonValueType(value) {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  return typeof value;
}

function normalizeException(details) {
  const text = boundedText(details.text, "JavaScript evaluation failed");
  const description = boundedText(details.exception?.description, "");
  return {
    text,
    ...(description ? { description } : {}),
    lineNumber: boundedPosition(details.lineNumber),
    columnNumber: boundedPosition(details.columnNumber),
  };
}

function boundedText(value, fallback) {
  if (typeof value !== "string" || value.trim() === "") return fallback;
  return [...value].slice(0, MAX_EXCEPTION_CHARS).join("");
}

function boundedPosition(value) {
  return Number.isSafeInteger(value) && value >= 0 ? value : 0;
}

function ensureResultBytes(result, maxBytes) {
  let bytes;
  try {
    bytes = new TextEncoder().encode(JSON.stringify(result)).byteLength;
  } catch {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "The evaluation result is not JSON-safe");
  }
  if (bytes > maxBytes) {
    throw protocolError(
      ErrorCode.PAYLOAD_TOO_LARGE,
      `The normalized evaluation result exceeds ${maxBytes} bytes`,
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
