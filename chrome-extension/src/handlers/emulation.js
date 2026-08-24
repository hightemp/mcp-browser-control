import { ErrorCode, assertFreshDocument, mapChromeError, protocolError } from "../protocol.js";

const DEFAULT_COMMAND_TIMEOUT_MS = 30_000;
const CDP_COMMANDS = Object.freeze([
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
const RESET_OPERATIONS = Object.freeze([
  Object.freeze(["Emulation.clearDeviceMetricsOverride", Object.freeze({})]),
  Object.freeze(["Emulation.setTouchEmulationEnabled", Object.freeze({ enabled: false })]),
  Object.freeze([
    "Network.emulateNetworkConditions",
    Object.freeze({
      offline: false,
      latency: 0,
      downloadThroughput: -1,
      uploadThroughput: -1,
    }),
  ]),
  Object.freeze(["Emulation.setUserAgentOverride", Object.freeze({ userAgent: "" })]),
  Object.freeze(["Emulation.setLocaleOverride", Object.freeze({ locale: "" })]),
  Object.freeze(["Emulation.setTimezoneOverride", Object.freeze({ timezoneId: "" })]),
  Object.freeze(["Emulation.clearGeolocationOverride", Object.freeze({})]),
  Object.freeze([
    "Emulation.setEmulatedMedia",
    Object.freeze({ media: "", features: Object.freeze([]) }),
  ]),
]);

export function createEmulationHandlers(chromeAPI, { cdpSessions } = {}) {
  const states = new Map();
  const queues = new Map();

  async function execute(request, parentSignal) {
    const timeoutMs = request.timeoutMs || DEFAULT_COMMAND_TIMEOUT_MS;
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
        if (request.command === "emulation.reset") {
          return serializeTab(queues, tab.id, async () => {
            throwIfCancelled(signal);
            await resetEmulation(states, tab.id, signal);
            return emulationResult(undefined, tab.id, "");
          });
        }
        await assertPageAccess(chromeAPI, tab);
        const document = await resolveRootDocument(chromeAPI, request, tab.id);
        throwIfCancelled(signal);

        return serializeTab(queues, tab.id, async () => {
          throwIfCancelled(signal);
          const currentTab = await resolveTab(chromeAPI, tab.id);
          await assertPageAccess(chromeAPI, currentTab);
          const currentDocument = await resolveRootDocument(chromeAPI, request, tab.id);
          assertFreshDocument(document.documentId, currentDocument.documentId);
          throwIfCancelled(signal);

          switch (request.command) {
            case "emulation.set":
              return replaceEmulation({
                chromeAPI,
                cdpSessions,
                states,
                tabId: tab.id,
                documentId: document.documentId,
                settings: request.params,
                signal,
              });
            case "emulation.get":
              return emulationResult(states.get(tab.id), tab.id, document.documentId);
            default:
              throw protocolError(ErrorCode.INVALID_COMMAND, "Unknown emulation command");
          }
        });
      },
      parentSignal,
      timeoutMs,
    );
  }

  return { set: execute, get: execute, reset: execute };
}

async function replaceEmulation({
  chromeAPI,
  cdpSessions,
  states,
  tabId,
  documentId,
  settings,
  signal,
}) {
  let state = states.get(tabId);
  if (!state) {
    state = {
      tabId,
      settings: null,
      lease: null,
      active: true,
    };
    try {
      state.lease = await cdpSessions.acquire(
        { tabId },
        {
          consumerId: `emulation:${tabId}`,
          domains: ["Emulation", "Network"],
          commands: CDP_COMMANDS,
          signal,
          onDetach: () => {
            state.active = false;
            state.settings = null;
            if (states.get(tabId) === state) states.delete(tabId);
          },
        },
      );
      states.set(tabId, state);
    } catch (error) {
      state.active = false;
      throw error;
    }
  }

  try {
    await resetLease(state.lease, signal);
    await applySettings(state.lease, settings, signal);
    const finalTab = await resolveTab(chromeAPI, tabId);
    await assertPageAccess(chromeAPI, finalTab);
    const finalDocument = await currentRootDocument(chromeAPI, tabId);
    assertFreshDocument(documentId, finalDocument.documentId);
    throwIfCancelled(signal);
    state.settings = cloneSettings(settings);
    return emulationResult(state, tabId, documentId);
  } catch (error) {
    await bestEffortReset(state.lease);
    await releaseState(states, state);
    if (signal.aborted) throw signal.reason;
    throw error;
  }
}

async function resetEmulation(states, tabId, signal) {
  const state = states.get(tabId);
  if (!state) return;
  let resetError;
  try {
    await resetLease(state.lease, signal);
  } catch (error) {
    resetError = error;
  } finally {
    await releaseState(states, state);
  }
  if (signal.aborted) throw signal.reason;
  if (resetError) throw resetError;
}

async function resetLease(lease, signal) {
  let firstError;
  for (const [method, params] of RESET_OPERATIONS) {
    try {
      await lease.sendCommand(method, params, { signal });
    } catch (error) {
      firstError ||= error;
    }
  }
  if (firstError) throw firstError;
}

async function bestEffortReset(lease) {
  if (!lease) return;
  try {
    await resetLease(lease, undefined);
  } catch {
    // Releasing the managed debugger lease remains the final reset boundary.
  }
}

async function applySettings(lease, settings, signal) {
  if (settings.viewport) {
    const params = {
      width: settings.viewport.width,
      height: settings.viewport.height,
      deviceScaleFactor: settings.viewport.deviceScaleFactor,
      mobile: settings.viewport.mobile,
    };
    if (settings.viewport.orientation) {
      params.screenOrientation = {
        type: settings.viewport.orientation,
        angle: orientationAngle(settings.viewport.orientation),
      };
    }
    await lease.sendCommand("Emulation.setDeviceMetricsOverride", params, { signal });
  }
  if (settings.touch) {
    await lease.sendCommand(
      "Emulation.setTouchEmulationEnabled",
      {
        enabled: settings.touch.enabled,
        ...(settings.touch.maxTouchPoints !== undefined
          ? { maxTouchPoints: settings.touch.maxTouchPoints }
          : {}),
      },
      { signal },
    );
  }
  if (settings.network) {
    await lease.sendCommand(
      "Network.emulateNetworkConditions",
      {
        offline: settings.network.offline === true,
        latency: settings.network.latencyMs ?? 0,
        downloadThroughput: kbpsToBytes(settings.network.downloadKbps),
        uploadThroughput: kbpsToBytes(settings.network.uploadKbps),
        ...(settings.network.connectionType
          ? { connectionType: settings.network.connectionType }
          : {}),
      },
      { signal },
    );
  }
  if (settings.userAgent) {
    await lease.sendCommand(
      "Emulation.setUserAgentOverride",
      {
        userAgent: settings.userAgent.value,
        ...(settings.userAgent.acceptLanguage
          ? { acceptLanguage: settings.userAgent.acceptLanguage }
          : {}),
        ...(settings.userAgent.platform ? { platform: settings.userAgent.platform } : {}),
      },
      { signal },
    );
  }
  if (settings.locale !== undefined) {
    await lease.sendCommand("Emulation.setLocaleOverride", { locale: settings.locale }, { signal });
  }
  if (settings.timezoneId !== undefined) {
    await lease.sendCommand(
      "Emulation.setTimezoneOverride",
      { timezoneId: settings.timezoneId },
      { signal },
    );
  }
  if (settings.geolocation) {
    await lease.sendCommand("Emulation.setGeolocationOverride", settings.geolocation, { signal });
  }
  if (settings.media) {
    await lease.sendCommand(
      "Emulation.setEmulatedMedia",
      {
        media: settings.media.type || "",
        features: mediaFeatures(settings.media),
      },
      { signal },
    );
  }
}

function mediaFeatures(media) {
  return [
    ["prefers-color-scheme", media.colorScheme],
    ["prefers-reduced-motion", media.reducedMotion],
    ["forced-colors", media.forcedColors],
    ["prefers-contrast", media.contrast],
  ]
    .filter(([, value]) => Boolean(value))
    .map(([name, value]) => ({ name, value }));
}

function emulationResult(state, tabId, documentId) {
  if (!state?.active || !state.settings) {
    return {
      active: false,
      tabId,
      documentId,
      applied: [],
      resetOnDetach: true,
      warnings: [],
    };
  }
  return {
    active: true,
    tabId,
    documentId,
    settings: cloneSettings(state.settings),
    applied: Object.keys(state.settings).sort(),
    resetOnDetach: true,
    warnings: ["Emulation is tab-scoped and persists across navigation until reset or detach"],
  };
}

async function releaseState(states, state) {
  if (!state.active) return;
  state.active = false;
  state.settings = null;
  if (states.get(state.tabId) === state) states.delete(state.tabId);
  await state.lease?.release();
}

function cloneSettings(settings) {
  return JSON.parse(JSON.stringify(settings));
}

function orientationAngle(orientation) {
  return {
    portraitPrimary: 0,
    landscapePrimary: 90,
    portraitSecondary: 180,
    landscapeSecondary: 270,
  }[orientation];
}

function kbpsToBytes(value) {
  return value === undefined ? -1 : (value * 1_000) / 8;
}

function serializeTab(queues, tabId, operation) {
  const previous = queues.get(tabId) || Promise.resolve();
  const current = previous.catch(() => undefined).then(operation);
  queues.set(tabId, current);
  return current.finally(() => {
    if (queues.get(tabId) === current) queues.delete(tabId);
  });
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
