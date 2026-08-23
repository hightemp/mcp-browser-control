import assert from "node:assert/strict";
import test from "node:test";

class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  static instances = [];

  constructor(url) {
    this.url = url;
    this.readyState = FakeWebSocket.CONNECTING;
    this.listeners = new Map();
    this.sent = [];
    FakeWebSocket.instances.push(this);
  }

  addEventListener(type, listener) {
    const listeners = this.listeners.get(type) || new Set();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  open() {
    this.readyState = FakeWebSocket.OPEN;
    this.emit("open", {});
  }

  send(data) {
    assert.equal(this.readyState, FakeWebSocket.OPEN);
    this.sent.push(JSON.parse(data));
  }

  receive(message) {
    this.emit("message", { data: JSON.stringify(message) });
  }

  close(code = 1000, reason = "") {
    if (this.readyState === FakeWebSocket.CLOSED) return;
    this.readyState = FakeWebSocket.CLOSED;
    this.emit("close", { code, reason });
  }

  serverClose() {
    this.close(1006, "Connection lost");
  }

  emit(type, event) {
    for (const listener of this.listeners.get(type) || []) listener(event);
  }
}

const browserId = "11111111-1111-4111-8111-111111111111";
const chromeMock = createChromeMock({
  browserId,
  settings: {
    endpoint: "ws://127.0.0.1:8090/ws",
    displayName: "Test Chromium",
    autoConnect: false,
    featureFlags: { pageAutomation: true },
  },
});

globalThis.chrome = chromeMock.api;
globalThis.WebSocket = FakeWebSocket;
Object.defineProperty(globalThis.navigator, "userAgentData", {
  configurable: true,
  value: { brands: [{ brand: "Chromium", version: "116" }] },
});

await import("../src/service-worker.js");

test("service worker pairs and reconnects with the stored credential", async () => {
  await waitFor(() => chromeMock.badgeTexts.at(-1)?.text === "OFF");

  const rejectedReset = await chromeMock.sendRuntimeMessage({ type: "RESET_IDENTITY" });
  assert.equal(rejectedReset.success, false);
  assert.equal(rejectedReset.error.code, "CONFIRMATION_REQUIRED");
  assert.equal(chromeMock.storageValues.browserId, browserId);

  const pairingResponse = await chromeMock.sendRuntimeMessage({
    type: "PAIR",
    pairingCode: "12345678",
  });
  assert.equal(pairingResponse.success, true);

  const firstSocket = await waitForSocket(0);
  assert.equal(firstSocket.url, "ws://127.0.0.1:8090/ws");
  firstSocket.open();
  const firstHello = await waitForSentMessage(firstSocket, "hello");
  assert.equal(firstHello.browserId, browserId);
  assert.equal(firstHello.params.pairingCode, "1234-5678");
  assert.equal(firstHello.params.credential, undefined);
  assert.equal(firstHello.params.displayName, "Test Chromium");

  firstSocket.receive(welcome("connection-1", "stored-credential"));
  await waitFor(() => chromeMock.storageValues.credential === "stored-credential");
  let statusResponse = await chromeMock.sendRuntimeMessage({ type: "GET_STATUS" });
  assert.equal(statusResponse.data.status, "connected");
  assert.equal(statusResponse.data.connectionId, "connection-1");
  assert.equal(statusResponse.data.paired, true);

  firstSocket.serverClose();
  await waitFor(() => chromeMock.createdAlarms.some(({ name }) => name === reconnectAlarm));
  chromeMock.events.alarms.emit({ name: reconnectAlarm });

  const secondSocket = await waitForSocket(1);
  secondSocket.open();
  const reconnectHello = await waitForSentMessage(secondSocket, "hello");
  assert.equal(reconnectHello.browserId, browserId);
  assert.equal(reconnectHello.params.credential, "stored-credential");
  assert.equal(reconnectHello.params.pairingCode, undefined);

  secondSocket.receive(welcome("connection-2"));
  await waitFor(async () => {
    const response = await chromeMock.sendRuntimeMessage({ type: "GET_STATUS" });
    return response.data.status === "connected" && response.data.connectionId === "connection-2";
  });

  statusResponse = await chromeMock.sendRuntimeMessage({ type: "DISCONNECT" });
  assert.equal(statusResponse.success, true);
  assert.equal(statusResponse.data.status, "disconnected");
  assert.equal(statusResponse.data.settings.autoConnect, false);
  assert.equal(secondSocket.readyState, FakeWebSocket.CLOSED);
});

const reconnectAlarm = "mcp-browser-control-reconnect";

function welcome(connectionId, credential = undefined) {
  return {
    protocolVersion: "1.0",
    type: "welcome",
    browserId,
    connectionId,
    result: {
      browserId,
      connectionId,
      ...(credential ? { credential } : {}),
    },
    timestamp: new Date().toISOString(),
  };
}

function createChromeMock(initialStorage) {
  const storageValues = structuredClone(initialStorage);
  const events = {
    alarms: fakeEvent(),
    installed: fakeEvent(),
    messages: fakeEvent(),
    permissionAdded: fakeEvent(),
    permissionRemoved: fakeEvent(),
    startup: fakeEvent(),
  };
  const badgeTexts = [];
  const createdAlarms = [];

  return {
    api: {
      action: {
        async setBadgeBackgroundColor() {},
        async setBadgeText(value) {
          badgeTexts.push(value);
        },
      },
      alarms: {
        onAlarm: events.alarms,
        async clear() {
          return true;
        },
        async create(name, options) {
          createdAlarms.push({ name, options });
        },
      },
      permissions: {
        onAdded: events.permissionAdded,
        onRemoved: events.permissionRemoved,
        async contains() {
          return false;
        },
        async getAll() {
          return { permissions: ["tabs"], origins: [] };
        },
      },
      runtime: {
        onInstalled: events.installed,
        onMessage: events.messages,
        onStartup: events.startup,
        async getPlatformInfo() {
          return { os: "linux", arch: "x86-64" };
        },
        getManifest() {
          return { version: "0.1.0-test" };
        },
        async sendMessage() {},
      },
      storage: {
        local: {
          async get(keys) {
            const names = Array.isArray(keys) ? keys : [keys];
            return Object.fromEntries(
              names.filter((key) => key in storageValues).map((key) => [key, storageValues[key]]),
            );
          },
          async remove(keys) {
            for (const key of Array.isArray(keys) ? keys : [keys]) delete storageValues[key];
          },
          async set(updates) {
            Object.assign(storageValues, structuredClone(updates));
          },
        },
      },
      tabs: {},
    },
    badgeTexts,
    createdAlarms,
    events,
    storageValues,
    sendRuntimeMessage(message) {
      const [listener] = events.messages.listeners();
      assert.ok(listener, "runtime message listener is registered");
      return new Promise((resolve) => {
        assert.equal(listener(message, {}, resolve), true);
      });
    },
  };
}

function fakeEvent() {
  const registered = new Set();
  return {
    addListener(listener) {
      registered.add(listener);
    },
    emit(value) {
      for (const listener of registered) listener(value);
    },
    listeners() {
      return [...registered];
    },
  };
}

async function waitForSocket(index) {
  await waitFor(() => FakeWebSocket.instances.length > index);
  return FakeWebSocket.instances[index];
}

async function waitForSentMessage(socket, type) {
  await waitFor(() => socket.sent.some((message) => message.type === type));
  return socket.sent.find((message) => message.type === type);
}

async function waitFor(predicate) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (await predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  throw new Error("Condition was not reached");
}
