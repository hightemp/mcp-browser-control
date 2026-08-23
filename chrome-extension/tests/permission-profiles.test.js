import assert from "node:assert/strict";
import test from "node:test";

import {
  enabledPermissionProfileNames,
  permissionProfileDefinitions,
  permissionProfileStates,
  permissionRemovalFor,
  permissionRequestFor,
} from "../src/permission-profiles.js";

const corePermissions = ["alarms", "scripting", "storage", "tabs", "webNavigation"];

test("permission profiles expose the fixed Core, Observe, Debug, and Personal matrix", () => {
  assert.deepEqual(
    permissionProfileDefinitions().map((profile) => profile.id),
    ["core", "observe", "debug", "personal"],
  );
  assert.equal(permissionProfileDefinitions()[0].optional, false);
  assert.deepEqual(permissionProfileDefinitions()[1].origins, ["http://*/*", "https://*/*"]);
  assert.deepEqual(permissionProfileDefinitions()[2].permissions, ["debugger"]);
  assert.equal(permissionProfileDefinitions()[3].dependencies.includes("observe"), true);
});

test("profile state distinguishes disabled, partial, and enabled grants", () => {
  const states = permissionProfileStates({
    permissions: [...corePermissions, "debugger", "bookmarks", "cookies"],
    origins: [],
  });
  assert.deepEqual(states.map(({ id, state }) => [id, state]), [
    ["core", "enabled"],
    ["observe", "disabled"],
    ["debug", "enabled"],
    ["personal", "partial"],
  ]);

  assert.equal(permissionProfileStates({
    permissions: corePermissions,
    origins: ["https://example.com/*"],
  })[1].state, "partial");

  assert.deepEqual(
    enabledPermissionProfileNames({
      permissions: [
        ...corePermissions,
        "bookmarks",
        "browsingData",
        "clipboardRead",
        "clipboardWrite",
        "cookies",
        "downloads",
        "history",
        "sessions",
      ],
      origins: ["http://*/*", "https://*/*"],
    }),
    ["Core", "Observe", "Personal data"],
  );
});

test("Personal data requests Observe dependency but removes only its own grants", () => {
  const request = permissionRequestFor("personal");
  assert.deepEqual(request.origins, ["http://*/*", "https://*/*"]);
  assert.equal(request.permissions.includes("cookies"), true);
  assert.equal(request.permissions.includes("history"), true);

  const removal = permissionRemovalFor("personal");
  assert.equal("origins" in removal, false);
  assert.equal(removal.permissions.includes("cookies"), true);
  assert.throws(() => permissionRequestFor("core"), /Unknown optional permission profile/);
});
