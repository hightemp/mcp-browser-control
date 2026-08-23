import { mapChromeError } from "../protocol.js";

export function createWindowHandlers(chromeAPI) {
  return {
    async list() {
      return callChrome(async () => {
        const windows = await chromeAPI.windows.getAll({
          populate: false,
          windowTypes: ["normal", "popup"],
        });
        return {
          windows: windows.map(describeWindow),
          totalCount: windows.length,
        };
      });
    },

    async get(request) {
      return callChrome(async () => ({
        window: describeWindow(
          await chromeAPI.windows.get(request.target.windowId, {
            populate: false,
          }),
        ),
      }));
    },

    async create(request) {
      return callChrome(async () => {
        const { urls, ...createData } = request.params;
        const window = await chromeAPI.windows.create({
          ...createData,
          ...(urls ? { url: urls } : {}),
        });
        return { window: describeWindow(window) };
      });
    },

    async update(request) {
      return callChrome(async () => ({
        window: describeWindow(
          await chromeAPI.windows.update(request.target.windowId, request.params),
        ),
      }));
    },

    async focus(request) {
      return callChrome(async () => ({
        window: describeWindow(
          await chromeAPI.windows.update(request.target.windowId, {
            focused: true,
          }),
        ),
      }));
    },

    async close(request) {
      return callChrome(async () => {
        await chromeAPI.windows.remove(request.target.windowId);
        return { windowId: request.target.windowId, closed: true };
      });
    },
  };
}

async function callChrome(operation) {
  try {
    return await operation();
  } catch (error) {
    throw mapChromeError(error);
  }
}

function describeWindow(window) {
  return {
    id: window.id,
    focused: window.focused,
    top: window.top,
    left: window.left,
    width: window.width,
    height: window.height,
    incognito: window.incognito,
    type: window.type,
    state: window.state,
    alwaysOnTop: window.alwaysOnTop,
  };
}
