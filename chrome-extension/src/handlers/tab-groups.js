import { ErrorCode, mapChromeError, protocolError } from "../protocol.js";

export function createTabGroupHandlers(chromeAPI) {
  return {
    async group(request) {
      const options = { tabIds: [...request.params.tabIds] };
      if (request.params.groupId !== undefined) {
        options.groupId = request.params.groupId;
      } else if (request.params.windowId !== undefined) {
        options.createProperties = { windowId: request.params.windowId };
      }
      const groupId = await callChrome(() => chromeAPI.tabs.group(options));
      return { groupId, tabIds: options.tabIds };
    },

    async ungroup(request) {
      const tabIds = [...request.params.tabIds];
      await callChrome(() => chromeAPI.tabs.ungroup(tabIds));
      return { tabIds, ungrouped: true };
    },

    async update(request) {
      const { groupId, ...update } = request.params;
      const group = await callChrome(() => chromeAPI.tabGroups.update(groupId, update));
      if (!group) {
        throw protocolError(
          ErrorCode.TAB_GROUP_NOT_FOUND,
          "The target tab group is no longer available",
          true,
        );
      }
      return { group: describeTabGroup(group) };
    },
  };
}

function describeTabGroup(group) {
  return {
    id: group.id,
    windowId: group.windowId,
    title: group.title,
    color: group.color,
    collapsed: group.collapsed,
    shared: group.shared,
  };
}

async function callChrome(operation) {
  try {
    return await operation();
  } catch (error) {
    throw mapChromeError(error);
  }
}
