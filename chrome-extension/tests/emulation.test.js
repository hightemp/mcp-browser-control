import assert from "node:assert/strict";
import test from "node:test";

import { createEmulationHandlers } from "../src/handlers/emulation.js";

const target = { tabId: 7, frameId: 0, documentId: "document-1" };
const settings = {
  viewport: {
    width: 390,
    height: 844,
    deviceScaleFactor: 3,
    mobile: true,
    orientation: "landscapePrimary",
  },
  touch: { enabled: true, maxTouchPoints: 5 },
  network: {
    offline: false,
    latencyMs: 80,
    downloadKbps: 2_000,
    uploadKbps: 1_000,
    connectionType: "cellular4g",
  },
  userAgent: {
    value: "ExampleBrowser/1.0",
    acceptLanguage: "en-US",
    platform: "Linux armv8l",
  },
  locale: "en_US",
  timezoneId: "America/New_York",
  geolocation: {
    latitude: 40.7,
    longitude: -74,
    accuracy: 20,
    altitude: 10,
    heading: 180,
    speed: 2,
  },
  media: {
    type: "screen",
    colorScheme: "dark",
    reducedMotion: "reduce",
    forcedColors: "none",
    contrast: "more",
  },
};

test("emulation replaces state through one exact managed lease and resets it", async () => {
  const calls = [];
  let leaseOptions;
  let released = 0;
  let acquired = 0;
  const lease = {
    async sendCommand(method, params) {
      calls.push([method, params]);
      return {};
    },
    async release() {
      released += 1;
    },
  };
  const handlers = createEmulationHandlers(createChromeAPI(), {
    cdpSessions: {
      async acquire(debuggee, options) {
        acquired += 1;
        assert.deepEqual(debuggee, { tabId: 7 });
        leaseOptions = options;
        return lease;
      },
    },
  });
  const signal = new AbortController().signal;

  const setResult = await handlers.set(
    { requestId: "set-1", command: "emulation.set", target, params: settings },
    signal,
  );
  assert.equal(setResult.active, true);
  assert.equal(setResult.tabId, 7);
  assert.equal(setResult.documentId, "document-1");
  assert.equal(setResult.resetOnDetach, true);
  assert.deepEqual(setResult.settings, settings);
  assert.deepEqual(setResult.applied, [
    "geolocation",
    "locale",
    "media",
    "network",
    "timezoneId",
    "touch",
    "userAgent",
    "viewport",
  ]);
  assert.equal(acquired, 1);
  assert.deepEqual(leaseOptions.domains, ["Emulation", "Network"]);
  assert.deepEqual(leaseOptions.commands, [
    "Emulation.clearDeviceMetricsOverride",
    "Emulation.clearGeolocationOverride",
    "Emulation.setDeviceMetricsOverride",
    "Emulation.setEmulatedMedia",
    "Emulation.setGeolocationOverride",
    "Emulation.setLocaleOverride",
    "Emulation.setTimezoneOverride",
    "Emulation.setTouchEmulationEnabled",
    "Emulation.setUserAgentOverride",
    "Network.emulateNetworkConditions",
  ]);
  assert.equal(leaseOptions.events, undefined);
  assert.deepEqual(
    calls.slice(0, 8).map(([method]) => method),
    [
      "Emulation.clearDeviceMetricsOverride",
      "Emulation.setTouchEmulationEnabled",
      "Network.emulateNetworkConditions",
      "Emulation.setUserAgentOverride",
      "Emulation.setLocaleOverride",
      "Emulation.setTimezoneOverride",
      "Emulation.clearGeolocationOverride",
      "Emulation.setEmulatedMedia",
    ],
  );
  assert.deepEqual(calls[8], [
    "Emulation.setDeviceMetricsOverride",
    {
      width: 390,
      height: 844,
      deviceScaleFactor: 3,
      mobile: true,
      screenOrientation: { type: "landscapePrimary", angle: 90 },
    },
  ]);
  assert.deepEqual(calls[10], [
    "Network.emulateNetworkConditions",
    {
      offline: false,
      latency: 80,
      downloadThroughput: 250_000,
      uploadThroughput: 125_000,
      connectionType: "cellular4g",
    },
  ]);
  assert.deepEqual(calls.at(-1), [
    "Emulation.setEmulatedMedia",
    {
      media: "screen",
      features: [
        { name: "prefers-color-scheme", value: "dark" },
        { name: "prefers-reduced-motion", value: "reduce" },
        { name: "forced-colors", value: "none" },
        { name: "prefers-contrast", value: "more" },
      ],
    },
  ]);

  const getResult = await handlers.get(
    { requestId: "get-1", command: "emulation.get", target, params: {} },
    signal,
  );
  assert.equal(getResult.active, true);
  assert.deepEqual(getResult.settings, settings);
  assert.equal(acquired, 1);

  const callCountBeforeReset = calls.length;
  const resetResult = await handlers.reset(
    { requestId: "reset-1", command: "emulation.reset", target, params: {} },
    signal,
  );
  assert.equal(resetResult.active, false);
  assert.equal(resetResult.documentId, "");
  assert.equal(calls.length, callCountBeforeReset + 8);
  assert.equal(released, 1);
});

test("emulation state is forgotten on external detach", async () => {
  let leaseOptions;
  const handlers = createEmulationHandlers(createChromeAPI(), {
    cdpSessions: {
      async acquire(_debuggee, options) {
        leaseOptions = options;
        return { sendCommand: async () => ({}), release: async () => undefined };
      },
    },
  });
  const signal = new AbortController().signal;
  await handlers.set(
    {
      requestId: "set-1",
      command: "emulation.set",
      target,
      params: { timezoneId: "UTC" },
    },
    signal,
  );

  leaseOptions.onDetach({ tabId: 7, reason: "permission_revoked" });
  const result = await handlers.get(
    { requestId: "get-1", command: "emulation.get", target, params: {} },
    signal,
  );
  assert.equal(result.active, false);
  assert.deepEqual(result.applied, []);
});

test("emulation serializes replacement operations for the same tab", async () => {
  const calls = [];
  let acquired = 0;
  let startFirstApply;
  let releaseFirstApply;
  const firstApplyStarted = new Promise((resolve) => {
    startFirstApply = resolve;
  });
  const firstApplyGate = new Promise((resolve) => {
    releaseFirstApply = resolve;
  });
  const handlers = createEmulationHandlers(createChromeAPI(), {
    cdpSessions: {
      async acquire() {
        acquired += 1;
        return {
          async sendCommand(method, params) {
            calls.push([method, params]);
            if (method === "Emulation.setTimezoneOverride" && params.timezoneId === "UTC") {
              startFirstApply();
              await firstApplyGate;
            }
            return {};
          },
          release: async () => undefined,
        };
      },
    },
  });
  const signal = new AbortController().signal;
  const first = handlers.set(
    {
      requestId: "set-1",
      command: "emulation.set",
      target,
      params: { timezoneId: "UTC" },
    },
    signal,
  );
  await firstApplyStarted;
  const second = handlers.set(
    {
      requestId: "set-2",
      command: "emulation.set",
      target,
      params: { locale: "fr_FR" },
    },
    signal,
  );
  await Promise.resolve();
  assert.equal(calls.length, 9);
  releaseFirstApply();
  await Promise.all([first, second]);

  assert.equal(acquired, 1);
  assert.equal(calls.length, 18);
  assert.equal(calls[9][0], "Emulation.clearDeviceMetricsOverride");
  const current = await handlers.get(
    { requestId: "get-1", command: "emulation.get", target, params: {} },
    signal,
  );
  assert.deepEqual(current.settings, { locale: "fr_FR" });
});

test("emulation rolls back and releases its lease after a partial apply failure", async () => {
  const calls = [];
  let released = 0;
  const handlers = createEmulationHandlers(createChromeAPI(), {
    cdpSessions: {
      async acquire() {
        return {
          async sendCommand(method, params) {
            calls.push([method, params]);
            if (method === "Emulation.setTimezoneOverride" && params.timezoneId === "UTC") {
              const error = new Error("Timezone rejected");
              error.code = "INVALID_MESSAGE";
              throw error;
            }
            return {};
          },
          async release() {
            released += 1;
          },
        };
      },
    },
  });

  await assert.rejects(
    handlers.set(
      {
        requestId: "set-1",
        command: "emulation.set",
        target,
        params: { viewport: settings.viewport, timezoneId: "UTC" },
      },
      new AbortController().signal,
    ),
    (error) => error.code === "INVALID_MESSAGE",
  );
  assert.equal(released, 1);
  assert.ok(
    calls.filter(
      ([method, params]) => method === "Emulation.setTimezoneOverride" && params.timezoneId === "",
    ).length >= 2,
  );
  assert.deepEqual(calls.at(-1), ["Emulation.setEmulatedMedia", { media: "", features: [] }]);
});

test("emulation rejects missing Debug permission before acquiring a lease", async () => {
  let acquired = false;
  const handlers = createEmulationHandlers(createChromeAPI({ debuggerGranted: false }), {
    cdpSessions: {
      async acquire() {
        acquired = true;
      },
    },
  });

  await assert.rejects(
    handlers.set(
      {
        requestId: "set-1",
        command: "emulation.set",
        target,
        params: { timezoneId: "UTC" },
      },
      new AbortController().signal,
    ),
    (error) => error.code === "PERMISSION_REQUIRED",
  );
  assert.equal(acquired, false);
});

test("emulation reset remains available without target-origin access", async () => {
  const handlers = createEmulationHandlers(createChromeAPI({ siteGranted: false }), {
    cdpSessions: { acquire: async () => assert.fail("reset must not acquire an empty state") },
  });
  const result = await handlers.reset(
    {
      requestId: "reset-1",
      command: "emulation.reset",
      target: { tabId: 7, frameId: 0 },
      params: {},
    },
    new AbortController().signal,
  );
  assert.equal(result.active, false);
});

function createChromeAPI({ debuggerGranted = true, siteGranted = true } = {}) {
  return {
    tabs: {
      get: async (tabId) => ({ id: tabId, url: "https://example.com/" }),
      query: async () => [],
    },
    permissions: {
      contains: async (query) =>
        query.permissions?.includes("debugger") ? debuggerGranted : siteGranted,
    },
    webNavigation: {
      getFrame: async () => ({ documentId: "document-1", url: "https://example.com/" }),
    },
  };
}
