(() => {
  const ENGINE_VERSION = "1.0";
  const DEFAULT_REFERENCE_TTL_MS = 60_000;
  const MAX_ELEMENT_REFERENCES = 5_000;
  const MAX_DIAGNOSTIC_CANDIDATES = 5;

  if (globalThis.__mcpBrowserLocatorEngine?.version === ENGINE_VERSION) {
    return;
  }

  function createLocatorEngine({
    document: rootDocument,
    window: rootWindow,
    now = () => Date.now(),
    createID = defaultID,
    referenceTTLMS = DEFAULT_REFERENCE_TTL_MS,
  }) {
    const references = new Map();
    const elementIDs = new WeakMap();

    function query(locator, { documentId = "" } = {}) {
      purgeReferences();
      const strategy = locatorStrategy(locator);
      let matches;
      switch (strategy) {
        case "css":
          matches = queryCSS(locator.css, locator.includeShadowDOM);
          break;
        case "xpath":
          matches = queryXPath(locator.xpath, locator.includeShadowDOM);
          break;
        case "text":
          matches = queryText(locator.text, locator.includeShadowDOM);
          break;
        case "role":
          matches = queryRole(locator.role, locator.name, locator.includeShadowDOM);
          break;
        case "label":
          matches = queryLabel(locator.label, locator.includeShadowDOM);
          break;
        case "placeholder":
          matches = queryAttribute("placeholder", locator.placeholder, locator.includeShadowDOM);
          break;
        case "alt":
          matches = queryAttribute("alt", locator.alt, locator.includeShadowDOM);
          break;
        case "title":
          matches = queryAttribute("title", locator.title, locator.includeShadowDOM);
          break;
        case "testId":
          matches = queryTestID(locator.testId, locator.includeShadowDOM);
          break;
        case "coordinates":
          matches = queryCoordinates(locator.coordinates, locator.includeShadowDOM);
          break;
        case "element":
          matches = [resolveReference(locator.element, documentId)];
          break;
        default:
          throw commandError("INVALID_MESSAGE", "Locator has no supported strategy");
      }
      return {
        strategy,
        matches: uniqueElements(matches),
      };
    }

    function resolve(locator, { documentId = "", strictDefault = false } = {}) {
      const result = query(locator, { documentId });
      const count = result.matches.length;
      const strict = locator.strict ?? strictDefault;
      const diagnostics = locatorDiagnostics(result.strategy, result.matches, strict);
      if (count === 0) {
        throw commandError(
          "ELEMENT_NOT_FOUND",
          `No element matched the ${result.strategy} locator`,
          diagnostics,
        );
      }

      if (locator.nth !== undefined) {
        if (!result.matches[locator.nth]) {
          throw commandError(
            "ELEMENT_NOT_FOUND",
            `Locator matched ${count} element(s), but nth ${locator.nth} is out of range`,
            { ...diagnostics, nth: locator.nth },
          );
        }
        return {
          element: result.matches[locator.nth],
          count,
          strategy: result.strategy,
        };
      }

      if (strict && count !== 1) {
        throw commandError(
          "STRICT_MODE_VIOLATION",
          `Strict ${result.strategy} locator matched ${count} elements`,
          diagnostics,
        );
      }
      return { element: result.matches[0], count, strategy: result.strategy };
    }

    async function ensureActionable(element, { pointer = false } = {}) {
      assertAttached(element);
      if (!isVisible(element)) {
        throw commandError("ELEMENT_NOT_FOUND", "Element is not visible", {
          reason: "not-visible",
        });
      }
      if (!isEnabled(element)) {
        throw commandError("INVALID_MESSAGE", "Element is disabled", {
          reason: "disabled",
        });
      }

      let rect = element.getBoundingClientRect();
      if (!isInViewport(rect)) {
        element.scrollIntoView({ block: "center", inline: "center" });
        await nextRender();
        assertAttached(element);
        if (!isVisible(element)) {
          throw commandError("ELEMENT_NOT_FOUND", "Element is not visible after scrolling", {
            reason: "not-visible",
          });
        }
        rect = element.getBoundingClientRect();
      }

      if (pointer) {
        const style = rootWindow.getComputedStyle?.(element);
        if (style?.pointerEvents === "none") {
          throw commandError("INVALID_MESSAGE", "Element does not receive pointer events", {
            reason: "pointer-events-none",
          });
        }
        const x = clamp(rect.left + rect.width / 2, 0, rootWindow.innerWidth - 1);
        const y = clamp(rect.top + rect.height / 2, 0, rootWindow.innerHeight - 1);
        const hit = deepestElementFromPoint(rootDocument, x, y);
        if (hit && hit !== element && !element.contains?.(hit)) {
          throw commandError("INVALID_MESSAGE", "Element is covered by another element", {
            reason: "obscured",
            coveringElement: diagnosticElement(hit),
          });
        }
      }
      return rect;
    }

    function describeElement(element, index, documentId) {
      const rect = element.getBoundingClientRect();
      const reference = createReference(element, documentId);
      const attributes = [...(element.attributes || [])];
      return {
        index,
        tagName: element.tagName,
        id: String(element.id || "").slice(0, 500),
        className: String(element.className || "").slice(0, 1_000),
        text: normalizedText(element.textContent).slice(0, 500),
        role: elementRole(element),
        name: accessibleName(element).slice(0, 500),
        visible: isVisible(element),
        enabled: isEnabled(element),
        reference,
        attributes: Object.fromEntries(
          attributes
            .slice(0, 100)
            .map((attribute) => [
              attribute.name.slice(0, 200),
              shouldRedactAttribute(element, attribute)
                ? "[REDACTED]"
                : attribute.value.slice(0, 2_000),
            ]),
        ),
        attributesTruncated: attributes.length > 100,
        boundingBox: {
          x: rect.x,
          y: rect.y,
          width: rect.width,
          height: rect.height,
        },
      };
    }

    function createReference(element, documentId) {
      if (!documentId) {
        throw commandError("INTERNAL_ERROR", "The current document identity is unavailable");
      }
      let elementId = elementIDs.get(element);
      if (!elementId) {
        elementId = createID();
        elementIDs.set(element, elementId);
      }
      references.delete(elementId);
      if (references.size >= MAX_ELEMENT_REFERENCES) {
        purgeReferences();
      }
      while (references.size >= MAX_ELEMENT_REFERENCES) {
        references.delete(references.keys().next().value);
      }
      references.set(elementId, {
        element,
        documentId,
        expiresAt: now() + referenceTTLMS,
      });
      return { elementId, documentId };
    }

    function resolveReference(reference, documentId) {
      if (!documentId || reference.documentId !== documentId) {
        throw commandError("STALE_TARGET", "The element belongs to a stale document", {
          expectedDocumentId: reference.documentId,
        });
      }
      const stored = references.get(reference.elementId);
      if (
        !stored ||
        stored.documentId !== documentId ||
        stored.expiresAt <= now() ||
        stored.element.isConnected === false
      ) {
        references.delete(reference.elementId);
        throw commandError("STALE_TARGET", "The element reference expired or became detached", {
          elementId: reference.elementId,
          expectedDocumentId: reference.documentId,
        });
      }
      references.delete(reference.elementId);
      stored.expiresAt = now() + referenceTTLMS;
      references.set(reference.elementId, stored);
      return stored.element;
    }

    function purgeReferences() {
      const currentTime = now();
      for (const [elementId, reference] of references) {
        if (reference.expiresAt <= currentTime || reference.element.isConnected === false) {
          references.delete(elementId);
        }
      }
    }

    function queryCSS(selector, includeShadowDOM) {
      try {
        return queryRoots(includeShadowDOM).flatMap((root) => [...root.querySelectorAll(selector)]);
      } catch {
        throw commandError("INVALID_MESSAGE", "The CSS locator is invalid");
      }
    }

    function queryXPath(expression, includeShadowDOM) {
      const matches = [];
      try {
        for (const root of queryRoots(includeShadowDOM)) {
          const result = rootDocument.evaluate(expression, root, null, 7, null);
          for (let index = 0; index < result.snapshotLength; index += 1) {
            const node = result.snapshotItem(index);
            if (node?.nodeType === 1) {
              matches.push(node);
            }
          }
        }
      } catch {
        throw commandError("INVALID_MESSAGE", "The XPath locator is invalid");
      }
      return matches;
    }

    function queryText(value, includeShadowDOM) {
      const expected = normalizedText(value);
      return collectElements(includeShadowDOM).filter((element) => {
        if (normalizedText(element.textContent) !== expected) {
          return false;
        }
        return ![...(element.children || [])].some(
          (child) => normalizedText(child.textContent) === expected,
        );
      });
    }

    function queryRole(role, name, includeShadowDOM) {
      const expectedRole = normalizedText(role).toLowerCase();
      const expectedName = name === undefined ? null : normalizedText(name).toLowerCase();
      return collectElements(includeShadowDOM).filter((element) => {
        if (elementRole(element) !== expectedRole) {
          return false;
        }
        return expectedName === null || accessibleName(element).toLowerCase() === expectedName;
      });
    }

    function queryLabel(value, includeShadowDOM) {
      const expected = normalizedText(value).toLowerCase();
      const controls = [];
      for (const label of collectElements(includeShadowDOM).filter(
        (element) => element.tagName === "LABEL",
      )) {
        if (normalizedText(label.textContent).toLowerCase() !== expected) {
          continue;
        }
        const control =
          label.control ||
          (label.htmlFor ? rootDocument.getElementById(label.htmlFor) : null) ||
          label.querySelector?.("button,input,meter,output,progress,select,textarea");
        if (control) {
          controls.push(control);
        }
      }
      return controls;
    }

    function queryAttribute(name, value, includeShadowDOM) {
      return collectElements(includeShadowDOM).filter(
        (element) => element.getAttribute?.(name) === value,
      );
    }

    function queryTestID(value, includeShadowDOM) {
      return collectElements(includeShadowDOM).filter((element) =>
        ["data-testid", "data-test-id", "data-test"].some(
          (name) => element.getAttribute?.(name) === value,
        ),
      );
    }

    function queryCoordinates(coordinates, includeShadowDOM) {
      const element = includeShadowDOM
        ? deepestElementFromPoint(rootDocument, coordinates.x, coordinates.y)
        : rootDocument.elementFromPoint(coordinates.x, coordinates.y);
      return element ? [element] : [];
    }

    function queryRoots(includeShadowDOM) {
      const roots = [rootDocument];
      if (!includeShadowDOM) {
        return roots;
      }
      for (let rootIndex = 0; rootIndex < roots.length; rootIndex += 1) {
        for (const element of roots[rootIndex].querySelectorAll("*")) {
          if (element.shadowRoot) {
            roots.push(element.shadowRoot);
          }
        }
      }
      return roots;
    }

    function collectElements(includeShadowDOM) {
      return uniqueElements(
        queryRoots(includeShadowDOM).flatMap((root) => [...root.querySelectorAll("*")]),
      );
    }

    function isVisible(element) {
      if (element.hidden || element.getAttribute?.("aria-hidden") === "true") {
        return false;
      }
      const style = rootWindow.getComputedStyle?.(element);
      if (
        style?.display === "none" ||
        style?.visibility === "hidden" ||
        style?.visibility === "collapse" ||
        Number.parseFloat(style?.opacity ?? "1") === 0
      ) {
        return false;
      }
      const rect = element.getBoundingClientRect();
      return rect.width > 0 && rect.height > 0;
    }

    function isEnabled(element) {
      return element.disabled !== true && element.getAttribute?.("aria-disabled") !== "true";
    }

    function assertAttached(element) {
      if (!element || element.isConnected === false) {
        throw commandError("STALE_TARGET", "The element is no longer attached to the document");
      }
    }

    function isInViewport(rect) {
      return (
        rect.bottom > 0 &&
        rect.right > 0 &&
        rect.top < rootWindow.innerHeight &&
        rect.left < rootWindow.innerWidth
      );
    }

    function nextRender() {
      return new Promise((resolve) => {
        const schedule = rootWindow.requestAnimationFrame || ((callback) => callback());
        schedule(() => schedule(resolve));
      });
    }

    function locatorDiagnostics(strategy, matches, strict) {
      return {
        strategy,
        matchCount: matches.length,
        strict,
        candidates: matches
          .slice(0, MAX_DIAGNOSTIC_CANDIDATES)
          .map((element) => diagnosticElement(element)),
      };
    }

    function diagnosticElement(element) {
      return {
        tagName: element.tagName,
        id: element.id || "",
        role: elementRole(element),
        name: accessibleName(element).slice(0, 200),
        text: normalizedText(element.textContent).slice(0, 200),
        visible: isVisible(element),
      };
    }

    function accessibleName(element) {
      const ariaLabel = element.getAttribute?.("aria-label");
      if (ariaLabel) {
        return normalizedText(ariaLabel);
      }
      const labelledBy = element.getAttribute?.("aria-labelledby");
      if (labelledBy) {
        const root = element.getRootNode?.() || rootDocument;
        const value = labelledBy
          .split(/\s+/)
          .map((id) => root.getElementById?.(id) || rootDocument.getElementById?.(id))
          .filter(Boolean)
          .map((label) => normalizedText(label.textContent))
          .join(" ");
        if (value) {
          return value;
        }
      }
      if (element.labels?.length) {
        return normalizedText([...element.labels].map((label) => label.textContent).join(" "));
      }
      for (const attribute of ["alt", "title"]) {
        const value = element.getAttribute?.(attribute);
        if (value) {
          return normalizedText(value);
        }
      }
      if (["button", "submit", "reset"].includes(String(element.type || "").toLowerCase())) {
        if (element.value) {
          return normalizedText(element.value);
        }
      }
      return normalizedText(element.textContent);
    }

    function elementRole(element) {
      const explicit = element.getAttribute?.("role")?.trim().split(/\s+/)[0];
      if (explicit) {
        return explicit.toLowerCase();
      }
      const tag = String(element.tagName || "").toLowerCase();
      const type = String(element.type || "").toLowerCase();
      if (tag === "button") return "button";
      if (tag === "a" && element.hasAttribute?.("href")) return "link";
      if (tag === "select") return element.multiple || element.size > 1 ? "listbox" : "combobox";
      if (tag === "textarea") return "textbox";
      if (tag === "img") return "img";
      if (/^h[1-6]$/.test(tag)) return "heading";
      if (tag === "input") {
        if (["button", "submit", "reset", "image"].includes(type)) return "button";
        if (type === "checkbox") return "checkbox";
        if (type === "radio") return "radio";
        if (type === "range") return "slider";
        if (type !== "hidden") return "textbox";
      }
      return "";
    }

    return Object.freeze({
      query,
      resolve,
      ensureActionable,
      describeElement,
      createReference,
      isVisible,
      isEnabled,
    });
  }

  function locatorStrategy(locator) {
    for (const strategy of [
      "css",
      "xpath",
      "text",
      "role",
      "label",
      "placeholder",
      "alt",
      "title",
      "testId",
      "coordinates",
      "element",
    ]) {
      if (locator[strategy] !== undefined) {
        return strategy;
      }
    }
    return "";
  }

  function deepestElementFromPoint(root, x, y) {
    let element = root.elementFromPoint?.(x, y) || null;
    while (element?.shadowRoot?.elementFromPoint) {
      const nested = element.shadowRoot.elementFromPoint(x, y);
      if (!nested || nested === element) {
        break;
      }
      element = nested;
    }
    return element;
  }

  function uniqueElements(elements) {
    return [...new Set(elements.filter(Boolean))];
  }

  function normalizedText(value) {
    return String(value || "")
      .replace(/\s+/g, " ")
      .trim();
  }

  function clamp(value, minimum, maximum) {
    return Math.min(Math.max(value, minimum), Math.max(minimum, maximum));
  }

  function shouldRedactAttribute(element, attribute) {
    if (/(?:password|secret|token|authorization|cookie|api[-_]?key)/i.test(attribute.name)) {
      return true;
    }
    if (attribute.name.toLowerCase() !== "value") {
      return false;
    }
    const identity = [
      element.type,
      element.id,
      element.getAttribute?.("name"),
      element.getAttribute?.("autocomplete"),
    ]
      .filter(Boolean)
      .join(" ");
    return /(?:password|secret|token|credential|authorization|cookie|api[-_]?key)/i.test(identity);
  }

  function defaultID() {
    return (
      globalThis.crypto?.randomUUID?.() ||
      `element-${Date.now()}-${Math.random().toString(16).slice(2)}`
    );
  }

  function commandError(code, message, details = undefined) {
    const error = new Error(message);
    error.code = code;
    if (details !== undefined) {
      error.details = details;
    }
    return error;
  }

  globalThis.__mcpBrowserLocatorEngine = Object.freeze({
    version: ENGINE_VERSION,
    create: createLocatorEngine,
  });
})();
