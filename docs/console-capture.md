# Console and Page-Error Capture

Status: implemented bridge baseline with optional managed CDP enrichment

Last reviewed: 2026-08-24

The four typed console tools start, stop, clear, and read a capture associated
with one browser, tab, frame, and document. The baseline uses packaged MAIN and
ISOLATED world bridges and remains available without the optional Debug
permission. A root-frame start adds CDP enrichment when Debug is already
granted; failure to attach is reported as a warning and does not stop the bridge
capture.

## Backends and Event Scope

| Backend | Sources | Scope reported on entries |
| --- | --- | --- |
| `bridge` | console methods, JavaScript errors, unhandled rejections, resource-load errors | `frame` |
| `cdp` Runtime | `consoleAPICalled`, `exceptionThrown` after root execution-context filtering | `frame` |
| `cdp` Log and Network | `entryAdded`, `loadingFailed` | `tab` |

Both backends write to the same content-owned ring buffer. Reads therefore use
one monotonically increasing cursor and the existing level, kind, time, and
limit filters. Every returned entry includes `backend` and `scope`. Bridge and
CDP observations may overlap; callers that aggregate counts should account for
that provenance rather than assuming every entry is unique.

The CDP consumer acquires one shared root-tab lease with these exact methods:

- commands: `Page.getFrameTree`, `Runtime.enable`, `Log.enable`, and
  `Network.enable`;
- events: `Runtime.consoleAPICalled`, `Runtime.exceptionThrown`,
  `Log.entryAdded`, and `Network.loadingFailed`.

It does not request child targets, response bodies, headers, cookies, object
properties, or arbitrary Runtime evaluation. Runtime events are accepted only
when the session manager maps their execution context to the root document.
Log and Network events cannot be reliably assigned to one frame through these
event shapes, so they are explicitly marked as tab-scoped.

## Bounds and Sensitive Data

The unified ring is bounded by entry count and two million serialized
characters. Individual entries, argument count, object depth, property count,
stack depth, URLs, and strings have independent limits. CDP RemoteObjects use
only primitive `value`, `unserializableValue`, `description`, and bounded
preview properties; capture never invokes getters or follows object handles.

All events are treated as untrusted data. The ISOLATED-world receiver verifies
the extension sender, bridge version, frame ID, and document ID before accepting
an internal CDP entry. It then applies the same structural sanitization and
credential, authorization, and sensitive-query redaction used for bridge
events. A page can still forge or poison MAIN-world bridge observations through
same-window messaging, so console output must never be used as executable input
or authorization evidence.

## Lifecycle and Failure Semantics

The bridge capture is document-scoped and must be started again after
navigation. Its optional CDP lease is released on typed stop or matching frame
navigation. External debugger detach invalidates the capture, and the shared
manager force-detaches all leases after Debug permission revocation, extension
disconnect, or MCP WebSocket closure.

Opening DevTools or another debugger can prevent CDP attachment. In that case,
`browser_start_console_capture` succeeds with `cdpEnriched: false`, reports only
the `bridge` backend, and includes a safe warning. No raw browser error text is
returned. CDP event backpressure remains bounded by the session manager before
entries reach the ring buffer.

## Verification Boundary

Automated tests cover bridge capture/filter/redaction, exceptions, stop/clear,
ring eviction, CDP method/event allowlists, root-context filtering, tab-scoped
failures, target identity, navigation release, and debugger-conflict fallback.
Release validation still requires real Chrome and Edge smoke tests for attach,
event delivery, navigation, DevTools conflict, permission revocation, and
disconnect cleanup.

Protocol field definitions come from the official Chrome DevTools Protocol
references for [Runtime](https://chromedevtools.github.io/devtools-protocol/tot/Runtime/),
[Log](https://chromedevtools.github.io/devtools-protocol/tot/Log/), and
[Network](https://chromedevtools.github.io/devtools-protocol/tot/Network/).
