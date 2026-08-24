import assert from "node:assert/strict";
import test from "node:test";

import { detectCapabilities } from "../src/capabilities.js";
import { COMMAND_NAMES } from "../src/command-router.js";

test("capability detection uses browser version, APIs, and permissions", () => {
  assert.deepEqual(
    detectCapabilities({
      browserVersion: "116.0.0.0",
      apis: {
        tabs: true,
        captureVisibleTab: true,
        debugger: true,
        tabGrouping: true,
        tabGroups: true,
        sessions: true,
        scripting: true,
        webNavigation: true,
        frameTree: true,
        windows: true,
      },
      permissions: {
        permissions: ["tabs", "scripting", "debugger", "tabGroups", "sessions"],
        origins: ["https://example.com/*"],
      },
      featureFlags: { pageAutomation: true },
    }),
    COMMAND_NAMES,
  );
});

test("capability detection removes unavailable or disabled commands", () => {
  assert.deepEqual(
    detectCapabilities({
      browserVersion: "115",
      apis: { tabs: true, scripting: true },
      permissions: {
        permissions: ["tabs", "scripting"],
        origins: ["https://example.com/*"],
      },
    }),
    ["browser.ping"],
  );
  assert.deepEqual(
    detectCapabilities({
      browserVersion: "116",
      apis: { tabs: false, scripting: true },
      permissions: {
        permissions: ["tabs", "scripting"],
        origins: ["https://example.com/*"],
      },
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
    tabCapabilitiesWithoutStop(),
  );

  assert.deepEqual(
    detectCapabilities({
      browserVersion: "116",
      apis: { tabs: true, scripting: true },
      permissions: {
        permissions: ["tabs"],
        origins: ["https://example.com/*"],
      },
      featureFlags: { pageAutomation: true },
    }),
    tabCapabilitiesWithoutStop(),
  );

  assert.deepEqual(
    detectCapabilities({
      browserVersion: "116",
      apis: {
        tabs: true,
        tabGrouping: true,
        tabGroups: false,
        sessions: false,
      },
      permissions: { permissions: ["tabs"], origins: [] },
    }),
    [...tabCapabilitiesWithoutStop(), "tabs.group", "tabs.ungroup"],
  );

  const withoutDocumentIdentity = detectCapabilities({
    browserVersion: "116",
    apis: { tabs: true, scripting: true, webNavigation: false },
    permissions: {
      permissions: ["tabs", "scripting"],
      origins: ["https://example.com/*"],
    },
  });
  assert.equal(withoutDocumentIdentity.includes("tabs.stop"), true);
  assert.equal(
    withoutDocumentIdentity.some((name) => name.startsWith("page.")),
    false,
  );

  const withoutFrameTree = detectCapabilities({
    browserVersion: "116",
    apis: {
      tabs: true,
      scripting: true,
      webNavigation: true,
      frameTree: false,
    },
    permissions: {
      permissions: ["tabs", "scripting"],
      origins: ["https://example.com/*"],
    },
  });
  assert.equal(withoutFrameTree.includes("page.getHTML"), true);
  assert.equal(withoutFrameTree.includes("page.info"), false);
  assert.equal(withoutFrameTree.includes("page.screenshot"), false);
  assert.equal(withoutFrameTree.includes("page.printToPDF"), false);

  const withDebugger = detectCapabilities({
    browserVersion: "125",
    apis: { tabs: true, scripting: true, webNavigation: true, debugger: true },
    permissions: {
      permissions: ["tabs", "scripting", "debugger"],
      origins: ["https://example.com/*"],
    },
  });
  assert.equal(withDebugger.includes("page.printToPDF"), true);
});

function tabCapabilitiesWithoutStop() {
  return COMMAND_NAMES.filter(
    (capability) =>
      capability === "browser.ping" ||
      (capability.startsWith("tabs.") &&
        !["tabs.stop", "tabs.group", "tabs.ungroup"].includes(capability)),
  );
}
