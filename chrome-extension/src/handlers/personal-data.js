import { ErrorCode, mapChromeError, protocolError } from "../protocol.js";

const DEFAULT_TIMEOUT_MS = 30_000;
const MAX_TIMEOUT_MS = 120_000;
const DEFAULT_PAGE_ITEMS = 50;
const MAX_PAGE_ITEMS = 200;
const MAX_SCAN_ITEMS = 10_000;
const MAX_URL_BYTES = 8_192;
const MAX_TITLE_BYTES = 2_048;
const MAX_ID_BYTES = 256;
const MAX_QUERY_BYTES = 1_024;
const MAX_SAFE_INTEGER = Number.MAX_SAFE_INTEGER;
const encoder = new TextEncoder();

const HISTORY_TRANSITIONS = new Set([
  "link",
  "typed",
  "auto_bookmark",
  "auto_subframe",
  "manual_subframe",
  "generated",
  "auto_toplevel",
  "form_submit",
  "reload",
  "keyword",
  "keyword_generated",
]);
const SENSITIVE_QUERY_NAME =
  /^(?:password|passwd|passphrase|secret|client[-_]?secret|token|id[-_]?token|credential|authorization|cookie|api[-_]?key|access[-_]?token|refresh[-_]?token)$/iu;

export function createPersonalDataHandlers(chromeAPI) {
  return {
    history: createHistoryHandlers(chromeAPI),
    bookmarks: createBookmarkHandlers(chromeAPI),
    readingList: createReadingListHandlers(chromeAPI),
  };
}

function createHistoryHandlers(chromeAPI) {
  async function search(request, parentSignal) {
    const params = validateHistorySearch(request.params);
    return run(chromeAPI, "history", request, parentSignal, async (signal) => {
      const found = await callAPI(chromeAPI.history, "search", [
        {
          text: params.text,
          maxResults: MAX_SCAN_ITEMS + 1,
          ...(params.startTime !== undefined ? { startTime: params.startTime } : {}),
          ...(params.endTime !== undefined ? { endTime: params.endTime } : {}),
        },
      ]);
      throwIfCancelled(signal);
      const normalized = normalizeBoundedCollection(found, normalizeHistoryItem, "history");
      return pageResult("history", normalized, params.cursor, params.limit, "items");
    });
  }

  async function getVisits(request, parentSignal) {
    const params = validateURLPage(request.params);
    return run(chromeAPI, "history", request, parentSignal, async (signal) => {
      const found = await callAPI(chromeAPI.history, "getVisits", [{ url: params.url }]);
      throwIfCancelled(signal);
      const normalized = normalizeBoundedCollection(found, normalizeVisit, "history visits");
      return pageResult("visits", normalized, params.cursor, params.limit, "visits");
    });
  }

  async function deleteUrl(request, parentSignal) {
    const params = validateConfirmedURL(request.params, "Deleting URL history");
    return run(chromeAPI, "history", request, parentSignal, async (signal) => {
      const visits = await callAPI(chromeAPI.history, "getVisits", [{ url: params.url }]);
      if (!Array.isArray(visits) || visits.length > MAX_SCAN_ITEMS) {
        throw protocolError(ErrorCode.PAYLOAD_TOO_LARGE, "The URL has too many history visits");
      }
      await callAPI(chromeAPI.history, "deleteUrl", [{ url: params.url }]);
      throwIfCancelled(signal);
      return mutationResult("delete_url", { deletedCount: visits.length, scope: "url" });
    });
  }

  async function deleteRange(request, parentSignal) {
    const params = validateHistoryRange(request.params);
    return run(chromeAPI, "history", request, parentSignal, async (signal) => {
      await callAPI(chromeAPI.history, "deleteRange", [
        { startTime: params.startTime, endTime: params.endTime },
      ]);
      throwIfCancelled(signal);
      return mutationResult("delete_range", {
        deletedCount: 0,
        scope: "range",
        startTime: params.startTime,
        endTime: params.endTime,
        warnings: ["The browser API does not report the number of deleted visits"],
      });
    });
  }

  async function deleteAll(request, parentSignal) {
    const params = objectParams(request.params);
    allowedKeys(params, ["confirm"]);
    validateConfirmation(params, "Clearing browser history");
    return run(chromeAPI, "history", request, parentSignal, async (signal) => {
      await callAPI(chromeAPI.history, "deleteAll", []);
      throwIfCancelled(signal);
      return mutationResult("delete_all", {
        deletedCount: 0,
        scope: "all",
        warnings: ["The browser API does not report the number of deleted visits"],
      });
    });
  }

  return { search, getVisits, deleteUrl, deleteRange, deleteAll };
}

function createBookmarkHandlers(chromeAPI) {
  async function list(request, parentSignal) {
    const params = validateBookmarkList(request.params);
    return run(chromeAPI, "bookmarks", request, parentSignal, async (signal) => {
      const found = params.parentId
        ? await callAPI(chromeAPI.bookmarks, "getChildren", [params.parentId])
        : await callAPI(chromeAPI.bookmarks, "search", [params.query]);
      throwIfCancelled(signal);
      const normalized = normalizeBoundedCollection(found, normalizeBookmark, "bookmarks");
      return pageResult("bookmarks", normalized, params.cursor, params.limit, "bookmarks");
    });
  }

  async function create(request, parentSignal) {
    const params = validateBookmarkCreate(request.params);
    return run(chromeAPI, "bookmarks", request, parentSignal, async (signal) => {
      const created = normalizeBookmark(
        await callAPI(chromeAPI.bookmarks, "create", [
          {
            title: params.title,
            ...(params.parentId ? { parentId: params.parentId } : {}),
            ...(params.index !== undefined ? { index: params.index } : {}),
            ...(params.url ? { url: params.url } : {}),
          },
        ]),
      );
      throwIfCancelled(signal);
      return bookmarkMutation("create", created);
    });
  }

  async function update(request, parentSignal) {
    const params = validateBookmarkUpdate(request.params);
    return run(chromeAPI, "bookmarks", request, parentSignal, async (signal) => {
      const updated = normalizeBookmark(
        await callAPI(chromeAPI.bookmarks, "update", [
          params.bookmarkId,
          {
            ...(params.title !== undefined ? { title: params.title } : {}),
            ...(params.url !== undefined ? { url: params.url } : {}),
          },
        ]),
      );
      throwIfCancelled(signal);
      return bookmarkMutation("update", updated);
    });
  }

  async function move(request, parentSignal) {
    const params = validateBookmarkMove(request.params);
    return run(chromeAPI, "bookmarks", request, parentSignal, async (signal) => {
      const moved = normalizeBookmark(
        await callAPI(chromeAPI.bookmarks, "move", [
          params.bookmarkId,
          {
            ...(params.parentId ? { parentId: params.parentId } : {}),
            ...(params.index !== undefined ? { index: params.index } : {}),
          },
        ]),
      );
      throwIfCancelled(signal);
      return bookmarkMutation("move", moved);
    });
  }

  async function remove(request, parentSignal) {
    const params = validateBookmarkRemove(request.params);
    return run(chromeAPI, "bookmarks", request, parentSignal, async (signal) => {
      const before = await callAPI(chromeAPI.bookmarks, "get", [params.bookmarkId]);
      if (!Array.isArray(before) || before.length !== 1) {
        throw protocolError(ErrorCode.INVALID_MESSAGE, "The bookmark was not found");
      }
      const bookmark = normalizeBookmark(before[0]);
      if (params.recursive && bookmark.url) {
        throw protocolError(ErrorCode.INVALID_MESSAGE, "Recursive removal applies only to folders");
      }
      await callAPI(chromeAPI.bookmarks, params.recursive ? "removeTree" : "remove", [
        params.bookmarkId,
      ]);
      throwIfCancelled(signal);
      return {
        kind: "bookmark_mutation",
        bookmarks: [],
        totalMatched: 0,
        nextCursor: "",
        bookmarkId: params.bookmarkId,
        operation: params.recursive ? "remove_tree" : "remove",
        changed: true,
        removedIds: [params.bookmarkId],
        warnings: params.recursive
          ? ["The removed folder may have contained additional bookmark nodes"]
          : [],
      };
    });
  }

  return { list, create, update, move, remove };
}

function createReadingListHandlers(chromeAPI) {
  async function list(request, parentSignal) {
    const params = validateReadingListQuery(request.params);
    return run(chromeAPI, "readingList", request, parentSignal, async (signal) => {
      const found = await callAPI(chromeAPI.readingList, "query", [
        {
          ...(params.title ? { title: params.title } : {}),
          ...(params.url ? { url: params.url } : {}),
          ...(params.hasBeenRead !== undefined ? { hasBeenRead: params.hasBeenRead } : {}),
        },
      ]);
      throwIfCancelled(signal);
      const normalized = normalizeBoundedCollection(
        found,
        normalizeReadingListEntry,
        "reading list",
      ).sort(
        (left, right) =>
          right.lastUpdateTime - left.lastUpdateTime || left.url.localeCompare(right.url),
      );
      return pageResult("reading_list", normalized, params.cursor, params.limit, "entries");
    });
  }

  async function add(request, parentSignal) {
    const params = validateReadingListAdd(request.params);
    return run(chromeAPI, "readingList", request, parentSignal, async (signal) => {
      await callAPI(chromeAPI.readingList, "addEntry", [
        { url: params.url, title: params.title, hasBeenRead: params.hasBeenRead },
      ]);
      throwIfCancelled(signal);
      return readingListMutation(chromeAPI, "add", params.url, signal);
    });
  }

  async function update(request, parentSignal) {
    const params = validateReadingListUpdate(request.params);
    return run(chromeAPI, "readingList", request, parentSignal, async (signal) => {
      await callAPI(chromeAPI.readingList, "updateEntry", [
        {
          url: params.url,
          ...(params.title !== undefined ? { title: params.title } : {}),
          ...(params.hasBeenRead !== undefined ? { hasBeenRead: params.hasBeenRead } : {}),
        },
      ]);
      throwIfCancelled(signal);
      return readingListMutation(chromeAPI, "update", params.url, signal);
    });
  }

  async function remove(request, parentSignal) {
    const params = validateExactURL(request.params);
    return run(chromeAPI, "readingList", request, parentSignal, async (signal) => {
      const before = await callAPI(chromeAPI.readingList, "query", [{ url: params.url }]);
      if (!Array.isArray(before) || before.length !== 1) {
        throw protocolError(ErrorCode.INVALID_MESSAGE, "The reading-list entry was not found");
      }
      await callAPI(chromeAPI.readingList, "removeEntry", [{ url: params.url }]);
      throwIfCancelled(signal);
      return {
        kind: "reading_list_mutation",
        entries: [],
        totalMatched: 0,
        nextCursor: "",
        operation: "remove",
        changed: true,
        targetUrl: boundedBrowserURL(params.url),
        warnings: [],
      };
    });
  }

  return { list, add, update, remove };
}

async function readingListMutation(chromeAPI, operation, url, signal) {
  const found = await callAPI(chromeAPI.readingList, "query", [{ url }]);
  throwIfCancelled(signal);
  if (!Array.isArray(found) || found.length !== 1) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "The reading-list mutation was not observable");
  }
  return {
    kind: "reading_list_mutation",
    entries: [normalizeReadingListEntry(found[0])],
    totalMatched: 1,
    nextCursor: "",
    operation,
    changed: true,
    targetUrl: normalizeReadingListEntry(found[0]).url,
    warnings: [],
  };
}

function bookmarkMutation(operation, bookmark) {
  return {
    kind: "bookmark_mutation",
    bookmarks: [bookmark],
    totalMatched: 1,
    nextCursor: "",
    bookmarkId: bookmark.id,
    operation,
    changed: true,
    removedIds: [],
    warnings: [],
  };
}

function mutationResult(operation, extra = {}) {
  return {
    kind: "history_mutation",
    items: [],
    visits: [],
    totalMatched: 0,
    nextCursor: "",
    operation,
    changed: true,
    deletedCount: extra.deletedCount || 0,
    scope: extra.scope || "",
    startTime: extra.startTime || 0,
    endTime: extra.endTime || 0,
    warnings: extra.warnings || [],
  };
}

function pageResult(kind, items, cursor, limit, field) {
  const offset = cursor ? Number(cursor) : 0;
  const page = items.slice(offset, offset + limit);
  return {
    kind,
    [field]: page,
    totalMatched: items.length,
    nextCursor: offset + page.length < items.length ? String(offset + page.length) : "",
    warnings: [],
  };
}

function normalizeBoundedCollection(value, normalize, label) {
  if (!Array.isArray(value)) throw invalidBrowserResult(label);
  if (value.length > MAX_SCAN_ITEMS) {
    throw protocolError(ErrorCode.PAYLOAD_TOO_LARGE, `The ${label} exceeds the bounded scan limit`);
  }
  return value.map(normalize);
}

function normalizeHistoryItem(item) {
  if (!item || typeof item !== "object") throw invalidBrowserResult("history");
  return {
    id: boundedID(item.id, "history item"),
    url: boundedBrowserURL(item.url),
    title: boundedText(item.title || "", MAX_TITLE_BYTES, "history title"),
    lastVisitTime: boundedTime(item.lastVisitTime, false),
    visitCount: boundedCount(item.visitCount),
    typedCount: boundedCount(item.typedCount),
  };
}

function normalizeVisit(item) {
  if (!item || typeof item !== "object") throw invalidBrowserResult("history visits");
  const transition = boundedText(item.transition || "link", 64, "history transition");
  if (!HISTORY_TRANSITIONS.has(transition)) throw invalidBrowserResult("history transition");
  return {
    id: boundedID(item.id, "history visit"),
    visitId: boundedID(item.visitId, "history visit"),
    referringVisitId: boundedOptionalID(item.referringVisitId),
    visitTime: boundedTime(item.visitTime, false),
    transition,
  };
}

function normalizeBookmark(item) {
  if (!item || typeof item !== "object") throw invalidBrowserResult("bookmarks");
  if (item.syncing !== undefined && typeof item.syncing !== "boolean") {
    throw invalidBrowserResult("bookmark sync state");
  }
  return {
    id: boundedID(item.id, "bookmark"),
    parentId: boundedOptionalID(item.parentId),
    index: boundedCount(item.index),
    title: boundedText(item.title || "", MAX_TITLE_BYTES, "bookmark title"),
    url: item.url ? boundedBrowserURL(item.url) : "",
    dateAdded: boundedTime(item.dateAdded, false),
    dateGroupModified: boundedTime(item.dateGroupModified, false),
    unmodifiable: boundedText(item.unmodifiable || "", 64, "bookmark state"),
    syncing: item.syncing === undefined ? false : Boolean(item.syncing),
  };
}

function normalizeReadingListEntry(item) {
  if (!item || typeof item !== "object") throw invalidBrowserResult("reading list");
  if (typeof item.hasBeenRead !== "boolean") throw invalidBrowserResult("reading-list state");
  return {
    url: boundedBrowserURL(item.url),
    title: boundedText(item.title || "", MAX_TITLE_BYTES, "reading-list title"),
    hasBeenRead: Boolean(item.hasBeenRead),
    creationTime: boundedTime(item.creationTime, true),
    lastUpdateTime: boundedTime(item.lastUpdateTime, true),
  };
}

function validateHistorySearch(params) {
  params = objectParams(params);
  allowedKeys(params, ["text", "startTime", "endTime", "cursor", "limit"]);
  const page = validatePage(params);
  const text = optionalBoundedText(params.text, MAX_QUERY_BYTES, "params.text") || "";
  const startTime = optionalTime(params.startTime, "params.startTime");
  const endTime = optionalTime(params.endTime, "params.endTime");
  if (startTime !== undefined && endTime !== undefined && startTime >= endTime) {
    throw invalidParams("params.startTime must be before params.endTime");
  }
  return { ...page, text, startTime, endTime };
}

function validateURLPage(params) {
  params = objectParams(params);
  allowedKeys(params, ["url", "cursor", "limit"]);
  return { ...validatePage(params), url: exactHTTPURL(params.url) };
}

function validateConfirmedURL(params, label) {
  params = objectParams(params);
  allowedKeys(params, ["url", "confirm"]);
  if (params.confirm !== true) {
    throw protocolError(ErrorCode.CONFIRMATION_REQUIRED, `${label} requires confirm: true`);
  }
  return { url: exactHTTPURL(params.url), confirm: true };
}

function validateHistoryRange(params) {
  params = objectParams(params);
  allowedKeys(params, ["startTime", "endTime", "confirm"]);
  validateConfirmation(params, "Deleting a history range");
  const startTime = requiredTime(params.startTime, "params.startTime");
  const endTime = requiredTime(params.endTime, "params.endTime");
  if (startTime >= endTime) throw invalidParams("params.startTime must be before params.endTime");
  return { startTime, endTime, confirm: true };
}

function validateConfirmation(params, label) {
  params = objectParams(params);
  if (params.confirm !== true) {
    throw protocolError(ErrorCode.CONFIRMATION_REQUIRED, `${label} requires confirm: true`);
  }
  return params;
}

function validateBookmarkList(params) {
  params = objectParams(params);
  allowedKeys(params, ["query", "parentId", "cursor", "limit"]);
  const query = optionalBoundedText(params.query, MAX_QUERY_BYTES, "params.query") || "";
  const parentId = optionalID(params.parentId, "params.parentId");
  if (query && parentId)
    throw invalidParams("params.query and params.parentId are mutually exclusive");
  return { ...validatePage(params), query, parentId };
}

function validateBookmarkCreate(params) {
  params = objectParams(params);
  allowedKeys(params, ["title", "url", "parentId", "index"]);
  return {
    title: requiredBoundedText(params.title, MAX_TITLE_BYTES, "params.title"),
    url: params.url === undefined ? "" : exactHTTPURL(params.url),
    parentId: optionalID(params.parentId, "params.parentId"),
    index: optionalIndex(params.index),
  };
}

function validateBookmarkUpdate(params) {
  params = objectParams(params);
  allowedKeys(params, ["bookmarkId", "title", "url"]);
  const title =
    params.title === undefined
      ? undefined
      : requiredBoundedText(params.title, MAX_TITLE_BYTES, "params.title");
  const url = params.url === undefined ? undefined : exactHTTPURL(params.url);
  if (title === undefined && url === undefined) {
    throw invalidParams("params.title or params.url is required");
  }
  return { bookmarkId: requiredID(params.bookmarkId, "params.bookmarkId"), title, url };
}

function validateBookmarkMove(params) {
  params = objectParams(params);
  allowedKeys(params, ["bookmarkId", "parentId", "index"]);
  const parentId = optionalID(params.parentId, "params.parentId");
  const index = optionalIndex(params.index);
  if (!parentId && index === undefined)
    throw invalidParams("params.parentId or params.index is required");
  return { bookmarkId: requiredID(params.bookmarkId, "params.bookmarkId"), parentId, index };
}

function validateBookmarkRemove(params) {
  params = objectParams(params);
  allowedKeys(params, ["bookmarkId", "recursive", "confirm"]);
  if (params.recursive !== undefined && typeof params.recursive !== "boolean") {
    throw invalidParams("params.recursive must be a boolean");
  }
  if (params.confirm !== undefined && typeof params.confirm !== "boolean") {
    throw invalidParams("params.confirm must be a boolean");
  }
  const recursive = params.recursive === true;
  if (recursive && params.confirm !== true) {
    throw protocolError(
      ErrorCode.CONFIRMATION_REQUIRED,
      "Recursive bookmark removal requires confirm: true",
    );
  }
  return { bookmarkId: requiredID(params.bookmarkId, "params.bookmarkId"), recursive };
}

function validateReadingListQuery(params) {
  params = objectParams(params);
  allowedKeys(params, ["title", "url", "hasBeenRead", "cursor", "limit"]);
  if (params.hasBeenRead !== undefined && typeof params.hasBeenRead !== "boolean") {
    throw invalidParams("params.hasBeenRead must be a boolean");
  }
  return {
    ...validatePage(params),
    title: optionalBoundedText(params.title, MAX_TITLE_BYTES, "params.title") || "",
    url: params.url === undefined ? "" : exactHTTPURL(params.url),
    hasBeenRead: params.hasBeenRead,
  };
}

function validateReadingListAdd(params) {
  params = objectParams(params);
  allowedKeys(params, ["url", "title", "hasBeenRead"]);
  if (typeof params.hasBeenRead !== "boolean") {
    throw invalidParams("params.hasBeenRead must be a boolean");
  }
  return {
    url: exactHTTPURL(params.url),
    title: requiredBoundedText(params.title, MAX_TITLE_BYTES, "params.title"),
    hasBeenRead: params.hasBeenRead,
  };
}

function validateReadingListUpdate(params) {
  params = objectParams(params);
  allowedKeys(params, ["url", "title", "hasBeenRead"]);
  const title =
    params.title === undefined
      ? undefined
      : requiredBoundedText(params.title, MAX_TITLE_BYTES, "params.title");
  if (params.hasBeenRead !== undefined && typeof params.hasBeenRead !== "boolean") {
    throw invalidParams("params.hasBeenRead must be a boolean");
  }
  if (title === undefined && params.hasBeenRead === undefined) {
    throw invalidParams("params.title or params.hasBeenRead is required");
  }
  return { url: exactHTTPURL(params.url), title, hasBeenRead: params.hasBeenRead };
}

function validateExactURL(params) {
  params = objectParams(params);
  allowedKeys(params, ["url"]);
  return { url: exactHTTPURL(params.url) };
}

function validatePage(params) {
  const limit = params.limit === undefined ? DEFAULT_PAGE_ITEMS : params.limit;
  if (!Number.isInteger(limit) || limit < 1 || limit > MAX_PAGE_ITEMS) {
    throw invalidParams("params.limit must be between 1 and 200");
  }
  const cursor = params.cursor === undefined ? "" : params.cursor;
  if (
    typeof cursor !== "string" ||
    (cursor !== "" && (!/^[1-9][0-9]*$/.test(cursor) || Number(cursor) >= MAX_SCAN_ITEMS))
  ) {
    throw invalidParams("params.cursor is invalid");
  }
  return { limit, cursor };
}

async function run(chromeAPI, permission, request, parentSignal, operation) {
  return withTimeout(
    async (signal) => {
      await requirePermission(chromeAPI, permission);
      const result = await operation(signal);
      await requirePermission(chromeAPI, permission);
      return result;
    },
    parentSignal,
    validateTimeout(request.timeoutMs),
  );
}

async function requirePermission(chromeAPI, permission) {
  if (!chromeAPI[permission] || !chromeAPI.permissions?.contains) {
    throw protocolError(ErrorCode.CAPABILITY_UNAVAILABLE, `The ${permission} API is unavailable`);
  }
  if (!(await chromeAPI.permissions.contains({ permissions: [permission] }))) {
    throw protocolError(
      ErrorCode.PERMISSION_REQUIRED,
      `${permission} access is required. Grant Personal data in extension settings.`,
    );
  }
}

async function callAPI(api, method, args) {
  if (typeof api?.[method] !== "function") {
    throw protocolError(ErrorCode.CAPABILITY_UNAVAILABLE, "The browser API method is unavailable");
  }
  try {
    return await api[method](...args);
  } catch (error) {
    throw mapChromeError(error);
  }
}

function exactHTTPURL(value) {
  const text = requiredBoundedText(value, MAX_URL_BYTES, "params.url");
  let parsed;
  try {
    parsed = new URL(text);
  } catch {
    throw invalidParams("params.url must be a valid HTTP(S) URL");
  }
  if (
    !["http:", "https:"].includes(parsed.protocol) ||
    !parsed.hostname ||
    parsed.username ||
    parsed.password
  ) {
    throw invalidParams("params.url must be an HTTP(S) URL without credentials");
  }
  return parsed.href;
}

function boundedBrowserURL(value) {
  const text = boundedText(value || "", MAX_URL_BYTES, "browser URL");
  let parsed;
  try {
    parsed = new URL(text);
  } catch {
    throw invalidBrowserResult("URL");
  }
  if (!parsed.protocol || (!parsed.hostname && ["http:", "https:"].includes(parsed.protocol))) {
    throw invalidBrowserResult("URL");
  }
  if (parsed.username || parsed.password) {
    parsed.username = "";
    parsed.password = "";
  }
  for (const key of [...parsed.searchParams.keys()]) {
    if (SENSITIVE_QUERY_NAME.test(key)) parsed.searchParams.set(key, "[REDACTED]");
  }
  parsed.hash = "";
  return parsed.href;
}

function objectParams(params) {
  if (!params || typeof params !== "object" || Array.isArray(params)) {
    throw invalidParams("params must be an object");
  }
  return params;
}

function allowedKeys(params, keys) {
  const allowed = new Set(keys);
  for (const key of Object.keys(params)) {
    if (!allowed.has(key)) throw invalidParams(`params.${key} is not allowed`);
  }
}

function requiredBoundedText(value, maxBytes, field) {
  if (typeof value !== "string" || !value.trim() || encoder.encode(value).length > maxBytes) {
    throw invalidParams(`${field} must be a non-empty bounded string`);
  }
  return value;
}

function optionalBoundedText(value, maxBytes, field) {
  if (value === undefined) return undefined;
  if (typeof value !== "string" || encoder.encode(value).length > maxBytes) {
    throw invalidParams(`${field} must be a bounded string`);
  }
  return value;
}

function boundedText(value, maxBytes, field) {
  if (typeof value !== "string" || encoder.encode(value).length > maxBytes || hasControl(value)) {
    throw invalidBrowserResult(field);
  }
  return value;
}

function hasControl(value) {
  return [...value].some((character) => {
    const code = character.codePointAt(0);
    return code < 32 || code === 127;
  });
}

function requiredID(value, field) {
  if (typeof value !== "string" || !value || encoder.encode(value).length > MAX_ID_BYTES) {
    throw invalidParams(`${field} must be a non-empty bounded string`);
  }
  return value;
}

function optionalID(value, field) {
  return value === undefined ? "" : requiredID(value, field);
}

function boundedID(value, label) {
  if (typeof value !== "string" || !value || encoder.encode(value).length > MAX_ID_BYTES) {
    throw invalidBrowserResult(label);
  }
  return value;
}

function boundedOptionalID(value) {
  if (value === undefined || value === null || value === "") return "";
  return boundedID(value, "identifier");
}

function optionalIndex(value) {
  if (value === undefined) return undefined;
  if (!Number.isInteger(value) || value < 0 || value > MAX_SAFE_INTEGER) {
    throw invalidParams("params.index must be a non-negative safe integer");
  }
  return value;
}

function boundedCount(value) {
  if (value === undefined || value === null) return 0;
  if (!Number.isInteger(value) || value < 0 || value > MAX_SAFE_INTEGER) {
    throw invalidBrowserResult("count");
  }
  return value;
}

function optionalTime(value, field) {
  if (value === undefined) return undefined;
  return requiredTime(value, field);
}

function requiredTime(value, field) {
  if (
    typeof value !== "number" ||
    !Number.isFinite(value) ||
    value < 0 ||
    value > MAX_SAFE_INTEGER
  ) {
    throw invalidParams(`${field} must be a non-negative epoch time in milliseconds`);
  }
  return value;
}

function boundedTime(value, required) {
  if (value === undefined || value === null) {
    if (!required) return 0;
    throw invalidBrowserResult("time");
  }
  if (
    typeof value !== "number" ||
    !Number.isFinite(value) ||
    value < 0 ||
    value > MAX_SAFE_INTEGER
  ) {
    throw invalidBrowserResult("time");
  }
  return value;
}

function validateTimeout(value) {
  if (value === undefined) return DEFAULT_TIMEOUT_MS;
  if (!Number.isInteger(value) || value < 1 || value > MAX_TIMEOUT_MS) {
    throw invalidParams("timeoutMs must be between 1 and 120000");
  }
  return value;
}

function withTimeout(operation, parentSignal, timeoutMs) {
  if (parentSignal.aborted) return Promise.reject(cancelledError());
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort("timeout"), timeoutMs);
  const onAbort = () => controller.abort("cancelled");
  parentSignal.addEventListener("abort", onAbort, { once: true });
  return operation(controller.signal)
    .catch((error) => {
      if (controller.signal.aborted) {
        if (controller.signal.reason === "timeout") {
          throw protocolError(ErrorCode.TIMEOUT, "Command timed out", true);
        }
        throw cancelledError();
      }
      throw error;
    })
    .finally(() => {
      clearTimeout(timer);
      parentSignal.removeEventListener("abort", onAbort);
    });
}

function throwIfCancelled(signal) {
  if (signal.aborted) throw cancelledError();
}

function cancelledError() {
  return protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
}

function invalidParams(message) {
  return protocolError(ErrorCode.INVALID_MESSAGE, message);
}

function invalidBrowserResult(label) {
  return protocolError(ErrorCode.INVALID_MESSAGE, `The browser returned invalid ${label} data`);
}
