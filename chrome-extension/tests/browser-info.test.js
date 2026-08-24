import assert from "node:assert/strict";
import test from "node:test";

import { detectBrowserInfo } from "../src/browser-info.js";

test("browser detection prefers the product brand over Chromium and ignores GREASE", () => {
  assert.deepEqual(
    detectBrowserInfo({
      userAgentData: {
        brands: [
          { brand: "Not_A Brand", version: "99" },
          { brand: "Not;A=Brand", version: "99" },
          { brand: "Chromium", version: "151" },
          { brand: "Microsoft Edge", version: "151" },
        ],
      },
    }),
    { name: "Microsoft Edge", version: "151" },
  );
  assert.deepEqual(
    detectBrowserInfo({
      userAgentData: {
        brands: [
          { brand: "Chromium", version: "151" },
          { brand: "Google Chrome", version: "151" },
        ],
      },
    }),
    { name: "Google Chrome", version: "151" },
  );
});

test("browser detection falls back to desktop Chromium user-agent tokens", () => {
  for (const testCase of [
    {
      userAgent: "Mozilla/5.0 Chrome/151.0.0.0 Safari/537.36 Edg/151.0.0.0",
      want: { name: "Microsoft Edge", version: "151.0.0.0" },
    },
    {
      userAgent: "Mozilla/5.0 Chrome/151.0.0.0 Safari/537.36 OPR/117.0.0.0",
      want: { name: "Opera", version: "117.0.0.0" },
    },
    {
      userAgent: "Mozilla/5.0 Chrome/151.0.0.0 Safari/537.36",
      want: { name: "Google Chrome", version: "151.0.0.0" },
    },
    { userAgent: "unknown", want: { name: "Chromium", version: "" } },
  ]) {
    assert.deepEqual(detectBrowserInfo({ userAgent: testCase.userAgent }), testCase.want);
  }
});

test("browser detection preserves an unknown Chromium product brand", () => {
  assert.deepEqual(
    detectBrowserInfo({ userAgentData: { brands: [{ brand: "Brave", version: "151" }] } }),
    { name: "Brave", version: "151" },
  );
});
