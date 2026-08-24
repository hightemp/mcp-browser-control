import assert from "node:assert/strict";
import test from "node:test";

import { createPersonalDataHandlers } from "../src/handlers/personal-data.js";
import { ErrorCode } from "../src/protocol.js";

test("history search and visits are bounded, paginated, and credential-safe", async () => {
  const harness = createHarness();
  const handlers = createPersonalDataHandlers(harness.chromeAPI).history;
  const page = await handlers.search(
    request("history.search", { text: "docs", limit: 1 }),
    signal(),
  );

  assert.deepEqual(harness.historySearches, [{ text: "docs", maxResults: 10_001 }]);
  assert.equal(page.kind, "history");
  assert.equal(page.totalMatched, 2);
  assert.equal(page.nextCursor, "1");
  assert.equal(page.items[0].url, "https://example.com/private?token=%5BREDACTED%5D");
  assert.equal(JSON.stringify(page).includes("alice"), false);

  const visits = await handlers.getVisits(
    request("history.getVisits", { url: "https://example.com/private", limit: 50 }),
    signal(),
  );
  assert.equal(visits.visits[0].transition, "link");
});

test("history bulk mutations require confirmation and use distinct browser methods", async () => {
  const harness = createHarness();
  const handlers = createPersonalDataHandlers(harness.chromeAPI).history;

  await assert.rejects(
    handlers.deleteRange(
      request("history.deleteRange", { startTime: 10, endTime: 20, confirm: false }),
      signal(),
    ),
    (error) => error.code === ErrorCode.CONFIRMATION_REQUIRED,
  );
  const range = await handlers.deleteRange(
    request("history.deleteRange", { startTime: 10, endTime: 20, confirm: true }),
    signal(),
  );
  const all = await handlers.deleteAll(request("history.deleteAll", { confirm: true }), signal());

  assert.deepEqual(harness.historyRanges, [{ startTime: 10, endTime: 20 }]);
  assert.equal(harness.historyClears, 1);
  assert.equal(range.scope, "range");
  assert.equal(all.scope, "all");
});

test("bookmark reads paginate and mutations preserve typed browser arguments", async () => {
  const harness = createHarness();
  const handlers = createPersonalDataHandlers(harness.chromeAPI).bookmarks;
  const listed = await handlers.list(
    request("bookmarks.list", { parentId: "1", limit: 1 }),
    signal(),
  );
  assert.equal(listed.bookmarks.length, 1);
  assert.equal(listed.nextCursor, "1");
  assert.deepEqual(harness.bookmarkChildren, ["1"]);

  const created = await handlers.create(
    request("bookmarks.create", {
      title: "Example",
      url: "https://example.com/",
      parentId: "1",
      index: 2,
    }),
    signal(),
  );
  assert.equal(created.operation, "create");
  assert.deepEqual(harness.bookmarkCreates, [
    { title: "Example", url: "https://example.com/", parentId: "1", index: 2 },
  ]);
});

test("recursive bookmark removal requires confirmation before browser access", async () => {
  const harness = createHarness();
  const handlers = createPersonalDataHandlers(harness.chromeAPI).bookmarks;
  await assert.rejects(
    handlers.remove(
      request("bookmarks.remove", { bookmarkId: "folder", recursive: true }),
      signal(),
    ),
    (error) => error.code === ErrorCode.CONFIRMATION_REQUIRED,
  );
  assert.deepEqual(harness.bookmarkGets, []);

  const removed = await handlers.remove(
    request("bookmarks.remove", { bookmarkId: "folder", recursive: true, confirm: true }),
    signal(),
  );
  assert.deepEqual(harness.bookmarkTrees, ["folder"]);
  assert.equal(removed.operation, "remove_tree");
});

test("reading-list operations are paginated, typed, and permission-gated", async () => {
  const harness = createHarness();
  const handlers = createPersonalDataHandlers(harness.chromeAPI).readingList;
  const listed = await handlers.list(
    request("readingList.list", { hasBeenRead: false, limit: 1 }),
    signal(),
  );
  assert.equal(listed.entries[0].url, "https://example.com/newer");
  assert.equal(listed.nextCursor, "1");

  const added = await handlers.add(
    request("readingList.add", {
      url: "https://example.com/article",
      title: "Article",
      hasBeenRead: false,
    }),
    signal(),
  );
  assert.equal(added.operation, "add");
  assert.deepEqual(harness.readingAdds, [
    { url: "https://example.com/article", title: "Article", hasBeenRead: false },
  ]);

  const denied = createHarness({ permissions: [] });
  await assert.rejects(
    createPersonalDataHandlers(denied.chromeAPI).readingList.list(
      request("readingList.list", { limit: 50 }),
      signal(),
    ),
    (error) => error.code === ErrorCode.PERMISSION_REQUIRED,
  );
  assert.equal(denied.readingQueries.length, 0);
});

test("single history, bookmark, and reading-list mutations stay exact and typed", async () => {
  const harness = createHarness();
  const handlers = createPersonalDataHandlers(harness.chromeAPI);

  const deleted = await handlers.history.deleteUrl(
    request("history.deleteUrl", { url: "https://example.com/private", confirm: true }),
    signal(),
  );
  assert.equal(deleted.deletedCount, 1);
  assert.deepEqual(harness.historyDeletes, [{ url: "https://example.com/private" }]);

  await handlers.bookmarks.update(
    request("bookmarks.update", { bookmarkId: "2", title: "Updated" }),
    signal(),
  );
  await handlers.bookmarks.move(
    request("bookmarks.move", { bookmarkId: "2", parentId: "3", index: 1 }),
    signal(),
  );
  await handlers.bookmarks.remove(
    request("bookmarks.remove", { bookmarkId: "2", recursive: false }),
    signal(),
  );
  assert.deepEqual(harness.bookmarkUpdates, [["2", { title: "Updated" }]]);
  assert.deepEqual(harness.bookmarkMoves, [["2", { parentId: "3", index: 1 }]]);
  assert.deepEqual(harness.bookmarkRemoves, ["2"]);

  await handlers.readingList.update(
    request("readingList.update", {
      url: "https://example.com/article",
      hasBeenRead: true,
    }),
    signal(),
  );
  await handlers.readingList.remove(
    request("readingList.remove", { url: "https://example.com/article" }),
    signal(),
  );
  assert.deepEqual(harness.readingUpdates, [
    { url: "https://example.com/article", hasBeenRead: true },
  ]);
  assert.deepEqual(harness.readingRemoves, [{ url: "https://example.com/article" }]);
});

function createHarness({ permissions = ["history", "bookmarks", "readingList"] } = {}) {
  const granted = new Set(permissions);
  const historySearches = [];
  const historyRanges = [];
  const historyDeletes = [];
  let historyClears = 0;
  const bookmarkChildren = [];
  const bookmarkCreates = [];
  const bookmarkGets = [];
  const bookmarkTrees = [];
  const bookmarkUpdates = [];
  const bookmarkMoves = [];
  const bookmarkRemoves = [];
  const readingQueries = [];
  const readingAdds = [];
  const readingUpdates = [];
  const readingRemoves = [];
  const readingEntries = [
    readingEntry("https://example.com/older", 10),
    readingEntry("https://example.com/newer", 20),
  ];
  const chromeAPI = {
    permissions: {
      contains: async ({ permissions: requested }) =>
        requested.every((value) => granted.has(value)),
    },
    history: {
      search: async (query) => {
        historySearches.push(query);
        return [
          {
            id: "1",
            url: "https://alice:secret@example.com/private?token=secret",
            title: "Private",
            lastVisitTime: 20,
            visitCount: 2,
            typedCount: 1,
          },
          {
            id: "2",
            url: "https://example.com/docs",
            title: "Docs",
            lastVisitTime: 10,
            visitCount: 1,
            typedCount: 0,
          },
        ];
      },
      getVisits: async () => [
        {
          id: "1",
          visitId: "11",
          referringVisitId: "",
          visitTime: 20,
          transition: "link",
        },
      ],
      deleteUrl: async (details) => historyDeletes.push(details),
      deleteRange: async (range) => historyRanges.push(range),
      deleteAll: async () => {
        historyClears += 1;
      },
    },
    bookmarks: {
      search: async () => bookmarkItems(),
      getChildren: async (id) => {
        bookmarkChildren.push(id);
        return bookmarkItems();
      },
      get: async (id) => {
        bookmarkGets.push(id);
        return [bookmarkItem({ id, title: "Folder", url: undefined })];
      },
      create: async (details) => {
        bookmarkCreates.push(details);
        return bookmarkItem({ id: "created", ...details });
      },
      update: async (id, changes) => {
        bookmarkUpdates.push([id, changes]);
        return bookmarkItem({ id, ...changes });
      },
      move: async (id, destination) => {
        bookmarkMoves.push([id, destination]);
        return bookmarkItem({ id, ...destination });
      },
      remove: async (id) => bookmarkRemoves.push(id),
      removeTree: async (id) => bookmarkTrees.push(id),
    },
    readingList: {
      query: async (query) => {
        readingQueries.push(query);
        if (query.url) {
          return [
            readingEntry(query.url, 30, {
              title: query.url.endsWith("article") ? "Article" : "Entry",
            }),
          ];
        }
        return readingEntries;
      },
      addEntry: async (entry) => readingAdds.push(entry),
      updateEntry: async (entry) => readingUpdates.push(entry),
      removeEntry: async (entry) => readingRemoves.push(entry),
    },
  };
  return {
    chromeAPI,
    historySearches,
    historyRanges,
    historyDeletes,
    get historyClears() {
      return historyClears;
    },
    bookmarkChildren,
    bookmarkCreates,
    bookmarkGets,
    bookmarkTrees,
    bookmarkUpdates,
    bookmarkMoves,
    bookmarkRemoves,
    readingQueries,
    readingAdds,
    readingUpdates,
    readingRemoves,
  };
}

function bookmarkItems() {
  return [
    bookmarkItem({ id: "2", title: "Example", url: "https://example.com/" }),
    bookmarkItem({ id: "3", title: "Docs", url: "https://example.com/docs" }),
  ];
}

function bookmarkItem(overrides = {}) {
  return {
    id: "2",
    parentId: "1",
    index: 0,
    title: "Example",
    url: "https://example.com/",
    dateAdded: 10,
    ...overrides,
  };
}

function readingEntry(url, lastUpdateTime, overrides = {}) {
  return {
    url,
    title: "Entry",
    hasBeenRead: false,
    creationTime: 1,
    lastUpdateTime,
    ...overrides,
  };
}

function request(command, params) {
  return { requestId: "personal-test", command, params, timeoutMs: 1_000 };
}

function signal() {
  return new AbortController().signal;
}
