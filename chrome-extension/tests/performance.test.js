import assert from "node:assert/strict";
import test from "node:test";

import { createPerformanceHandlers } from "../src/handlers/performance.js";

const target = { tabId: 7, frameId: 0, documentId: "document-1" };
const captureParams = { kind: "coverage", durationMs: 100, maxBytes: 256 * 1024 };

test("performance metrics use one exact read-only lease and validate values", async () => {
  let options;
  const handlers = createPerformanceHandlers(createChromeAPI(), {
    cdpSessions: {
      async withSession(debuggee, leaseOptions, operation) {
        assert.deepEqual(debuggee, { tabId: 7 });
        options = leaseOptions;
        return operation({
          async sendCommand(method, params) {
            assert.equal(method, "Performance.getMetrics");
            assert.deepEqual(params, {});
            return { metrics: [{ name: "Timestamp", value: 1.5 }] };
          },
        });
      },
    },
  });

  const result = await handlers.metrics(request("performance.metrics", {}), signal());
  assert.deepEqual(result, {
    tabId: 7,
    documentId: "document-1",
    metrics: [{ name: "Timestamp", value: 1.5 }],
    warnings: [],
  });
  assert.deepEqual(options.domains, ["Performance"]);
  assert.deepEqual(options.commands, ["Performance.getMetrics"]);
  assert.equal(options.events, undefined);
});

test("trace capture owns completion, stream reads, and close through an exact lease", async () => {
  const calls = [];
  let options;
  const traceJSON = JSON.stringify({ traceEvents: [{ name: "navigationStart", ts: 1 }] });
  const handlers = createPerformanceHandlers(createChromeAPI(), {
    cdpSessions: {
      async withSession(_debuggee, leaseOptions, operation) {
        options = leaseOptions;
        return operation({
          async sendCommand(method, params) {
            calls.push([method, params]);
            if (method === "Tracing.end") {
              await leaseOptions.onEvent({
                method: "Tracing.tracingComplete",
                params: { stream: "trace-stream", traceFormat: "json", streamCompression: "none" },
              });
            }
            if (method === "IO.read") return { data: traceJSON, eof: true };
            return {};
          },
        });
      },
    },
  });

  const result = await handlers.capture(
    request("performance.capture", { ...captureParams, kind: "trace" }, 2_000),
    signal(),
  );
  assert.equal(result.kind, "trace");
  assert.equal(result.mimeType, "application/json");
  assert.equal(result.byteLength, Buffer.byteLength(traceJSON));
  assert.deepEqual(JSON.parse(Buffer.from(result.dataBase64, "base64").toString()), {
    traceEvents: [{ name: "navigationStart", ts: 1 }],
  });
  assert.deepEqual(options.domains, ["Tracing", "IO"]);
  assert.deepEqual(options.commands, ["Tracing.start", "Tracing.end", "IO.read", "IO.close"]);
  assert.deepEqual(options.events, ["Tracing.tracingComplete"]);
  assert.deepEqual(calls[0], [
    "Tracing.start",
    {
      categories: "blink.user_timing,devtools.timeline,loading,v8.execute",
      transferMode: "ReturnAsStream",
      streamFormat: "json",
      streamCompression: "none",
    },
  ]);
  assert.equal(
    calls.some(([method]) => method === "IO.close"),
    true,
  );
});

test("coverage, CPU profile, and audits use separate bounded session contracts", async () => {
  const cases = [
    {
      kind: "coverage",
      domains: ["Profiler"],
      commands: [
        "Profiler.enable",
        "Profiler.startPreciseCoverage",
        "Profiler.takePreciseCoverage",
        "Profiler.stopPreciseCoverage",
        "Profiler.disable",
      ],
      result(method) {
        if (method === "Profiler.takePreciseCoverage") {
          return {
            timestamp: 1.5,
            result: [{ scriptId: "1", url: "https://example.com/app.js", functions: [] }],
          };
        }
        return {};
      },
    },
    {
      kind: "cpuProfile",
      domains: ["Profiler"],
      commands: [
        "Profiler.enable",
        "Profiler.setSamplingInterval",
        "Profiler.start",
        "Profiler.stop",
        "Profiler.disable",
      ],
      result(method) {
        return method === "Profiler.stop"
          ? { profile: { nodes: [], startTime: 1, endTime: 2, samples: [], timeDeltas: [] } }
          : {};
      },
    },
    {
      kind: "audits",
      domains: ["Audits"],
      commands: ["Audits.enable", "Audits.disable"],
      result() {
        return {};
      },
    },
  ];

  for (const testCase of cases) {
    let options;
    const calls = [];
    const handlers = createPerformanceHandlers(createChromeAPI(), {
      cdpSessions: {
        async withSession(_debuggee, leaseOptions, operation) {
          options = leaseOptions;
          return operation({
            async sendCommand(method, params) {
              calls.push([method, params]);
              if (testCase.kind === "audits" && method === "Audits.enable") {
                await leaseOptions.onEvent({
                  method: "Audits.issueAdded",
                  params: { issue: { code: "CookieIssue", details: {} } },
                });
              }
              return testCase.result(method);
            },
          });
        },
      },
    });

    const result = await handlers.capture(
      request("performance.capture", { ...captureParams, kind: testCase.kind }, 2_000),
      signal(),
    );
    const artifact = JSON.parse(Buffer.from(result.dataBase64, "base64").toString());
    assert.equal(artifact.kind, testCase.kind);
    assert.deepEqual(options.domains, testCase.domains);
    assert.deepEqual(options.commands, testCase.commands);
    assert.deepEqual(
      options.events,
      testCase.kind === "audits" ? ["Audits.issueAdded"] : undefined,
    );
    assert.deepEqual(
      calls.map(([method]) => method),
      testCase.kind === "coverage"
        ? [
            "Profiler.enable",
            "Profiler.startPreciseCoverage",
            "Profiler.takePreciseCoverage",
            "Profiler.stopPreciseCoverage",
            "Profiler.disable",
          ]
        : testCase.kind === "cpuProfile"
          ? [
              "Profiler.enable",
              "Profiler.setSamplingInterval",
              "Profiler.start",
              "Profiler.stop",
              "Profiler.disable",
            ]
          : ["Audits.enable", "Audits.disable"],
    );
    if (testCase.kind === "audits") {
      assert.equal(artifact.issueCount, 1);
      assert.equal(artifact.issues[0].code, "CookieIssue");
    }
  }
});

test("performance capture rejects missing gates and stale documents", async () => {
  let sessions = 0;
  const cdpSessions = {
    async withSession(_debuggee, _options, operation) {
      sessions += 1;
      return operation({
        async sendCommand() {
          return { metrics: [] };
        },
      });
    },
  };
  const missingDebug = createPerformanceHandlers(createChromeAPI({ debuggerGranted: false }), {
    cdpSessions,
  });
  await assert.rejects(
    missingDebug.metrics(request("performance.metrics", {}), signal()),
    (error) => error.code === "PERMISSION_REQUIRED",
  );
  const missingSite = createPerformanceHandlers(createChromeAPI({ siteGranted: false }), {
    cdpSessions,
  });
  await assert.rejects(
    missingSite.metrics(request("performance.metrics", {}), signal()),
    (error) => error.code === "PERMISSION_REQUIRED",
  );
  const stale = createPerformanceHandlers(
    createChromeAPI({ documentIds: ["document-1", "document-1", "document-2"] }),
    { cdpSessions },
  );
  await assert.rejects(
    stale.metrics(request("performance.metrics", {}), signal()),
    (error) => error.code === "STALE_TARGET",
  );
  assert.equal(sessions, 1);
});

test("performance capture cleans up profiler state when cancelled", async () => {
  const calls = [];
  const controller = new AbortController();
  const handlers = createPerformanceHandlers(createChromeAPI(), {
    cdpSessions: {
      async withSession(_debuggee, _options, operation) {
        return operation({
          async sendCommand(method) {
            calls.push(method);
            if (method === "Profiler.start") controller.abort();
            return {};
          },
        });
      },
    },
  });
  await assert.rejects(
    handlers.capture(
      request("performance.capture", { ...captureParams, kind: "cpuProfile" }, 2_000),
      controller.signal,
    ),
    (error) => error.code === "CANCELLED",
  );
  assert.deepEqual(calls.slice(-2), ["Profiler.stop", "Profiler.disable"]);
});

test("performance handler rejects invalid shapes, bounds, and oversized artifacts", async () => {
  const never = {
    async withSession() {
      assert.fail("invalid parameters must not acquire a lease");
    },
  };
  const handlers = createPerformanceHandlers(createChromeAPI(), { cdpSessions: never });
  for (const value of [
    { kind: "heapSnapshot", durationMs: 100, maxBytes: 100_000 },
    { kind: "trace", durationMs: 99, maxBytes: 100_000 },
    { kind: "coverage", durationMs: 100, maxBytes: 1 },
    { kind: "audits", durationMs: 100, maxBytes: 100_000, categories: ["*"] },
  ]) {
    await assert.rejects(
      handlers.capture(request("performance.capture", value, 2_000), signal()),
      (error) => ["INVALID_MESSAGE", "INVALID_COMMAND"].includes(error.code),
    );
  }

  const oversized = createPerformanceHandlers(createChromeAPI(), {
    cdpSessions: {
      async withSession(_debuggee, _options, operation) {
        return operation({
          async sendCommand(method) {
            if (method === "Profiler.stop") {
              return { profile: { nodes: [{ callFrame: { functionName: "x".repeat(70_000) } }] } };
            }
            return {};
          },
        });
      },
    },
  });
  await assert.rejects(
    oversized.capture(
      request(
        "performance.capture",
        { kind: "cpuProfile", durationMs: 100, maxBytes: 64 * 1024 },
        2_000,
      ),
      signal(),
    ),
    (error) => error.code === "PAYLOAD_TOO_LARGE",
  );

  const boundedAudits = createPerformanceHandlers(createChromeAPI(), {
    cdpSessions: {
      async withSession(_debuggee, leaseOptions, operation) {
        return operation({
          async sendCommand(method) {
            if (method === "Audits.enable") {
              await leaseOptions.onEvent({
                method: "Audits.issueAdded",
                params: { issue: { code: "LargeIssue", details: { value: "x".repeat(70_000) } } },
              });
            }
            return {};
          },
        });
      },
    },
  });
  const auditResult = await boundedAudits.capture(
    request("performance.capture", { kind: "audits", durationMs: 100, maxBytes: 64 * 1024 }, 2_000),
    signal(),
  );
  const auditArtifact = JSON.parse(Buffer.from(auditResult.dataBase64, "base64").toString());
  assert.equal(auditArtifact.issueCount, 0);
  assert.equal(auditArtifact.truncated, true);
  assert.equal(auditResult.warnings.length, 1);
});

function request(command, params, timeoutMs = 1_000) {
  return { requestId: `${command}-1`, command, target, params, timeoutMs };
}

function signal() {
  return new AbortController().signal;
}

function createChromeAPI({ debuggerGranted = true, siteGranted = true, documentIds } = {}) {
  const documents = [...(documentIds || ["document-1"])];
  let documentIndex = 0;
  return {
    tabs: {
      async get(tabId) {
        assert.equal(tabId, 7);
        return { id: 7, url: "https://example.com/page" };
      },
      async query() {
        return [{ id: 7, url: "https://example.com/page" }];
      },
    },
    permissions: {
      async contains(value) {
        if (value.permissions) return debuggerGranted;
        if (value.origins) return siteGranted;
        return false;
      },
    },
    webNavigation: {
      async getFrame() {
        const documentId = documents[Math.min(documentIndex, documents.length - 1)];
        documentIndex += 1;
        return { documentId };
      },
    },
  };
}
