import { ErrorCode, protocolError } from "./protocol.js";

export function createNetworkActivityObserver(chromeAPI, now = () => Date.now()) {
  const activeByTab = new Map();
  const lastActivityByTab = new Map();
  const subscribersByTab = new Map();
  const webRequest = chromeAPI.webRequest;
  const available = Boolean(
    webRequest?.onBeforeRequest?.addListener &&
    webRequest?.onCompleted?.addListener &&
    webRequest?.onErrorOccurred?.addListener,
  );
  let started = false;

  function start() {
    if (!available || started) return available;
    try {
      webRequest.onBeforeRequest.addListener(onStarted, {
        urls: ["<all_urls>"],
      });
      webRequest.onCompleted.addListener(onFinished, { urls: ["<all_urls>"] });
      webRequest.onErrorOccurred.addListener(onFinished, {
        urls: ["<all_urls>"],
      });
      started = true;
      return true;
    } catch {
      return false;
    }
  }

  function onStarted(details) {
    if (!tracked(details)) return;
    const requests = activeByTab.get(details.tabId) || new Set();
    requests.add(details.requestId);
    activeByTab.set(details.tabId, requests);
    recordActivity(details.tabId);
  }

  function onFinished(details) {
    if (!tracked(details)) return;
    const requests = activeByTab.get(details.tabId);
    requests?.delete(details.requestId);
    if (requests?.size === 0) activeByTab.delete(details.tabId);
    recordActivity(details.tabId);
  }

  function tracked(details) {
    return Number.isInteger(details?.tabId) && details.tabId >= 0 && details.type !== "websocket";
  }

  function recordActivity(tabId) {
    lastActivityByTab.set(tabId, now());
    for (const subscriber of subscribersByTab.get(tabId) || []) subscriber();
  }

  function waitForIdle({ tabId, idleMs, signal }) {
    if (!start()) {
      return Promise.reject(
        protocolError(
          ErrorCode.CAPABILITY_UNAVAILABLE,
          "Network activity observation is unavailable in this browser",
        ),
      );
    }
    if (signal.aborted) {
      return Promise.reject(abortReason(signal));
    }
    const startedAt = now();
    let idleTimer = null;
    let settled = false;
    let resolvePromise;
    let rejectPromise;

    const cleanup = () => {
      if (idleTimer !== null) clearTimeout(idleTimer);
      const subscribers = subscribersByTab.get(tabId);
      subscribers?.delete(onActivity);
      if (subscribers?.size === 0) subscribersByTab.delete(tabId);
      signal.removeEventListener("abort", onAbort);
    };
    const finish = (operation) => {
      if (settled) return;
      settled = true;
      cleanup();
      operation();
    };
    const evaluate = () => {
      if (idleTimer !== null) {
        clearTimeout(idleTimer);
        idleTimer = null;
      }
      if ((activeByTab.get(tabId)?.size || 0) !== 0) return;
      const lastActivity = Math.max(lastActivityByTab.get(tabId) || startedAt, startedAt);
      const remaining = Math.max(0, idleMs - (now() - lastActivity));
      idleTimer = setTimeout(
        () =>
          finish(() =>
            resolvePromise({
              condition: "networkIdle",
              matched: true,
              idleMs,
              activeRequests: 0,
              elapsedMs: Math.max(0, now() - startedAt),
              mode: "event",
              timestamp: new Date().toISOString(),
            }),
          ),
        remaining,
      );
    };
    const onActivity = () => evaluate();
    const onAbort = () => finish(() => rejectPromise(abortReason(signal)));
    const subscribers = subscribersByTab.get(tabId) || new Set();
    subscribers.add(onActivity);
    subscribersByTab.set(tabId, subscribers);
    signal.addEventListener("abort", onAbort, { once: true });

    const promise = new Promise((resolve, reject) => {
      resolvePromise = resolve;
      rejectPromise = reject;
    });
    evaluate();
    return promise;
  }

  function reset() {
    const tabIds = new Set([...activeByTab.keys(), ...subscribersByTab.keys()]);
    activeByTab.clear();
    lastActivityByTab.clear();
    for (const tabId of tabIds) {
      for (const subscriber of subscribersByTab.get(tabId) || []) subscriber();
    }
  }

  return Object.freeze({
    available,
    start,
    reset,
    waitForIdle,
    activeRequestCount: (tabId) => activeByTab.get(tabId)?.size || 0,
  });
}

function abortReason(signal) {
  return typeof signal.reason?.code === "string"
    ? signal.reason
    : protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
}
