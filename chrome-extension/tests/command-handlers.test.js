import assert from "node:assert/strict";
import test from "node:test";

import { createBrowserHandlers } from "../src/handlers/browser.js";
import { createPageHandlers } from "../src/handlers/page.js";
import { createTabHandlers } from "../src/handlers/tabs.js";
import { ErrorCode } from "../src/protocol.js";

test("browser and tab handlers return stable domain results", async () => {
  const browser = createBrowserHandlers(() => new Date("2026-01-02T03:04:05.000Z"));
  assert.deepEqual(browser.ping(), { pong: true, time: "2026-01-02T03:04:05.000Z" });

  const tabs = createTabHandlers({
    tabs: {
      query: async () => [{
        id: 7,
        windowId: 3,
        index: 1,
        active: true,
        pinned: false,
        mutedInfo: { muted: true },
        status: "complete",
        title: "Example",
        url: "https://example.com/",
        favIconUrl: "https://example.com/favicon.ico",
        incognito: false,
      }],
    },
  });
  const result = await tabs.list();
  assert.equal(result.totalCount, 1);
  assert.equal(result.tabs[0].id, 7);
  assert.equal(result.tabs[0].muted, true);
});

test("page handlers preserve addressing and structured content errors", async () => {
  const sent = [];
  const chromeAPI = {
    tabs: {
      get: async () => ({ id: 7, url: "https://example.com/page" }),
      sendMessage: async (...args) => {
        sent.push(args);
        return {
          error: {
            code: ErrorCode.ELEMENT_NOT_FOUND,
            message: "No matching element",
            details: { matches: 0 },
          },
        };
      },
    },
    permissions: { contains: async () => true },
    scripting: { executeScript: async () => undefined },
    webNavigation: { getFrame: async () => ({ documentId: "document-1" }) },
  };
  const page = createPageHandlers(chromeAPI);
  const request = {
    command: "page.click",
    target: { tabId: 7, frameId: 2, documentId: "document-1" },
    params: { selector: "button" },
  };

  await assert.rejects(
    page.click(request, new AbortController().signal),
    (error) => error.code === ErrorCode.ELEMENT_NOT_FOUND
      && error.details.matches === 0,
  );
  assert.equal(sent.length, 1);
  assert.equal(sent[0][0], 7);
  assert.deepEqual(sent[0][2], { frameId: 2 });
});
