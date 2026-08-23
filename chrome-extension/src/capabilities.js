const MINIMUM_BROWSER_VERSION = 116;

export function detectCapabilities({
  browserVersion = "",
  apis = {},
  permissions = {},
  featureFlags = {},
} = {}) {
  const capabilities = ["browser.ping"];
  const majorVersion = parseMajorVersion(browserVersion);
  if (majorVersion !== null && majorVersion < MINIMUM_BROWSER_VERSION) {
    return capabilities;
  }

  if (apis.windows) {
    capabilities.push(
      "windows.list",
      "windows.get",
      "windows.create",
      "windows.update",
      "windows.focus",
      "windows.close",
    );
  }

  const grantedPermissions = new Set(permissions.permissions || []);
  if (apis.tabs && grantedPermissions.has("tabs")) {
    capabilities.push(
      "tabs.list",
      "tabs.get",
      "tabs.create",
      "tabs.activate",
      "tabs.navigate",
      "tabs.reload",
    );
    if (
      featureFlags.pageAutomation !== false
      && apis.scripting
      && grantedPermissions.has("scripting")
      && (permissions.origins || []).length > 0
    ) {
      capabilities.push("tabs.stop");
    }
    capabilities.push(
      "tabs.back",
      "tabs.forward",
      "tabs.move",
      "tabs.duplicate",
      "tabs.close",
      "tabs.pin",
      "tabs.mute",
      "tabs.getZoom",
      "tabs.setZoom",
    );
    if (apis.tabGrouping) {
      capabilities.push("tabs.group", "tabs.ungroup");
    }
    if (apis.tabGroups && grantedPermissions.has("tabGroups")) {
      capabilities.push("tabGroups.update");
    }
  }

  if (apis.sessions && grantedPermissions.has("sessions")) {
    capabilities.push("sessions.recentlyClosed", "sessions.restore");
  }

  const hasWebsiteAccess = (permissions.origins || []).length > 0;
  if (
    featureFlags.pageAutomation !== false
    && apis.scripting
    && grantedPermissions.has("scripting")
    && hasWebsiteAccess
  ) {
    capabilities.push(
      "page.getHTML",
      "page.getHTMLBySelector",
      "page.click",
      "page.fill",
    );
  }
  return capabilities;
}

function parseMajorVersion(version) {
  const match = String(version).match(/^(\d+)/);
  return match ? Number.parseInt(match[1], 10) : null;
}
