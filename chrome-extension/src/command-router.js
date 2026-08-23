import {
  ErrorCode,
  normalizeError,
  protocolError,
  validateLocator,
  validateTarget,
} from "./protocol.js";

const COMMANDS = Object.freeze({
  "browser.ping": Object.freeze({ domain: "browser", handler: "ping", validate: validateEmpty }),
  "tabs.list": Object.freeze({ domain: "tabs", handler: "list", validate: validateEmpty }),
  "page.getHTML": Object.freeze({ domain: "page", handler: "getHTML", validate: validateEmpty }),
  "page.getHTMLBySelector": Object.freeze({
    domain: "page",
    handler: "getHTMLBySelector",
    validate: validateSelector,
  }),
  "page.click": Object.freeze({ domain: "page", handler: "click", validate: validateAction }),
  "page.fill": Object.freeze({ domain: "page", handler: "fill", validate: validateFill }),
});

export const COMMAND_NAMES = Object.freeze(Object.keys(COMMANDS));

export class CommandRouter {
  constructor({ getBrowserId, getCapabilities, handlers }) {
    this.getBrowserId = getBrowserId;
    this.getCapabilities = getCapabilities;
    this.handlers = handlers;
    this.activeRequests = new Map();
  }

  async execute(request, respond) {
    if (this.activeRequests.has(request.requestId)) {
      return false;
    }

    const controller = new AbortController();
    this.activeRequests.set(request.requestId, controller);
    let outcome;
    try {
      const result = await this.dispatch(request, controller.signal);
      throwIfCancelled(controller.signal);
      outcome = { success: true, result };
    } catch (error) {
      const normalized = controller.signal.aborted
        ? protocolError(ErrorCode.CANCELLED, "Command was cancelled", true)
        : error;
      outcome = {
        success: false,
        error: normalizeError(normalized, {
          requestId: request.requestId,
          target: request.target,
        }),
      };
    }

    try {
      await respond(outcome);
      return true;
    } finally {
      this.activeRequests.delete(request.requestId);
    }
  }

  cancel(requestId) {
    const controller = this.activeRequests.get(requestId);
    if (!controller) {
      return false;
    }
    controller.abort();
    return true;
  }

  async dispatch(request, signal) {
    throwIfCancelled(signal);
    const definition = COMMANDS[request.command];
    if (!definition) {
      throw protocolError(
        ErrorCode.INVALID_COMMAND,
        `The extension does not support command "${request.command}"`,
      );
    }

    const browserId = await this.getBrowserId();
    validateTarget(request.target, browserId);
    definition.validate(request.params, request.target);

    const capabilities = new Set(await this.getCapabilities());
    if (!capabilities.has(request.command)) {
      throw protocolError(
        ErrorCode.CAPABILITY_UNAVAILABLE,
        `Command "${request.command}" is not available with the current browser permissions`,
      );
    }

    const handler = this.handlers?.[definition.domain]?.[definition.handler];
    if (typeof handler !== "function") {
      throw protocolError(ErrorCode.INTERNAL_ERROR, "The command handler is unavailable");
    }
    throwIfCancelled(signal);
    return handler(request, signal);
  }
}

function validateEmpty(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, []);
}

function validateSelector(params) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["selector"]);
  assertNonEmptyString(params.selector, "params.selector");
}

function validateAction(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, ["selector", "coordinates", "locator", "index"]);
  validateElementAddress(params, target);
  validateIndex(params.index);
}

function validateFill(params, target) {
  validateParamsObject(params);
  assertAllowedProperties(params, [
    "selector",
    "coordinates",
    "locator",
    "index",
    "value",
    "clear",
  ]);
  validateElementAddress(params, target);
  validateIndex(params.index);
  if (params.value === undefined || params.value === null) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.value is required");
  }
  if (params.clear !== undefined && typeof params.clear !== "boolean") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.clear must be a boolean");
  }
}

function validateElementAddress(params, target) {
  const addresses = [params.selector, params.coordinates, params.locator]
    .filter((value) => value !== undefined);
  if (addresses.length !== 1) {
    throw protocolError(
      ErrorCode.INVALID_MESSAGE,
      "Exactly one of params.selector, params.coordinates, or params.locator is required",
    );
  }
  if (params.selector !== undefined) {
    assertNonEmptyString(params.selector, "params.selector");
  }
  if (params.coordinates !== undefined) {
    validateLocator({ coordinates: params.coordinates }, target);
  }
  if (params.locator !== undefined) {
    validateLocator(params.locator, target);
  }
}

function validateParamsObject(params) {
  if (!params || typeof params !== "object" || Array.isArray(params)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params must be an object");
  }
}

function assertAllowedProperties(params, allowed) {
  const unexpected = Object.keys(params).find((property) => !allowed.includes(property));
  if (unexpected) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `Unexpected parameter "${unexpected}"`);
  }
}

function assertNonEmptyString(value, path) {
  if (typeof value !== "string" || value.trim() === "") {
    throw protocolError(ErrorCode.INVALID_MESSAGE, `${path} must not be empty`);
  }
}

function validateIndex(index) {
  if (index !== undefined && (!Number.isInteger(index) || index < 0)) {
    throw protocolError(ErrorCode.INVALID_MESSAGE, "params.index must be a non-negative integer");
  }
}

function throwIfCancelled(signal) {
  if (signal.aborted) {
    throw protocolError(ErrorCode.CANCELLED, "Command was cancelled", true);
  }
}
