import { ErrorCode, protocolError } from "./protocol.js";

export const CDP_PROTOCOL_VERSION = "1.3";

export const CDP_ALLOWED_DOMAINS = Object.freeze([
  "Accessibility",
  "DOM",
  "Emulation",
  "IO",
  "Input",
  "Log",
  "Network",
  "Page",
  "Performance",
  "Runtime",
  "Target",
  "Tracing",
]);

const DEFAULT_LIMITS = Object.freeze({
  maxSessions: 8,
  maxConsumersPerSession: 8,
  maxEventsPerConsumer: 256,
  maxEventBytes: 256 * 1024,
  maxQueuedEventBytes: 2 * 1024 * 1024,
  maxCommandBytes: 1024 * 1024,
  maxCommandResultBytes: 4 * 1024 * 1024,
});

const CHILD_TARGET_FILTER = Object.freeze([{ type: "iframe", exclude: false }]);

export function createCDPSessionManager(chromeAPI, options = {}) {
  return new CDPSessionManager(chromeAPI, options);
}

export class CDPSessionManager {
  constructor(chromeAPI, options = {}) {
    this.chromeAPI = chromeAPI;
    this.debuggerAPI = null;
    this.listenersInstalled = false;
    this.requiredVersion = options.requiredVersion || CDP_PROTOCOL_VERSION;
    this.allowedDomains = new Set(options.allowedDomains || CDP_ALLOWED_DOMAINS);
    this.limits = normalizeLimits(options.limits);
    this.supportsFlatSessions =
      options.supportsFlatSessions ?? browserSupportsFlatSessions(options.browserVersion);
    this.onConsumerError = options.onConsumerError || (() => {});
    this.sessions = new Map();
    this.disposed = false;

    this.handleEvent = this.handleEvent.bind(this);
    this.handleDetach = this.handleDetach.bind(this);
    this.installDebuggerListeners();
  }

  async acquire(target, options = {}) {
    this.ensureAvailable();
    const debuggee = normalizeDebuggee(target);
    const consumer = this.createConsumer(options);
    if (consumer.includeChildTargets && !this.supportsFlatSessions) {
      throw protocolError(
        ErrorCode.CAPABILITY_UNAVAILABLE,
        "Child CDP targets require Chrome 125 or newer",
        false,
        { reason: "flat_sessions_unavailable" },
      );
    }
    throwIfAborted(options.signal);

    const key = debuggeeKey(debuggee);
    let session = this.sessions.get(key);
    if (!session) {
      if (this.sessions.size >= this.limits.maxSessions) {
        throw protocolError(
          ErrorCode.BACKPRESSURE,
          "The browser has reached the managed CDP session limit",
          true,
        );
      }
      session = this.createSession(key, debuggee);
      this.sessions.set(key, session);
      session.ready = this.attachSession(session);
    }
    if (session.consumers.has(consumer.id)) {
      throw protocolError(
        ErrorCode.INVALID_COMMAND,
        `CDP consumer "${consumer.id}" already owns a lease for this target`,
      );
    }
    if (session.consumers.size >= this.limits.maxConsumersPerSession) {
      throw protocolError(
        ErrorCode.BACKPRESSURE,
        "The target has reached the CDP consumer limit",
        true,
      );
    }

    session.consumers.set(consumer.id, consumer);
    if (consumer.includeChildTargets) session.childConsumerCount += 1;

    try {
      await waitWithSignal(session.ready, options.signal);
      if (!consumer.active || session.state !== "attached") {
        throw protocolError(
          ErrorCode.BROWSER_DISCONNECTED,
          "The CDP session ended while the consumer was attaching",
          true,
        );
      }
      if (consumer.includeChildTargets) {
        await waitWithSignal(this.ensureAutoAttach(session), options.signal);
      }
      throwIfAborted(options.signal);
      return this.createLease(session, consumer);
    } catch (error) {
      await this.releaseConsumer(session, consumer);
      throw normalizeDebuggerError(error, "attach");
    }
  }

  async withSession(target, options, operation) {
    const lease = await this.acquire(target, options);
    try {
      return await operation(lease);
    } finally {
      await lease.release();
    }
  }

  async detachAll(reason = "manager_shutdown") {
    const sessions = [...this.sessions.values()];
    await Promise.allSettled(
      sessions.map((session) => this.closeSession(session, reason, { notify: true })),
    );
  }

  async dispose() {
    if (this.disposed) return;
    this.disposed = true;
    await this.detachAll("manager_disposed");
    this.removeDebuggerListeners();
  }

  stats() {
    return {
      sessionCount: this.sessions.size,
      supportsFlatSessions: this.supportsFlatSessions,
      sessions: [...this.sessions.values()].map((session) => ({
        tabId: session.debuggee.tabId,
        state: session.state,
        consumerCount: session.consumers.size,
        childSessionCount: session.childSessions.size,
        frameCount: session.frameContexts.size,
      })),
    };
  }

  ensureAvailable() {
    if (this.disposed) {
      throw protocolError(ErrorCode.CAPABILITY_UNAVAILABLE, "The CDP session manager is closed");
    }
    this.installDebuggerListeners();
    if (
      !this.debuggerAPI?.attach ||
      !this.debuggerAPI?.detach ||
      !this.debuggerAPI?.sendCommand ||
      !this.debuggerAPI?.onEvent ||
      !this.debuggerAPI?.onDetach
    ) {
      throw protocolError(
        ErrorCode.CAPABILITY_UNAVAILABLE,
        "The browser debugger API is unavailable",
      );
    }
  }

  installDebuggerListeners() {
    const currentAPI = this.chromeAPI?.debugger;
    if (currentAPI !== this.debuggerAPI) {
      this.removeDebuggerListeners();
      this.debuggerAPI = currentAPI;
    }
    if (
      !this.listenersInstalled &&
      this.debuggerAPI?.onEvent?.addListener &&
      this.debuggerAPI?.onDetach?.addListener
    ) {
      this.debuggerAPI.onEvent.addListener(this.handleEvent);
      this.debuggerAPI.onDetach.addListener(this.handleDetach);
      this.listenersInstalled = true;
    }
  }

  removeDebuggerListeners() {
    if (!this.listenersInstalled) return;
    this.debuggerAPI?.onEvent?.removeListener?.(this.handleEvent);
    this.debuggerAPI?.onDetach?.removeListener?.(this.handleDetach);
    this.listenersInstalled = false;
  }

  createConsumer(options) {
    const id = String(options.consumerId || "").trim();
    if (!id || id.length > 128) {
      throw protocolError(
        ErrorCode.INVALID_COMMAND,
        "A CDP consumerId between 1 and 128 characters is required",
      );
    }
    if (!Array.isArray(options.domains) || options.domains.length === 0) {
      throw protocolError(ErrorCode.INVALID_COMMAND, "At least one CDP domain is required");
    }
    if (options.domains.length > this.allowedDomains.size) {
      throw protocolError(ErrorCode.INVALID_COMMAND, "Too many CDP domains were requested");
    }
    const domains = new Set();
    for (const domain of options.domains) {
      if (typeof domain !== "string" || !this.allowedDomains.has(domain)) {
        throw protocolError(
          ErrorCode.CAPABILITY_UNAVAILABLE,
          `CDP domain "${String(domain)}" is not allowlisted`,
        );
      }
      if (domains.has(domain)) {
        throw protocolError(ErrorCode.INVALID_COMMAND, `Duplicate CDP domain "${domain}"`);
      }
      domains.add(domain);
    }
    const commands = validateMethodAllowlist(options.commands, "commands", domains);
    const events = validateMethodAllowlist(options.events, "events", domains);
    for (const callbackName of ["onEvent", "onDetach"]) {
      if (options[callbackName] !== undefined && typeof options[callbackName] !== "function") {
        throw protocolError(ErrorCode.INVALID_COMMAND, `${callbackName} must be a function`);
      }
    }
    if (options.onEvent && events.size === 0) {
      throw protocolError(
        ErrorCode.INVALID_COMMAND,
        "A CDP event callback requires at least one exact event method",
      );
    }
    return {
      id,
      domains,
      commands,
      events,
      includeChildTargets: options.includeChildTargets === true,
      onEvent: options.onEvent,
      onDetach: options.onDetach,
      queue: [],
      queueBytes: 0,
      droppedEvents: 0,
      draining: false,
      active: true,
    };
  }

  createSession(key, debuggee) {
    return {
      key,
      debuggee,
      state: "attaching",
      ready: null,
      closeRequested: "",
      consumers: new Map(),
      childConsumerCount: 0,
      childSessions: new Map(),
      frameContexts: new Map(),
      autoAttachEnabled: false,
      autoAttachPromise: null,
    };
  }

  async attachSession(session) {
    try {
      await this.debuggerAPI.attach(session.debuggee, this.requiredVersion);
      session.state = "attached";
      if (session.closeRequested || session.consumers.size === 0 || this.disposed) {
        await this.closeSession(session, session.closeRequested || "unused_session");
      }
    } catch (error) {
      if (this.sessions.get(session.key) === session) this.sessions.delete(session.key);
      session.state = "failed";
      this.deactivateConsumers(session);
      throw normalizeDebuggerError(error, "attach");
    }
  }

  createLease(session, consumer) {
    let released = false;
    return Object.freeze({
      tabId: session.debuggee.tabId,
      supportsChildTargets: this.supportsFlatSessions,
      sendCommand: (method, params = {}, commandOptions = {}) =>
        this.sendCommand(session, consumer, method, params, commandOptions),
      frameContexts: (frameId = undefined) => this.frameContextsFor(session, consumer, frameId),
      release: async () => {
        if (released) return;
        released = true;
        await this.releaseConsumer(session, consumer);
      },
    });
  }

  async sendCommand(session, consumer, method, params, options) {
    if (!consumer.active || session.state !== "attached") {
      throw protocolError(
        ErrorCode.BROWSER_DISCONNECTED,
        "The CDP lease is no longer active",
        true,
      );
    }
    throwIfAborted(options.signal);
    const domain = methodDomain(method);
    if (!consumer.domains.has(domain) || !consumer.commands.has(method)) {
      throw protocolError(
        ErrorCode.CAPABILITY_UNAVAILABLE,
        `CDP method "${method}" is not allowlisted for this consumer`,
      );
    }
    validateCommandParams(params);
    ensureBoundedJSON(params, this.limits.maxCommandBytes, "CDP command parameters");

    const source = { ...session.debuggee };
    if (options.sessionId !== undefined) {
      if (!consumer.includeChildTargets || !this.supportsFlatSessions) {
        throw protocolError(
          ErrorCode.CAPABILITY_UNAVAILABLE,
          "This CDP consumer cannot address child targets",
        );
      }
      if (!session.childSessions.has(options.sessionId)) {
        throw protocolError(ErrorCode.STALE_TARGET, "The child CDP session is no longer available");
      }
      source.sessionId = options.sessionId;
    }

    try {
      const result = await waitWithSignal(
        this.debuggerAPI.sendCommand(source, method, params),
        options.signal,
      );
      ensureBoundedJSON(result, this.limits.maxCommandResultBytes, "CDP command result");
      return result ?? {};
    } catch (error) {
      throw normalizeDebuggerError(error, "command");
    }
  }

  frameContextsFor(session, consumer, frameId) {
    if (!consumer.active || session.state !== "attached") {
      throw protocolError(
        ErrorCode.BROWSER_DISCONNECTED,
        "The CDP lease is no longer active",
        true,
      );
    }
    const entries =
      frameId === undefined
        ? session.frameContexts.entries()
        : [[frameId, session.frameContexts.get(frameId)]];
    const result = [];
    for (const [currentFrameId, contexts] of entries) {
      if (!contexts) continue;
      for (const context of contexts.values()) {
        if (context.sessionId && !consumer.includeChildTargets) continue;
        result.push({ ...context, frameId: currentFrameId });
      }
    }
    return result.sort((left, right) => left.contextId - right.contextId);
  }

  async releaseConsumer(session, consumer) {
    if (!consumer.active) return;
    consumer.active = false;
    consumer.queue.length = 0;
    consumer.queueBytes = 0;
    if (session.consumers.get(consumer.id) !== consumer) return;
    session.consumers.delete(consumer.id);
    if (consumer.includeChildTargets) {
      session.childConsumerCount = Math.max(0, session.childConsumerCount - 1);
      if (session.childConsumerCount === 0 && session.consumers.size > 0) {
        await this.disableAutoAttach(session);
      }
    }
    if (session.consumers.size === 0) {
      if (session.state === "attaching") {
        session.closeRequested = "last_consumer_released";
        return;
      }
      await this.closeSession(session, "last_consumer_released");
    }
  }

  async ensureAutoAttach(session) {
    if (session.autoAttachEnabled || session.state !== "attached") return;
    if (session.autoAttachPromise) return session.autoAttachPromise;
    session.autoAttachPromise = this.sendInfrastructureCommand(
      session.debuggee,
      "Target.setAutoAttach",
      autoAttachParams(true),
    )
      .then(() => {
        session.autoAttachEnabled = true;
      })
      .finally(() => {
        session.autoAttachPromise = null;
      });
    return session.autoAttachPromise;
  }

  async disableAutoAttach(session) {
    if (session.state !== "attached") return;
    if (session.autoAttachPromise) {
      try {
        await session.autoAttachPromise;
      } catch {
        return;
      }
    }
    if (!session.autoAttachEnabled) return;
    session.autoAttachEnabled = false;
    session.childSessions.clear();
    removeChildFrameContexts(session);
    try {
      await this.sendInfrastructureCommand(
        session.debuggee,
        "Target.setAutoAttach",
        autoAttachParams(false),
      );
    } catch (error) {
      this.reportConsumerError(error, {
        phase: "disable_auto_attach",
        tabId: session.debuggee.tabId,
      });
    }
  }

  async sendInfrastructureCommand(source, method, params) {
    try {
      return await this.debuggerAPI.sendCommand(source, method, params);
    } catch (error) {
      throw normalizeDebuggerError(error, "command");
    }
  }

  handleEvent(source, method, params = {}) {
    const session = this.sessions.get(debuggeeKey(source));
    if (!session || session.state !== "attached") return;
    if (source.sessionId && !session.childSessions.has(source.sessionId)) return;

    if (method === "Target.attachedToTarget" && session.childConsumerCount > 0) {
      const childSessionId = params?.sessionId;
      if (
        typeof childSessionId === "string" &&
        childSessionId &&
        params?.targetInfo?.type === "iframe"
      ) {
        session.childSessions.set(childSessionId, {
          sessionId: childSessionId,
          parentSessionId: source.sessionId || "",
          targetId: String(params.targetInfo?.targetId || ""),
          type: String(params.targetInfo?.type || ""),
        });
        const childSource = { ...session.debuggee, sessionId: childSessionId };
        void this.sendInfrastructureCommand(
          childSource,
          "Target.setAutoAttach",
          autoAttachParams(true),
        ).catch((error) => {
          this.reportConsumerError(error, {
            phase: "child_auto_attach",
            tabId: session.debuggee.tabId,
          });
        });
      }
    } else if (method === "Target.detachedFromTarget") {
      const childSessionId = params?.sessionId;
      if (typeof childSessionId === "string") {
        removeChildSessionTree(session, childSessionId);
      }
    }

    updateFrameContexts(session, source, method, params);
    let domain;
    try {
      domain = methodDomain(method);
    } catch {
      return;
    }
    for (const consumer of session.consumers.values()) {
      if (!consumer.onEvent || !consumer.domains.has(domain) || !consumer.events.has(method)) {
        continue;
      }
      if (source.sessionId && !consumer.includeChildTargets) continue;
      this.enqueueEvent(consumer, {
        tabId: session.debuggee.tabId,
        ...(source.sessionId ? { sessionId: source.sessionId } : {}),
        method,
        params,
      });
    }
  }

  handleDetach(source, reason) {
    const session = this.sessions.get(debuggeeKey(source));
    if (!session) return;
    if (this.sessions.get(session.key) === session) this.sessions.delete(session.key);
    session.state = "detached";
    this.notifyAndDeactivateConsumers(session, reason || "browser_detached");
    session.childSessions.clear();
    session.frameContexts.clear();
  }

  enqueueEvent(consumer, event) {
    const size = jsonByteLength(event);
    if (size > this.limits.maxEventBytes) {
      consumer.droppedEvents += 1;
      return;
    }
    while (
      consumer.queue.length >= this.limits.maxEventsPerConsumer ||
      consumer.queueBytes + size > this.limits.maxQueuedEventBytes
    ) {
      const dropped = consumer.queue.shift();
      if (!dropped) break;
      consumer.queueBytes -= dropped.size;
      consumer.droppedEvents += 1;
    }
    consumer.queue.push({ event, size });
    consumer.queueBytes += size;
    if (!consumer.draining) void this.drainConsumer(consumer);
  }

  async drainConsumer(consumer) {
    consumer.draining = true;
    try {
      while (consumer.active && consumer.queue.length > 0) {
        const queued = consumer.queue.shift();
        consumer.queueBytes -= queued.size;
        const droppedBefore = consumer.droppedEvents;
        consumer.droppedEvents = 0;
        try {
          await consumer.onEvent({
            ...queued.event,
            ...(droppedBefore > 0 ? { droppedBefore } : {}),
          });
        } catch (error) {
          this.reportConsumerError(error, { phase: "event_consumer", consumerId: consumer.id });
        }
      }
    } finally {
      consumer.draining = false;
    }
  }

  async closeSession(session, reason, { notify = false } = {}) {
    if (session.state === "detached" || session.state === "failed") return;
    if (session.state === "attaching") {
      session.closeRequested = reason;
      if (notify) this.notifyAndDeactivateConsumers(session, reason);
      return;
    }
    if (session.state === "detaching") return;

    session.state = "detaching";
    if (this.sessions.get(session.key) === session) this.sessions.delete(session.key);
    if (notify) this.notifyAndDeactivateConsumers(session, reason);
    else this.deactivateConsumers(session);
    session.childSessions.clear();
    session.frameContexts.clear();
    try {
      await this.debuggerAPI.detach(session.debuggee);
    } catch (error) {
      const normalized = normalizeDebuggerError(error, "detach");
      if (![ErrorCode.TAB_NOT_FOUND, ErrorCode.BROWSER_DISCONNECTED].includes(normalized.code)) {
        this.reportConsumerError(normalized, { phase: "detach", tabId: session.debuggee.tabId });
      }
    } finally {
      session.state = "detached";
    }
  }

  notifyAndDeactivateConsumers(session, reason) {
    for (const consumer of session.consumers.values()) {
      consumer.active = false;
      consumer.queue.length = 0;
      consumer.queueBytes = 0;
      if (consumer.onDetach) {
        void Promise.resolve(
          consumer.onDetach({ tabId: session.debuggee.tabId, reason: String(reason) }),
        ).catch((error) => {
          this.reportConsumerError(error, { phase: "detach_consumer", consumerId: consumer.id });
        });
      }
    }
    session.consumers.clear();
    session.childConsumerCount = 0;
  }

  deactivateConsumers(session) {
    for (const consumer of session.consumers.values()) {
      consumer.active = false;
      consumer.queue.length = 0;
      consumer.queueBytes = 0;
    }
    session.consumers.clear();
    session.childConsumerCount = 0;
  }

  reportConsumerError(error, context) {
    try {
      this.onConsumerError(normalizeDebuggerError(error, "consumer"), context);
    } catch {
      // Diagnostics must not break debugger lifecycle cleanup.
    }
  }
}

function normalizeDebuggee(target) {
  if (
    !target ||
    typeof target !== "object" ||
    Array.isArray(target) ||
    !Number.isInteger(target.tabId) ||
    target.tabId < 0 ||
    Object.keys(target).some((key) => key !== "tabId")
  ) {
    throw protocolError(
      ErrorCode.INVALID_COMMAND,
      "Managed CDP sessions require exactly one non-negative tabId",
    );
  }
  return Object.freeze({ tabId: target.tabId });
}

function debuggeeKey(source) {
  return Number.isInteger(source?.tabId) ? `tab:${source.tabId}` : "invalid";
}

function methodDomain(method) {
  if (typeof method !== "string" || !/^[A-Z][A-Za-z0-9]*\.[A-Za-z][A-Za-z0-9]*$/.test(method)) {
    throw protocolError(ErrorCode.INVALID_COMMAND, "CDP method must use Domain.method syntax");
  }
  return method.slice(0, method.indexOf("."));
}

function validateCommandParams(params) {
  if (!params || typeof params !== "object" || Array.isArray(params)) {
    throw protocolError(ErrorCode.INVALID_COMMAND, "CDP command parameters must be an object");
  }
}

function validateMethodAllowlist(methods = [], field, domains) {
  if (!Array.isArray(methods) || methods.length > 64) {
    throw protocolError(
      ErrorCode.INVALID_COMMAND,
      `CDP ${field} must be an array with at most 64 entries`,
    );
  }
  const result = new Set();
  for (const method of methods) {
    const domain = methodDomain(method);
    if (!domains.has(domain)) {
      throw protocolError(
        ErrorCode.INVALID_COMMAND,
        `CDP ${field} method "${method}" requires the "${domain}" domain`,
      );
    }
    if (result.has(method)) {
      throw protocolError(ErrorCode.INVALID_COMMAND, `Duplicate CDP ${field} method "${method}"`);
    }
    result.add(method);
  }
  return result;
}

function autoAttachParams(enabled) {
  return {
    autoAttach: enabled,
    waitForDebuggerOnStart: false,
    flatten: true,
    filter: CHILD_TARGET_FILTER.map((entry) => ({ ...entry })),
  };
}

function browserSupportsFlatSessions(version) {
  const match = String(version || "").match(/^(\d+)/);
  return Boolean(match && Number.parseInt(match[1], 10) >= 125);
}

function normalizeLimits(overrides = {}) {
  const limits = { ...DEFAULT_LIMITS, ...overrides };
  for (const [name, value] of Object.entries(limits)) {
    if (!Number.isInteger(value) || value < 1) {
      throw protocolError(ErrorCode.INVALID_COMMAND, `CDP limit "${name}" must be positive`);
    }
  }
  if (limits.maxQueuedEventBytes < limits.maxEventBytes) {
    throw protocolError(
      ErrorCode.INVALID_COMMAND,
      "The CDP event queue byte limit cannot be smaller than one event",
    );
  }
  return limits;
}

function updateFrameContexts(session, source, method, params) {
  if (method === "Runtime.executionContextCreated") {
    const context = params?.context;
    const frameId = context?.auxData?.frameId;
    if (typeof frameId !== "string" || !frameId || !Number.isInteger(context?.id)) return;
    let contexts = session.frameContexts.get(frameId);
    if (!contexts) {
      contexts = new Map();
      session.frameContexts.set(frameId, contexts);
    }
    contexts.set(contextKey(source.sessionId, context.id), {
      contextId: context.id,
      ...(source.sessionId ? { sessionId: source.sessionId } : {}),
      isDefault: context.auxData?.isDefault === true,
    });
  } else if (method === "Runtime.executionContextDestroyed") {
    const contextId = params?.executionContextId;
    for (const [frameId, contexts] of session.frameContexts) {
      contexts.delete(contextKey(source.sessionId, contextId));
      if (contexts.size === 0) session.frameContexts.delete(frameId);
    }
  } else if (method === "Runtime.executionContextsCleared") {
    if (source.sessionId) removeFrameContextsForSession(session, source.sessionId);
    else {
      for (const [frameId, contexts] of session.frameContexts) {
        for (const [contextId, context] of contexts) {
          if (!context.sessionId) contexts.delete(contextId);
        }
        if (contexts.size === 0) session.frameContexts.delete(frameId);
      }
    }
  }
}

function contextKey(sessionId, contextId) {
  return `${sessionId || "root"}:${String(contextId)}`;
}

function removeChildSessionTree(session, rootSessionId) {
  const pending = [rootSessionId];
  while (pending.length > 0) {
    const sessionId = pending.pop();
    for (const child of session.childSessions.values()) {
      if (child.parentSessionId === sessionId) pending.push(child.sessionId);
    }
    session.childSessions.delete(sessionId);
    removeFrameContextsForSession(session, sessionId);
  }
}

function removeFrameContextsForSession(session, sessionId) {
  for (const [frameId, contexts] of session.frameContexts) {
    for (const [contextId, context] of contexts) {
      if (context.sessionId === sessionId) contexts.delete(contextId);
    }
    if (contexts.size === 0) session.frameContexts.delete(frameId);
  }
}

function removeChildFrameContexts(session) {
  for (const [frameId, contexts] of session.frameContexts) {
    for (const [contextId, context] of contexts) {
      if (context.sessionId) contexts.delete(contextId);
    }
    if (contexts.size === 0) session.frameContexts.delete(frameId);
  }
}

function ensureBoundedJSON(value, maxBytes, label) {
  if (jsonByteLength(value) > maxBytes) {
    throw protocolError(ErrorCode.PAYLOAD_TOO_LARGE, `${label} exceeds the configured byte limit`);
  }
}

function jsonByteLength(value) {
  try {
    return new TextEncoder().encode(JSON.stringify(value ?? null)).byteLength;
  } catch {
    return Number.POSITIVE_INFINITY;
  }
}

function throwIfAborted(signal) {
  if (signal?.aborted) {
    throw protocolError(ErrorCode.CANCELLED, "The CDP operation was cancelled", true);
  }
}

function waitWithSignal(promise, signal) {
  if (!signal) return promise;
  throwIfAborted(signal);
  return new Promise((resolve, reject) => {
    const abort = () => {
      reject(protocolError(ErrorCode.CANCELLED, "The CDP operation was cancelled", true));
    };
    signal.addEventListener("abort", abort, { once: true });
    Promise.resolve(promise).then(
      (value) => {
        signal.removeEventListener("abort", abort);
        resolve(value);
      },
      (error) => {
        signal.removeEventListener("abort", abort);
        reject(error);
      },
    );
  });
}

function normalizeDebuggerError(error, phase) {
  if (error?.code && Object.values(ErrorCode).includes(error.code)) return error;
  const message = String(error?.message || error || "").toLowerCase();
  if (
    message.includes("another debugger") ||
    message.includes("already attached") ||
    message.includes("devtools")
  ) {
    return protocolError(
      ErrorCode.CAPABILITY_UNAVAILABLE,
      "The target is already controlled by DevTools or another debugger",
      false,
      { reason: "debugger_conflict" },
    );
  }
  if (
    message.includes("no tab with id") ||
    message.includes("tab was closed") ||
    message.includes("target closed")
  ) {
    return protocolError(ErrorCode.TAB_NOT_FOUND, "The CDP target is no longer available", true);
  }
  if (message.includes("not attached") || message.includes("debuggee")) {
    return protocolError(
      ErrorCode.BROWSER_DISCONNECTED,
      "The CDP session is no longer attached",
      true,
    );
  }
  if (message.includes("permission") || message.includes("debugger api is not allowed")) {
    return protocolError(
      ErrorCode.PERMISSION_REQUIRED,
      "Debug permission is required. Grant it from the extension settings page.",
    );
  }
  if (message.includes("cannot access") || message.includes("restricted")) {
    return protocolError(
      ErrorCode.RESTRICTED_URL,
      "The browser does not allow debugging this page",
    );
  }
  if (message.includes("target.setautoattach") || message.includes("sessionid")) {
    return protocolError(
      ErrorCode.CAPABILITY_UNAVAILABLE,
      "Child CDP targets are unavailable in this browser",
      false,
      { reason: "flat_sessions_unavailable" },
    );
  }
  return protocolError(ErrorCode.INTERNAL_ERROR, `The browser debugger ${phase} operation failed`);
}
