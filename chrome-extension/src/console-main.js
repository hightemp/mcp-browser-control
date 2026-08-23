(() => {
  const BRIDGE_VERSION = "1.0";
  const CONTROL_TYPE = "MCP_BROWSER_CONSOLE_CONTROL";
  const EVENT_TYPE = "MCP_BROWSER_CONSOLE_EVENT";
  const MARKER = "__mcpBrowserConsoleMainBridge";
  const METHODS = ["debug", "log", "info", "warn", "error", "dir", "table", "trace"];
  const LEVELS = Object.freeze({
    debug: "debug",
    log: "log",
    info: "info",
    warn: "warn",
    error: "error",
    dir: "log",
    table: "log",
    trace: "debug",
  });

  if (globalThis[MARKER]?.version === BRIDGE_VERSION) return;

  const state = {
    version: BRIDGE_VERSION,
    active: false,
    captureConsole: true,
    captureErrors: true,
  };
  Object.defineProperty(globalThis, MARKER, {
    value: state,
    configurable: true,
    enumerable: false,
    writable: false,
  });

  window.addEventListener("message", (event) => {
    if (event.source !== window || event.data?.type !== CONTROL_TYPE
      || event.data?.bridgeVersion !== BRIDGE_VERSION) return;
    if (event.data.action === "start") {
      state.active = true;
      state.captureConsole = event.data.captureConsole !== false;
      state.captureErrors = event.data.captureErrors !== false;
    } else if (event.data.action === "stop") {
      state.active = false;
    }
  });

  for (const method of METHODS) {
    const original = console[method];
    if (typeof original !== "function") continue;
    const wrapped = function mcpBrowserConsoleCapture(...args) {
      const result = Reflect.apply(original, this, args);
      if (state.active && state.captureConsole) {
        emit({
          kind: "console",
          level: LEVELS[method],
          method,
          args: safeSerialize(args),
        });
      }
      return result;
    };
    try {
      Object.defineProperty(wrapped, "name", { value: original.name || method });
      console[method] = wrapped;
    } catch {
      // Some pages freeze console methods. Error capture remains available.
    }
  }

  window.addEventListener("error", (event) => {
    if (!state.active || !state.captureErrors) return;
    if (event.target && event.target !== window) {
      emit({
        kind: "resourceError",
        level: "error",
        method: "resource",
        args: safeSerialize([{
          tagName: String(event.target.tagName || "").slice(0, 100),
          url: resourceURL(event.target),
        }]),
      });
      return;
    }
    emit({
      kind: "exception",
      level: "error",
      method: "error",
      args: safeSerialize([event.error || event.message || "Uncaught error"]),
      stack: redactString(event.error?.stack || ""),
      source: redactString(event.filename || ""),
      line: boundedInteger(event.lineno),
      column: boundedInteger(event.colno),
    });
  }, true);

  window.addEventListener("unhandledrejection", (event) => {
    if (!state.active || !state.captureErrors) return;
    emit({
      kind: "unhandledRejection",
      level: "error",
      method: "unhandledrejection",
      args: safeSerialize([event.reason]),
      stack: redactString(event.reason?.stack || ""),
    });
  });

  function emit(entry) {
    try {
      window.postMessage({
        type: EVENT_TYPE,
        bridgeVersion: BRIDGE_VERSION,
        timestamp: new Date().toISOString(),
        entry,
      }, "*");
    } catch {
      // Capturing must never change page behavior.
    }
  }

  function safeSerialize(values) {
    const budget = { nodes: 0 };
    const seen = new WeakSet();
    return values.slice(0, 20).map((value) => serializeValue(value, 0, budget, seen));
  }

  function serializeValue(value, depth, budget, seen) {
    budget.nodes += 1;
    if (budget.nodes > 250) return "[Truncated]";
    if (value === null || value === undefined || typeof value === "boolean") return value ?? null;
    if (typeof value === "string") return redactString(value);
    if (typeof value === "number") return Number.isFinite(value) ? value : String(value);
    if (typeof value === "bigint") return `${value}n`;
    if (typeof value === "symbol") return String(value).slice(0, 2_000);
    if (typeof value === "function") return `[Function ${String(value.name || "anonymous").slice(0, 200)}]`;
    if (depth >= 4) return `[${objectType(value)}]`;
    if (seen.has(value)) return "[Circular]";
    seen.add(value);

    if (value instanceof Error) {
      return {
        name: redactString(value.name),
        message: redactString(value.message),
        stack: redactString(value.stack || ""),
      };
    }
    if (Array.isArray(value)) {
      return value.slice(0, 20).map((item) => serializeValue(item, depth + 1, budget, seen));
    }
    if (Number.isInteger(value.nodeType) && typeof value.nodeName === "string") {
      return {
        nodeName: String(value.nodeName).slice(0, 100),
        id: String(value.id || "").slice(0, 200),
        className: String(value.className || "").slice(0, 500),
      };
    }

    const output = {};
    let descriptors;
    try {
      descriptors = Object.getOwnPropertyDescriptors(value);
    } catch {
      return `[${objectType(value)}]`;
    }
    for (const key of Object.keys(descriptors).slice(0, 50)) {
      if (sensitiveName(key)) {
        output[key] = "[REDACTED]";
        continue;
      }
      const descriptor = descriptors[key];
      output[key] = "value" in descriptor
        ? serializeValue(descriptor.value, depth + 1, budget, seen)
        : "[Accessor]";
    }
    return output;
  }

  function objectType(value) {
    try {
      return Object.prototype.toString.call(value).slice(8, -1).slice(0, 100) || "Object";
    } catch {
      return "Object";
    }
  }

  function resourceURL(target) {
    const value = target.currentSrc || target.src || target.href || "";
    return redactString(value);
  }

  function redactString(value) {
    return String(value || "")
      .slice(0, 4_000)
      .replace(/(https?:\/\/)[^/@\s:]+:[^/@\s]+@/gi, "$1[REDACTED]@")
      .replace(/\bBearer\s+[A-Za-z0-9._~+/=-]+/gi, "Bearer [REDACTED]")
      .replace(/([?&#](?:password|secret|token|credential|authorization|cookie|api[-_]?key)=)[^&#\s]*/gi, "$1[REDACTED]")
      .replace(/((?:password|secret|token|credential|authorization|cookie|api[-_]?key)\s*[:=]\s*)[^,;\s&]+/gi, "$1[REDACTED]");
  }

  function sensitiveName(name) {
    return /(?:password|secret|token|credential|authorization|cookie|api[-_]?key)/i.test(name);
  }

  function boundedInteger(value) {
    return Number.isInteger(value) && value >= 0 && value <= 10_000_000 ? value : 0;
  }
})();
