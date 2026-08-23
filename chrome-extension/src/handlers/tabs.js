import { ErrorCode, mapChromeError, protocolError } from "../protocol.js";

export function createTabHandlers(chromeAPI) {
  async function resolveTab(request) {
    if (Number.isInteger(request.target?.tabId)) {
      return callChrome(() => chromeAPI.tabs.get(request.target.tabId));
    }
    const tabs = await callChrome(() => chromeAPI.tabs.query({
      active: true,
      lastFocusedWindow: true,
    }));
    if (!tabs[0]) {
      throw protocolError(ErrorCode.TAB_NOT_FOUND, "No active tab was found");
    }
    return tabs[0];
  }

  async function updatedTab(request, update) {
    const tab = await resolveTab(request);
    return describeTab(await callChrome(() => chromeAPI.tabs.update(tab.id, update)));
  }

  async function afterNavigation(request, operation) {
    const tab = await resolveTab(request);
    await callChrome(() => operation(tab.id));
    return describeTab(await callChrome(() => chromeAPI.tabs.get(tab.id)));
  }

  return {
    async list() {
      const tabs = await callChrome(() => chromeAPI.tabs.query({}));
      return { tabs: tabs.map(describeTab), totalCount: tabs.length };
    },

    async get(request) {
      return { tab: describeTab(await resolveTab(request)) };
    },

    async create(request) {
      return callChrome(async () => ({
        tab: describeTab(await chromeAPI.tabs.create(request.params)),
      }));
    },

    async activate(request) {
      return { tab: await updatedTab(request, { active: true }) };
    },

    async navigate(request) {
      return { tab: await updatedTab(request, { url: request.params.url }) };
    },

    async reload(request) {
      return {
        tab: await afterNavigation(request, (tabId) => chromeAPI.tabs.reload(tabId, {
          bypassCache: Boolean(request.params.bypassCache),
        })),
      };
    },

    async stop(request) {
      const tab = await resolveTab(request);
      await assertPageAccess(chromeAPI, tab);
      await callChrome(() => chromeAPI.scripting.executeScript({
        target: { tabId: tab.id },
        func: () => window.stop(),
      }));
      return { tabId: tab.id, stopped: true };
    },

    async back(request) {
      return { tab: await afterNavigation(request, (tabId) => chromeAPI.tabs.goBack(tabId)) };
    },

    async forward(request) {
      return { tab: await afterNavigation(request, (tabId) => chromeAPI.tabs.goForward(tabId)) };
    },

    async move(request) {
      const tab = await resolveTab(request);
      const moved = await callChrome(() => chromeAPI.tabs.move(tab.id, request.params));
      return { tab: describeTab(Array.isArray(moved) ? moved[0] : moved) };
    },

    async duplicate(request) {
      const tab = await resolveTab(request);
      return {
        tab: describeTab(await callChrome(() => chromeAPI.tabs.duplicate(tab.id))),
      };
    },

    async close(request) {
      const tab = await resolveTab(request);
      await callChrome(() => chromeAPI.tabs.remove(tab.id));
      return { tabId: tab.id, closed: true };
    },

    async pin(request) {
      return { tab: await updatedTab(request, { pinned: request.params.pinned }) };
    },

    async mute(request) {
      return { tab: await updatedTab(request, { muted: request.params.muted }) };
    },

    async getZoom(request) {
      const tab = await resolveTab(request);
      return {
        tabId: tab.id,
        factor: await callChrome(() => chromeAPI.tabs.getZoom(tab.id)),
      };
    },

    async setZoom(request) {
      const tab = await resolveTab(request);
      await callChrome(() => chromeAPI.tabs.setZoom(tab.id, request.params.factor));
      return { tabId: tab.id, factor: request.params.factor };
    },
  };
}

async function assertPageAccess(chromeAPI, tab) {
  let parsed;
  try {
    parsed = new URL(tab.url);
  } catch {
    throw protocolError(ErrorCode.RESTRICTED_URL, "The tab URL cannot be accessed");
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw protocolError(ErrorCode.RESTRICTED_URL, `Cannot access ${parsed.protocol} pages`);
  }
  const granted = await chromeAPI.permissions.contains({
    origins: [`${parsed.protocol}//${parsed.host}/*`],
  });
  if (!granted) {
    throw protocolError(
      ErrorCode.PERMISSION_REQUIRED,
      "Site access is required to stop this tab. Grant it from the extension popup.",
      false,
      { origin: parsed.origin },
    );
  }
}

async function callChrome(operation) {
  try {
    return await operation();
  } catch (error) {
    throw mapChromeError(error);
  }
}

export function describeTab(tab) {
  return {
    id: tab.id,
    windowId: tab.windowId,
    index: tab.index,
    active: tab.active,
    highlighted: tab.highlighted,
    pinned: tab.pinned,
    muted: Boolean(tab.mutedInfo?.muted),
    audible: tab.audible,
    discarded: tab.discarded,
    autoDiscardable: tab.autoDiscardable,
    status: tab.status,
    title: tab.title,
    url: tab.url,
    pendingUrl: tab.pendingUrl,
    favIconUrl: tab.favIconUrl,
    incognito: tab.incognito,
    groupId: tab.groupId,
    sessionId: tab.sessionId,
  };
}
