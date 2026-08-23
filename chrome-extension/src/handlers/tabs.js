import { mapChromeError } from "../protocol.js";

export function createTabHandlers(chromeAPI) {
  return {
    async list() {
      let tabs;
      try {
        tabs = await chromeAPI.tabs.query({});
      } catch (error) {
        throw mapChromeError(error);
      }
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
    },
  };
}
