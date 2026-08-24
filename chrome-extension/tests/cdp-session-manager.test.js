import assert from "node:assert/strict";
import test from "node:test";

import { CDP_ALLOWED_DOMAINS, createCDPSessionManager } from "../src/cdp-session-manager.js";
import { ErrorCode } from "../src/protocol.js";

test("CDP manager shares one root attachment and reference-counts consumers", async () => {
  const browser = createFakeChrome();
  const manager = createCDPSessionManager(browser, { browserVersion: "125.0.0.0" });

  const network = await manager.acquire(
    { tabId: 7 },
    { consumerId: "network", domains: ["Network"] },
  );
  const page = await manager.acquire({ tabId: 7 }, { consumerId: "page", domains: ["Page"] });

  assert.deepEqual(browser.debugger.attachCalls, [
    { target: { tabId: 7 }, requiredVersion: "1.3" },
  ]);
  assert.equal(manager.stats().sessions[0].consumerCount, 2);

  await network.release();
  assert.equal(browser.debugger.detachCalls.length, 0);
  await page.release();
  assert.deepEqual(browser.debugger.detachCalls, [{ tabId: 7 }]);
  assert.equal(manager.stats().sessionCount, 0);
  await manager.dispose();
});

test("CDP manager enforces the global and per-consumer domain allowlists", async () => {
  const browser = createFakeChrome();
  const manager = createCDPSessionManager(browser, { browserVersion: "125" });

  await assert.rejects(
    manager.acquire({ tabId: 1 }, { consumerId: "unsafe", domains: ["Security"] }),
    (error) => error.code === ErrorCode.CAPABILITY_UNAVAILABLE,
  );
  assert.equal(CDP_ALLOWED_DOMAINS.includes("Security"), false);
  assert.equal(CDP_ALLOWED_DOMAINS.includes("Audits"), true);
  assert.equal(CDP_ALLOWED_DOMAINS.includes("Profiler"), true);
  assert.equal(browser.debugger.attachCalls.length, 0);

  const lease = await manager.acquire(
    { tabId: 1 },
    {
      consumerId: "metrics",
      domains: ["Performance"],
      commands: ["Performance.getMetrics"],
    },
  );
  await assert.rejects(
    lease.sendCommand("Runtime.evaluate", { expression: "1 + 1" }),
    (error) => error.code === ErrorCode.CAPABILITY_UNAVAILABLE,
  );
  await assert.rejects(
    lease.sendCommand("invalid", {}),
    (error) => error.code === ErrorCode.INVALID_COMMAND,
  );
  await assert.rejects(
    lease.sendCommand("Performance.enable", {}),
    (error) => error.code === ErrorCode.CAPABILITY_UNAVAILABLE,
  );
  assert.equal(browser.debugger.commandCalls.length, 0);

  browser.debugger.results.set("Performance.getMetrics", { metrics: [{ name: "A", value: 1 }] });
  assert.deepEqual(await lease.sendCommand("Performance.getMetrics"), {
    metrics: [{ name: "A", value: 1 }],
  });
  assert.deepEqual(browser.debugger.commandCalls[0], {
    source: { tabId: 1 },
    method: "Performance.getMetrics",
    params: {},
  });
  await lease.release();
  await manager.dispose();
});

test("CDP manager maps DevTools attachment conflicts to a stable safe error", async () => {
  const browser = createFakeChrome({
    attachError: new Error("Another debugger is already attached to the tab with id: 9"),
  });
  const manager = createCDPSessionManager(browser, { browserVersion: "125" });

  await assert.rejects(
    manager.acquire({ tabId: 9 }, { consumerId: "network", domains: ["Network"] }),
    (error) => {
      assert.equal(error.code, ErrorCode.CAPABILITY_UNAVAILABLE);
      assert.equal(error.details.reason, "debugger_conflict");
      assert.equal(error.message.includes("tab with id"), false);
      return true;
    },
  );
  assert.equal(manager.stats().sessionCount, 0);
  await manager.dispose();
});

test("CDP manager discovers an optional debugger API granted after startup", async () => {
  const browser = {};
  const manager = createCDPSessionManager(browser, { browserVersion: "125" });
  await assert.rejects(
    manager.acquire(
      { tabId: 8 },
      {
        consumerId: "page",
        domains: ["Page"],
        commands: ["Page.getLayoutMetrics"],
      },
    ),
    (error) => error.code === ErrorCode.CAPABILITY_UNAVAILABLE,
  );

  browser.debugger = createFakeChrome().debugger;
  const lease = await manager.acquire(
    { tabId: 8 },
    {
      consumerId: "page",
      domains: ["Page"],
      commands: ["Page.getLayoutMetrics"],
    },
  );
  assert.equal(browser.debugger.attachCalls.length, 1);
  await lease.release();
  await manager.dispose();
});

test("browser onDetach invalidates every lease and notifies consumers", async () => {
  const browser = createFakeChrome();
  const detachEvents = [];
  const manager = createCDPSessionManager(browser, { browserVersion: "125" });
  const lease = await manager.acquire(
    { tabId: 4 },
    {
      consumerId: "console",
      domains: ["Runtime"],
      commands: ["Runtime.enable"],
      onDetach: (event) => detachEvents.push(event),
    },
  );

  browser.debugger.onDetach.emit({ tabId: 4 }, "canceled_by_user");
  await flushTasks();

  assert.deepEqual(detachEvents, [{ tabId: 4, reason: "canceled_by_user" }]);
  assert.equal(manager.stats().sessionCount, 0);
  await assert.rejects(
    lease.sendCommand("Runtime.enable"),
    (error) => error.code === ErrorCode.BROWSER_DISCONNECTED,
  );
  await lease.release();
  assert.equal(browser.debugger.detachCalls.length, 0);
  await manager.dispose();
});

test("child targets fail closed before Chrome 125", async () => {
  const browser = createFakeChrome();
  const manager = createCDPSessionManager(browser, { browserVersion: "124.0.6367.0" });

  await assert.rejects(
    manager.acquire(
      { tabId: 2 },
      {
        consumerId: "frames",
        domains: ["Runtime"],
        includeChildTargets: true,
      },
    ),
    (error) =>
      error.code === ErrorCode.CAPABILITY_UNAVAILABLE &&
      error.details.reason === "flat_sessions_unavailable",
  );
  assert.equal(browser.debugger.attachCalls.length, 0);
  await manager.dispose();
});

test("flat sessions recurse into child frames and retain frame context addressing", async () => {
  const browser = createFakeChrome();
  const childEvents = [];
  const rootEvents = [];
  const manager = createCDPSessionManager(browser, { browserVersion: "125" });
  const childLease = await manager.acquire(
    { tabId: 12 },
    {
      consumerId: "accessibility",
      domains: ["Runtime", "Target"],
      commands: ["Runtime.getIsolateId"],
      events: ["Target.attachedToTarget", "Runtime.executionContextCreated"],
      includeChildTargets: true,
      onEvent: (event) => childEvents.push(event),
    },
  );
  const rootLease = await manager.acquire(
    { tabId: 12 },
    {
      consumerId: "root-runtime",
      domains: ["Runtime"],
      commands: ["Runtime.getIsolateId"],
      events: ["Runtime.executionContextCreated"],
      onEvent: (event) => rootEvents.push(event),
    },
  );

  assert.deepEqual(browser.debugger.commandCalls[0], {
    source: { tabId: 12 },
    method: "Target.setAutoAttach",
    params: {
      autoAttach: true,
      waitForDebuggerOnStart: false,
      flatten: true,
      filter: [{ type: "iframe", exclude: false }],
    },
  });

  browser.debugger.onEvent.emit({ tabId: 12 }, "Target.attachedToTarget", {
    sessionId: "child-1",
    targetInfo: { targetId: "target-1", type: "iframe" },
  });
  browser.debugger.onEvent.emit(
    { tabId: 12, sessionId: "child-1" },
    "Runtime.executionContextCreated",
    {
      context: {
        id: 41,
        auxData: { frameId: "frame-1", isDefault: true },
      },
    },
  );
  await flushTasks();

  assert.equal(
    browser.debugger.commandCalls.some(
      (call) => call.source.sessionId === "child-1" && call.method === "Target.setAutoAttach",
    ),
    true,
  );
  assert.equal(
    childEvents.some((event) => event.sessionId === "child-1"),
    true,
  );
  assert.equal(rootEvents.length, 0);
  assert.deepEqual(childLease.frameContexts("frame-1"), [
    { frameId: "frame-1", contextId: 41, sessionId: "child-1", isDefault: true },
  ]);
  assert.deepEqual(rootLease.frameContexts("frame-1"), []);

  browser.debugger.onEvent.emit({ tabId: 12, sessionId: "child-1" }, "Target.attachedToTarget", {
    sessionId: "child-2",
    targetInfo: { targetId: "target-2", type: "iframe" },
  });
  browser.debugger.onEvent.emit(
    { tabId: 12, sessionId: "child-2" },
    "Runtime.executionContextCreated",
    {
      context: {
        id: 41,
        auxData: { frameId: "frame-2", isDefault: true },
      },
    },
  );
  await flushTasks();
  assert.deepEqual(childLease.frameContexts("frame-2"), [
    { frameId: "frame-2", contextId: 41, sessionId: "child-2", isDefault: true },
  ]);

  browser.debugger.results.set("Runtime.getIsolateId", { id: "isolate-1" });
  assert.deepEqual(
    await childLease.sendCommand("Runtime.getIsolateId", {}, { sessionId: "child-1" }),
    { id: "isolate-1" },
  );
  await assert.rejects(
    rootLease.sendCommand("Runtime.getIsolateId", {}, { sessionId: "child-1" }),
    (error) => error.code === ErrorCode.CAPABILITY_UNAVAILABLE,
  );

  browser.debugger.onEvent.emit({ tabId: 12 }, "Target.detachedFromTarget", {
    sessionId: "child-1",
  });
  await assert.rejects(
    childLease.sendCommand("Runtime.getIsolateId", {}, { sessionId: "child-1" }),
    (error) => error.code === ErrorCode.STALE_TARGET,
  );
  await assert.rejects(
    childLease.sendCommand("Runtime.getIsolateId", {}, { sessionId: "child-2" }),
    (error) => error.code === ErrorCode.STALE_TARGET,
  );
  assert.deepEqual(childLease.frameContexts("frame-1"), []);
  assert.deepEqual(childLease.frameContexts("frame-2"), []);

  await childLease.release();
  assert.equal(
    browser.debugger.commandCalls.some(
      (call) => call.method === "Target.setAutoAttach" && call.params.autoAttach === false,
    ),
    true,
  );
  await rootLease.release();
  await manager.dispose();
});

test("event fan-out is bounded and reports dropped events to a slow consumer", async () => {
  const browser = createFakeChrome();
  const received = [];
  let releaseFirst;
  const firstBlocked = new Promise((resolve) => {
    releaseFirst = resolve;
  });
  const manager = createCDPSessionManager(browser, {
    browserVersion: "125",
    limits: {
      maxEventsPerConsumer: 2,
      maxEventBytes: 1_024,
      maxQueuedEventBytes: 2_048,
    },
  });
  const lease = await manager.acquire(
    { tabId: 3 },
    {
      consumerId: "network",
      domains: ["Network"],
      events: ["Network.requestWillBeSent"],
      onEvent: async (event) => {
        received.push(event);
        if (received.length === 1) await firstBlocked;
      },
    },
  );

  for (let request = 1; request <= 4; request += 1) {
    browser.debugger.onEvent.emit({ tabId: 3 }, "Network.requestWillBeSent", {
      requestId: String(request),
    });
  }
  await flushTasks();
  assert.deepEqual(
    received.map((event) => event.params.requestId),
    ["1"],
  );
  assert.equal(manager.stats().sessions[0].queuedEventCount, 2);
  assert.equal(manager.stats().sessions[0].droppedEventCount, 1);

  releaseFirst();
  await eventually(() => received.length === 3);
  assert.deepEqual(
    received.map((event) => event.params.requestId),
    ["1", "3", "4"],
  );
  assert.equal(received[1].droppedBefore, 1);
  assert.equal(manager.stats().sessions[0].queuedEventCount, 0);
  assert.equal(manager.stats().sessions[0].queuedEventBytes, 0);
  assert.equal(manager.stats().sessions[0].droppedEventCount, 1);
  await lease.release();
  await manager.dispose();
});

test("oversized events and command payloads fail without unbounded buffering", async () => {
  const browser = createFakeChrome();
  const received = [];
  const manager = createCDPSessionManager(browser, {
    browserVersion: "125",
    limits: {
      maxEventBytes: 100,
      maxCommandBytes: 100,
      maxCommandResultBytes: 100,
    },
  });
  const lease = await manager.acquire(
    { tabId: 6 },
    {
      consumerId: "network",
      domains: ["Network"],
      commands: ["Network.getResponseBody"],
      events: ["Network.dataReceived", "Network.loadingFinished"],
      onEvent: (event) => received.push(event),
    },
  );

  browser.debugger.onEvent.emit({ tabId: 6 }, "Network.dataReceived", {
    body: "x".repeat(1_000),
  });
  browser.debugger.onEvent.emit({ tabId: 6 }, "Network.loadingFinished", {
    requestId: "safe",
  });
  await eventually(() => received.length === 1);
  assert.equal(received[0].droppedBefore, 1);

  await assert.rejects(
    lease.sendCommand("Network.getResponseBody", { requestId: "x".repeat(200) }),
    (error) => error.code === ErrorCode.PAYLOAD_TOO_LARGE,
  );
  browser.debugger.results.set("Network.getResponseBody", { body: "x".repeat(200) });
  await assert.rejects(
    lease.sendCommand("Network.getResponseBody", { requestId: "small" }),
    (error) => error.code === ErrorCode.PAYLOAD_TOO_LARGE,
  );
  await lease.release();
  await manager.dispose();
});

test("cancelling a pending acquire detaches a late successful attachment", async () => {
  const deferred = createDeferred();
  const browser = createFakeChrome({ attachPromise: deferred.promise });
  const manager = createCDPSessionManager(browser, { browserVersion: "125" });
  const controller = new AbortController();
  const acquiring = manager.acquire(
    { tabId: 21 },
    { consumerId: "network", domains: ["Network"], signal: controller.signal },
  );

  controller.abort();
  await assert.rejects(acquiring, (error) => error.code === ErrorCode.CANCELLED);
  deferred.resolve();
  await eventually(() => browser.debugger.detachCalls.length === 1);
  assert.deepEqual(browser.debugger.detachCalls, [{ tabId: 21 }]);
  assert.equal(manager.stats().sessionCount, 0);
  await manager.dispose();
});

test("session and consumer limits return backpressure without extra attaches", async () => {
  const browser = createFakeChrome();
  const manager = createCDPSessionManager(browser, {
    browserVersion: "125",
    limits: { maxSessions: 1, maxConsumersPerSession: 1 },
  });
  const lease = await manager.acquire({ tabId: 1 }, { consumerId: "first", domains: ["Page"] });

  await assert.rejects(
    manager.acquire({ tabId: 1 }, { consumerId: "second", domains: ["Page"] }),
    (error) => error.code === ErrorCode.BACKPRESSURE,
  );
  await assert.rejects(
    manager.acquire({ tabId: 2 }, { consumerId: "other-tab", domains: ["Page"] }),
    (error) => error.code === ErrorCode.BACKPRESSURE,
  );
  assert.equal(browser.debugger.attachCalls.length, 1);
  await lease.release();
  await manager.dispose();
});

test("detachAll force-detaches every managed target and invalidates leases", async () => {
  const browser = createFakeChrome();
  const reasons = [];
  const manager = createCDPSessionManager(browser, { browserVersion: "125" });
  const first = await manager.acquire(
    { tabId: 31 },
    {
      consumerId: "first",
      domains: ["Page"],
      onDetach: (event) => reasons.push(event),
    },
  );
  await manager.acquire(
    { tabId: 32 },
    {
      consumerId: "second",
      domains: ["Page"],
      onDetach: (event) => reasons.push(event),
    },
  );

  await manager.detachAll("server_disconnected");
  await flushTasks();
  assert.deepEqual(browser.debugger.detachCalls, [{ tabId: 31 }, { tabId: 32 }]);
  assert.deepEqual(reasons, [
    { tabId: 31, reason: "server_disconnected" },
    { tabId: 32, reason: "server_disconnected" },
  ]);
  assert.equal(manager.stats().sessionCount, 0);
  await assert.rejects(
    first.sendCommand("Page.getLayoutMetrics"),
    (error) => error.code === ErrorCode.BROWSER_DISCONNECTED,
  );
  await manager.dispose();
});

function createFakeChrome({ attachError, attachPromise } = {}) {
  const onEvent = createFakeEvent();
  const onDetach = createFakeEvent();
  const results = new Map();
  const debuggerAPI = {
    onEvent,
    onDetach,
    attachCalls: [],
    detachCalls: [],
    commandCalls: [],
    results,
    async attach(target, requiredVersion) {
      this.attachCalls.push({ target: { ...target }, requiredVersion });
      if (attachError) throw attachError;
      if (attachPromise) await attachPromise;
    },
    async detach(target) {
      this.detachCalls.push({ ...target });
    },
    async sendCommand(source, method, params = {}) {
      this.commandCalls.push({ source: { ...source }, method, params: structuredClone(params) });
      return structuredClone(results.get(method));
    },
  };
  return { debugger: debuggerAPI };
}

function createFakeEvent() {
  const listeners = new Set();
  return {
    addListener(listener) {
      listeners.add(listener);
    },
    removeListener(listener) {
      listeners.delete(listener);
    },
    emit(...args) {
      for (const listener of listeners) listener(...args);
    },
  };
}

function createDeferred() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

async function flushTasks() {
  await new Promise((resolve) => setImmediate(resolve));
}

async function eventually(condition) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    if (condition()) return;
    await flushTasks();
  }
  assert.fail("Condition was not met before the test deadline");
}
