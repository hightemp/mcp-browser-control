import assert from "node:assert/strict";
import test from "node:test";

import { badgeForStatus, permissionProfilesFor } from "../src/status.js";

test("badge presentation distinguishes connection states", () => {
  const connected = badgeForStatus("connected");
  const disconnected = badgeForStatus("disconnected");
  const error = badgeForStatus("error");

  assert.equal(connected.text, "ON");
  assert.equal(disconnected.text, "OFF");
  assert.equal(error.text, "!");
  assert.notEqual(connected.color, disconnected.color);
  assert.notEqual(disconnected.color, error.color);
  assert.deepEqual(badgeForStatus("unknown"), disconnected);
});

test("permission profile summary reflects optional website access", () => {
  assert.deepEqual(permissionProfilesFor({ permissions: ["tabs"] }), ["Core"]);
  assert.deepEqual(
    permissionProfilesFor({ permissions: ["tabs"], origins: ["https://example.com/*"] }),
    ["Core", "Website access"],
  );
});
