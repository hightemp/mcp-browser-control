import {
  ErrorCode,
  MessageType,
  createMessage,
  normalizeError,
  normalizePairingCode,
  protocolError,
  validateIncomingMessage,
  validateServerEndpoint,
} from "./protocol.js";
import {
  getStoredIdentity,
  initializeStoredState,
  resetStoredIdentity,
} from "./identity.js";
import { badgeForStatus, permissionProfilesFor } from "./status.js";
import { detectCapabilities } from "./capabilities.js";
import { CommandRouter } from "./command-router.js";
import { createBrowserHandlers } from "./handlers/browser.js";
import { createPageHandlers } from "./handlers/page.js";
import { createTabHandlers } from "./handlers/tabs.js";
import { createTabGroupHandlers } from "./handlers/tab-groups.js";
import { createSessionHandlers } from "./handlers/sessions.js";
import { createWindowHandlers } from "./handlers/windows.js";

const DEFAULT_SETTINGS = Object.freeze({
  endpoint: "ws://127.0.0.1:8090/ws",
  displayName: "",
  autoConnect: true,
  featureFlags: Object.freeze({
    pageAutomation: true,
  }),
});
const RECONNECT_ALARM = "mcp-browser-control-reconnect";
const KEEPALIVE_INTERVAL_MS = 20_000;
const MAX_RECONNECT_DELAY_MS = 30_000;

let socket = null;
let connectionId = "";
let status = "disconnected";
let reconnectAttempts = 0;
let reconnectTimer = null;
let keepaliveTimer = null;
let pendingPairingCode = "";
let authenticationBlocked = false;
let pendingRevocation = null;
let lastError = "";
let lastPingSentAt = 0;
const commandRouter = new CommandRouter({
  getBrowserId: getIdentity,
  getCapabilities: getCurrentCapabilities,
  handlers: {
    browser: createBrowserHandlers(),
    page: createPageHandlers(chrome),
    sessions: createSessionHandlers(chrome),
    tabs: createTabHandlers(chrome),
    tabGroups: createTabGroupHandlers(chrome),
    windows: createWindowHandlers(chrome),
  },
});

void connect();

chrome.runtime.onStartup.addListener(() => {
  void connect();
});

chrome.runtime.onInstalled.addListener(() => {
  void initializeSettings().then(connect);
});

chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === RECONNECT_ALARM) {
    void connect();
  }
});

chrome.permissions.onAdded.addListener(() => {
  void sendCapabilitiesChanged();
});

chrome.permissions.onRemoved.addListener(() => {
  void sendCapabilitiesChanged();
});

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  void handleRuntimeMessage(message)
    .then(sendResponse)
    .catch((error) => sendResponse({ success: false, error: normalizeError(error) }));
  return true;
});

async function handleRuntimeMessage(message) {
  switch (message?.type) {
    case "GET_STATUS":
      return { success: true, data: await getStatus() };
    case "SAVE_SETTINGS": {
      const endpoint = validateServerEndpoint(message.settings?.endpoint);
      const currentSettings = await getSettings();
      const settings = {
        endpoint,
        displayName: String(message.settings?.displayName || "").trim(),
        autoConnect: Boolean(message.settings?.autoConnect),
        featureFlags: {
          pageAutomation: message.settings?.featureFlags?.pageAutomation
            ?? currentSettings.featureFlags.pageAutomation,
        },
      };
      await chrome.storage.local.set({ settings });
      await disconnect(false);
      if (settings.autoConnect) {
        await connect();
      }
      return { success: true, data: await getStatus() };
    }
    case "CONNECT":
	  authenticationBlocked = false;
      await chrome.storage.local.set({
        settings: { ...(await getSettings()), autoConnect: true },
      });
      await connect();
      return { success: true, data: await getStatus() };
    case "DISCONNECT":
      await chrome.storage.local.set({
        settings: { ...(await getSettings()), autoConnect: false },
      });
      await disconnect(true);
      return { success: true, data: await getStatus() };
    case "PAIR": {
      pendingPairingCode = normalizePairingCode(message.pairingCode);
      authenticationBlocked = false;
      await chrome.storage.local.remove("credential");
      await chrome.storage.local.set({
        settings: { ...(await getSettings()), autoConnect: true },
      });
      await disconnect(false);
      await connect();
      return { success: true, data: await getStatus() };
    }
    case "REVOKE_PAIRING": {
      await requestPairingRevocation();
      await chrome.storage.local.remove("credential");
      pendingPairingCode = "";
      authenticationBlocked = true;
      await disconnect(true, "pairing_required");
      return { success: true, data: await getStatus() };
    }
    case "RESET_IDENTITY": {
      if (message.confirm !== true) {
        throw protocolError(
          ErrorCode.CONFIRMATION_REQUIRED,
          "Resetting browser identity requires explicit confirmation",
        );
      }
      await disconnect(true, "pairing_required");
      pendingPairingCode = "";
      authenticationBlocked = true;
      await resetStoredIdentity(chrome.storage.local, true);
      return { success: true, data: await getStatus() };
    }
    default:
      throw protocolError(ErrorCode.INVALID_COMMAND, "Unknown extension UI command");
  }
}

async function initializeSettings() {
  await initializeStoredState(chrome.storage.local, DEFAULT_SETTINGS);
}

async function getIdentity() {
  return getStoredIdentity(chrome.storage.local, DEFAULT_SETTINGS);
}

async function getSettings() {
  await initializeSettings();
  const { settings } = await chrome.storage.local.get("settings");
  return {
    ...DEFAULT_SETTINGS,
    ...settings,
    featureFlags: {
      ...DEFAULT_SETTINGS.featureFlags,
      ...(settings?.featureFlags || {}),
    },
  };
}

async function connect() {
  if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) {
    return;
  }

  const settings = await getSettings();
  if (!settings.autoConnect) {
    await updateStatus("disconnected");
    return;
  }
	if (authenticationBlocked) {
	  await updateStatus("pairing_required");
	  return;
	}

  let endpoint;
  try {
    endpoint = validateServerEndpoint(settings.endpoint);
  } catch (error) {
    await updateStatus("error", error.message);
    return;
  }

  clearReconnectTimer();
  await updateStatus("connecting");
  const currentSocket = new WebSocket(endpoint);
  let incomingMessages = Promise.resolve();
  socket = currentSocket;

  currentSocket.addEventListener("open", () => {
    void sendHello(currentSocket);
  });
  currentSocket.addEventListener("message", (event) => {
	 incomingMessages = incomingMessages
	   .then(() => handleSocketMessage(currentSocket, event.data))
	   .catch(async (error) => {
	     await updateStatus("error", error.message || "Failed to process a server message");
	     currentSocket.close(1011, "Message processing failed");
	   });
  });
  currentSocket.addEventListener("close", () => {
	void incomingMessages.then(() => handleSocketClose(currentSocket));
  });
  currentSocket.addEventListener("error", () => {
    if (socket === currentSocket) {
      void updateStatus("error", "WebSocket connection failed");
    }
  });
}

async function handleSocketClose(currentSocket) {
	if (socket !== currentSocket) {
	  return;
	}
	socket = null;
	connectionId = "";
	stopKeepalive();
	if (pendingRevocation) {
	  clearTimeout(pendingRevocation.timeout);
	  pendingRevocation.reject(protocolError(
	    ErrorCode.BROWSER_DISCONNECTED,
	    "The browser disconnected before pairing could be revoked",
	    true,
	  ));
	  pendingRevocation = null;
	}
	if (authenticationBlocked) {
	  return;
	}
	await updateStatus("disconnected");
	await scheduleReconnect();
}

async function sendHello(currentSocket) {
  const browserId = await getIdentity();
  const settings = await getSettings();
  const platform = await chrome.runtime.getPlatformInfo();
  const permissions = await chrome.permissions.getAll();
  const manifest = chrome.runtime.getManifest();
	const { credential } = await chrome.storage.local.get("credential");

  const hello = createMessage(MessageType.HELLO, {
    browserId,
    params: {
      displayName: settings.displayName || `Chromium ${browserId.slice(0, 8)}`,
      extensionVersion: manifest.version,
	  ...(credential ? { credential } : {}),
	  ...(!credential && pendingPairingCode ? { pairingCode: pendingPairingCode } : {}),
      browser: {
        name: getBrowserName(),
        version: getBrowserVersion(),
        os: platform.os,
        arch: platform.arch,
      },
      capabilities: capabilitiesFor(permissions, settings.featureFlags),
      permissions: [...(permissions.permissions || []), ...(permissions.origins || [])],
      incognito: false,
    },
  });
  currentSocket.send(JSON.stringify(hello));
  await updateStatus("handshaking");
}

async function handleSocketMessage(currentSocket, rawMessage) {
  if (currentSocket !== socket) {
    return;
  }

  let message;
  try {
    message = JSON.parse(rawMessage);
    validateIncomingMessage(message, await getIdentity());
  } catch (error) {
    await updateStatus("error", error.message);
    currentSocket.close(1002, "Invalid protocol message");
    return;
  }

  switch (message.type) {
    case MessageType.WELCOME:
      connectionId = message.connectionId;
      if (message.result?.credential) {
        await chrome.storage.local.set({ credential: message.result.credential });
      }
      await chrome.storage.local.set({
        connectionDiagnostics: {
          lastConnectedAt: new Date().toISOString(),
          latencyMS: null,
        },
      });
      pendingPairingCode = "";
      authenticationBlocked = false;
      reconnectAttempts = 0;
      await chrome.alarms.clear(RECONNECT_ALARM);
      startKeepalive();
      await updateStatus("connected");
      break;
	case MessageType.AUTH_ERROR: {
	  const authError = message.error || {};
	  authenticationBlocked = authError.code === ErrorCode.PAIRING_REQUIRED;
	  pendingPairingCode = "";
	  if (authenticationBlocked) {
	    await chrome.storage.local.remove("credential");
	  }
	  await updateStatus(
	    authenticationBlocked ? "pairing_required" : "error",
	    authError.message || "Browser authentication failed",
	  );
	  currentSocket.close(1008, "Browser authentication failed");
	  break;
	}
	case MessageType.REVOKE: {
	  if (!pendingRevocation) {
	    break;
	  }
	  const revocation = pendingRevocation;
	  pendingRevocation = null;
	  clearTimeout(revocation.timeout);
	  if (message.success === false) {
	    revocation.reject(protocolError(
	      message.error?.code || ErrorCode.INTERNAL_ERROR,
	      message.error?.message || "The server could not revoke browser pairing",
	      Boolean(message.error?.retryable),
	    ));
	    break;
	  }
	  authenticationBlocked = true;
	  revocation.resolve();
	  break;
	}
    case MessageType.REQUEST:
      void executeRequest(message);
      break;
    case MessageType.CANCEL:
      commandRouter.cancel(message.requestId);
      break;
    case MessageType.PING:
      sendMessage(createMessage(MessageType.PONG, {
        browserId: await getIdentity(),
        connectionId,
      }));
      break;
    case MessageType.PONG:
      await recordKeepaliveLatency();
      break;
    default:
      break;
  }
}

async function executeRequest(request) {
  await commandRouter.execute(request, async (outcome) => {
    sendMessage(createMessage(MessageType.RESPONSE, {
      requestId: request.requestId,
      browserId: await getIdentity(),
      connectionId,
      target: request.target,
      ...outcome,
    }));
  });
}

function sendMessage(message) {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return false;
  }
  socket.send(JSON.stringify(message));
  return true;
}

async function requestPairingRevocation() {
	if (!socket || socket.readyState !== WebSocket.OPEN || !connectionId) {
	  throw protocolError(
	    ErrorCode.BROWSER_DISCONNECTED,
	    "Connect the paired browser before revoking its credential",
	    true,
	  );
	}
	if (pendingRevocation) {
	  throw protocolError(ErrorCode.INVALID_COMMAND, "Pairing revocation is already in progress");
	}
	const browserId = await getIdentity();
	await new Promise((resolve, reject) => {
	  const timeout = setTimeout(() => {
	    pendingRevocation = null;
	    reject(protocolError(
	      ErrorCode.BROWSER_DISCONNECTED,
	      "Timed out while revoking browser pairing",
	      true,
	    ));
	  }, 5_000);
	  pendingRevocation = { resolve, reject, timeout };
	  if (!sendMessage(createMessage(MessageType.REVOKE, { browserId, connectionId }))) {
	    clearTimeout(timeout);
	    pendingRevocation = null;
	    reject(protocolError(ErrorCode.BROWSER_DISCONNECTED, "Browser connection is unavailable", true));
	  }
	});
}

async function sendCapabilitiesChanged() {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return;
  }
  const browserId = await getIdentity();
  const settings = await getSettings();
  const permissions = await chrome.permissions.getAll();
  sendMessage(createMessage(MessageType.CAPABILITIES_CHANGED, {
    browserId,
    connectionId,
    params: {
      capabilities: capabilitiesFor(permissions, settings.featureFlags),
      permissions: [...(permissions.permissions || []), ...(permissions.origins || [])],
    },
  }));
}

async function getCurrentCapabilities() {
  const [settings, permissions] = await Promise.all([
    getSettings(),
    chrome.permissions.getAll(),
  ]);
  return capabilitiesFor(permissions, settings.featureFlags);
}

function capabilitiesFor(permissions, featureFlags = DEFAULT_SETTINGS.featureFlags) {
  return detectCapabilities({
    browserVersion: getBrowserVersion(),
    apis: {
      tabs: Boolean(chrome.tabs),
      tabGrouping: Boolean(chrome.tabs?.group && chrome.tabs?.ungroup),
      tabGroups: Boolean(chrome.tabGroups?.update),
      sessions: Boolean(chrome.sessions?.getRecentlyClosed && chrome.sessions?.restore),
      scripting: Boolean(chrome.scripting),
      webNavigation: Boolean(chrome.webNavigation?.getFrame),
      frameTree: Boolean(chrome.webNavigation?.getAllFrames),
      windows: Boolean(chrome.windows),
    },
    permissions,
    featureFlags,
  });
}

function startKeepalive() {
  stopKeepalive();
  void sendKeepalive();
  keepaliveTimer = setInterval(() => {
    void sendKeepalive();
  }, KEEPALIVE_INTERVAL_MS);
}

async function sendKeepalive() {
  const browserId = await getIdentity();
  if (sendMessage(createMessage(MessageType.PING, { browserId, connectionId }))) {
    lastPingSentAt = Date.now();
  }
}

async function recordKeepaliveLatency() {
  if (lastPingSentAt === 0) {
    return;
  }
  const latencyMS = Math.max(0, Date.now() - lastPingSentAt);
  lastPingSentAt = 0;
  const { connectionDiagnostics = {} } = await chrome.storage.local.get("connectionDiagnostics");
  await chrome.storage.local.set({
    connectionDiagnostics: { ...connectionDiagnostics, latencyMS },
  });
  await broadcastStatusChanged();
}

function stopKeepalive() {
  if (keepaliveTimer !== null) {
    clearInterval(keepaliveTimer);
    keepaliveTimer = null;
  }
  lastPingSentAt = 0;
}

async function scheduleReconnect() {
  const settings = await getSettings();
  if (!settings.autoConnect || reconnectTimer !== null || authenticationBlocked) {
    return;
  }
  const exponential = Math.min(1000 * 2 ** reconnectAttempts, MAX_RECONNECT_DELAY_MS);
  const delay = Math.round(exponential * (0.8 + Math.random() * 0.4));
  reconnectAttempts += 1;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    void connect();
  }, delay);
  await chrome.alarms.create(RECONNECT_ALARM, { delayInMinutes: 1 });
}

function clearReconnectTimer() {
  if (reconnectTimer !== null) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
}

async function disconnect(manual, nextStatus = "disconnected") {
  clearReconnectTimer();
  stopKeepalive();
  if (manual) {
    reconnectAttempts = 0;
  }
  const currentSocket = socket;
  socket = null;
  connectionId = "";
  if (currentSocket) {
    currentSocket.close(1000, "Disconnected by user");
  }
  await chrome.alarms.clear(RECONNECT_ALARM);
  await updateStatus(nextStatus);
}

async function getStatus() {
  const browserId = await getIdentity();
  const settings = await getSettings();
  const permissions = await chrome.permissions.getAll();
  const { credential, connectionDiagnostics = {} } = await chrome.storage.local.get([
    "credential",
    "connectionDiagnostics",
  ]);
  return {
    status,
    error: lastError,
    browserId,
    connectionId,
    browserName: getBrowserName(),
    browserVersion: getBrowserVersion(),
    settings,
    paired: Boolean(credential),
    capabilities: capabilitiesFor(permissions, settings.featureFlags),
    permissions,
    permissionProfiles: permissionProfilesFor(permissions),
    lastConnectedAt: connectionDiagnostics.lastConnectedAt || "",
    latencyMS: Number.isFinite(connectionDiagnostics.latencyMS)
      ? connectionDiagnostics.latencyMS
      : null,
  };
}

async function updateStatus(nextStatus, error = "") {
  status = nextStatus;
  lastError = error;
  const badge = badgeForStatus(nextStatus);
  await chrome.action.setBadgeBackgroundColor({ color: badge.color });
  await chrome.action.setBadgeText({ text: badge.text });
  await broadcastStatusChanged();
}

async function broadcastStatusChanged() {
  try {
    await chrome.runtime.sendMessage({
      type: "CONNECTION_STATUS_CHANGED",
      data: { status, error: lastError },
    });
  } catch {
    // The popup is normally closed, so no receiver is expected.
  }
}

function getBrowserName() {
  const brands = navigator.userAgentData?.brands || [];
  return brands.find((brand) => !brand.brand.includes("Not"))?.brand || "Chromium";
}

function getBrowserVersion() {
  const brands = navigator.userAgentData?.brands || [];
  return brands.find((brand) => !brand.brand.includes("Not"))?.version || "";
}
