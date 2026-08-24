(() => {
  const BRIDGE_VERSION = "1.0";
  const EVENT_TYPE = "MCP_BROWSER_CONSOLE_EVENT";
  const CONTROL_TYPE = "MCP_BROWSER_CONSOLE_CONTROL";
  const DEFAULT_BUFFER_SIZE = 1_000;
  const MAX_BUFFER_SIZE = 5_000;
  const MAX_BUFFER_CHARS = 2_000_000;
  const MAX_ENTRY_CHARS = 32_000;
  const LEVELS = new Set(["debug", "log", "info", "warn", "error"]);
  const KINDS = new Set(["console", "exception", "unhandledRejection", "resourceError"]);
  const MARKER = "__mcpBrowserConsoleContentBridge";

  if (globalThis[MARKER]?.version === BRIDGE_VERSION) return;

  const state = {
    version: BRIDGE_VERSION,
    active: false,
    captureConsole: true,
    captureErrors: true,
    bufferSize: DEFAULT_BUFFER_SIZE,
    entries: [],
    bufferedChars: 0,
    dropped: 0,
    nextCursor: 1,
    frameId: 0,
    documentId: "",
  };
  Object.defineProperty(globalThis, MARKER, {
    value: state,
    configurable: true,
    enumerable: false,
    writable: false,
  });

  window.addEventListener("message", (event) => {
    if (
      event.source !== window ||
      event.data?.type !== EVENT_TYPE ||
      event.data?.bridgeVersion !== BRIDGE_VERSION ||
      !state.active
    )
      return;
    const entry = normalizeEntry(event.data.entry, event.data.timestamp);
    if (!entry) return;
    if (!acceptsEntry(entry)) return;
    appendEntry(entry);
  });

  chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
    if (sender?.id !== chrome.runtime.id) return false;
    if (message?.type === "MCP_BROWSER_CONSOLE_READY") {
      sendResponse({ ready: true, bridgeVersion: BRIDGE_VERSION });
      return false;
    }
    if (
      message?.type === "MCP_BROWSER_CONSOLE_CDP_EVENT" &&
      message.bridgeVersion === BRIDGE_VERSION
    ) {
      const matchesTarget =
        message.frameId === state.frameId && message.documentId === state.documentId;
      const entry =
        state.active && matchesTarget ? normalizeEntry(message.entry, message.timestamp) : null;
      const accepted = Boolean(entry && acceptsEntry(entry));
      if (accepted) appendEntry(entry);
      sendResponse({ accepted });
      return false;
    }
    if (
      message?.type !== "MCP_BROWSER_CONSOLE_COMMAND" ||
      message.bridgeVersion !== BRIDGE_VERSION ||
      !isPlainObject(message.params)
    )
      return false;
    try {
      state.frameId = Number.isInteger(message.frameId) ? message.frameId : 0;
      state.documentId =
        typeof message.documentId === "string" ? message.documentId.slice(0, 200) : "";
      sendResponse({
        success: true,
        result: dispatch(message.command, message.params),
      });
    } catch (error) {
      sendResponse({
        success: false,
        error: {
          code: error.code || "INVALID_MESSAGE",
          message: String(error.message || "Console command failed").slice(0, 1_000),
          retryable: Boolean(error.retryable),
        },
      });
    }
    return false;
  });

  function dispatch(command, params) {
    switch (command) {
      case "console.start":
        return start(params);
      case "console.stop":
        requireEmpty(params);
        state.active = false;
        postControl("stop");
        return captureState();
      case "console.clear":
        requireEmpty(params);
        state.entries = [];
        state.bufferedChars = 0;
        state.dropped = 0;
        return { ...captureState(), cleared: true };
      case "console.read":
        return read(params);
      default:
        throw commandError("INVALID_COMMAND", `Unknown console command "${command}"`);
    }
  }

  function start(params) {
    assertAllowed(params, ["bufferSize", "captureConsole", "captureErrors"]);
    const bufferSize = params.bufferSize ?? DEFAULT_BUFFER_SIZE;
    if (!Number.isInteger(bufferSize) || bufferSize < 1 || bufferSize > MAX_BUFFER_SIZE) {
      throw commandError("INVALID_MESSAGE", "bufferSize is out of range");
    }
    if (params.captureConsole !== undefined && typeof params.captureConsole !== "boolean") {
      throw commandError("INVALID_MESSAGE", "captureConsole must be a boolean");
    }
    if (params.captureErrors !== undefined && typeof params.captureErrors !== "boolean") {
      throw commandError("INVALID_MESSAGE", "captureErrors must be a boolean");
    }
    const captureConsole = params.captureConsole !== false;
    const captureErrors = params.captureErrors !== false;
    if (!captureConsole && !captureErrors) {
      throw commandError("INVALID_MESSAGE", "At least one capture source must be enabled");
    }
    state.bufferSize = bufferSize;
    state.captureConsole = captureConsole;
    state.captureErrors = captureErrors;
    state.active = true;
    enforceBufferBounds();
    postControl("start");
    return {
      ...captureState(),
      documentScoped: true,
      warnings: ["Console capture is document-scoped and must be restarted after navigation"],
    };
  }

  function read(params) {
    assertAllowed(params, ["levels", "kinds", "cursor", "limit", "since"]);
    const levels = validateFilter(params.levels, LEVELS, "levels");
    const kinds = validateFilter(params.kinds, KINDS, "kinds");
    const cursor = parseCursor(params.cursor);
    const limit = params.limit ?? 100;
    if (!Number.isInteger(limit) || limit < 1 || limit > 200) {
      throw commandError("INVALID_MESSAGE", "limit is out of range");
    }
    let since = 0;
    if (params.since !== undefined) {
      if (typeof params.since !== "string" || !Number.isFinite(Date.parse(params.since))) {
        throw commandError("INVALID_MESSAGE", "since must be an RFC 3339 timestamp");
      }
      since = Date.parse(params.since);
    }

    const oldestCursor = state.entries[0]?.cursor ?? state.nextCursor;
    const cursorExpired = cursor > 0 && cursor < oldestCursor - 1;
    const entries = [];
    let scannedCursor = cursor;
    for (const entry of state.entries) {
      if (entry.cursor <= cursor) continue;
      scannedCursor = entry.cursor;
      if (levels && !levels.has(entry.level)) continue;
      if (kinds && !kinds.has(entry.kind)) continue;
      if (Date.parse(entry.timestamp) < since) continue;
      entries.push({ ...entry });
      if (entries.length >= limit) break;
    }
    const hasMore = state.entries.some((entry) => entry.cursor > scannedCursor);
    const warnings = [];
    if (!state.active) warnings.push("Console capture is not active for this document");
    if (cursorExpired) warnings.push("The requested cursor predates the retained ring buffer");
    if (state.dropped > 0) warnings.push("Older console entries were evicted by buffer limits");
    return {
      ...captureState(),
      entries,
      returnedCount: entries.length,
      nextCursor: String(scannedCursor),
      hasMore,
      cursorExpired,
      warnings,
    };
  }

  function appendEntry(entry) {
    entry.cursor = state.nextCursor;
    state.nextCursor += 1;
    let encoded = JSON.stringify(entry);
    if (encoded.length > MAX_ENTRY_CHARS) {
      entry.args = ["[Console arguments exceeded the per-entry limit]"];
      entry.stack = String(entry.stack || "").slice(0, 2_000);
      entry.truncated = true;
      encoded = JSON.stringify(entry);
    }
    Object.defineProperty(entry, "__bufferChars", {
      value: encoded.length,
      enumerable: false,
    });
    state.entries.push(entry);
    state.bufferedChars += entry.__bufferChars;
    enforceBufferBounds();
  }

  function acceptsEntry(entry) {
    return entry.kind === "console" ? state.captureConsole : state.captureErrors;
  }

  function enforceBufferBounds() {
    while (state.entries.length > state.bufferSize || state.bufferedChars > MAX_BUFFER_CHARS) {
      const removed = state.entries.shift();
      state.bufferedChars -= removed?.__bufferChars || 0;
      state.dropped += 1;
    }
  }

  function normalizeEntry(candidate, timestamp) {
    if (!isPlainObject(candidate) || !KINDS.has(candidate.kind) || !LEVELS.has(candidate.level)) {
      return null;
    }
    const normalizedTimestamp =
      typeof timestamp === "string" && Number.isFinite(Date.parse(timestamp))
        ? new Date(timestamp).toISOString()
        : new Date().toISOString();
    return {
      cursor: 0,
      timestamp: normalizedTimestamp,
      level: candidate.level,
      kind: candidate.kind,
      backend: candidate.backend === "cdp" ? "cdp" : "bridge",
      scope: candidate.scope === "tab" ? "tab" : "frame",
      method: typeof candidate.method === "string" ? candidate.method.slice(0, 100) : "",
      args: sanitizeValue(Array.isArray(candidate.args) ? candidate.args : []),
      ...(typeof candidate.stack === "string"
        ? { stack: redactString(candidate.stack).slice(0, 8_000) }
        : {}),
      ...(typeof candidate.source === "string"
        ? { source: redactString(candidate.source).slice(0, 4_000) }
        : {}),
      ...(Number.isInteger(candidate.line) ? { line: Math.max(0, candidate.line) } : {}),
      ...(Number.isInteger(candidate.column) ? { column: Math.max(0, candidate.column) } : {}),
    };
  }

  function sanitizeValue(value, depth = 0, budget = { nodes: 0 }, seen = new WeakSet()) {
    budget.nodes += 1;
    if (budget.nodes > 250) return "[Truncated]";
    if (value === null || value === undefined || typeof value === "boolean") return value ?? null;
    if (typeof value === "string") return redactString(value).slice(0, 4_000);
    if (typeof value === "number") return Number.isFinite(value) ? value : String(value);
    if (typeof value !== "object" || depth >= 4) return String(value).slice(0, 500);
    if (seen.has(value)) return "[Circular]";
    seen.add(value);
    if (Array.isArray(value)) {
      return value.slice(0, 20).map((item) => sanitizeValue(item, depth + 1, budget, seen));
    }
    const output = {};
    for (const key of Object.keys(value).slice(0, 50)) {
      if (sensitiveName(key)) {
        output[key] = "[REDACTED]";
      } else {
        try {
          output[key] = sanitizeValue(value[key], depth + 1, budget, seen);
        } catch {
          output[key] = "[Unavailable]";
        }
      }
    }
    return output;
  }

  function postControl(action) {
    window.postMessage(
      {
        type: CONTROL_TYPE,
        bridgeVersion: BRIDGE_VERSION,
        action,
        captureConsole: state.captureConsole,
        captureErrors: state.captureErrors,
      },
      "*",
    );
  }

  function captureState() {
    return {
      active: state.active,
      captureConsole: state.captureConsole,
      captureErrors: state.captureErrors,
      bufferSize: state.bufferSize,
      bufferedCount: state.entries.length,
      droppedCount: state.dropped,
      frameId: state.frameId,
      documentId: state.documentId,
    };
  }

  function validateFilter(values, allowed, name) {
    if (values === undefined) return null;
    if (
      !Array.isArray(values) ||
      values.length > allowed.size ||
      values.some((value) => !allowed.has(value)) ||
      new Set(values).size !== values.length
    ) {
      throw commandError("INVALID_MESSAGE", `${name} contains invalid values`);
    }
    return values.length > 0 ? new Set(values) : null;
  }

  function parseCursor(value) {
    if (value === undefined || value === "") return 0;
    if (typeof value !== "string" || !/^\d+$/.test(value)) {
      throw commandError("INVALID_MESSAGE", "cursor must be an unsigned integer string");
    }
    const cursor = Number.parseInt(value, 10);
    if (!Number.isSafeInteger(cursor))
      throw commandError("INVALID_MESSAGE", "cursor is out of range");
    return cursor;
  }

  function requireEmpty(params) {
    assertAllowed(params, []);
  }

  function assertAllowed(params, allowed) {
    const unexpected = Object.keys(params).find((property) => !allowed.includes(property));
    if (unexpected) throw commandError("INVALID_MESSAGE", `Unexpected parameter "${unexpected}"`);
  }

  function redactString(value) {
    return String(value || "")
      .replace(/(https?:\/\/)[^/@\s:]+:[^/@\s]+@/gi, "$1[REDACTED]@")
      .replace(/\bBearer\s+[A-Za-z0-9._~+/=-]+/gi, "Bearer [REDACTED]")
      .replace(
        /([?&#](?:password|secret|token|credential|authorization|cookie|api[-_]?key)=)[^&#\s]*/gi,
        "$1[REDACTED]",
      )
      .replace(
        /((?:password|secret|token|credential|authorization|cookie|api[-_]?key)\s*[:=]\s*)[^,;\s&]+/gi,
        "$1[REDACTED]",
      );
  }

  function sensitiveName(name) {
    return /(?:password|secret|token|credential|authorization|cookie|api[-_]?key)/i.test(name);
  }

  function commandError(code, message) {
    const error = new Error(message);
    error.code = code;
    return error;
  }

  function isPlainObject(value) {
    return Boolean(value) && typeof value === "object" && !Array.isArray(value);
  }
})();
