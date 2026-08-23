import { ErrorCode, mapChromeError, protocolError } from "../protocol.js";
import { describeTab } from "./tabs.js";

export function createSessionHandlers(chromeAPI) {
  return {
    async recentlyClosed(request) {
      const filter = request.params.maxResults === undefined
        ? {}
        : { maxResults: request.params.maxResults };
      const sessions = await callChrome(() => chromeAPI.sessions.getRecentlyClosed(filter));
      return {
        sessions: sessions.map(describeSession),
        totalCount: sessions.length,
      };
    },

    async restore(request) {
      const session = request.params.sessionId === undefined
        ? await callChrome(() => chromeAPI.sessions.restore())
        : await callChrome(() => chromeAPI.sessions.restore(request.params.sessionId));
      if (!session) {
        throw protocolError(
          ErrorCode.SESSION_NOT_FOUND,
          "The recently closed session is no longer available",
          true,
        );
      }
      return { session: describeSession(session) };
    },
  };
}

function describeSession(session) {
  return {
    lastModified: session.lastModified,
    ...(session.tab ? { tab: describeTab(session.tab) } : {}),
    ...(session.window ? { window: describeSessionWindow(session.window) } : {}),
  };
}

function describeSessionWindow(window) {
  return {
    id: window.id,
    sessionId: window.sessionId,
    focused: window.focused,
    incognito: window.incognito,
    type: window.type,
    state: window.state,
    alwaysOnTop: window.alwaysOnTop,
    tabs: (window.tabs || []).map(describeTab),
  };
}

async function callChrome(operation) {
  try {
    return await operation();
  } catch (error) {
    throw mapChromeError(error);
  }
}
