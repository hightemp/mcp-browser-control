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

  const grantedPermissions = new Set(permissions.permissions || []);
  if (apis.tabs && grantedPermissions.has("tabs")) {
    capabilities.push("tabs.list");
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
