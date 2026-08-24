import assert from "node:assert/strict";
import test from "node:test";

import { createDownloadHandlers } from "../src/handlers/downloads.js";
import { ErrorCode } from "../src/protocol.js";

test("download list is bounded, paginated, and strips local paths and URL secrets", async () => {
  const harness = createHarness({
    items: [
      downloadItem({
        id: 1,
        url: "https://example.com/private/file.zip?token=secret#part",
        finalUrl: "https://cdn.example.com/file.zip?signature=secret",
        filename: "/home/alice/Downloads/report.zip",
      }),
      downloadItem({ id: 2, filename: "C:\\Users\\alice\\Downloads\\private.txt" }),
      downloadItem({ id: 3, incognito: true }),
    ],
  });
  const result = await createDownloadHandlers(harness.chromeAPI).list(
    request("downloads.list", { limit: 1, allowIncognito: false }),
    signal(),
  );

  assert.deepEqual(harness.searches[0], { orderBy: ["-startTime"], limit: 10_001 });
  assert.equal(result.totalMatched, 2);
  assert.equal(result.nextCursor, "1");
  assert.equal(result.downloads[0].sourceUrl, "https://example.com/private/file.zip");
  assert.equal(result.downloads[0].finalUrl, "https://cdn.example.com/file.zip");
  assert.equal(result.downloads[0].fileName, "report.zip");
  assert.equal(result.downloads[0].pathRedacted, true);
  assert.equal(JSON.stringify(result).includes("/home/alice"), false);
  assert.equal(JSON.stringify(result).includes("secret"), false);
});

test("download create uses fixed safe options and does not return file metadata", async () => {
  const harness = createHarness();
  const result = await createDownloadHandlers(harness.chromeAPI).create(
    request("downloads.create", {
      url: "https://example.com/archive.zip?ticket=opaque",
      allowIncognito: false,
    }),
    signal(),
  );

  assert.deepEqual(harness.creates, [
    {
      url: "https://example.com/archive.zip?ticket=opaque",
      conflictAction: "uniquify",
      saveAs: false,
    },
  ]);
  assert.equal(result.downloadId, 91);
  assert.deepEqual(result.downloads, []);
  assert.equal("filename" in harness.creates[0], false);
  assert.equal("headers" in harness.creates[0], false);
});

test("download lifecycle validates state and returns basename-only status", async () => {
  const harness = createHarness({
    items: [downloadItem({ id: 7, state: "in_progress", paused: false, canResume: true })],
  });
  const handlers = createDownloadHandlers(harness.chromeAPI);

  const paused = await handlers.pause(
    request("downloads.pause", { downloadId: 7, allowIncognito: false }),
    signal(),
  );
  assert.equal(paused.operation, "pause");
  assert.equal(paused.downloads[0].fileName, "example.zip");
  assert.deepEqual(harness.lifecycle, [["pause", 7]]);

  harness.items[0].paused = true;
  await assert.rejects(
    handlers.pause(request("downloads.pause", { downloadId: 7, allowIncognito: false }), signal()),
    (error) => error.code === ErrorCode.INVALID_MESSAGE,
  );
});

test("download history erase requires confirmation and never removes the file", async () => {
  const harness = createHarness({ items: [downloadItem({ id: 8, state: "complete" })] });
  const handlers = createDownloadHandlers(harness.chromeAPI);
  await assert.rejects(
    handlers.erase(
      request("downloads.erase", { downloadId: 8, confirm: false, allowIncognito: false }),
      signal(),
    ),
    (error) => error.code === ErrorCode.CONFIRMATION_REQUIRED,
  );

  const result = await handlers.erase(
    request("downloads.erase", { downloadId: 8, confirm: true, allowIncognito: false }),
    signal(),
  );
  assert.deepEqual(harness.erases, [{ id: 8 }]);
  assert.deepEqual(result.erasedIds, [8]);
  assert.match(result.warnings[0], /not deleted/);
  assert.equal("removeFile" in harness.chromeAPI.downloads, false);
});

test("download handlers fail closed on permission, missing IDs, and incognito scope", async () => {
  const denied = createHarness({ permission: false });
  await assert.rejects(
    createDownloadHandlers(denied.chromeAPI).list(
      request("downloads.list", { limit: 50, allowIncognito: false }),
      signal(),
    ),
    (error) => error.code === ErrorCode.PERMISSION_REQUIRED,
  );
  assert.equal(denied.searches.length, 0);

  const missing = createHarness({ items: [] });
  await assert.rejects(
    createDownloadHandlers(missing.chromeAPI).get(
      request("downloads.get", { downloadId: 404, allowIncognito: false }),
      signal(),
    ),
    (error) => error.code === ErrorCode.DOWNLOAD_NOT_FOUND,
  );

  const incognito = createHarness({ items: [downloadItem({ id: 9, incognito: true })] });
  await assert.rejects(
    createDownloadHandlers(incognito.chromeAPI).cancel(
      request("downloads.cancel", { downloadId: 9, allowIncognito: false }),
      signal(),
    ),
    (error) => error.code === ErrorCode.RESTRICTED_URL,
  );
  assert.deepEqual(incognito.lifecycle, []);
});

function createHarness({ items = [], permission = true } = {}) {
  const searches = [];
  const creates = [];
  const lifecycle = [];
  const erases = [];
  return {
    items,
    searches,
    creates,
    lifecycle,
    erases,
    chromeAPI: {
      permissions: {
        async contains(details) {
          assert.deepEqual(details, { permissions: ["downloads"] });
          return permission;
        },
      },
      downloads: {
        async search(query) {
          searches.push(query);
          if (query.id !== undefined) return items.filter((item) => item.id === query.id);
          return items;
        },
        async download(options) {
          creates.push(options);
          return 91;
        },
        async pause(id) {
          lifecycle.push(["pause", id]);
        },
        async resume(id) {
          lifecycle.push(["resume", id]);
        },
        async cancel(id) {
          lifecycle.push(["cancel", id]);
        },
        async erase(query) {
          erases.push(query);
          return [query.id];
        },
      },
    },
  };
}

function downloadItem(overrides = {}) {
  return {
    id: 1,
    url: "https://example.com/example.zip",
    finalUrl: "https://example.com/example.zip",
    filename: "/home/alice/Downloads/example.zip",
    state: "in_progress",
    paused: false,
    canResume: true,
    danger: "safe",
    bytesReceived: 10,
    totalBytes: 100,
    fileSize: -1,
    exists: true,
    incognito: false,
    mime: "application/zip",
    startTime: "2026-08-24T10:00:00.000Z",
    ...overrides,
  };
}

function request(command, params) {
  return { command, params, timeoutMs: 1_000 };
}

function signal() {
  return new AbortController().signal;
}
