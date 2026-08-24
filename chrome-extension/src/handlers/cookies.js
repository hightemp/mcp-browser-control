import { ErrorCode, assertFreshDocument, mapChromeError, protocolError } from "../protocol.js";

const DEFAULT_TIMEOUT_MS = 30_000;
const MAX_TIMEOUT_MS = 120_000;
const MAX_COOKIES = 200;
const MAX_SCAN_COOKIES = 10_000;
const MAX_NAME_BYTES = 256;
const MAX_VALUE_BYTES = 4_096;
const MAX_DOMAIN_BYTES = 253;
const MAX_PATH_BYTES = 2_048;
const MAX_STORE_ID_BYTES = 256;
const MASKED_VALUE = "[MASKED]";
const OMITTED_VALUE = "[OMITTED]";
const COOKIE_NAME = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;
const SAME_SITE = new Set(["no_restriction", "lax", "strict", "unspecified"]);
const encoder = new TextEncoder();

export function createCookieHandlers(chromeAPI, { getSettings = async () => ({}) } = {}) {
  async function list(request, parentSignal) {
    const params = validateListParams(request.params);
    return runCookieOperation(chromeAPI, request, parentSignal, async (target, signal) => {
      const sensitive = request.command === "cookies.listSensitive";
      if (sensitive) await requireSensitiveMode(getSettings);
      const cookies = await callCookies(chromeAPI, "getAll", cookieQuery(params, target.storeId));
      throwIfCancelled(signal);
      if (!Array.isArray(cookies) || cookies.length > MAX_SCAN_COOKIES) throw invalidCookieResult();
      const offset = params.cursor ? Number(params.cursor) : 0;
      const page = cookies.slice(offset, offset + params.limit);
      await recheckTarget(chromeAPI, target);
      return cookieResult("list", target, page, sensitive, {
        totalMatched: cookies.length,
        nextCursor: offset + page.length < cookies.length ? String(offset + page.length) : "",
      });
    });
  }

  async function get(request, parentSignal) {
    const params = validateIdentityParams(request.params);
    return runCookieOperation(chromeAPI, request, parentSignal, async (target, signal) => {
      const sensitive = request.command === "cookies.getSensitive";
      if (sensitive) await requireSensitiveMode(getSettings);
      const cookie = await callCookies(chromeAPI, "get", {
        url: params.url,
        name: params.name,
        storeId: target.storeId,
        ...(params.partitionKey ? { partitionKey: params.partitionKey } : {}),
      });
      throwIfCancelled(signal);
      await recheckTarget(chromeAPI, target);
      return cookieResult("get", target, cookie ? [cookie] : [], sensitive, {
        totalMatched: cookie ? 1 : 0,
      });
    });
  }

  async function set(request, parentSignal) {
    const params = validateSetParams(request.params);
    return runCookieOperation(chromeAPI, request, parentSignal, async (target, signal) => {
      const details = { ...params, storeId: target.storeId };
      const cookie = await callCookies(chromeAPI, "set", details);
      throwIfCancelled(signal);
      if (!cookie) throw invalidCookieResult();
      await recheckTarget(chromeAPI, target);
      return cookieResult("set", target, [cookie], false, { totalMatched: 1 });
    });
  }

  async function remove(request, parentSignal) {
    const params = validateIdentityParams(request.params);
    return runCookieOperation(chromeAPI, request, parentSignal, async (target, signal) => {
      const removed = await callCookies(chromeAPI, "remove", {
        url: params.url,
        name: params.name,
        storeId: target.storeId,
        ...(params.partitionKey ? { partitionKey: params.partitionKey } : {}),
      });
      throwIfCancelled(signal);
      await recheckTarget(chromeAPI, target);
      return {
        kind: "remove",
        tabId: target.tab.id,
        documentId: target.documentId,
        origin: target.origin,
        valuesIncluded: false,
        cookies: [],
        totalMatched: 0,
        nextCursor: "",
        removed: Boolean(removed),
        warnings: [],
      };
    });
  }

  return {
    list,
    listSensitive: list,
    get,
    getSensitive: get,
    set,
    remove,
  };
}

async function runCookieOperation(chromeAPI, request, parentSignal, operation) {
  const timeoutMs = validateTimeout(request.timeoutMs);
  return withTimeout(
    async (signal) => {
      const target = await prepareTarget(chromeAPI, request, signal);
      return operation(target, signal);
    },
    parentSignal,
    timeoutMs,
  );
}

async function prepareTarget(chromeAPI, request, signal) {
  if (!chromeAPI.cookies?.getAllCookieStores) {
    throw protocolError(ErrorCode.CAPABILITY_UNAVAILABLE, "The browser cookie API is unavailable");
  }
  const tab = await resolveTab(chromeAPI, request.target?.tabId);
  const tabURL = parseHTTPURL(tab.url, "The selected tab URL is unavailable");
  const requestedURL = parseHTTPURL(request.params.url, "params.url must be an HTTP(S) URL");
  if (requestedURL.origin !== tabURL.origin) {
    throw protocolError(
      ErrorCode.RESTRICTED_URL,
      "Cookie operations are limited to the selected root-document origin",
    );
  }
  await requireCookieAccess(chromeAPI, tabURL);
  const frame = await currentRootDocument(chromeAPI, tab.id);
  if (request.target?.documentId) {
    assertFreshDocument(request.target.documentId, frame.documentId);
  }
  const storeId = await resolveCookieStore(chromeAPI, tab.id, request.params.storeId);
  throwIfCancelled(signal);
  return {
    tab,
    origin: tabURL.origin,
    documentId: frame.documentId,
    storeId,
  };
}

async function recheckTarget(chromeAPI, target) {
  const tab = await resolveTab(chromeAPI, target.tab.id);
  const currentURL = parseHTTPURL(tab.url, "The selected tab URL is unavailable");
  if (currentURL.origin !== target.origin) {
    throw protocolError(ErrorCode.STALE_TARGET, "The target tab navigated to another origin", true);
  }
  await requireCookieAccess(chromeAPI, currentURL);
  const frame = await currentRootDocument(chromeAPI, target.tab.id);
  assertFreshDocument(target.documentId, frame.documentId);
  await resolveCookieStore(chromeAPI, target.tab.id, target.storeId);
}

async function requireCookieAccess(chromeAPI, tabURL) {
  const granted = await chromeAPI.permissions.contains({
    permissions: ["cookies"],
    origins: [`${tabURL.protocol}//${tabURL.hostname}/*`],
  });
  if (!granted) {
    throw protocolError(
      ErrorCode.PERMISSION_REQUIRED,
      "Cookies and site access are required. Grant Personal data and Observe in settings.",
      false,
      { origin: tabURL.origin },
    );
  }
}

async function resolveCookieStore(chromeAPI, tabId, requestedStoreId) {
  let stores;
  try {
    stores = await chromeAPI.cookies.getAllCookieStores();
  } catch (error) {
    throw mapChromeError(error);
  }
  if (!Array.isArray(stores)) throw invalidCookieResult();
  const containing = stores.filter(
    (store) =>
      store && safeStoreId(store.id) && Array.isArray(store.tabIds) && store.tabIds.includes(tabId),
  );
  const selected = requestedStoreId
    ? containing.find((store) => store.id === requestedStoreId)
    : containing[0];
  if (!selected) {
    throw protocolError(
      ErrorCode.PERMISSION_REQUIRED,
      "The requested cookie store does not contain the selected tab",
    );
  }
  return selected.id;
}

function validateListParams(params) {
  assertObject(params, [
    "url",
    "domain",
    "name",
    "path",
    "secure",
    "session",
    "storeId",
    "partitionKey",
    "cursor",
    "limit",
  ]);
  const parsed = parseHTTPURL(params.url, "params.url must be an HTTP(S) URL");
  validateDomain(params.domain, parsed.hostname, true);
  if (params.name !== undefined) validateCookieName(params.name, "params.name");
  if (params.path !== undefined) validatePath(params.path);
  validateOptionalBoolean(params.secure, "params.secure");
  validateOptionalBoolean(params.session, "params.session");
  validateStoreId(params.storeId);
  validatePartitionKey(params.partitionKey, parsed.origin);
  if (
    params.cursor !== undefined &&
    (typeof params.cursor !== "string" ||
      !/^\d+$/.test(params.cursor) ||
      !Number.isSafeInteger(Number(params.cursor)) ||
      Number(params.cursor) < 1)
  ) {
    throw invalidCookieParams("params.cursor is invalid");
  }
  if (!Number.isInteger(params.limit) || params.limit < 1 || params.limit > MAX_COOKIES) {
    throw invalidCookieParams("params.limit is outside the supported range");
  }
  return params;
}

function validateIdentityParams(params) {
  assertObject(params, ["url", "name", "storeId", "partitionKey"]);
  const parsed = parseHTTPURL(params.url, "params.url must be an HTTP(S) URL");
  validateCookieName(params.name, "params.name");
  validateStoreId(params.storeId);
  validatePartitionKey(params.partitionKey, parsed.origin);
  return params;
}

function validateSetParams(params) {
  assertObject(params, [
    "url",
    "name",
    "value",
    "domain",
    "path",
    "secure",
    "httpOnly",
    "sameSite",
    "expirationDate",
    "storeId",
    "partitionKey",
  ]);
  const parsed = parseHTTPURL(params.url, "params.url must be an HTTP(S) URL");
  validateCookieName(params.name, "params.name");
  if (
    typeof params.value !== "string" ||
    byteLength(params.value) > MAX_VALUE_BYTES ||
    hasControl(params.value) ||
    params.value.includes(";")
  ) {
    throw invalidCookieParams("params.value is invalid or exceeds 4096 bytes");
  }
  validateDomain(params.domain, parsed.hostname, true);
  if (params.path !== undefined) validatePath(params.path);
  validateOptionalBoolean(params.secure, "params.secure");
  validateOptionalBoolean(params.httpOnly, "params.httpOnly");
  if (params.sameSite !== undefined && !SAME_SITE.has(params.sameSite)) {
    throw invalidCookieParams("params.sameSite is unsupported");
  }
  if (params.sameSite === "no_restriction" && params.secure !== true) {
    throw invalidCookieParams("SameSite=None requires secure=true");
  }
  if (
    params.expirationDate !== undefined &&
    (!Number.isFinite(params.expirationDate) || params.expirationDate <= 0)
  ) {
    throw invalidCookieParams("params.expirationDate must be a positive finite timestamp");
  }
  validateStoreId(params.storeId);
  validatePartitionKey(params.partitionKey, parsed.origin);
  return params;
}

function cookieQuery(params, storeId) {
  const query = { url: params.url, storeId };
  for (const name of ["domain", "name", "path", "secure", "session", "partitionKey"]) {
    if (params[name] !== undefined) query[name] = params[name];
  }
  return query;
}

function cookieResult(kind, target, cookies, sensitive, extras) {
  const warnings = [];
  const mapped = cookies.map((cookie) => mapCookie(cookie, sensitive, target, warnings));
  return {
    kind,
    tabId: target.tab.id,
    documentId: target.documentId,
    origin: target.origin,
    valuesIncluded: sensitive,
    cookies: mapped,
    totalMatched: extras.totalMatched,
    nextCursor: extras.nextCursor || "",
    removed: false,
    warnings: [...new Set(warnings)].slice(0, 4),
  };
}

function mapCookie(cookie, sensitive, target, warnings) {
  if (!cookie || typeof cookie !== "object") throw invalidCookieResult();
  validateCookieName(cookie.name, "cookie.name");
  validateDomain(cookie.domain, new URL(target.origin).hostname, false);
  validatePath(cookie.path);
  validateStoreId(cookie.storeId);
  if (
    !SAME_SITE.has(cookie.sameSite) ||
    typeof cookie.value !== "string" ||
    hasControl(cookie.value)
  ) {
    throw invalidCookieResult();
  }
  const valueLength = byteLength(cookie.value);
  if (valueLength > 1_000_000) throw invalidCookieResult();
  if (valueLength > MAX_VALUE_BYTES) {
    warnings.push("One or more oversized cookie values were omitted");
  }
  const valueIncluded = sensitive && valueLength <= MAX_VALUE_BYTES;
  const mapped = {
    name: cookie.name,
    value: valueIncluded ? cookie.value : sensitive ? OMITTED_VALUE : MASKED_VALUE,
    valueIncluded,
    valueLength,
    domain: cookie.domain.toLowerCase(),
    hostOnly: Boolean(cookie.hostOnly),
    path: cookie.path,
    secure: Boolean(cookie.secure),
    httpOnly: Boolean(cookie.httpOnly),
    sameSite: cookie.sameSite,
    session: Boolean(cookie.session),
    storeId: cookie.storeId,
  };
  if (!mapped.session) {
    if (!Number.isFinite(cookie.expirationDate) || cookie.expirationDate <= 0) {
      throw invalidCookieResult();
    }
    mapped.expirationDate = cookie.expirationDate;
  }
  if (cookie.partitionKey !== undefined) {
    validatePartitionKey(cookie.partitionKey, target.origin);
    mapped.partitionKey = {
      topLevelSite: new URL(cookie.partitionKey.topLevelSite).origin,
      ...(cookie.partitionKey.hasCrossSiteAncestor !== undefined
        ? { hasCrossSiteAncestor: cookie.partitionKey.hasCrossSiteAncestor }
        : {}),
    };
  }
  return mapped;
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

async function callCookies(chromeAPI, method, details) {
  try {
    return await chromeAPI.cookies[method](details);
  } catch (error) {
    throw mapChromeError(error);
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

function parseHTTPURL(value, message) {
  let parsed;
  try {
    parsed = new URL(value);
  } catch {
    throw invalidCookieParams(message);
  }
  if (
    !["http:", "https:"].includes(parsed.protocol) ||
    parsed.username ||
    parsed.password ||
    parsed.hash ||
    String(value).length > 8192
  ) {
    throw invalidCookieParams(message);
  }
  return parsed;
}

function validateDomain(value, urlHost, optional) {
  if (value === undefined && optional) return;
  if (typeof value !== "string" || value !== value.trim() || byteLength(value) > MAX_DOMAIN_BYTES) {
    throw invalidCookieParams("Cookie domain is invalid");
  }
  const host = value.toLowerCase().replace(/^\./, "");
  if (!validHostname(host) || !(urlHost === host || urlHost.endsWith(`.${host}`))) {
    throw invalidCookieParams("Cookie domain must contain the URL host or a parent domain");
  }
}

function validHostname(value) {
  return (
    value.length > 0 &&
    !value.startsWith(".") &&
    !value.endsWith(".") &&
    value
      .split(".")
      .every(
        (label) =>
          label.length > 0 && label.length <= 63 && /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(label),
      )
  );
}

function validateCookieName(value, path) {
  if (
    typeof value !== "string" ||
    byteLength(value) === 0 ||
    byteLength(value) > MAX_NAME_BYTES ||
    !COOKIE_NAME.test(value)
  ) {
    throw invalidCookieParams(`${path} is invalid`);
  }
}

function validatePath(value) {
  if (
    typeof value !== "string" ||
    !value.startsWith("/") ||
    byteLength(value) > MAX_PATH_BYTES ||
    hasControl(value) ||
    value.includes(";")
  ) {
    throw invalidCookieParams("Cookie path is invalid");
  }
}

function validateStoreId(value) {
  if (value !== undefined && !safeStoreId(value)) {
    throw invalidCookieParams("Cookie storeId is invalid");
  }
}

function safeStoreId(value) {
  return typeof value === "string" && byteLength(value) <= MAX_STORE_ID_BYTES && !hasControl(value);
}

function validatePartitionKey(value, origin) {
  if (value === undefined) return;
  assertObject(value, ["topLevelSite", "hasCrossSiteAncestor"]);
  const site = parseHTTPURL(value.topLevelSite, "partitionKey.topLevelSite is invalid");
  if (site.origin !== origin || (site.pathname !== "/" && site.pathname !== "") || site.search) {
    throw invalidCookieParams(
      "partitionKey.topLevelSite must exactly match the selected URL origin",
    );
  }
  validateOptionalBoolean(value.hasCrossSiteAncestor, "partitionKey.hasCrossSiteAncestor");
}

function assertObject(value, allowedKeys) {
  if (
    !value ||
    typeof value !== "object" ||
    Array.isArray(value) ||
    Object.keys(value).some((key) => !allowedKeys.includes(key))
  ) {
    throw invalidCookieParams("Cookie parameters have an invalid shape");
  }
}

function validateOptionalBoolean(value, path) {
  if (value !== undefined && typeof value !== "boolean") {
    throw invalidCookieParams(`${path} must be a boolean`);
  }
}

function validateTimeout(value) {
  const timeout = value || DEFAULT_TIMEOUT_MS;
  if (!Number.isInteger(timeout) || timeout < 1 || timeout > MAX_TIMEOUT_MS) {
    throw invalidCookieParams("Cookie timeout is outside the supported range");
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

function throwIfCancelled(signal) {
  if (signal.aborted) {
    throw signal.reason || protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
  }
}

function byteLength(value) {
  return encoder.encode(value).byteLength;
}

function hasControl(value) {
  return [...value].some((character) => {
    const code = character.codePointAt(0);
    return code < 32 || code === 127;
  });
}

function invalidCookieParams(message) {
  return protocolError(ErrorCode.INVALID_MESSAGE, message);
}

function invalidCookieResult() {
  return protocolError(ErrorCode.INVALID_MESSAGE, "The browser returned an invalid cookie result");
}
