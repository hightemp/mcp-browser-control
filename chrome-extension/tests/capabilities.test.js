import assert from "node:assert/strict";
import test from "node:test";

import { detectCapabilities } from "../src/capabilities.js";

test("capability detection uses browser version, APIs, and permissions", () => {
  assert.deepEqual(
    detectCapabilities({
      browserVersion: "116.0.0.0",
      apis: { tabs: true, scripting: true },
      permissions: { permissions: ["tabs", "scripting"], origins: ["https://example.com/*"] },
      featureFlags: { pageAutomation: true },
    }),
    [
      "browser.ping",
      "tabs.list",
      "page.getHTML",
      "page.getHTMLBySelector",
      "page.click",
      "page.fill",
    ],
  );
});

test("capability detection removes unavailable or disabled commands", () => {
  assert.deepEqual(
    detectCapabilities({
      browserVersion: "115",
      apis: { tabs: true, scripting: true },
      permissions: { permissions: ["tabs", "scripting"], origins: ["https://example.com/*"] },
    }),
    ["browser.ping"],
  );
  assert.deepEqual(
    detectCapabilities({
      browserVersion: "116",
      apis: { tabs: false, scripting: true },
      permissions: { permissions: ["tabs", "scripting"], origins: ["https://example.com/*"] },
      featureFlags: { pageAutomation: false },
    }),
    ["browser.ping"],
  );
  assert.deepEqual(
    detectCapabilities({
      browserVersion: "",
      apis: { tabs: true, scripting: true },
      permissions: { permissions: ["tabs"], origins: [] },
    }),
    ["browser.ping", "tabs.list"],
  );

  assert.deepEqual(
    detectCapabilities({
      browserVersion: "116",
      apis: { tabs: true, scripting: true },
      permissions: { permissions: ["tabs"], origins: ["https://example.com/*"] },
      featureFlags: { pageAutomation: true },
    }),
    ["browser.ping", "tabs.list"],
  );
});
