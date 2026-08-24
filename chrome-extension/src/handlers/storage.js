import { ErrorCode, assertFreshDocument, mapChromeError, protocolError } from "../protocol.js";

const DEFAULT_TIMEOUT_MS = 30_000;
const MAX_TIMEOUT_MS = 120_000;
const MAX_PAGE_ITEMS = 200;
const MAX_SCAN_ITEMS = 10_000;
const MAX_KEY_BYTES = 1_024;
const MAX_VALUE_BYTES = 64 * 1_024;
const MAX_OBSERVED_VALUE_BYTES = 1_000_000;
const MAX_NAME_BYTES = 1_024;
const STORAGE_TYPES = new Set(["localStorage", "sessionStorage"]);
const CLEAR_TYPES = new Set(["localStorage", "sessionStorage", "cacheStorage", "indexedDB"]);
const MASKED_VALUE = "[MASKED]";
const OMITTED_VALUE = "[OMITTED]";
const encoder = new TextEncoder();

export function createStorageHandlers(chromeAPI, { getSettings = async () => ({}) } = {}) {
  async function list(request, parentSignal) {
    const params = validateListParams(request.params);
    const sensitive = request.command === "storage.listSensitive";
    if (sensitive) await requireSensitiveMode(getSettings);
    return executeStorage(chromeAPI, request, parentSignal, "list", {
      ...params,
      includeValues: sensitive,
    });
  }

  async function get(request, parentSignal) {
    const params = validateItemParams(request.params);
    const sensitive = request.command === "storage.getSensitive";
    if (sensitive) await requireSensitiveMode(getSettings);
    return executeStorage(chromeAPI, request, parentSignal, "get", {
      ...params,
      includeValues: sensitive,
    });
  }

  async function set(request, parentSignal) {
    return executeStorage(
      chromeAPI,
      request,
      parentSignal,
      "set",
      validateSetParams(request.params),
    );
  }

  async function remove(request, parentSignal) {
    return executeStorage(
      chromeAPI,
      request,
      parentSignal,
      "remove",
      validateItemParams(request.params),
    );
  }

  async function cacheMetadata(request, parentSignal) {
    return executeStorage(
      chromeAPI,
      request,
      parentSignal,
      "cacheMetadata",
      validateMetadataParams(request.params),
    );
  }

  async function indexedDBMetadata(request, parentSignal) {
    return executeStorage(
      chromeAPI,
      request,
      parentSignal,
      "indexedDBMetadata",
      validateMetadataParams(request.params),
    );
  }

  async function clear(request, parentSignal) {
    return executeStorage(
      chromeAPI,
      request,
      parentSignal,
      "clear",
      validateClearParams(request.params),
    );
  }

  return {
    list,
    listSensitive: list,
    get,
    getSensitive: get,
    set,
    remove,
    cacheMetadata,
    indexedDBMetadata,
    clear,
  };
}

async function executeStorage(chromeAPI, request, parentSignal, operation, params) {
  return withTimeout(
    async (signal) => {
      const target = await prepareTarget(chromeAPI, request, signal);
      let results;
      try {
        results = await abortable(
          chromeAPI.scripting.executeScript({
            target: { tabId: target.tab.id, documentIds: [target.documentId] },
            world: "ISOLATED",
            func: isolatedStorageOperation,
            args: [operation, params],
          }),
          signal,
        );
      } catch (error) {
        if (typeof error?.code === "string") throw error;
        throw mapChromeError(error);
      }
      if (
        !Array.isArray(results) ||
        results.length !== 1 ||
        results[0]?.frameId !== 0 ||
        results[0]?.documentId !== target.documentId
      ) {
        throw invalidStorageResult();
      }
      const payload = unwrapInjectedResult(results[0].result);
      const normalized = normalizeInjectedResult(operation, payload, params.includeValues === true);
      await recheckTarget(chromeAPI, target);
      return {
        ...normalized,
        tabId: target.tab.id,
        documentId: target.documentId,
        origin: target.origin,
      };
    },
    parentSignal,
    validateTimeout(request.timeoutMs),
  );
}

async function prepareTarget(chromeAPI, request, signal) {
  if (!chromeAPI.scripting?.executeScript) {
    throw protocolError(
      ErrorCode.CAPABILITY_UNAVAILABLE,
      "The isolated storage backend is unavailable",
    );
  }
  const tab = await resolveTab(chromeAPI, request.target?.tabId);
  const tabURL = parseHTTPURL(tab.url);
  if (tabURL.origin !== request.params.origin) {
    throw protocolError(
      ErrorCode.RESTRICTED_URL,
      "Storage operations are limited to the selected root-document origin",
    );
  }
  await requireStorageAccess(chromeAPI, tabURL);
  const frame = await currentRootDocument(chromeAPI, tab.id);
  if (request.target?.documentId) {
    assertFreshDocument(request.target.documentId, frame.documentId);
  }
  throwIfCancelled(signal);
  return { tab, origin: tabURL.origin, documentId: frame.documentId };
}

async function recheckTarget(chromeAPI, target) {
  const tab = await resolveTab(chromeAPI, target.tab.id);
  const currentURL = parseHTTPURL(tab.url);
  if (currentURL.origin !== target.origin) {
    throw protocolError(ErrorCode.STALE_TARGET, "The target tab navigated to another origin", true);
  }
  await requireStorageAccess(chromeAPI, currentURL);
  const frame = await currentRootDocument(chromeAPI, target.tab.id);
  assertFreshDocument(target.documentId, frame.documentId);
}

async function requireStorageAccess(chromeAPI, tabURL) {
  const granted = await chromeAPI.permissions.contains({
    permissions: ["browsingData"],
    origins: [`${tabURL.protocol}//${tabURL.hostname}/*`],
  });
  if (!granted) {
    throw protocolError(
      ErrorCode.PERMISSION_REQUIRED,
      "Personal data and site access are required for origin storage",
      false,
      { origin: tabURL.origin },
    );
  }
}

function validateListParams(params) {
  assertObject(params, ["origin", "storageType", "cursor", "limit"]);
  validateOrigin(params.origin);
  validateStorageType(params.storageType);
  validateCursor(params.cursor);
  validateLimit(params.limit);
  return params;
}

function validateItemParams(params) {
  assertObject(params, ["origin", "storageType", "key"]);
  validateOrigin(params.origin);
  validateStorageType(params.storageType);
  validateBoundedString(params.key, MAX_KEY_BYTES, "params.key");
  return params;
}

function validateSetParams(params) {
  assertObject(params, ["origin", "storageType", "key", "value"]);
  validateOrigin(params.origin);
  validateStorageType(params.storageType);
  validateBoundedString(params.key, MAX_KEY_BYTES, "params.key");
  validateBoundedString(params.value, MAX_VALUE_BYTES, "params.value");
  return params;
}

function validateMetadataParams(params) {
  assertObject(params, ["origin", "cursor", "limit"]);
  validateOrigin(params.origin);
  validateCursor(params.cursor);
  validateLimit(params.limit);
  return params;
}

function validateClearParams(params) {
  assertObject(params, ["origin", "types", "confirm"]);
  validateOrigin(params.origin);
  if (
    params.confirm !== true ||
    !Array.isArray(params.types) ||
    params.types.length < 1 ||
    params.types.length > CLEAR_TYPES.size ||
    new Set(params.types).size !== params.types.length ||
    params.types.some((value) => !CLEAR_TYPES.has(value))
  ) {
    throw protocolError(
      params.confirm === true ? ErrorCode.INVALID_MESSAGE : ErrorCode.CONFIRMATION_REQUIRED,
      params.confirm === true
        ? "params.types contains an unsupported or duplicate storage type"
        : "Clearing origin storage requires confirm: true",
    );
  }
  return params;
}

function validateOrigin(value) {
  const parsed = parseHTTPURL(value);
  if (parsed.origin !== value || parsed.pathname !== "/" || parsed.search || parsed.hash) {
    throw invalidStorageParams("params.origin must be an exact HTTP(S) origin");
  }
}

function validateStorageType(value) {
  if (!STORAGE_TYPES.has(value)) {
    throw invalidStorageParams("params.storageType is unsupported");
  }
}

function validateCursor(value) {
  if (
    value !== undefined &&
    (typeof value !== "string" ||
      !/^\d+$/.test(value) ||
      !Number.isSafeInteger(Number(value)) ||
      Number(value) < 1)
  ) {
    throw invalidStorageParams("params.cursor is invalid");
  }
}

function validateLimit(value) {
  if (!Number.isInteger(value) || value < 1 || value > MAX_PAGE_ITEMS) {
    throw invalidStorageParams("params.limit is outside the supported range");
  }
}

function normalizeInjectedResult(operation, raw, expectedValuesIncluded) {
  if (!isObject(raw) || typeof raw.valuesIncluded !== "boolean") {
    throw invalidStorageResult();
  }
  const common = {
    valuesIncluded: Boolean(raw.valuesIncluded),
    supported: raw.supported === true,
    warnings: normalizeWarnings(raw.warnings),
    nextCursor: normalizeCursor(raw.nextCursor),
    totalMatched: normalizeCount(raw.totalMatched),
    storageType: typeof raw.storageType === "string" ? raw.storageType : "",
    items: [],
    caches: [],
    databases: [],
    operation: typeof raw.operation === "string" ? raw.operation : "",
    changed: Boolean(raw.changed),
    requestedTypes: [],
    clearedTypes: [],
    clearedCounts: null,
  };
  if (!common.supported) throw invalidStorageResult();
  if (
    common.valuesIncluded !== (["list", "get"].includes(operation) ? expectedValuesIncluded : false)
  ) {
    throw invalidStorageResult();
  }
  switch (operation) {
    case "list":
    case "get": {
      const expectedKind = operation === "list" ? "items" : "item";
      if (
        raw.kind !== expectedKind ||
        !STORAGE_TYPES.has(common.storageType) ||
        !Array.isArray(raw.items) ||
        raw.items.length > (operation === "get" ? 1 : MAX_PAGE_ITEMS)
      ) {
        throw invalidStorageResult();
      }
      common.items = raw.items.map((item) => normalizeItem(item, common.valuesIncluded));
      break;
    }
    case "set":
    case "remove":
      if (
        raw.kind !== "mutation" ||
        raw.operation !== operation ||
        !STORAGE_TYPES.has(common.storageType) ||
        typeof raw.changed !== "boolean"
      ) {
        throw invalidStorageResult();
      }
      break;
    case "cacheMetadata":
      if (raw.kind !== "cacheMetadata" || !Array.isArray(raw.caches)) {
        throw invalidStorageResult();
      }
      common.caches = raw.caches.map((cache) => {
        if (!isObject(cache)) throw invalidStorageResult();
        validateBoundedString(cache.name, MAX_NAME_BYTES, "cache.name");
        return { name: cache.name };
      });
      if (common.caches.length > MAX_PAGE_ITEMS) throw invalidStorageResult();
      break;
    case "indexedDBMetadata":
      if (raw.kind !== "indexedDBMetadata" || !Array.isArray(raw.databases)) {
        throw invalidStorageResult();
      }
      common.databases = raw.databases.map((database) => {
        if (
          !isObject(database) ||
          !Number.isSafeInteger(database.version) ||
          database.version < 1
        ) {
          throw invalidStorageResult();
        }
        validateBoundedString(database.name, MAX_NAME_BYTES, "database.name");
        return { name: database.name, version: database.version };
      });
      if (common.databases.length > MAX_PAGE_ITEMS) throw invalidStorageResult();
      break;
    case "clear":
      if (
        raw.kind !== "clear" ||
        raw.operation !== "clear" ||
        !validStorageTypes(raw.requestedTypes, CLEAR_TYPES) ||
        !validStorageTypes(raw.clearedTypes, new Set(raw.requestedTypes)) ||
        !isObject(raw.clearedCounts)
      ) {
        throw invalidStorageResult();
      }
      common.requestedTypes = [...raw.requestedTypes];
      common.clearedTypes = [...raw.clearedTypes];
      common.clearedCounts = {};
      for (const type of common.requestedTypes) {
        const count = raw.clearedCounts[type];
        if (!Number.isInteger(count) || count < 0 || count > MAX_SCAN_ITEMS) {
          throw invalidStorageResult();
        }
        common.clearedCounts[type] = count;
      }
      if (Object.keys(raw.clearedCounts).length !== common.requestedTypes.length) {
        throw invalidStorageResult();
      }
      break;
    default:
      throw invalidStorageResult();
  }
  return {
    kind: raw.kind,
    ...common,
  };
}

function normalizeItem(item, valuesIncluded) {
  if (!isObject(item)) throw invalidStorageResult();
  validateBoundedString(item.key, MAX_KEY_BYTES, "item.key");
  if (
    !Number.isInteger(item.valueLength) ||
    item.valueLength < 0 ||
    item.valueLength > MAX_OBSERVED_VALUE_BYTES ||
    typeof item.valueIncluded !== "boolean" ||
    typeof item.value !== "string"
  ) {
    throw invalidStorageResult();
  }
  if (!valuesIncluded && (item.valueIncluded || item.value !== MASKED_VALUE)) {
    throw invalidStorageResult();
  }
  if (
    valuesIncluded &&
    !(
      (item.valueIncluded &&
        byteLength(item.value) === item.valueLength &&
        item.valueLength <= MAX_VALUE_BYTES) ||
      (!item.valueIncluded && item.value === OMITTED_VALUE && item.valueLength > MAX_VALUE_BYTES)
    )
  ) {
    throw invalidStorageResult();
  }
  return {
    key: item.key,
    value: item.value,
    valueIncluded: item.valueIncluded,
    valueLength: item.valueLength,
  };
}

function unwrapInjectedResult(value) {
  if (!isObject(value) || typeof value.ok !== "boolean") throw invalidStorageResult();
  if (value.ok) return value.data;
  const error = value.error;
  const allowed = new Set([
    ErrorCode.INVALID_MESSAGE,
    ErrorCode.STALE_TARGET,
    ErrorCode.CAPABILITY_UNAVAILABLE,
    ErrorCode.PAYLOAD_TOO_LARGE,
    ErrorCode.INTERNAL_ERROR,
  ]);
  if (!isObject(error) || !allowed.has(error.code) || typeof error.message !== "string") {
    throw invalidStorageResult();
  }
  throw protocolError(error.code, error.message.slice(0, 500), Boolean(error.retryable));
}

async function requireSensitiveMode(getSettings) {
  const settings = await getSettings();
  if (settings?.featureFlags?.sensitiveData !== true) {
    throw protocolError(
      ErrorCode.CAPABILITY_UNAVAILABLE,
      "Sensitive data mode is disabled in extension settings",
    );
  }
}

async function resolveTab(chromeAPI, explicitTabId) {
  if (Number.isInteger(explicitTabId)) {
    try {
      return await chromeAPI.tabs.get(explicitTabId);
    } catch {
      throw protocolError(ErrorCode.TAB_NOT_FOUND, `Tab ${explicitTabId} was not found`, true);
    }
  }
  const tabs = await chromeAPI.tabs.query({ active: true, lastFocusedWindow: true });
  if (!tabs[0]) throw protocolError(ErrorCode.TAB_NOT_FOUND, "No active tab was found", true);
  return tabs[0];
}

async function currentRootDocument(chromeAPI, tabId) {
  try {
    const frame = await chromeAPI.webNavigation.getFrame({ tabId, frameId: 0 });
    if (frame?.documentId) return frame;
  } catch (error) {
    throw mapChromeError(error);
  }
  throw protocolError(ErrorCode.FRAME_NOT_FOUND, "The root document is unavailable", true);
}

function parseHTTPURL(value) {
  let parsed;
  try {
    parsed = new URL(value);
  } catch {
    throw invalidStorageParams("A valid HTTP(S) origin is required");
  }
  if (!["http:", "https:"].includes(parsed.protocol) || parsed.username || parsed.password) {
    throw invalidStorageParams("A valid HTTP(S) origin is required");
  }
  return parsed;
}

function validStorageTypes(values, allowed) {
  return (
    Array.isArray(values) &&
    values.length > 0 &&
    values.length <= allowed.size &&
    new Set(values).size === values.length &&
    values.every((value) => allowed.has(value))
  );
}

function normalizeWarnings(value) {
  if (
    !Array.isArray(value) ||
    value.length > 4 ||
    value.some(
      (warning) =>
        typeof warning !== "string" || warning.trim().length === 0 || warning.length > 256,
    )
  ) {
    throw invalidStorageResult();
  }
  return [...new Set(value)];
}

function normalizeCursor(value) {
  if (value === "") return "";
  validateCursor(value);
  return value;
}

function normalizeCount(value) {
  if (!Number.isInteger(value) || value < 0 || value > MAX_SCAN_ITEMS) {
    throw invalidStorageResult();
  }
  return value;
}

function validateBoundedString(value, maximum, path) {
  if (typeof value !== "string" || byteLength(value) > maximum) {
    throw invalidStorageParams(`${path} exceeds its UTF-8 byte limit`);
  }
}

function assertObject(value, allowedKeys) {
  if (!isObject(value) || Object.keys(value).some((key) => !allowedKeys.includes(key))) {
    throw invalidStorageParams("Storage parameters have an invalid shape");
  }
}

function isObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function validateTimeout(value) {
  const timeout = value || DEFAULT_TIMEOUT_MS;
  if (!Number.isInteger(timeout) || timeout < 1 || timeout > MAX_TIMEOUT_MS) {
    throw invalidStorageParams("Storage timeout is outside the supported range");
  }
  return timeout;
}

function withTimeout(operation, parentSignal, timeoutMs) {
  if (parentSignal.aborted) {
    return Promise.reject(protocolError(ErrorCode.CANCELLED, "Command was cancelled", true));
  }
  const controller = new AbortController();
  const onParentAbort = () =>
    controller.abort(protocolError(ErrorCode.CANCELLED, "Command was cancelled", true));
  parentSignal.addEventListener("abort", onParentAbort, { once: true });
  const timeout = setTimeout(
    () =>
      controller.abort(
        protocolError(ErrorCode.TIMEOUT, `Command timed out after ${timeoutMs} ms`, true),
      ),
    timeoutMs,
  );
  return operation(controller.signal).finally(() => {
    clearTimeout(timeout);
    parentSignal.removeEventListener("abort", onParentAbort);
  });
}

function abortable(promise, signal) {
  throwIfCancelled(signal);
  return new Promise((resolve, reject) => {
    const onAbort = () => reject(signal.reason);
    signal.addEventListener("abort", onAbort, { once: true });
    Promise.resolve(promise)
      .then(resolve, reject)
      .finally(() => signal.removeEventListener("abort", onAbort));
  });
}

function throwIfCancelled(signal) {
  if (signal.aborted) {
    throw signal.reason || protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
  }
}

function byteLength(value) {
  return encoder.encode(value).byteLength;
}

function invalidStorageParams(message) {
  return protocolError(ErrorCode.INVALID_MESSAGE, message);
}

function invalidStorageResult() {
  return protocolError(ErrorCode.INVALID_MESSAGE, "The browser returned an invalid storage result");
}

// This function is serialized by chrome.scripting and must remain self-contained.
export async function isolatedStorageOperation(operation, params) {
  const MAX_SCAN = 10_000;
  const MAX_KEY = 1_024;
  const MAX_VALUE = 64 * 1_024;
  const MAX_OBSERVED = 1_000_000;
  const MAX_NAME = 1_024;
  const encoder = new TextEncoder();
  const bytes = (value) => encoder.encode(value).byteLength;
  const ok = (data) => ({ ok: true, data });
  const fail = (code, message, retryable = false) => ({
    ok: false,
    error: { code, message, retryable },
  });
  const base = (kind) => ({
    kind,
    storageType: "",
    valuesIncluded: false,
    items: [],
    caches: [],
    databases: [],
    totalMatched: 0,
    nextCursor: "",
    operation: "",
    changed: false,
    supported: true,
    requestedTypes: [],
    clearedTypes: [],
    clearedCounts: null,
    warnings: [],
  });
  const page = (values) => {
    if (!Array.isArray(values) || values.length > MAX_SCAN) {
      return null;
    }
    const offset = params.cursor ? Number(params.cursor) : 0;
    return {
      values: values.slice(offset, offset + params.limit),
      totalMatched: values.length,
      nextCursor:
        offset + Math.min(params.limit, values.length - offset) < values.length
          ? String(offset + params.limit)
          : "",
    };
  };
  const storageArea = () =>
    params.storageType === "localStorage" ? globalThis.localStorage : globalThis.sessionStorage;
  const item = (key, value) => {
    const valueLength = bytes(value);
    if (bytes(key) > MAX_KEY || valueLength > MAX_OBSERVED) return null;
    const valueIncluded = params.includeValues === true && valueLength <= MAX_VALUE;
    return {
      key,
      value: valueIncluded ? value : params.includeValues ? "[OMITTED]" : "[MASKED]",
      valueIncluded,
      valueLength,
    };
  };
  const storageEntries = (storage) => {
    if (storage.length > MAX_SCAN) return null;
    const entries = [];
    for (let index = 0; index < storage.length; index += 1) {
      const key = storage.key(index);
      if (typeof key !== "string") return null;
      const value = storage.getItem(key);
      if (typeof value !== "string") return null;
      entries.push([key, value]);
    }
    entries.sort(([left], [right]) => left.localeCompare(right));
    return entries;
  };
  const deleteDatabase = (name) =>
    new Promise((resolve) => {
      const request = globalThis.indexedDB.deleteDatabase(name);
      request.onsuccess = () => resolve(true);
      request.onerror = () => resolve(false);
      request.onblocked = () => resolve(false);
    });

  if (globalThis.location.origin !== params.origin) {
    return fail("STALE_TARGET", "The root document origin changed", true);
  }
  try {
    if (operation === "list" || operation === "get") {
      const storage = storageArea();
      const result = base(operation === "list" ? "items" : "item");
      result.storageType = params.storageType;
      result.valuesIncluded = params.includeValues === true;
      if (operation === "get") {
        const value = storage.getItem(params.key);
        if (value !== null) {
          const mapped = item(params.key, value);
          if (!mapped) return fail("PAYLOAD_TOO_LARGE", "The storage item exceeds limits");
          result.items = [mapped];
          result.totalMatched = 1;
        }
        return ok(result);
      }
      const entries = storageEntries(storage);
      if (!entries) return fail("PAYLOAD_TOO_LARGE", "The storage area exceeds scan limits");
      const pagination = page(entries);
      if (!pagination) return fail("PAYLOAD_TOO_LARGE", "The storage area exceeds scan limits");
      const items = pagination.values.map(([key, value]) => item(key, value));
      if (items.some((value) => value === null)) {
        return fail("PAYLOAD_TOO_LARGE", "A storage item exceeds limits");
      }
      result.items = items;
      result.totalMatched = pagination.totalMatched;
      result.nextCursor = pagination.nextCursor;
      if (items.some((value) => !value.valueIncluded && params.includeValues)) {
        result.warnings.push("One or more oversized storage values were omitted");
      }
      return ok(result);
    }

    if (operation === "set" || operation === "remove") {
      const storage = storageArea();
      const previous = storage.getItem(params.key);
      if (operation === "set") storage.setItem(params.key, params.value);
      else storage.removeItem(params.key);
      const result = base("mutation");
      result.storageType = params.storageType;
      result.operation = operation;
      result.changed = operation === "set" ? previous !== params.value : previous !== null;
      return ok(result);
    }

    if (operation === "cacheMetadata") {
      if (!globalThis.caches?.keys) {
        return fail("CAPABILITY_UNAVAILABLE", "Cache Storage is unavailable for this origin");
      }
      const names = await globalThis.caches.keys();
      if (
        !Array.isArray(names) ||
        names.length > MAX_SCAN ||
        names.some((name) => typeof name !== "string" || bytes(name) > MAX_NAME)
      ) {
        return fail("PAYLOAD_TOO_LARGE", "Cache Storage metadata exceeds limits");
      }
      names.sort((left, right) => left.localeCompare(right));
      const pagination = page(names);
      const result = base("cacheMetadata");
      result.caches = pagination.values.map((name) => ({ name }));
      result.totalMatched = pagination.totalMatched;
      result.nextCursor = pagination.nextCursor;
      return ok(result);
    }

    if (operation === "indexedDBMetadata") {
      if (typeof globalThis.indexedDB?.databases !== "function") {
        return fail("CAPABILITY_UNAVAILABLE", "IndexedDB metadata is unavailable for this origin");
      }
      const databases = await globalThis.indexedDB.databases();
      if (
        !Array.isArray(databases) ||
        databases.length > MAX_SCAN ||
        databases.some(
          (database) =>
            typeof database?.name !== "string" ||
            bytes(database.name) > MAX_NAME ||
            !Number.isSafeInteger(database.version) ||
            database.version < 1,
        )
      ) {
        return fail("PAYLOAD_TOO_LARGE", "IndexedDB metadata exceeds limits");
      }
      databases.sort((left, right) =>
        left.name === right.name
          ? left.version - right.version
          : left.name.localeCompare(right.name),
      );
      const pagination = page(databases);
      const result = base("indexedDBMetadata");
      result.databases = pagination.values.map(({ name, version }) => ({ name, version }));
      result.totalMatched = pagination.totalMatched;
      result.nextCursor = pagination.nextCursor;
      return ok(result);
    }

    if (operation === "clear") {
      const result = base("clear");
      result.operation = "clear";
      result.requestedTypes = [...params.types];
      result.clearedCounts = {};

      // Complete every availability and size check before the first mutation.
      // Browser storage APIs are not transactional, but a failed preflight must
      // never leave an earlier requested storage type already cleared.
      const inventories = {};
      for (const type of params.types) {
        if (type === "localStorage" || type === "sessionStorage") {
          const storage =
            type === "localStorage" ? globalThis.localStorage : globalThis.sessionStorage;
          if (storage.length > MAX_SCAN) {
            return fail("PAYLOAD_TOO_LARGE", "The storage area exceeds clear limits");
          }
          inventories[type] = { storage, count: storage.length };
        } else if (type === "cacheStorage") {
          if (!globalThis.caches?.keys) {
            return fail("CAPABILITY_UNAVAILABLE", "Cache Storage is unavailable for this origin");
          }
          const names = await globalThis.caches.keys();
          if (
            !Array.isArray(names) ||
            names.length > MAX_SCAN ||
            names.some((name) => typeof name !== "string" || bytes(name) > MAX_NAME)
          ) {
            return fail("PAYLOAD_TOO_LARGE", "Cache Storage exceeds clear limits");
          }
          inventories[type] = { names };
        } else if (type === "indexedDB") {
          if (typeof globalThis.indexedDB?.databases !== "function") {
            return fail(
              "CAPABILITY_UNAVAILABLE",
              "IndexedDB metadata is unavailable for this origin",
            );
          }
          const databases = await globalThis.indexedDB.databases();
          if (
            !Array.isArray(databases) ||
            databases.length > MAX_SCAN ||
            databases.some(
              (database) => typeof database?.name !== "string" || bytes(database.name) > MAX_NAME,
            )
          ) {
            return fail("PAYLOAD_TOO_LARGE", "IndexedDB metadata exceeds clear limits");
          }
          inventories[type] = { names: databases.map(({ name }) => name) };
        }
      }

      for (const type of params.types) {
        let count = 0;
        const inventory = inventories[type];
        if (type === "localStorage" || type === "sessionStorage") {
          inventory.storage.clear();
          count = inventory.count;
        } else if (type === "cacheStorage") {
          const deleted = await Promise.all(
            inventory.names.map((name) => globalThis.caches.delete(name)),
          );
          count = deleted.filter(Boolean).length;
          if (count !== inventory.names.length) {
            result.warnings.push("One or more caches could not be deleted");
          }
        } else if (type === "indexedDB") {
          const deleted = await Promise.all(inventory.names.map((name) => deleteDatabase(name)));
          count = deleted.filter(Boolean).length;
          if (count !== inventory.names.length) {
            result.warnings.push("One or more IndexedDB databases were blocked or not deleted");
          }
        }
        result.clearedTypes.push(type);
        result.clearedCounts[type] = count;
      }
      result.warnings = [...new Set(result.warnings)].slice(0, 4);
      return ok(result);
    }
    return fail("INVALID_MESSAGE", "The storage operation is unsupported");
  } catch {
    return fail("INTERNAL_ERROR", "The isolated storage operation failed");
  }
}
