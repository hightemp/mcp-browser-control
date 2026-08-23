import {
  ErrorCode,
  assertFreshDocument,
  mapChromeError,
  protocolError,
} from "../protocol.js";

export function createPageHandlers(chromeAPI) {
  async function execute(request, signal) {
    const tab = await resolveTab(request.target?.tabId);
    await assertPageAccess(tab);
    const frameId = request.target?.frameId ?? 0;
    await assertCurrentDocument(request, tab.id, frameId);
    const payload = {
      type: "MCP_BROWSER_COMMAND",
      command: request.command,
      params: request.params,
    };

    throwIfCancelled(signal);
    let response;
    try {
      response = await chromeAPI.tabs.sendMessage(tab.id, payload, { frameId });
    } catch {
      try {
        throwIfCancelled(signal);
        await chromeAPI.scripting.executeScript({
          target: { tabId: tab.id, frameIds: [frameId] },
          files: ["src/content.js"],
        });
        throwIfCancelled(signal);
        response = await chromeAPI.tabs.sendMessage(tab.id, payload, { frameId });
      } catch (error) {
        throw mapChromeError(error);
      }
    }
    throwIfCancelled(signal);
    return unwrapPageResponse(response);
  }

  async function resolveTab(explicitTabId) {
    if (Number.isInteger(explicitTabId)) {
      try {
        return await chromeAPI.tabs.get(explicitTabId);
      } catch {
        throw protocolError(ErrorCode.TAB_NOT_FOUND, `Tab ${explicitTabId} was not found`);
      }
    }
    const tabs = await chromeAPI.tabs.query({ active: true, lastFocusedWindow: true });
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
    const granted = await chromeAPI.permissions.contains({ origins: [originPattern] });
    if (!granted) {
      throw protocolError(
        ErrorCode.PERMISSION_REQUIRED,
        "Site access is required. Grant it from the extension popup.",
        false,
        { origin: parsed.origin },
      );
    }
  }

  async function assertCurrentDocument(request, tabId, frameId) {
    const expectedDocumentId =
      request.target?.documentId || request.params.locator?.element?.documentId;
    if (!expectedDocumentId) {
      return;
    }

    let frame;
    try {
      frame = await chromeAPI.webNavigation.getFrame({ tabId, frameId });
    } catch (error) {
      throw mapChromeError(error);
    }
    if (!frame) {
      throw protocolError(
        ErrorCode.FRAME_NOT_FOUND,
        "The target frame is no longer available",
        true,
      );
    }
    assertFreshDocument(expectedDocumentId, frame.documentId);
  }

  return {
    getHTML: execute,
    getHTMLBySelector: execute,
    click: execute,
    fill: execute,
  };
}

function unwrapPageResponse(response) {
  if (response?.error) {
    throw protocolError(
      response.error.code || ErrorCode.INTERNAL_ERROR,
      response.error.message || "Page command failed",
      Boolean(response.error.retryable),
      response.error.details,
    );
  }
  return response;
}

function throwIfCancelled(signal) {
  if (signal.aborted) {
    throw protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
  }
}
