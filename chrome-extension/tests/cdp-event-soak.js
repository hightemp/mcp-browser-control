import assert from "node:assert/strict";
import test from "node:test";
import { performance } from "node:perf_hooks";

import { createCDPSessionManager } from "../src/cdp-session-manager.js";

const duration = parseDuration(process.env.MCP_BROWSER_SOAK_DURATION || "8h");
const burstInterval = parseDuration(process.env.MCP_BROWSER_SOAK_EVENT_INTERVAL || "250ms");
const eventsPerBurst = parsePositiveInteger(
  process.env.MCP_BROWSER_SOAK_EVENTS_PER_BURST || "128",
  "MCP_BROWSER_SOAK_EVENTS_PER_BURST",
);
const queueLimit = 32;

test("bounded CDP event queue survives the configured soak duration", async () => {
  const chromeAPI = createFakeChrome();
  const deliveryLatencies = createLatencyHistogram();
  let deliveredEvents = 0;
  let reportedDroppedEvents = 0;
  let sentinelDelivered;
  const sentinel = new Promise((resolve) => {
    sentinelDelivered = resolve;
  });
  const manager = createCDPSessionManager(chromeAPI, {
    browserVersion: "125",
    limits: {
      maxEventsPerConsumer: queueLimit,
      maxEventBytes: 4_096,
      maxQueuedEventBytes: queueLimit * 4_096,
    },
  });
  const lease = await manager.acquire(
    { tabId: 1 },
    {
      consumerId: "event-soak",
      domains: ["Network"],
      events: ["Network.requestWillBeSent"],
      onEvent: async (event) => {
        deliveredEvents += 1;
        reportedDroppedEvents += event.droppedBefore || 0;
        deliveryLatencies.observe(Math.max(0, performance.now() - event.params.emittedAt));
        if (event.params.sentinel === true) sentinelDelivered();
        await delay(2);
      },
    },
  );

  if (global.gc) global.gc();
  const startedAt = performance.now();
  const initialHeapBytes = process.memoryUsage().heapUsed;
  let peakHeapBytes = initialHeapBytes;
  let emittedEvents = 0;
  let peakQueuedEvents = 0;
  let sequence = 0;
  let nextProgressAt = startedAt + 15 * 60 * 1_000;

  while (performance.now() - startedAt < duration) {
    for (let index = 0; index < eventsPerBurst; index += 1) {
      emittedEvents += 1;
      sequence += 1;
      chromeAPI.debugger.onEvent.emit({ tabId: 1 }, "Network.requestWillBeSent", {
        requestId: String(sequence),
        emittedAt: performance.now(),
      });
    }
    const stats = manager.stats().sessions[0];
    peakQueuedEvents = Math.max(peakQueuedEvents, stats.queuedEventCount);
    peakHeapBytes = Math.max(peakHeapBytes, process.memoryUsage().heapUsed);
    assert.ok(stats.queuedEventCount <= queueLimit, "the event queue exceeded its item limit");
    assert.ok(
      stats.queuedEventBytes <= queueLimit * 4_096,
      "the event queue exceeded its byte limit",
    );
    if (performance.now() >= nextProgressAt) {
      console.log(
        `CDP soak progress: elapsedMs=${Math.round(performance.now() - startedAt)} emittedEvents=${emittedEvents}`,
      );
      nextProgressAt += 15 * 60 * 1_000;
    }
    await delay(burstInterval);
  }

  emittedEvents += 1;
  chromeAPI.debugger.onEvent.emit({ tabId: 1 }, "Network.requestWillBeSent", {
    requestId: "sentinel",
    emittedAt: performance.now(),
    sentinel: true,
  });
  await withTimeout(sentinel, 10_000, "timed out draining the CDP event queue");

  const finalStats = manager.stats().sessions[0];
  const droppedEvents = finalStats.droppedEventCount;
  assert.equal(finalStats.queuedEventCount, 0);
  assert.equal(droppedEvents, reportedDroppedEvents);
  assert.equal(emittedEvents, deliveredEvents + droppedEvents);
  assert.ok(droppedEvents > 0, "the soak did not exercise event backpressure");

  await lease.release();
  await manager.dispose();
  if (global.gc) global.gc();
  const finalHeapBytes = process.memoryUsage().heapUsed;
  const retainedHeapGrowthBytes = Math.max(0, finalHeapBytes - initialHeapBytes);
  const report = {
    component: "extension_cdp_events",
    durationMs: Math.round(performance.now() - startedAt),
    emittedEvents,
    deliveredEvents,
    droppedEvents,
    peakQueuedEvents,
    eventDeliveryP95Ms: deliveryLatencies.percentile(95),
    initialHeapBytes,
    peakHeapBytes,
    finalHeapBytes,
    retainedHeapGrowthBytes,
  };
  console.log(`SOAK_REPORT ${JSON.stringify(report)}`);
  assert.ok(retainedHeapGrowthBytes <= 32 * 1024 * 1024, "retained heap grew by more than 32 MiB");
  assert.equal(manager.stats().sessionCount, 0);
});

function createFakeChrome() {
  return {
    debugger: {
      onEvent: createEvent(),
      onDetach: createEvent(),
      async attach() {},
      async detach() {},
      async sendCommand() {
        return {};
      },
    },
  };
}

function createEvent() {
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

function parseDuration(value) {
  const match = String(value)
    .trim()
    .match(/^(\d+(?:\.\d+)?)(ms|s|m|h)$/);
  if (!match) throw new Error(`invalid soak duration: ${value}`);
  const factors = { ms: 1, s: 1_000, m: 60_000, h: 3_600_000 };
  const milliseconds = Number(match[1]) * factors[match[2]];
  if (!Number.isFinite(milliseconds) || milliseconds <= 0) {
    throw new Error(`invalid soak duration: ${value}`);
  }
  return milliseconds;
}

function parsePositiveInteger(value, name) {
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed <= 0) throw new Error(`${name} must be positive`);
  return parsed;
}

function createLatencyHistogram() {
  const buckets = new Uint32Array(60_001);
  let count = 0;
  return {
    observe(milliseconds) {
      const bucket = Math.min(buckets.length - 1, Math.max(0, Math.ceil(milliseconds)));
      buckets[bucket] += 1;
      count += 1;
    },
    percentile(requestedPercentile) {
      if (count === 0) return 0;
      const wanted = Math.ceil((count * requestedPercentile) / 100);
      let observed = 0;
      for (let index = 0; index < buckets.length; index += 1) {
        observed += buckets[index];
        if (observed >= wanted) return index;
      }
      return buckets.length - 1;
    },
  };
}

function delay(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

async function withTimeout(promise, milliseconds, message) {
  let timer;
  try {
    return await Promise.race([
      promise,
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error(message)), milliseconds);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}
