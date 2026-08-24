# Network Capture

Status: implemented

Last reviewed: 2026-08-24

Network diagnostics are six dedicated MCP `full` tools backed by one managed
root-document CDP consumer. They require Observe access to the current HTTP(S)
site and the optional Debug permission. Every command is bound to one browser,
tab, root frame, and document and is denied after navigation.

## Tools and Lifecycle

| Tool | Behavior |
| --- | --- |
| `browser_start_network_capture` | Replaces any capture for the tab and starts a bounded exact-method Network lease |
| `browser_stop_network_capture` | Releases the lease and retains metadata for ten minutes |
| `browser_clear_network_log` | Clears entries, cursors, internal body references, eviction counts, and redaction metadata |
| `browser_get_network_log` | Reads filtered metadata with a numeric cursor, up to 200 entries and 1 MB per result |
| `browser_get_network_body` | Retrieves and stores one same-origin textual request or response body while capture is active |
| `browser_export_network_har` | Stores bounded HAR 1.2-like metadata without request or response bodies |

The extension permits only `Network.enable`, `Network.getRequestPostData`, and
`Network.getResponseBody` on the consumer lease. Its event allowlist contains
request/response, extra-info, completion, failure, and served-from-cache
events. It does not call `Network.disable`, because console enrichment may
share the same root attachment and Network domain.

Stop, root navigation, Debug revocation, server disconnect, external debugger
detach, replacement, timeout, and cancellation release or invalidate the
lease. Navigation deletes stale document data immediately. Stopped or detached
captures retain metadata for ten minutes and then fail closed.

## Metadata Model and Bounds

Each request receives a monotonically increasing public `entryId`; the opaque
CDP `requestId` never appears in MCP results, artifacts, or audit logs. When CDP
reuses a request ID for a redirect, the completed hop and next request link
through `redirectFrom` and `redirectTo` public IDs.

Entries include bounded URL, method, resource type, initiator and stack summary,
request/response headers, status and protocol, response MIME and byte count,
CDP timing fields, cache state, completion duration, and failure details.
Reads can filter resource types, failures, status range, start time, and cursor.

One browser extension retains capture state for at most eight tabs. A ninth
active capture receives backpressure; an inactive retained capture can be
evicted early to make room. Each tab retains at most 5,000 entries and
2,000,000 serialized bytes; each entry is reduced to at most 32 KiB before
insertion. Oldest entries are evicted first. Reads report eviction and expired
cursors. One read returns at most 200 entries and accepts a byte ceiling from
64 KiB to 1 MB. These limits apply independently from the server's normal MCP
result budget and the CDP manager's event queue.

## Redaction and Body Boundary

Authorization, proxy authorization, Cookie, Set-Cookie, sensitive-looking
headers, URL credentials, secret query parameters, body fields, bearer tokens,
and sensitive failure text are redacted in the extension. The Go server applies
its independent JSON/text redaction before storing every body or HAR artifact.
Audit records contain only operation/kind, browser/tab, outcome, byte count, and
duration.

A body is available only while the capture lease is active, only for an entry
whose URL has the same origin as the captured root document, and only for a
fixed textual MIME allowlist: `text/*`, JSON and `+json`, XML/XHTML, JavaScript,
URL-encoded forms, and SVG. Multipart, binary, missing/unknown MIME, redirects,
failed responses, cross-origin bodies, and decoded payloads above 1 MB are
rejected. UTF-8 is required. Body bytes never appear inline in the MCP result.

HAR export is capped at 2 MB, drops older entries until the artifact fits, and
never includes bodies, cookies arrays, raw request IDs, or security details.
The response returns only artifact metadata and a
`browser://artifacts/{artifactId}` URI. Body and HAR artifacts use the shared
owner-only, unpredictable-ID, quota- and TTL-controlled store and always carry
a sensitive-content warning.

## Prohibited Behavior

The network tools cannot be invoked through `browser_send_command`. Stateful
capture, body, and HAR tools are excluded from batch; a bounded metadata read
may participate in a full-profile batch. The implementation does not expose
interception, request modification, authentication challenges, request replay,
raw body streams, cross-origin/binary bodies, cookie commands, cache disable or
clear, request blocking, or user-controlled Network/CDP methods. These remain
separate security changes.

The lifecycle and fields follow the official Chrome DevTools Protocol
[Network domain](https://chromedevtools.github.io/devtools-protocol/tot/Network/).
The export uses the stable HAR 1.2 field model while adding bounded `_mcp`
metadata; it is intentionally described as HAR-like rather than claiming that
every optional timing and size field is observable.

## Verification

Extension tests cover the exact lease, event normalization, redirect chains,
failures, cache/body availability, header/URL/body redaction, same-origin and
MIME denials, ring eviction, cursors, TTL, navigation/stale targets, permission
gates, cleanup, and body-free HAR output. Go tests cover multi-browser/root
document routing, input and wire validation, independent redaction, owner-only
artifacts, metadata-only audit, tool profiles, batch boundaries, and generic
command denial.
