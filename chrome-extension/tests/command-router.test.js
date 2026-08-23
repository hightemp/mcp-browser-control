import assert from "node:assert/strict";
import test from "node:test";

import { COMMAND_NAMES, CommandRouter } from "../src/command-router.js";
import { ErrorCode, protocolError } from "../src/protocol.js";

const browserId = "11111111-1111-4111-8111-111111111111";

test("router dispatches every allowlisted command to its domain handler", async () => {
  const calls = [];
  const handler = (request) => {
    calls.push(request.command);
    return { command: request.command };
  };
  const router = createRouter({
    handlers: {
      browser: { ping: handler },
      tabs: { list: handler },
      page: {
        getHTML: handler,
        getHTMLBySelector: handler,
        click: handler,
        fill: handler,
      },
    },
  });
  const paramsByCommand = {
    "browser.ping": {},
    "tabs.list": {},
    "page.getHTML": {},
    "page.getHTMLBySelector": { selector: "main" },
    "page.click": { coordinates: { x: 20, y: 40 } },
    "page.fill": { selector: "#email", value: "user@example.com", clear: true },
  };

  for (const [index, command] of COMMAND_NAMES.entries()) {
    const outcomes = [];
    const accepted = await router.execute(
      createRequest(command, paramsByCommand[command], `request-${index}`),
      (outcome) => outcomes.push(outcome),
    );
    assert.equal(accepted, true);
    assert.deepEqual(outcomes, [{ success: true, result: { command } }]);
  }
  assert.deepEqual(calls, COMMAND_NAMES);
});

test("router rejects unknown and currently unavailable commands", async () => {
  const router = createRouter({ capabilities: ["browser.ping"] });

  assert.equal(
    (await execute(router, createRequest("browser.unknown", {}))).error.code,
    ErrorCode.INVALID_COMMAND,
  );
  assert.equal(
    (await execute(router, createRequest("tabs.list", {}))).error.code,
    ErrorCode.CAPABILITY_UNAVAILABLE,
  );
});

test("router validates target and command params before invoking handlers", async () => {
  let calls = 0;
  const router = createRouter({
    handlers: {
      browser: { ping: () => { calls += 1; } },
      tabs: { list: () => { calls += 1; } },
      page: {
        getHTML: () => { calls += 1; },
        getHTMLBySelector: () => { calls += 1; },
        click: () => { calls += 1; },
        fill: () => { calls += 1; },
      },
    },
  });
  const invalidRequests = [
    createRequest("browser.ping", { unexpected: true }),
    createRequest("page.getHTMLBySelector", { selector: "" }),
    createRequest("page.click", { selector: "button", coordinates: { x: 1, y: 2 } }),
    createRequest("page.click", { coordinates: { x: -1, y: 2 } }),
    createRequest("page.fill", { selector: "input" }),
    createRequest("page.fill", { selector: "input", value: "x", clear: "yes" }),
    {
      ...createRequest("page.getHTML", {}),
      target: { browserId: "22222222-2222-4222-8222-222222222222", tabId: 1 },
    },
  ];

  for (const [index, request] of invalidRequests.entries()) {
    request.requestId = `invalid-${index}`;
    const outcome = await execute(router, request);
    assert.equal(outcome.success, false);
    assert.equal(outcome.error.code, ErrorCode.INVALID_MESSAGE);
  }
  assert.equal(calls, 0);
});

test("router emits one cancellation response and suppresses duplicate request IDs", async () => {
  let resolveHandler;
  const router = createRouter({
    handlers: {
      browser: {
        ping: () => new Promise((resolve) => { resolveHandler = resolve; }),
      },
      tabs: {},
      page: {},
    },
  });
  const request = createRequest("browser.ping", {}, "same-request");
  const outcomes = [];
  const first = router.execute(request, (outcome) => outcomes.push(outcome));
  await waitFor(() => resolveHandler !== undefined);

  assert.equal(await router.execute(request, (outcome) => outcomes.push(outcome)), false);
  assert.equal(router.cancel(request.requestId), true);
  resolveHandler({ pong: true });
  assert.equal(await first, true);
  assert.equal(router.cancel(request.requestId), false);
  assert.equal(outcomes.length, 1);
  assert.equal(outcomes[0].success, false);
  assert.equal(outcomes[0].error.code, ErrorCode.CANCELLED);
  assert.equal(outcomes[0].error.requestId, request.requestId);
});

test("router maps handler failures to structured protocol errors", async () => {
  const router = createRouter({
    handlers: {
      browser: {
        ping: () => {
          throw protocolError(ErrorCode.PERMISSION_REQUIRED, "Access required", false, {
            origin: "https://example.com",
          });
        },
      },
      tabs: {},
      page: {},
    },
  });

  const outcome = await execute(router, createRequest("browser.ping", {}));
  assert.deepEqual(outcome.error.details, { origin: "https://example.com" });
  assert.equal(outcome.error.code, ErrorCode.PERMISSION_REQUIRED);
  assert.equal("stack" in outcome.error, false);
});

function createRouter({ handlers = defaultHandlers(), capabilities = COMMAND_NAMES } = {}) {
  return new CommandRouter({
    getBrowserId: async () => browserId,
    getCapabilities: async () => capabilities,
    handlers,
  });
}

function defaultHandlers() {
  return {
    browser: { ping: () => ({ pong: true }) },
    tabs: { list: () => ({ tabs: [] }) },
    page: {
      getHTML: () => ({ html: "" }),
      getHTMLBySelector: () => ({ html: "" }),
      click: () => ({ clicked: true }),
      fill: () => ({ filled: true }),
    },
  };
}

function createRequest(command, params, requestId = "request-1") {
  return {
    requestId,
    browserId,
    command,
    params,
    target: { browserId, tabId: 1 },
  };
}

async function execute(router, request) {
  let received;
  await router.execute(request, (outcome) => { received = outcome; });
  return received;
}

async function waitFor(predicate) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    if (predicate()) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  throw new Error("Condition was not reached");
}
