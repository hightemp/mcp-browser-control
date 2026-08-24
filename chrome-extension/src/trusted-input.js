import { ErrorCode, protocolError } from "./protocol.js";

const TRUSTED_INPUT_COMMANDS = new Set([
  "page.click",
  "page.hover",
  "page.fill",
  "page.type",
  "page.clear",
  "page.press",
  "page.setChecked",
  "page.scroll",
]);

const KEY_DEFINITIONS = Object.freeze({
  Backspace: { code: "Backspace", windowsVirtualKeyCode: 8 },
  Tab: { code: "Tab", windowsVirtualKeyCode: 9 },
  Enter: { code: "Enter", windowsVirtualKeyCode: 13 },
  Shift: { code: "ShiftLeft", windowsVirtualKeyCode: 16 },
  Control: { code: "ControlLeft", windowsVirtualKeyCode: 17 },
  Alt: { code: "AltLeft", windowsVirtualKeyCode: 18 },
  Escape: { code: "Escape", windowsVirtualKeyCode: 27 },
  " ": { code: "Space", windowsVirtualKeyCode: 32 },
  PageUp: { code: "PageUp", windowsVirtualKeyCode: 33 },
  PageDown: { code: "PageDown", windowsVirtualKeyCode: 34 },
  End: { code: "End", windowsVirtualKeyCode: 35 },
  Home: { code: "Home", windowsVirtualKeyCode: 36 },
  ArrowLeft: { code: "ArrowLeft", windowsVirtualKeyCode: 37 },
  ArrowUp: { code: "ArrowUp", windowsVirtualKeyCode: 38 },
  ArrowRight: { code: "ArrowRight", windowsVirtualKeyCode: 39 },
  ArrowDown: { code: "ArrowDown", windowsVirtualKeyCode: 40 },
  Delete: { code: "Delete", windowsVirtualKeyCode: 46 },
  Meta: { code: "MetaLeft", windowsVirtualKeyCode: 91 },
});

const MODIFIER_BITS = Object.freeze({ Alt: 1, Control: 2, Meta: 4, Shift: 8 });
const MOUSE_BUTTON_BITS = Object.freeze({ left: 1, right: 2, middle: 4 });

export function createTrustedInputExecutor(chromeAPI, { cdpSessions, bridge }) {
  let sequence = 0;

  async function execute(request, { tab, frameId, documentId, signal }) {
    if (!TRUSTED_INPUT_COMMANDS.has(request.command)) {
      throw protocolError(
        ErrorCode.CAPABILITY_UNAVAILABLE,
        `Trusted CDP input is unavailable for ${request.command}; use the content backend`,
      );
    }
    if (frameId !== 0) {
      throw protocolError(
        ErrorCode.CAPABILITY_UNAVAILABLE,
        "Trusted CDP input currently supports only the root document",
      );
    }
    if (!cdpSessions) {
      throw protocolError(ErrorCode.CAPABILITY_UNAVAILABLE, "Managed CDP sessions are unavailable");
    }
    const debuggerGranted = await chromeAPI.permissions.contains({ permissions: ["debugger"] });
    if (!debuggerGranted) {
      throw protocolError(
        ErrorCode.PERMISSION_REQUIRED,
        "Debug permission is required. Grant it from the extension settings page.",
      );
    }
    throwIfCancelled(signal);

    sequence += 1;
    const inputParams = bridgeInputParams(request.params);
    const methods = trustedInputMethods(request);
    return cdpSessions.withSession(
      { tabId: tab.id },
      {
        consumerId: `input:${String(request.requestId || sequence).slice(0, 120)}`,
        domains: ["Input"],
        commands: methods,
        signal,
      },
      async (lease) => {
        const prepared = await bridge.execute({
          tabId: tab.id,
          frameId,
          documentId,
          operationId: `trusted-input-prepare:${String(request.requestId || sequence).slice(0, 100)}`,
          command: "page.prepareTrustedInput",
          params: { command: request.command, inputParams },
          signal,
        });
        throwIfCancelled(signal);
        await dispatchTrustedInput(lease, request, prepared, signal);
        throwIfCancelled(signal);
        if (request.params.waitForNavigation) {
          return preparedInteractionResult(request, prepared);
        }
        return bridge.execute({
          tabId: tab.id,
          frameId,
          documentId,
          operationId: `trusted-input-result:${String(request.requestId || sequence).slice(0, 100)}`,
          command: "page.readTrustedInputResult",
          params: { command: request.command, inputParams },
          signal,
        });
      },
    );
  }

  return { execute };
}

function preparedInteractionResult(request, prepared) {
  const result = {
    matchCount: prepared.matchCount,
    element: prepared.element,
    target: prepared.target,
    backend: "cdp",
    timestamp: new Date().toISOString(),
  };
  if (request.command === "page.click") {
    result.button = request.params.button || "left";
    result.clickCount = request.params.clickCount || 1;
  }
  if (request.command === "page.press") {
    result.key = request.params.key;
    result.modifiers = request.params.modifiers || [];
  }
  return Object.fromEntries(Object.entries(result).filter(([, value]) => value !== undefined));
}

function trustedInputMethods(request) {
  switch (request.command) {
    case "page.click":
    case "page.hover":
    case "page.setChecked":
    case "page.scroll":
      return ["Input.dispatchMouseEvent"];
    case "page.fill":
      return request.params.clear === false
        ? ["Input.insertText"]
        : ["Input.dispatchKeyEvent", "Input.insertText"];
    case "page.type":
      return ["Input.insertText"];
    case "page.clear":
    case "page.press":
      return ["Input.dispatchKeyEvent"];
    default:
      return [];
  }
}

function bridgeInputParams(params) {
  const result = {};
  for (const field of [
    "selector",
    "coordinates",
    "locator",
    "index",
    "button",
    "clickCount",
    "key",
    "modifiers",
    "checked",
    "deltaX",
    "deltaY",
  ]) {
    if (params[field] !== undefined) result[field] = params[field];
  }
  return result;
}

async function dispatchTrustedInput(lease, request, prepared, signal) {
  switch (request.command) {
    case "page.click":
      return dispatchClick(lease, prepared.point, request.params, signal);
    case "page.hover":
      return sendMouse(lease, "mouseMoved", prepared.point, {}, signal);
    case "page.fill":
      if (request.params.clear !== false) await clearFocusedElement(lease, signal);
      return insertText(lease, String(request.params.value), signal);
    case "page.type":
      return typeText(lease, request.params.text, request.params.delayMs || 0, signal);
    case "page.clear":
      return clearFocusedElement(lease, signal);
    case "page.press":
      return dispatchKey(lease, request.params.key, request.params.modifiers || [], signal);
    case "page.setChecked":
      if (!prepared.skip) return dispatchClick(lease, prepared.point, {}, signal);
      return undefined;
    case "page.scroll":
      return sendMouse(
        lease,
        "mouseWheel",
        prepared.point,
        { deltaX: request.params.deltaX || 0, deltaY: request.params.deltaY || 0 },
        signal,
      );
    default:
      throw protocolError(ErrorCode.CAPABILITY_UNAVAILABLE, "Trusted CDP input is unavailable");
  }
}

async function dispatchClick(lease, point, params, signal) {
  const button = params.button || "left";
  const count = params.clickCount || 1;
  await sendMouse(lease, "mouseMoved", point, {}, signal);
  for (let clickCount = 1; clickCount <= count; clickCount += 1) {
    await sendMouse(
      lease,
      "mousePressed",
      point,
      { button, buttons: MOUSE_BUTTON_BITS[button], clickCount },
      signal,
    );
    await sendMouse(lease, "mouseReleased", point, { button, buttons: 0, clickCount }, signal);
  }
}

function sendMouse(lease, type, point, extra, signal) {
  assertPoint(point);
  throwIfCancelled(signal);
  return lease.sendCommand(
    "Input.dispatchMouseEvent",
    { type, x: point.x, y: point.y, ...extra },
    { signal },
  );
}

async function clearFocusedElement(lease, signal) {
  await lease.sendCommand(
    "Input.dispatchKeyEvent",
    {
      type: "rawKeyDown",
      key: "a",
      code: "KeyA",
      windowsVirtualKeyCode: 65,
      commands: ["selectAll"],
    },
    { signal },
  );
  await lease.sendCommand(
    "Input.dispatchKeyEvent",
    { type: "keyUp", key: "a", code: "KeyA", windowsVirtualKeyCode: 65 },
    { signal },
  );
  await dispatchKey(lease, "Backspace", [], signal);
}

function insertText(lease, value, signal) {
  throwIfCancelled(signal);
  return lease.sendCommand("Input.insertText", { text: value }, { signal });
}

async function typeText(lease, value, delayMs, signal) {
  if (!delayMs) return insertText(lease, value, signal);
  for (const character of value) {
    await insertText(lease, character, signal);
    await cancellableDelay(delayMs, signal);
  }
}

async function dispatchKey(lease, key, modifierNames, signal) {
  const descriptor = keyDescriptor(key);
  const modifiers = modifierNames.reduce((value, name) => value | MODIFIER_BITS[name], 0);
  const printable = [...key].length === 1 && !modifierNames.some((name) => name !== "Shift");
  const common = { key, modifiers, ...descriptor };
  await lease.sendCommand(
    "Input.dispatchKeyEvent",
    {
      type: printable ? "keyDown" : "rawKeyDown",
      ...common,
      ...(printable ? { text: key, unmodifiedText: key } : {}),
    },
    { signal },
  );
  throwIfCancelled(signal);
  await lease.sendCommand("Input.dispatchKeyEvent", { type: "keyUp", ...common }, { signal });
}

function keyDescriptor(key) {
  if (KEY_DEFINITIONS[key]) return KEY_DEFINITIONS[key];
  if (/^[A-Za-z]$/.test(key)) {
    const upper = key.toUpperCase();
    return { code: `Key${upper}`, windowsVirtualKeyCode: upper.charCodeAt(0) };
  }
  if (/^[0-9]$/.test(key)) {
    return { code: `Digit${key}`, windowsVirtualKeyCode: key.charCodeAt(0) };
  }
  return {};
}

function assertPoint(point) {
  if (
    !point ||
    !Number.isFinite(point.x) ||
    !Number.isFinite(point.y) ||
    point.x < 0 ||
    point.y < 0
  ) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "The content script returned an invalid input point",
    );
  }
}

function cancellableDelay(delayMs, signal) {
  if (signal.aborted) return Promise.reject(cancelledError());
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve();
    }, delayMs);
    const onAbort = () => {
      clearTimeout(timer);
      signal.removeEventListener("abort", onAbort);
      reject(cancelledError());
    };
    signal.addEventListener("abort", onAbort, { once: true });
  });
}

function throwIfCancelled(signal) {
  if (signal.aborted) throw cancelledError();
}

function cancelledError() {
  return protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
}
