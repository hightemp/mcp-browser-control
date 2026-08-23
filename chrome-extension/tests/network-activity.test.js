import assert from "node:assert/strict";
import test from "node:test";

import { createNetworkActivityObserver } from "../src/network-activity.js";
import { ErrorCode } from "../src/protocol.js";

test("network activity observer waits for a quiet interval after requests finish", async () => {
  const before = fakeChromeEvent();
  const completed = fakeChromeEvent();
  const failed = fakeChromeEvent();
  const observer = createNetworkActivityObserver({
    webRequest: {
      onBeforeRequest: before,
      onCompleted: completed,
      onErrorOccurred: failed,
    },
  });
  assert.equal(observer.start(), true);
  before.emit({ tabId: 7, requestId: "request-1", type: "xmlhttprequest" });
  assert.equal(observer.activeRequestCount(7), 1);

  const waiting = observer.waitForIdle({
    tabId: 7,
    idleMs: 10,
    signal: new AbortController().signal,
  });
  completed.emit({ tabId: 7, requestId: "request-1", type: "xmlhttprequest" });
  const result = await waiting;

  assert.equal(result.condition, "networkIdle");
  assert.equal(result.matched, true);
  assert.equal(result.activeRequests, 0);
  assert.equal(result.elapsedMs >= 0, true);
});

test("network activity observer cancels without retaining subscribers", async () => {
  const before = fakeChromeEvent();
  const completed = fakeChromeEvent();
  const failed = fakeChromeEvent();
  const observer = createNetworkActivityObserver({
    webRequest: {
      onBeforeRequest: before,
      onCompleted: completed,
      onErrorOccurred: failed,
    },
  });
  assert.equal(observer.start(), true);
  before.emit({ tabId: 4, requestId: "request-1", type: "fetch" });
  const controller = new AbortController();
  const waiting = observer.waitForIdle({
    tabId: 4,
    idleMs: 100,
    signal: controller.signal,
  });
  controller.abort();

  await assert.rejects(waiting, (error) => error.code === ErrorCode.CANCELLED);
  completed.emit({ tabId: 4, requestId: "request-1", type: "fetch" });
  assert.equal(observer.activeRequestCount(4), 0);
});

function fakeChromeEvent() {
  const listeners = new Set();
  return {
    addListener(listener) {
      listeners.add(listener);
    },
    emit(details) {
      for (const listener of [...listeners]) listener(details);
    },
  };
}
