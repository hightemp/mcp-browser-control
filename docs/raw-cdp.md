# Reviewed Raw CDP

Status: implemented, disabled by default

Last reviewed: 2026-08-24

`browser_send_cdp_command` exposes a fixed, read-only Chrome DevTools Protocol
subset for advanced inspection that is not already convenient through a typed
tool. It is not an arbitrary CDP tunnel. Adding a method is a security-boundary
change and requires a method-specific parameter and result review.

## Required Gates

One call is accepted only when all of these conditions hold:

- the MCP server runs the `full` tool profile;
- the extension has Observe site access for the target HTTP(S) origin;
- the extension has the optional Debug permission;
- the user enabled **Reviewed read-only CDP commands** in extension settings;
- the browser advertises `cdp.sendReadOnly`;
- the target is the root frame of a live document; and
- server origin and incognito policy allows the current tab.

The feature flag is false for new and migrated settings. Granting Debug alone
does not enable this tool. Capability changes are advertised immediately when
the flag or permission state changes.

## Method Allowlist

| Method | Accepted `params` | Result boundary |
| --- | --- | --- |
| `Accessibility.getFullAXTree` | optional `depth` from 0 to 50 | exact `{nodes: [...]}` |
| `Accessibility.getPartialAXTree` | required positive safe `backendNodeId`; optional `fetchRelatives` | exact `{nodes: [...]}` |
| `Accessibility.queryAXTree` | required positive safe `backendNodeId`; optional `accessibleName` up to 500 characters and `role` up to 100 | exact `{nodes: [...]}` |
| `DOM.describeNode` | required positive safe `backendNodeId`; optional `depth` from 0 to 10 | exact `{node: {...}}` |
| `DOM.getBoxModel` | required positive safe `backendNodeId` | exact `{model: {...}}` |
| `Page.getLayoutMetrics` | empty object | only documented layout/content viewport objects |
| `Performance.getMetrics` | empty object | exact bounded metric name/value entries |

Only `backendNodeId` addressing is accepted for node methods. Frontend node
IDs, Runtime object IDs, frame IDs in method parameters, pierce traversal, and
caller-controlled sessions are not accepted.

Every request acquires one short-lived managed CDP lease for exactly one domain
and one exact method. It subscribes to no events and cannot address child
targets. Site access and root document identity are checked before acquisition,
immediately before the command, and after it. The lease is released by
`withSession()` on success, failure, timeout, or cancellation.

## Explicit Denials

The server and extension both reject unlisted methods. They additionally keep
explicit denial boundaries for Runtime execution, persistent scripts,
`DOM.setFileInputFiles`, browser closing, Network cookies/cache/modification,
Storage deletion, Fetch interception, arbitrary Target/Security/SystemInfo/
HeapProfiler/IO access, and every unknown domain. Tracing and IO stream handling
are exposed only through the dedicated performance tool, which owns their
lifecycle and artifacts.

`browser_send_command` rejects `cdp.sendReadOnly`, so callers cannot bypass the
typed Go validation or audit. The tool is excluded from `browser_batch` to keep
each expert invocation and deadline explicit.

## Limits and Results

| Limit | Default | Caller range |
| --- | ---: | ---: |
| Command timeout | 10,000 ms | 100–30,000 ms |
| Normalized result depth | 12 | 1–20 |
| Normalized JSON values | 2,000 | 2–5,000 |
| Characters per string | 2,000 | 1–10,000 |
| Complete extension response | 512 KiB | 64 KiB–1,000,000 bytes |
| Method parameter document | N/A | fixed 16 KiB maximum |
| Object-key length | N/A | fixed 256-character maximum |

The extension deterministically normalizes JSON values and reports depth,
node, string, or key trimming through `truncated` and `warnings`. If the
complete normalized response still exceeds `maxBytes`, the call fails with
`PAYLOAD_TOO_LARGE`; it is never streamed or written as an implicit artifact.
Non-finite values, cyclic objects, malformed method results, and any
`objectId`, `executionContextId`, `scriptId`, or `stream` key fail closed.

The response contains the method, tab and document identity, normalized CDP
result, truncation flag, exact normalized node count, and bounded warnings. The
extension redacts secret-shaped strings and flat DOM attribute values,
including password input values, before WebSocket transport. The Go server
then independently validates the wrapper and method result, rejects handles or
limit violations, and applies the normal server redaction boundary.

## Audit and Residual Risk

Every accepted or rejected server invocation produces a metadata-only audit
entry when the production logger is configured. It contains operation, bounded
method and browser IDs, tab ID, outcome code, response byte count, and duration.
Method parameters, result values, URLs, DOM attributes, and secret values are
never logged.

Read-only CDP methods can still reveal authenticated page structure, accessible
names, dimensions, and performance state. A malicious page may return
adversarial or secret-bearing strings, and cancellation cannot prove the
browser did not finish an in-flight read. Use a narrow origin allowlist and
trusted MCP clients. Redaction is defense in depth, not a guarantee that every
application-specific secret can be recognized.

## Verification Boundary

Automated Go and extension tests cover every allowed method, method-specific
parameters, exact lease allowlists, disabled-by-default capability advertising,
Debug and site gates, root-document races, multi-browser routing, result shapes,
depth/node/string/byte limits, prohibited handles, flat DOM attribute redaction,
metadata-only audit, generic-command bypass denial, and batch exclusion.

Release validation still requires current Chrome and Edge smoke tests for every
method, navigation races, DevTools conflicts, permission revocation,
cancellation, and repeated attach/release behavior. Protocol definitions are
from the official Chrome DevTools Protocol
[Accessibility](https://chromedevtools.github.io/devtools-protocol/tot/Accessibility/),
[DOM](https://chromedevtools.github.io/devtools-protocol/tot/DOM/),
[Page](https://chromedevtools.github.io/devtools-protocol/tot/Page/), and
[Performance](https://chromedevtools.github.io/devtools-protocol/tot/Performance/)
domains.
