import assert from "node:assert/strict";
import test from "node:test";

test("main-world console capture buffers, filters, redacts, stops, and clears", async () => {
  const originalWindow = globalThis.window;
  const originalChrome = globalThis.chrome;
  const originalConsole = globalThis.console;
  const fakeWindow = createFakeWindow();
  const originalCalls = [];
  const fakeConsole = Object.fromEntries(
    ["debug", "log", "info", "warn", "error", "dir", "table", "trace"].map((method) => [
      method,
      (...args) => originalCalls.push([method, args]),
    ]),
  );
  const runtimeListeners = [];
  globalThis.window = fakeWindow;
  globalThis.console = fakeConsole;
  globalThis.chrome = {
    runtime: {
      id: "extension-id",
      onMessage: { addListener: (listener) => runtimeListeners.push(listener) },
    },
  };

  try {
    const nonce = `${Date.now()}-${Math.random()}`;
    await import(`../src/console-content.js?test=${nonce}`);
    await import(`../src/console-main.js?test=${nonce}`);
    assert.equal(runtimeListeners.length, 1);
    const listener = runtimeListeners[0];

    const started = await consoleCommand(listener, "console.start", {
      bufferSize: 4,
      captureConsole: true,
      captureErrors: true,
    });
    assert.equal(started.result.active, true);
    assert.equal(started.result.documentScoped, true);

    fakeConsole.warn("request token=visible-secret https://example.com/?api_key=query-secret", {
      password: "object-secret",
      safe: "shown",
    });
    fakeWindow.emit("error", {
      target: fakeWindow,
      message: "Uncaught credential=error-secret",
      error: Object.assign(new Error("password=exception-secret"), {
        stack: "Error: token=stack-secret",
      }),
      filename: "https://example.com/app.js?token=source-secret",
      lineno: 12,
      colno: 4,
    });
    fakeWindow.emit("unhandledrejection", {
      target: fakeWindow,
      reason: new Error("Bearer rejection-secret"),
    });
    const cdpAccepted = await consoleCDPEvent(listener, {
      backend: "cdp",
      scope: "tab",
      kind: "exception",
      level: "error",
      method: "entryAdded",
      args: [{ password: "cdp-object-secret" }],
      source: "https://example.com/cdp.js?token=cdp-source-secret",
    });
    assert.equal(cdpAccepted.accepted, true);
    const wrongDocument = await consoleCDPEvent(
      listener,
      {
        backend: "cdp",
        scope: "frame",
        kind: "console",
        level: "log",
        args: ["wrong-document"],
      },
      { documentId: "document-2" },
    );
    assert.equal(wrongDocument.accepted, false);

    const read = await consoleCommand(listener, "console.read", {
      levels: ["warn", "error"],
      kinds: ["console", "exception", "unhandledRejection"],
      cursor: "0",
      limit: 10,
    });
    assert.equal(read.result.entries.length, 4);
    assert.equal(read.result.entries[0].level, "warn");
    assert.equal(read.result.entries[1].kind, "exception");
    assert.equal(read.result.entries[2].kind, "unhandledRejection");
    assert.equal(read.result.entries[3].backend, "cdp");
    assert.equal(read.result.entries[3].scope, "tab");
    assert.equal(read.result.nextCursor, "4");
    const serialized = JSON.stringify(read.result.entries);
    for (const secret of [
      "visible-secret",
      "query-secret",
      "object-secret",
      "error-secret",
      "exception-secret",
      "stack-secret",
      "source-secret",
      "rejection-secret",
      "cdp-object-secret",
      "cdp-source-secret",
    ]) {
      assert.equal(serialized.includes(secret), false, `${secret} was not redacted`);
    }
    assert.equal(serialized.includes("[REDACTED]"), true);
    assert.equal(originalCalls.length, 1);

    const stopped = await consoleCommand(listener, "console.stop", {});
    assert.equal(stopped.result.active, false);
    fakeConsole.error("after-stop");
    const afterStop = await consoleCommand(listener, "console.read", {
      cursor: "4",
    });
    assert.equal(afterStop.result.entries.length, 0);

    const cleared = await consoleCommand(listener, "console.clear", {});
    assert.equal(cleared.result.cleared, true);
    assert.equal(cleared.result.bufferedCount, 0);
  } finally {
    globalThis.window = originalWindow;
    globalThis.chrome = originalChrome;
    globalThis.console = originalConsole;
    delete globalThis.__mcpBrowserConsoleContentBridge;
    delete globalThis.__mcpBrowserConsoleMainBridge;
  }
});

test("console ring buffer reports eviction and cursor expiry", async () => {
  const originalWindow = globalThis.window;
  const originalChrome = globalThis.chrome;
  const fakeWindow = createFakeWindow();
  let listener;
  globalThis.window = fakeWindow;
  globalThis.chrome = {
    runtime: {
      id: "extension-id",
      onMessage: {
        addListener: (registered) => {
          listener = registered;
        },
      },
    },
  };
  try {
    await import(`../src/console-content.js?eviction=${Date.now()}-${Math.random()}`);
    await consoleCommand(listener, "console.start", { bufferSize: 2 });
    for (let index = 0; index < 4; index += 1) {
      fakeWindow.postMessage({
        type: "MCP_BROWSER_CONSOLE_EVENT",
        bridgeVersion: "1.0",
        timestamp: `2026-08-24T10:00:0${index}.000Z`,
        entry: { kind: "console", level: "log", method: "log", args: [index] },
      });
    }
    const read = await consoleCommand(listener, "console.read", {
      cursor: "1",
      limit: 10,
    });
    assert.deepEqual(
      read.result.entries.map((entry) => entry.cursor),
      [3, 4],
    );
    assert.equal(read.result.droppedCount, 2);
    assert.equal(read.result.cursorExpired, true);
    assert.equal(read.result.warnings.length, 2);
  } finally {
    globalThis.window = originalWindow;
    globalThis.chrome = originalChrome;
    delete globalThis.__mcpBrowserConsoleContentBridge;
  }
});

function consoleCDPEvent(listener, entry, target = {}) {
  return new Promise((resolve, reject) => {
    const handled = listener(
      {
        type: "MCP_BROWSER_CONSOLE_CDP_EVENT",
        bridgeVersion: "1.0",
        frameId: target.frameId ?? 2,
        documentId: target.documentId ?? "document-1",
        entry,
        timestamp: "2026-08-24T10:00:04.000Z",
      },
      { id: "extension-id" },
      resolve,
    );
    if (handled !== false) reject(new Error("Unexpected async CDP console handling"));
  });
}

function consoleCommand(listener, command, params) {
  return new Promise((resolve, reject) => {
    const handled = listener(
      {
        type: "MCP_BROWSER_CONSOLE_COMMAND",
        bridgeVersion: "1.0",
        operationId: `${command}-${Math.random()}`,
        command,
        params,
        frameId: 2,
        documentId: "document-1",
      },
      { id: "extension-id" },
      resolve,
    );
    if (handled !== false) reject(new Error(`Unexpected async handling for ${command}`));
  });
}

function createFakeWindow() {
  const listeners = new Map();
  const window = {
    addEventListener(type, listener) {
      const registered = listeners.get(type) || new Set();
      registered.add(listener);
      listeners.set(type, registered);
    },
    postMessage(data) {
      window.emit("message", { source: window, data, target: window });
    },
    emit(type, event) {
      for (const listener of [...(listeners.get(type) || [])]) listener(event);
    },
  };
  return window;
}
