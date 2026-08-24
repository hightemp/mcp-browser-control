# CDP Session Manager

Status: implemented infrastructure; used by typed console, PDF, accessibility, emulation, evaluation, reviewed raw CDP, and performance tools

Last reviewed: 2026-08-24

The extension owns one `chrome.debugger` attachment per tab and shares it among
bounded internal consumers. Typed console, network, screenshot, PDF,
accessibility, evaluation, emulation, input, and performance handlers must use
this manager instead of attaching directly.

Chrome documents `chrome.debugger` as its extension transport for the Chrome
DevTools Protocol. A root debuggee is addressed by tab ID, while Chrome 125+
adds flat child sessions addressed by `sessionId`. DevTools opening on an
attached tab or closing the target causes `onDetach`. See the official
[`chrome.debugger` reference](https://developer.chrome.com/docs/extensions/reference/api/debugger).

## Ownership Model

```text
tabId
  └─ one root chrome.debugger attachment
       ├─ console consumer lease
       ├─ emulation consumer lease
       ├─ network consumer lease
       └─ performance consumer lease
            └─ optional flat child sessions (Chrome 125+ only)
```

`createCDPSessionManager(chrome, { browserVersion })` installs exactly one
global `onEvent` listener and one `onDetach` listener. `acquire()` reserves a
named consumer lease. Concurrent acquisition for the same tab shares the
in-flight attach promise, so only one browser attachment is created.

The root session remains attached while at least one consumer is active.
Releasing the last lease detaches automatically. The extension also force
detaches all managed roots when the MCP WebSocket closes, the user disconnects,
or Debug permission is revoked. A target close or DevTools conflict invalidates
all leases without automatic reattachment.

The default limits are:

| Resource | Limit |
| --- | ---: |
| Concurrent root sessions | 8 |
| Consumers per root | 8 |
| Pending events per consumer | 256 |
| One queued event | 256 KiB |
| Queued event bytes per consumer | 2 MiB |
| Command parameters | 1 MiB |
| Command result | 4 MiB |

Limits are extension-owned configuration, must be positive, and fail closed.
They are not MCP parameters.

## Consumer Contract

A consumer must provide:

- a stable `consumerId`, unique within the tab session;
- a non-empty set of allowlisted CDP domains;
- exact command and event method allowlists within those domains;
- an optional bounded event callback;
- an optional detach callback; and
- `includeChildTargets: true` only when its feature genuinely needs OOPIFs or
  related child-frame targets.

The returned lease exposes only:

- `sendCommand(method, params, { sessionId, signal })`;
- `frameContexts(frameId?)`; and
- idempotent `release()`.

`sendCommand` verifies `Domain.method` syntax and requires the exact command on
the consumer lease. Event delivery likewise requires an exact event method;
requesting a domain never grants every method in it. A child `sessionId` must
have arrived from the manager's current root session and is rejected after
`Target.detachedFromTarget`. This prevents a consumer from addressing an
unrelated root or inventing a child handle.

`withSession()` is the preferred shape for request-scoped consumers because it
releases the lease in `finally`. Long-running capture consumers must release on
their typed stop operation and their timeout/cancellation path.

The infrastructure allowlist contains only domains planned by the approved
typed features:

`Accessibility`, `Audits`, `DOM`, `Emulation`, `IO`, `Input`, `Log`, `Network`,
`Page`, `Performance`, `Profiler`, `Runtime`, `Target`, and `Tracing`.

This is a first boundary, not authorization for every method in those domains.
Exact per-consumer command/event lists provide the second boundary. Each typed
consumer still needs strict parameters, result limits, origin and target
policy, profile/permission gates, audit, and the focused security decision in
[`security-review.md`](security-review.md). `Security`, `Fetch`, `Storage`,
`SystemInfo`, `HeapProfiler`, and every unknown domain fail closed.

## Events and Backpressure

Root events are routed by tab ID. Child events are routed only to consumers
that opted into child targets. Events are filtered by the consumer's domain
set before being queued.

Every consumer has an independent FIFO. A slow callback cannot block another
consumer or grow memory without bound. When a queue is full, the oldest pending
event is discarded. The next delivered event includes `droppedBefore`; an
oversized event is also counted as dropped. Consumer callback failures are
reported only through the manager's safe diagnostics hook and cannot interrupt
other consumers or detach cleanup.

CDP event payloads are still untrusted, sensitive browser data. A consumer must
normalize and redact them before any protocol response, artifact, audit record,
or ordinary log.

## Frames and Child Targets

There is no one-to-one relationship between frames and CDP targets. The manager
therefore exposes capabilities rather than pretending that every frame has a
child session:

| Browser version | Root target | Same-process frame contexts | Flat OOPIF child sessions |
| --- | --- | --- | --- |
| Chrome/Edge 116–124 | Supported | Indexed from `Runtime.executionContextCreated` when the consumer enables Runtime | Unavailable and rejected before attach |
| Chrome/Edge 125+ | Supported | Indexed from Runtime events | Available through recursive `Target.setAutoAttach` with an iframe-only filter |
| Other Chromium | Capability-gated | Best effort after browser validation | Disabled until the product/version passes child-session tests |

The manager never calls a separate `chrome.debugger.attach` for a child. On
Chrome 125+, the first child-aware consumer enables fixed flat auto-attach on
the root. `Target.attachedToTarget` registers the opaque session, and the same
fixed auto-attach command is applied recursively because Chrome auto-attach is
not recursive. Releasing the last child-aware consumer disables auto-attach
while preserving the root if another consumer remains.

`Runtime.executionContextCreated`, `Runtime.executionContextDestroyed`, and
`Runtime.executionContextsCleared` maintain a bounded-by-event-lifetime index
of frame ID, execution-context ID, default-context flag, and optional child
session ID. Navigation/context clear and child detach remove stale entries.
Feature handlers must still validate their requested frame against live
`webNavigation` target information and origin policy.

## Failure Semantics

| Condition | Stable result |
| --- | --- |
| DevTools or another debugger already attached | `CAPABILITY_UNAVAILABLE` with `debugger_conflict` reason |
| Debug permission missing | `PERMISSION_REQUIRED` |
| Tab/target closed | `TAB_NOT_FOUND` or invalidated lease |
| Unsupported child sessions | `CAPABILITY_UNAVAILABLE` with `flat_sessions_unavailable` reason |
| Session/consumer/event pressure | `BACKPRESSURE` or `droppedBefore` event metadata |
| Cancelled attach | `CANCELLED`; a late successful browser attach is detached automatically |
| External `onDetach` | All consumers notified once and all leases invalidated |

Raw browser error messages are not forwarded because they can contain target
details. CDP commands are not transactional. Cancellation discards a late
result but cannot prove that a command had no browser-side effect, so mutating
typed tools must implement their own restore and indeterminate-result rules.

## Verification Boundary

Unit tests cover shared attach, reference counts, automatic and forced detach,
DevTools conflict, external detach, domain rejection, command/result bounds,
session and consumer pressure, cancellation cleanup, Chrome 124 fail-closed
behavior, Chrome 125 recursive child sessions, frame-context cleanup, and slow
consumer event drops.

Each CDP-backed feature must add handler, protocol-contract,
multi-browser-isolation, permission-revocation, real Chrome, and Edge smoke
tests. The session manager alone does not cause a CDP capability to be
advertised and does not make any MCP tool available.

Console capture is the first long-running consumer. A root-frame start may hold
one lease for exact `Runtime`, `Log`, `Network`, and `Page` methods/events; stop,
navigation, detach, Debug permission revocation, and server disconnect release
or invalidate it. See [`console-capture.md`](console-capture.md).

Emulation is another long-running root-tab consumer. It owns exact Emulation and
Network setter/resetter methods, explicitly resets before replacement/release,
and relies on debugger detach as the final cleanup boundary. See
[`emulation.md`](emulation.md).

JavaScript evaluation is a short-lived root-tab consumer. It creates a
restricted isolated world, evaluates through exact `Page`/`Runtime` methods,
releases its unique object group in `finally`, and releases the lease after one
request. No remote handle is returned. See
[`javascript-evaluation.md`](javascript-evaluation.md).

Reviewed raw CDP is also a short-lived root-tab consumer, but its lease contains
exactly one method and its one domain. Both protocol boundaries independently
validate method-specific parameters and result shape; no events, child targets,
remote handles, streams, or session IDs are exposed. See
[`raw-cdp.md`](raw-cdp.md).

Performance diagnostics use short-lived, kind-specific root-tab leases.
Metrics allow only `Performance.getMetrics`; trace owns a fixed-category
`Tracing` session and its private `IO` stream; coverage and CPU profiling own
separate exact `Profiler` lifecycles; audits accept only `Audits.issueAdded`.
Every capture stops its domain state and releases the lease in cleanup, and no
stream handle or profiler session leaves the handler. See
[`performance-diagnostics.md`](performance-diagnostics.md).
