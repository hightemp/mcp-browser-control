const PROFILE_DEFINITIONS = Object.freeze([
  Object.freeze({
    id: "core",
    name: "Core",
    description: "Connects the browser, manages windows and tabs, and enables invoked captures.",
    permissions: Object.freeze([
      "activeTab",
      "alarms",
      "scripting",
      "storage",
      "tabs",
      "webNavigation",
    ]),
    origins: Object.freeze([]),
    dependencies: Object.freeze([]),
    tools: Object.freeze(["browser", "windows", "tabs", "invoked viewport screenshots"]),
    warning: "Installed with the extension. Chrome may warn about tabs and navigation history.",
    optional: false,
  }),
  Object.freeze({
    id: "observe",
    name: "Observe",
    description: "Reads and interacts with HTTP and HTTPS pages.",
    permissions: Object.freeze(["webRequest"]),
    origins: Object.freeze(["http://*/*", "https://*/*"]),
    dependencies: Object.freeze([]),
    tools: Object.freeze([
      "page inspection",
      "page actions",
      "page waits",
      "tab stop",
      "console metadata",
    ]),
    warning:
      "Chrome will ask to read and change data on visited websites and observe network activity.",
    optional: true,
  }),
  Object.freeze({
    id: "debug",
    name: "Debug",
    description: "Allows advanced CDP-backed diagnostics and emulation.",
    permissions: Object.freeze(["debugger"]),
    origins: Object.freeze([]),
    dependencies: Object.freeze([]),
    tools: Object.freeze([
      "PDF",
      "full-page and element screenshots",
      "trusted root-document input",
      "accessibility",
      "console enrichment",
      "emulation",
      "isolated evaluation",
      "reviewed raw CDP",
      "performance",
    ]),
    warning: "Chrome will warn that the extension can access the page debugger backend.",
    optional: true,
  }),
  Object.freeze({
    id: "personal",
    name: "Personal data",
    description: "Allows access to personal browser data and destructive cleanup operations.",
    permissions: Object.freeze([
      "bookmarks",
      "browsingData",
      "clipboardRead",
      "clipboardWrite",
      "cookies",
      "downloads",
      "history",
      "sessions",
      "tabGroups",
    ]),
    origins: Object.freeze([]),
    dependencies: Object.freeze(["observe"]),
    tools: Object.freeze([
      "cookies",
      "origin storage",
      "downloads",
      "sessions",
      "tab groups",
      "bookmarks",
      "history",
      "clipboard",
    ]),
    warning:
      "Chrome will list each personal-data category and tab-group access. Bulk deletion still requires confirmation.",
    optional: true,
  }),
]);

const DEFINITIONS_BY_ID = new Map(PROFILE_DEFINITIONS.map((profile) => [profile.id, profile]));

export function permissionProfileDefinitions() {
  return PROFILE_DEFINITIONS;
}

export function permissionProfileStates(grants = {}) {
  const grantedPermissions = new Set(grants.permissions || []);
  const grantedOrigins = new Set(grants.origins || []);
  const states = new Map();

  for (const profile of PROFILE_DEFINITIONS) {
    const grantedProfilePermissions = profile.permissions.filter((permission) =>
      grantedPermissions.has(permission),
    );
    const grantedProfileOrigins =
      profile.origins.length > 0 ? [...grantedOrigins].filter(isWebsiteOrigin) : [];
    const exactOriginsGranted = profile.origins.filter((origin) => grantedOrigins.has(origin));
    const itemCount = profile.permissions.length + profile.origins.length;
    const grantedCount = grantedProfilePermissions.length + exactOriginsGranted.length;
    const ownComplete = grantedCount === itemCount;
    const hasSomeAccess = grantedProfilePermissions.length > 0 || grantedProfileOrigins.length > 0;
    const dependenciesComplete = profile.dependencies.every(
      (dependency) => states.get(dependency)?.state === "enabled",
    );
    let state = "disabled";
    if (ownComplete && dependenciesComplete) {
      state = "enabled";
    } else if (hasSomeAccess || (ownComplete && !dependenciesComplete)) {
      state = "partial";
    }
    states.set(profile.id, {
      ...profile,
      state,
      grantedCount,
      itemCount,
      grantedOrigins: grantedProfileOrigins,
    });
  }
  return [...states.values()];
}

export function permissionRequestFor(profileId) {
  const profile = requireOptionalProfile(profileId);
  const profiles = collectWithDependencies(profile);
  return compactRequest({
    permissions: unique(profiles.flatMap((candidate) => candidate.permissions)),
    origins: unique(profiles.flatMap((candidate) => candidate.origins)),
  });
}

export function permissionRemovalFor(profileId) {
  const profile = requireOptionalProfile(profileId);
  return compactRequest({
    permissions: [...profile.permissions],
    origins: [...profile.origins],
  });
}

export function enabledPermissionProfileNames(grants = {}) {
  return permissionProfileStates(grants)
    .filter((profile) => profile.state === "enabled")
    .map((profile) => profile.name);
}

function requireOptionalProfile(profileId) {
  const profile = DEFINITIONS_BY_ID.get(profileId);
  if (!profile || !profile.optional) {
    throw new Error(`Unknown optional permission profile "${profileId}"`);
  }
  return profile;
}

function collectWithDependencies(profile, collected = new Map()) {
  if (collected.has(profile.id)) {
    return [...collected.values()];
  }
  for (const dependencyId of profile.dependencies) {
    collectWithDependencies(DEFINITIONS_BY_ID.get(dependencyId), collected);
  }
  collected.set(profile.id, profile);
  return [...collected.values()];
}

function compactRequest(request) {
  return Object.fromEntries(Object.entries(request).filter(([, values]) => values.length > 0));
}

function unique(values) {
  return [...new Set(values)];
}

function isWebsiteOrigin(origin) {
  return (
    typeof origin === "string" && (origin.startsWith("http://") || origin.startsWith("https://"))
  );
}
