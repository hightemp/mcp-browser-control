export const MINIMUM_BROWSER_VERSION = 116;

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
      featureFlags.pageAutomation !== false &&
      apis.scripting &&
      grantedPermissions.has("scripting") &&
      (permissions.origins || []).length > 0
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
    featureFlags.pageAutomation !== false &&
    apis.scripting &&
    apis.webNavigation &&
    grantedPermissions.has("scripting") &&
    hasWebsiteAccess
  ) {
    if (apis.frameTree) {
      capabilities.push("page.info");
    }
    capabilities.push(
      "page.getHTML",
      "page.getHTMLBySelector",
      "page.getText",
      "page.query",
      "page.getElement",
      "page.snapshot",
      "page.click",
      "page.fill",
      "page.hover",
      "page.focus",
      "page.blur",
      "page.type",
      "page.clear",
      "page.press",
      "page.select",
      "page.setChecked",
      "page.scroll",
      "page.drag",
      "page.dispatch",
      "page.submit",
      "page.wait",
    );
    if (apis.captureVisibleTab) capabilities.push("page.screenshot");
    if (apis.debugger && grantedPermissions.has("debugger")) {
      capabilities.push("page.printToPDF");
    }
    capabilities.push("console.start", "console.stop", "console.clear", "console.read");
  }
  return capabilities;
}

function parseMajorVersion(version) {
  const match = String(version).match(/^(\d+)/);
  return match ? Number.parseInt(match[1], 10) : null;
}
