import {
  ErrorCode,
  MessageType,
  createMessage,
  normalizeError,
  protocolError,
  validateIncomingMessage,
  validateServerEndpoint,
} from "./protocol.js";

const DEFAULT_SETTINGS = Object.freeze({
  endpoint: "ws://127.0.0.1:8090/ws",
  displayName: "",
  autoConnect: true,
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
const activeRequests = new Map();

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
      const settings = {
        endpoint,
        displayName: String(message.settings?.displayName || "").trim(),
        autoConnect: Boolean(message.settings?.autoConnect),
      };
      await chrome.storage.local.set({ settings });
      disconnect(false);
      if (settings.autoConnect) {
        await connect();
      }
      return { success: true, data: await getStatus() };
    }
    case "CONNECT":
      await chrome.storage.local.set({
        settings: { ...(await getSettings()), autoConnect: true },
      });
      await connect();
      return { success: true, data: await getStatus() };
    case "DISCONNECT":
      await chrome.storage.local.set({
        settings: { ...(await getSettings()), autoConnect: false },
      });
      disconnect(true);
      return { success: true, data: await getStatus() };
    default:
      throw protocolError(ErrorCode.INVALID_COMMAND, "Unknown extension UI command");
  }
}

async function initializeSettings() {
  const stored = await chrome.storage.local.get(["browserId", "settings"]);
  const updates = {};
  if (!stored.browserId) {
    updates.browserId = crypto.randomUUID();
  }
  if (!stored.settings) {
    updates.settings = DEFAULT_SETTINGS;
  }
  if (Object.keys(updates).length > 0) {
    await chrome.storage.local.set(updates);
  }
}

async function getIdentity() {
  await initializeSettings();
  const { browserId } = await chrome.storage.local.get("browserId");
  return browserId;
}

async function getSettings() {
  await initializeSettings();
  const { settings } = await chrome.storage.local.get("settings");
  return { ...DEFAULT_SETTINGS, ...settings };
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
  socket = currentSocket;

  currentSocket.addEventListener("open", () => {
    void sendHello(currentSocket);
  });
  currentSocket.addEventListener("message", (event) => {
    void handleSocketMessage(currentSocket, event.data);
  });
  currentSocket.addEventListener("close", () => {
    if (socket !== currentSocket) {
      return;
    }
    socket = null;
    connectionId = "";
    stopKeepalive();
    void updateStatus("disconnected");
    void scheduleReconnect();
  });
  currentSocket.addEventListener("error", () => {
    if (socket === currentSocket) {
      void updateStatus("error", "WebSocket connection failed");
    }
  });
}

async function sendHello(currentSocket) {
  const browserId = await getIdentity();
  const settings = await getSettings();
  const platform = await chrome.runtime.getPlatformInfo();
  const permissions = await chrome.permissions.getAll();
  const manifest = chrome.runtime.getManifest();

  const hello = createMessage(MessageType.HELLO, {
    browserId,
    params: {
      displayName: settings.displayName || `Chromium ${browserId.slice(0, 8)}`,
      extensionVersion: manifest.version,
      browser: {
        name: getBrowserName(),
        version: getBrowserVersion(),
        os: platform.os,
        arch: platform.arch,
      },
      capabilities: capabilitiesFor(permissions),
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
      reconnectAttempts = 0;
      await chrome.alarms.clear(RECONNECT_ALARM);
      startKeepalive();
      await updateStatus("connected");
      break;
    case MessageType.REQUEST:
      void executeRequest(message);
      break;
    case MessageType.CANCEL:
      activeRequests.get(message.requestId)?.abort();
      break;
    case MessageType.PING:
      sendMessage(createMessage(MessageType.PONG, {
        browserId: await getIdentity(),
        connectionId,
      }));
      break;
    case MessageType.PONG:
      break;
    default:
      break;
  }
}

async function executeRequest(request) {
  const controller = new AbortController();
  activeRequests.set(request.requestId, controller);

  try {
    const result = await dispatchCommand(request, controller.signal);
    if (controller.signal.aborted) {
      throw protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
    }
    sendMessage(createMessage(MessageType.RESPONSE, {
      requestId: request.requestId,
      browserId: await getIdentity(),
      connectionId,
      success: true,
      result,
    }));
  } catch (error) {
    sendMessage(createMessage(MessageType.RESPONSE, {
      requestId: request.requestId,
      browserId: await getIdentity(),
      connectionId,
      success: false,
      error: normalizeError(error),
    }));
  } finally {
    activeRequests.delete(request.requestId);
  }
}

async function dispatchCommand(request, signal) {
  if (signal.aborted) {
    throw protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
  }

  switch (request.command) {
    case "browser.ping":
      return { pong: true, time: new Date().toISOString() };
    case "tabs.list":
      return listTabs();
    case "page.getHTML":
    case "page.getHTMLBySelector":
    case "page.click":
    case "page.fill":
      return sendPageCommand(request, signal);
    default:
      throw protocolError(
        ErrorCode.CAPABILITY_UNAVAILABLE,
        `The extension does not support command "${request.command}"`,
      );
  }
}

async function listTabs() {
  const tabs = await chrome.tabs.query({});
  return {
    tabs: tabs.map((tab) => ({
      id: tab.id,
      windowId: tab.windowId,
      index: tab.index,
      active: tab.active,
      pinned: tab.pinned,
      muted: Boolean(tab.mutedInfo?.muted),
      status: tab.status,
      title: tab.title,
      url: tab.url,
      favIconUrl: tab.favIconUrl,
      incognito: tab.incognito,
    })),
    totalCount: tabs.length,
  };
}

async function sendPageCommand(request, signal) {
  const tab = await resolveTab(request.target?.tabId);
  await assertPageAccess(tab);
  const frameId = request.target?.frameId ?? 0;
  const payload = {
    type: "MCP_BROWSER_COMMAND",
    command: request.command,
    params: request.params || {},
  };

  if (signal.aborted) {
    throw protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
  }

  let response;
  try {
    response = await chrome.tabs.sendMessage(tab.id, payload, { frameId });
  } catch {
    await chrome.scripting.executeScript({
      target: { tabId: tab.id, frameIds: [frameId] },
      files: ["src/content.js"],
    });
    response = await chrome.tabs.sendMessage(tab.id, payload, { frameId });
  }
  return unwrapPageResponse(response);
}

function unwrapPageResponse(response) {
  if (response?.error) {
    throw protocolError(
      response.error.code || ErrorCode.INTERNAL_ERROR,
      response.error.message || "Page command failed",
      Boolean(response.error.retryable),
    );
  }
  return response;
}

async function resolveTab(explicitTabId) {
  if (Number.isInteger(explicitTabId)) {
    try {
      return await chrome.tabs.get(explicitTabId);
    } catch {
      throw protocolError(ErrorCode.TAB_NOT_FOUND, `Tab ${explicitTabId} was not found`);
    }
  }
  const tabs = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  if (!tabs[0]) {
    throw protocolError(ErrorCode.TAB_NOT_FOUND, "No active tab was found");
  }
  return tabs[0];
}

async function assertPageAccess(tab) {
  let parsed;
  try {
    parsed = new URL(tab.url);
  } catch {
    throw protocolError(ErrorCode.RESTRICTED_URL, "The tab URL cannot be accessed");
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw protocolError(ErrorCode.RESTRICTED_URL, `Cannot access ${parsed.protocol} pages`);
  }
  const originPattern = `${parsed.protocol}//${parsed.host}/*`;
  const granted = await chrome.permissions.contains({ origins: [originPattern] });
  if (!granted) {
    throw protocolError(
      ErrorCode.PERMISSION_REQUIRED,
      "Site access is required. Grant it from the extension popup.",
      false,
      { origin: parsed.origin },
    );
  }
}

function sendMessage(message) {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return false;
  }
  socket.send(JSON.stringify(message));
  return true;
}

async function sendCapabilitiesChanged() {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return;
  }
  const browserId = await getIdentity();
  const permissions = await chrome.permissions.getAll();
  sendMessage(createMessage(MessageType.CAPABILITIES_CHANGED, {
    browserId,
    connectionId,
    params: {
      capabilities: capabilitiesFor(permissions),
      permissions: [...(permissions.permissions || []), ...(permissions.origins || [])],
    },
  }));
}

function capabilitiesFor(permissions) {
  const capabilities = ["browser.ping", "tabs.list"];
  if ((permissions.origins || []).length > 0) {
    capabilities.push(
      "page.getHTML",
      "page.getHTMLBySelector",
      "page.click",
      "page.fill",
    );
  }
  return capabilities;
}

function startKeepalive() {
  stopKeepalive();
  keepaliveTimer = setInterval(() => {
    void getIdentity().then((browserId) => {
      sendMessage(createMessage(MessageType.PING, { browserId, connectionId }));
    });
  }, KEEPALIVE_INTERVAL_MS);
}

function stopKeepalive() {
  if (keepaliveTimer !== null) {
    clearInterval(keepaliveTimer);
    keepaliveTimer = null;
  }
}

async function scheduleReconnect() {
  const settings = await getSettings();
  if (!settings.autoConnect || reconnectTimer !== null) {
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

function disconnect(manual) {
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
  void chrome.alarms.clear(RECONNECT_ALARM);
  void updateStatus("disconnected");
}

async function getStatus() {
  const browserId = await getIdentity();
  const settings = await getSettings();
  const permissions = await chrome.permissions.getAll();
  return {
    status,
    browserId,
    connectionId,
    settings,
    capabilities: capabilitiesFor(permissions),
    permissions,
  };
}

async function updateStatus(nextStatus, error = "") {
  status = nextStatus;
  const colors = {
    connected: "#15803d",
    connecting: "#ca8a04",
    handshaking: "#ca8a04",
    disconnected: "#64748b",
    error: "#b91c1c",
  };
  await chrome.action.setBadgeBackgroundColor({ color: colors[nextStatus] || "#64748b" });
  await chrome.action.setBadgeText({ text: nextStatus === "connected" ? "ON" : "" });
  try {
    await chrome.runtime.sendMessage({
      type: "CONNECTION_STATUS_CHANGED",
      data: { status: nextStatus, error },
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
