import { ContentScriptBridge } from "../content-bridge.js";
import { ErrorCode, assertFreshDocument, mapChromeError, protocolError } from "../protocol.js";

const DEFAULT_COMMAND_TIMEOUT_MS = 30_000;
const MAX_SCANNED_AX_NODES = 20_000;
const MAX_FRAMES = 100;
const MAX_FRAME_DEPTH = 100;
const REFERENCE_BATCH_SIZE = 8;
const SENSITIVE_IDENTITY =
  /(?:password|passwd|passphrase|secret|token|credential|authorization|cookie|api[-_ ]?key)/i;

export function createAccessibilityHandlers(chromeAPI, { cdpSessions } = {}) {
  const bridge = new ContentScriptBridge(chromeAPI);
  let operationSequence = 0;

  function getTree(request, signal) {
    const timeoutMs = request.timeoutMs || DEFAULT_COMMAND_TIMEOUT_MS;
    return withTimeout(
      (commandSignal) => executeGetTree(request, commandSignal),
      signal,
      timeoutMs,
    );
  }

  async function executeGetTree(request, signal) {
    if (!cdpSessions) {
      throw protocolError(ErrorCode.CAPABILITY_UNAVAILABLE, "Managed CDP sessions are unavailable");
    }
    const tab = await resolveTab(chromeAPI, request.target?.tabId);
    await assertPageAccess(chromeAPI, tab);
    const debuggerGranted = await chromeAPI.permissions.contains({ permissions: ["debugger"] });
    if (!debuggerGranted) {
      throw protocolError(
        ErrorCode.PERMISSION_REQUIRED,
        "Debug permission is required. Grant it from the extension settings page.",
      );
    }
    const rootDocument = await resolveRootDocument(chromeAPI, request, tab.id);
    throwIfCancelled(signal);

    operationSequence += 1;
    const method =
      request.params.mode === "partial"
        ? "Accessibility.getPartialAXTree"
        : "Accessibility.getFullAXTree";
    const commandParams =
      request.params.mode === "partial"
        ? {
            backendNodeId: request.params.backendNodeId,
            fetchRelatives: request.params.fetchRelatives,
          }
        : { depth: request.params.maxDepth };

    return cdpSessions.withSession(
      { tabId: tab.id },
      {
        consumerId: `accessibility:${String(request.requestId || operationSequence).slice(0, 100)}`,
        domains: ["Accessibility", "Page"],
        commands: [method, "Page.getFrameTree"],
        signal,
      },
      async (lease) => {
        const currentTab = await resolveTab(chromeAPI, tab.id);
        await assertPageAccess(chromeAPI, currentTab);
        const currentDocument = await resolveRootDocument(chromeAPI, request, tab.id);
        assertFreshDocument(rootDocument.documentId, currentDocument.documentId);
        throwIfCancelled(signal);

        const frameTreeResult = await lease.sendCommand("Page.getFrameTree", {}, { signal });
        const treeResult = await lease.sendCommand(method, commandParams, { signal });
        const finalDocument = await resolveRootDocument(chromeAPI, request, tab.id);
        assertFreshDocument(rootDocument.documentId, finalDocument.documentId);
        throwIfCancelled(signal);

        const frameResult = normalizeFrameTree(frameTreeResult?.frameTree);
        const nodeResult = normalizeAXNodes(
          treeResult?.nodes,
          request.params,
          frameResult.rootFrameId,
        );
        const warnings = [...frameResult.warnings, ...nodeResult.warnings];
        if (request.params.includeElementReferences) {
          await attachElementReferences({
            bridge,
            nodes: nodeResult.nodes,
            tabId: tab.id,
            documentId: rootDocument.documentId,
            rootFrameId: frameResult.rootFrameId,
            maxReferences: request.params.maxElementReferences,
            requestId: request.requestId || operationSequence,
            signal,
            warnings,
          });
        }
        if (!request.params.includeLocators) {
          for (const node of nodeResult.nodes) delete node.locator;
        }

        return fitResultToBudget(
          {
            mode: request.params.mode,
            tabId: tab.id,
            documentId: rootDocument.documentId,
            rootFrameId: frameResult.rootFrameId,
            frameCount: frameResult.totalCount,
            frames: frameResult.frames,
            totalNodeCount: nodeResult.totalNodeCount,
            matchingNodeCount: nodeResult.matchingNodeCount,
            returnedNodeCount: nodeResult.nodes.length,
            nodes: nodeResult.nodes,
            truncated: frameResult.truncated || nodeResult.truncated,
            warnings: uniqueWarnings(warnings),
          },
          request.params.maxBytes,
        );
      },
    );
  }

  return { getTree };
}

function normalizeFrameTree(frameTree) {
  if (!frameTree?.frame || typeof frameTree.frame.id !== "string" || !frameTree.frame.id) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "The browser returned invalid frame metadata");
  }
  const frames = [];
  let totalCount = 0;
  let truncated = false;
  const visit = (entry, parentFrameId, depth) => {
    if (!entry?.frame || depth > MAX_FRAME_DEPTH) {
      truncated = true;
      return;
    }
    totalCount += 1;
    const frameId = boundedString(entry.frame.id, 256);
    if (!frameId) {
      throw protocolError(ErrorCode.INVALID_MESSAGE, "The browser returned invalid frame metadata");
    }
    if (frames.length < MAX_FRAMES) {
      frames.push({
        frameId,
        ...(parentFrameId ? { parentFrameId } : {}),
        ...(entry.frame.name ? { name: boundedString(entry.frame.name, 200) } : {}),
        ...(entry.frame.url ? { url: safeFrameURL(entry.frame.url) } : {}),
      });
    } else {
      truncated = true;
    }
    for (const child of Array.isArray(entry.childFrames) ? entry.childFrames : []) {
      visit(child, frameId, depth + 1);
    }
  };
  visit(frameTree, "", 0);
  return {
    rootFrameId: boundedString(frameTree.frame.id, 256),
    totalCount,
    frames,
    truncated,
    warnings: truncated ? ["Frame metadata was truncated at the configured limit"] : [],
  };
}

function normalizeAXNodes(rawNodes, params, rootFrameId) {
  if (!Array.isArray(rawNodes)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "The browser returned invalid AX nodes");
  }
  const scanned = rawNodes.slice(0, MAX_SCANNED_AX_NODES);
  const byID = new Map();
  for (const node of scanned) {
    const nodeId = boundedString(node?.nodeId, 256);
    if (!nodeId || byID.has(nodeId)) {
      throw protocolError(ErrorCode.INVALID_MESSAGE, "The browser returned invalid AX node IDs");
    }
    byID.set(nodeId, node);
  }

  const normalizedAll = scanned.map((node) => normalizeAXNode(node, byID, params, rootFrameId));
  attachLocatorHints(normalizedAll, params.includeLocators || params.includeElementReferences);
  const allowedRoles = new Set(params.roles);
  const nameFilter = params.nameContains.toLocaleLowerCase();
  const matching = normalizedAll.filter(
    (node) =>
      (params.includeIgnored || !node.ignored) &&
      (allowedRoles.size === 0 || allowedRoles.has(node.role.toLocaleLowerCase())) &&
      (!nameFilter || node.name.toLocaleLowerCase().includes(nameFilter)),
  );
  const nodes = matching.slice(0, params.maxNodes);
  const propertyTruncated = nodes.some((node) => node.propertiesTruncated);
  for (const node of nodes) delete node.propertiesTruncated;

  const truncated =
    rawNodes.length > scanned.length || matching.length > nodes.length || propertyTruncated;
  return {
    totalNodeCount: rawNodes.length,
    matchingNodeCount: matching.length,
    nodes,
    truncated,
    warnings: [
      ...(rawNodes.length > scanned.length
        ? ["Accessibility scanning was truncated at 20000 nodes"]
        : []),
      ...(matching.length > nodes.length ? ["Accessibility nodes were truncated by maxNodes"] : []),
      ...(propertyTruncated ? ["Accessibility properties were truncated by maxProperties"] : []),
    ],
  };
}

function normalizeAXNode(node, byID, params, rootFrameId) {
  const nodeId = boundedString(node.nodeId, 256);
  const parentId = boundedString(node.parentId, 256);
  const role = axValue(node.role, params.maxValueChars);
  const name = axValue(node.name, params.maxValueChars);
  const rawProperties = [
    ...(Array.isArray(node.properties) ? node.properties : []),
    ...(Array.isArray(node.ignoredReasons)
      ? node.ignoredReasons.map((property) => ({
          ...property,
          name: `ignoredReason.${String(property?.name || "unknown")}`,
        }))
      : []),
  ];
  const protectedValue = rawProperties.some(
    (property) => property?.name === "protected" && Boolean(property?.value?.value),
  );
  const sensitiveIdentity = protectedValue || SENSITIVE_IDENTITY.test(`${role} ${name}`);
  const properties = rawProperties.slice(0, params.maxProperties).map((property) => {
    const propertyName = boundedString(property?.name, 100) || "unknown";
    const sensitive = SENSITIVE_IDENTITY.test(propertyName);
    return {
      name: propertyName,
      ...(property?.value?.type ? { type: boundedString(property.value.type, 50) } : {}),
      ...(property?.value?.value !== undefined
        ? {
            value: sensitive ? "[REDACTED]" : axScalar(property.value.value, params.maxValueChars),
          }
        : {}),
    };
  });
  const backendNodeId =
    Number.isInteger(node.backendDOMNodeId) && node.backendDOMNodeId > 0
      ? node.backendDOMNodeId
      : undefined;
  return {
    nodeId,
    ...(parentId ? { parentId } : {}),
    depth: nodeDepth(node, byID),
    ignored: Boolean(node.ignored),
    ...(role ? { role } : {}),
    ...(name ? { name } : {}),
    ...(node.description?.value !== undefined
      ? { description: axValue(node.description, params.maxValueChars) }
      : {}),
    ...(node.value?.value !== undefined
      ? {
          value: sensitiveIdentity ? "[REDACTED]" : axValue(node.value, params.maxValueChars),
        }
      : {}),
    properties,
    ...(backendNodeId ? { backendNodeId } : {}),
    frameId: inheritedFrameID(node, byID, rootFrameId),
    propertiesTruncated: rawProperties.length > properties.length,
  };
}

function attachLocatorHints(nodes, enabled) {
  const groups = new Map();
  for (const node of nodes) {
    if (node.ignored || !node.role) continue;
    const key = JSON.stringify([node.role.toLocaleLowerCase(), node.name]);
    const group = groups.get(key) || [];
    group.push(node);
    groups.set(key, group);
  }
  if (!enabled) return;
  for (const group of groups.values()) {
    group.forEach((node, index) => {
      node.locator = {
        role: node.role,
        ...(node.name ? { name: node.name } : {}),
        ...(group.length === 1 ? { strict: true } : { nth: index, strict: false }),
      };
    });
  }
}

async function attachElementReferences({
  bridge,
  nodes,
  tabId,
  documentId,
  rootFrameId,
  maxReferences,
  requestId,
  signal,
  warnings,
}) {
  const candidates = nodes.filter(
    (node) => node.frameId === rootFrameId && node.locator?.strict === true,
  );
  const selected = candidates.slice(0, maxReferences);
  if (candidates.length > selected.length) {
    warnings.push("Element reference resolution was truncated by maxElementReferences");
  }
  for (let offset = 0; offset < selected.length; offset += REFERENCE_BATCH_SIZE) {
    throwIfCancelled(signal);
    const batch = selected.slice(offset, offset + REFERENCE_BATCH_SIZE);
    await Promise.all(
      batch.map(async (node, index) => {
        try {
          const result = await bridge.execute({
            tabId,
            frameId: 0,
            documentId,
            operationId: `a11y-ref:${String(requestId).slice(0, 80)}:${offset + index}`,
            command: "page.query",
            params: { locator: node.locator, limit: 2 },
            signal,
          });
          const element = result?.matchCount === 1 ? result.elements?.[0] : undefined;
          if (
            element?.reference?.documentId === documentId &&
            String(element.role || "").toLocaleLowerCase() === node.role.toLocaleLowerCase() &&
            String(element.name || "") === node.name
          ) {
            node.reference = {
              elementId: boundedString(element.reference.elementId, 256),
              documentId,
            };
            if (!node.reference.elementId) delete node.reference;
          }
        } catch (error) {
          if (signal.aborted) throw error;
        }
      }),
    );
  }
}

function fitResultToBudget(result, maxBytes) {
  if (jsonBytes(result) <= maxBytes) return result;
  const originalNodes = result.nodes;
  let low = 0;
  let high = originalNodes.length;
  while (low < high) {
    const middle = Math.ceil((low + high) / 2);
    const candidate = budgetCandidate(result, originalNodes, middle);
    if (jsonBytes(candidate) <= maxBytes) low = middle;
    else high = middle - 1;
  }
  let bounded = budgetCandidate(result, originalNodes, low);
  while (jsonBytes(bounded) > maxBytes && bounded.frames.length > 1) {
    bounded.frames = bounded.frames.slice(0, -1);
  }
  if (jsonBytes(bounded) > maxBytes) {
    throw protocolError(
      ErrorCode.PAYLOAD_TOO_LARGE,
      "Accessibility metadata exceeds the requested byte limit",
    );
  }
  return bounded;
}

function budgetCandidate(result, originalNodes, nodeCount) {
  return {
    ...result,
    nodes: originalNodes.slice(0, nodeCount),
    returnedNodeCount: nodeCount,
    truncated: true,
    warnings: uniqueWarnings([
      ...result.warnings,
      "Accessibility output was truncated by maxBytes",
    ]),
  };
}

function nodeDepth(node, byID) {
  let depth = 0;
  let current = node;
  const seen = new Set([node.nodeId]);
  while (current?.parentId && depth <= MAX_FRAME_DEPTH) {
    if (seen.has(current.parentId)) {
      throw protocolError(ErrorCode.INVALID_MESSAGE, "The browser returned a cyclic AX tree");
    }
    seen.add(current.parentId);
    current = byID.get(String(current.parentId));
    depth += 1;
    if (!current) break;
  }
  return Math.min(depth, 51);
}

function inheritedFrameID(node, byID, rootFrameId) {
  let current = node;
  const seen = new Set();
  while (current) {
    if (typeof current.frameId === "string" && current.frameId) {
      return boundedString(current.frameId, 256);
    }
    if (!current.parentId || seen.has(current.parentId)) break;
    seen.add(current.parentId);
    current = byID.get(String(current.parentId));
  }
  return rootFrameId;
}

function axValue(value, maxChars) {
  return value?.value === undefined ? "" : axScalar(value.value, maxChars);
}

function axScalar(value, maxChars) {
  if (value === null || value === undefined) return "";
  if (!["string", "number", "boolean"].includes(typeof value)) return "[complex]";
  return boundedString(String(value), maxChars);
}

function safeFrameURL(value) {
  try {
    const url = new URL(String(value));
    url.username = "";
    url.password = "";
    url.search = "";
    url.hash = "";
    return boundedString(url.toString(), 1_024);
  } catch {
    return "";
  }
}

function boundedString(value, maximum) {
  return typeof value === "string" ? value.slice(0, maximum) : "";
}

function jsonBytes(value) {
  return new TextEncoder().encode(JSON.stringify(value)).byteLength;
}

function uniqueWarnings(warnings) {
  return [...new Set(warnings.filter(Boolean))].slice(0, 10);
}

async function resolveTab(chromeAPI, explicitTabId) {
  if (Number.isInteger(explicitTabId)) {
    try {
      return await chromeAPI.tabs.get(explicitTabId);
    } catch {
      throw protocolError(ErrorCode.TAB_NOT_FOUND, `Tab ${explicitTabId} was not found`);
    }
  }
  let tabs;
  try {
    tabs = await chromeAPI.tabs.query({ active: true, lastFocusedWindow: true });
  } catch (error) {
    throw mapChromeError(error);
  }
  if (!tabs[0]) throw protocolError(ErrorCode.TAB_NOT_FOUND, "No active tab was found");
  return tabs[0];
}

async function assertPageAccess(chromeAPI, tab) {
  let parsed;
  try {
    parsed = new URL(tab.url);
  } catch {
    throw protocolError(ErrorCode.RESTRICTED_URL, "The tab URL cannot be accessed");
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw protocolError(ErrorCode.RESTRICTED_URL, `Cannot access ${parsed.protocol} pages`);
  }
  const granted = await chromeAPI.permissions.contains({
    origins: [`${parsed.protocol}//${parsed.hostname}/*`],
  });
  if (!granted) {
    throw protocolError(
      ErrorCode.PERMISSION_REQUIRED,
      "Site access is required. Grant it from the extension popup.",
      false,
      { origin: parsed.origin },
    );
  }
}

async function resolveRootDocument(chromeAPI, request, tabId) {
  let frame;
  try {
    frame = await chromeAPI.webNavigation.getFrame({ tabId, frameId: 0 });
  } catch (error) {
    throw mapChromeError(error);
  }
  if (!frame || typeof frame.documentId !== "string" || !frame.documentId) {
    throw protocolError(
      ErrorCode.CAPABILITY_UNAVAILABLE,
      "The browser did not provide a root document identity",
      true,
    );
  }
  if (request.target?.documentId) {
    assertFreshDocument(request.target.documentId, frame.documentId);
  }
  return frame;
}

function throwIfCancelled(signal) {
  if (signal.aborted) {
    throw typeof signal.reason?.code === "string"
      ? signal.reason
      : protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
  }
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
  return Promise.race([
    operation(controller.signal),
    new Promise((_, reject) => {
      controller.signal.addEventListener("abort", () => reject(controller.signal.reason), {
        once: true,
      });
    }),
  ]).finally(() => {
    clearTimeout(timeout);
    parentSignal.removeEventListener("abort", onParentAbort);
  });
}
