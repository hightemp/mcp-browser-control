import assert from "node:assert/strict";
import test from "node:test";

import { detectBrowserEngineVersion, detectBrowserInfo } from "../src/browser-info.js";

test("browser detection separates known Chromium product brands from the engine", () => {
  // Brand spellings come from browser vendor UA-CH documentation and examples.
  // Versions are representative and intentionally differ where products use
  // their own release numbering instead of Chromium's release number.
  const testCases = [
    {
      browser: "Google Chrome",
      productBrand: "Google Chrome",
      productVersion: "151",
      chromiumVersion: "151",
      greaseBrand: " Not;A Brand",
    },
    {
      browser: "Microsoft Edge",
      productBrand: "Microsoft Edge",
      productVersion: "120",
      chromiumVersion: "120",
      greaseBrand: "Not_A Brand",
    },
    {
      browser: "Brave",
      productBrand: "Brave",
      productVersion: "137",
      chromiumVersion: "137",
      greaseBrand: "Not/A)Brand",
    },
    {
      browser: "Opera",
      productBrand: "Opera",
      productVersion: "83",
      chromiumVersion: "97",
      greaseBrand: ";Not A Brand",
    },
    {
      browser: "Vivaldi",
      productBrand: "Vivaldi",
      productVersion: "146",
      chromiumVersion: "146",
      greaseBrand: "Not.A/Brand",
    },
    {
      browser: "YaBrowser",
      productBrand: "YaBrowser",
      productVersion: "26.4",
      chromiumVersion: "150",
      greaseBrand: "Not;A=Brand",
    },
  ];

  for (const testCase of testCases) {
    const navigatorLike = {
      userAgentData: {
        brands: [
          { brand: testCase.greaseBrand, version: "99" },
          { brand: "Chromium", version: testCase.chromiumVersion },
          { brand: testCase.productBrand, version: testCase.productVersion },
        ],
      },
    };

    assert.deepEqual(
      detectBrowserInfo(navigatorLike),
      { name: testCase.browser, version: testCase.productVersion },
      testCase.browser,
    );
    assert.equal(
      detectBrowserEngineVersion(navigatorLike),
      testCase.chromiumVersion,
      testCase.browser,
    );
  }
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
    detectBrowserInfo({
      userAgentData: { brands: [{ brand: "Custom Browser", version: "151" }] },
    }),
    { name: "Custom Browser", version: "151" },
  );
});

test("browser engine detection falls back to the Chrome user-agent token", () => {
  assert.equal(
    detectBrowserEngineVersion({
      userAgent: "Mozilla/5.0 Chrome/150.0.0.0 Safari/537.36 YaBrowser/26.4.0.0",
    }),
    "150.0.0.0",
  );
});
