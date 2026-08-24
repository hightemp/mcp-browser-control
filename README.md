# MCP Browser Control

MCP Browser Control is a local Go server and Manifest V3 extension for
controlling one or more Chromium browser profiles through the Model Context
Protocol.

## Current Status

The release-candidate implementation includes:

- multiple extensions can connect with stable browser IDs;
- every command is routed to exactly one browser connection;
- browser selection is isolated per MCP session;
- reconnecting a browser atomically replaces its old connection;
- recently disconnected browsers remain visible with timestamps and a safe
  disconnect reason until registry cleanup;
- one-time pairing issues persistent per-browser credentials and every later
  handshake is authenticated;
- STDIO and authenticated Streamable HTTP transports are available; deprecated
  legacy SSE requires an explicit opt-in;
- window, tab, tab-group, session, history, bookmark, reading-list, bounded page
  inspection, semantic snapshot, locator, DOM and trusted-CDP interaction,
  waits, viewport/full-page/element screenshots, managed-CDP PDF artifacts,
  bounded accessibility trees, reversible tab emulation,
  opt-in isolated-world JavaScript evaluation, a reviewed opt-in read-only CDP
  subset, bounded performance metrics/capture artifacts, and bridge/CDP console
  diagnostics work through the extension;
- Go race tests, real WebSocket tests, extension protocol tests, and a
  two-browser/two-MCP-session integration test are included.

Request interception is planned but not complete. Remote mode is not
implemented; do not expose either server port outside the local machine.
Review the complete [known limitations](docs/known-limitations.md) before a
production rollout.
The internal [CDP Session Manager](docs/cdp-session-manager.md) now provides
shared, bounded debugger lifecycle infrastructure, but it does not advertise a
CDP-backed MCP command by itself.

## Architecture

```text
 Chromium profile A ─┐
                     ├─ WebSocket protocol v1 ─ Go MCP server ─ MCP client A
 Chromium profile B ─┘                         └─────────────── MCP client B

 browserId → connectionId → windowId → tabId → frameId
```

Each extension installation stores a stable `browserId` in
`chrome.storage.local`. A WebSocket reconnect receives a new `connectionId`.
Pending requests are correlated by browser, connection, and request ID, which
prevents late responses or a replaced connection from crossing browser
boundaries.

## Requirements

- Go 1.26.7 or newer
- Chrome or a compatible Chromium browser version 116 or newer
- Node.js 20.19 or newer for extension development and tests

Chrome, Edge, other Chromium products, capability differences, and the test
policy are defined in [docs/browser-support.md](docs/browser-support.md).

## Run the Server

Install dependencies and start the default Streamable HTTP transport:

```bash
make deps
make run
```

The equivalent direct Go command is `go run ./cmd/server`.

Endpoints:

- MCP: `http://127.0.0.1:8896/mcp`
- Browser extensions: `ws://127.0.0.1:8090/ws`

Streamable HTTP requires a Bearer token. On first start, the server creates a
random token in the owner-only file printed in the startup log. The default is
`$XDG_CONFIG_HOME/mcp-browser-control/mcp-token` (usually
`~/.config/mcp-browser-control/mcp-token`). Configure the MCP client to send
`Authorization: Bearer <token>` on every `/mcp` request. The token itself is
never printed by the server.

STDIO:

```bash
make run ARGS="-t stdio"
```

Legacy SSE:

```bash
make run ARGS="-t sse -enable_legacy_sse"
```

Options:

- `-t` — `streamable-http`, `stdio`, or deprecated `sse`
- `-h` — MCP HTTP host; default `127.0.0.1`
- `-p` — MCP HTTP port; default `8896`
- `-ws_host` — extension WebSocket host; default `127.0.0.1`
- `-ws_port` — extension WebSocket port; default `8090`
- `-command_timeout` — default browser command timeout; default `15s`
- `-credential_file` — persistent hashed credential store; defaults to the
  operating system's user configuration directory
- `-mcp_token_file` — owner-only MCP HTTP Bearer token file
- `-enable_legacy_sse` — explicitly enable the deprecated SSE transport
- `-pairing_ttl` — lifetime of each one-time pairing code; default `10m`
- `-artifact_dir` — owner-only temporary artifact directory
- `-artifact_ttl` — artifact lifetime; default `24h`
- `-artifact_max_bytes` — shared artifact quota; default `536870912`

Flags, `MCP_BROWSER_*` environment variables, JSON configuration files,
payload limits, origin allowlists, profiles, artifacts, and logging settings
are documented in [docs/configuration.md](docs/configuration.md).

## Pair a Browser

The server prints an eight-digit one-time code during startup:

```text
Browser pairing code: 1234-5678 (expires at 2026-08-23T22:00:00+03:00)
```

Open the extension popup, enter the current code, and click **Pair**. A
successful pairing consumes that code immediately, stores a new credential in
`chrome.storage.local`, and causes the server to print the next one-time code.
Use that new code when pairing another browser profile.

The raw long-lived credential is never written to the server store. The server
persists only its SHA-256 hash in a file with owner-only permissions. To revoke
the current credential, connect the browser and click **Revoke pairing** in the
popup; the extension waits for the server acknowledgement before deleting its
local copy.

## Install the Extension

Follow [chrome-extension/INSTALL.md](chrome-extension/INSTALL.md). Install the
extension in each browser profile that should be independently selectable.

The extension requests the Observe profile separately from installation. Tab
discovery works without it; page inspection, interaction, waits, and bounded
network-activity observation require the user to click **Grant access to
websites** in the popup.

## Select a Browser

Start with `browser_list`:

```json
{
  "browsers": [
    {
      "browserId": "7ec37ee7-...",
      "displayName": "Work Chrome",
      "connected": true
    },
    {
      "browserId": "4eb33dbd-...",
      "displayName": "Edge QA",
      "connected": true
    }
  ],
  "connectedCount": 2
}
```

Then call `browser_select` for the current MCP session:

```json
{
  "browserId": "7ec37ee7-..."
}
```

Every target tool also accepts an explicit `browserId`, which takes precedence
over the session selection. If exactly one browser is connected, it is selected
implicitly. If several browsers are connected and no selection was made, the
server returns `AMBIGUOUS_BROWSER` rather than broadcasting the command.

## Available MCP Resources

- `browser://instances` — connected and retained browser instances;
- `browser://instances/{browserId}` — metadata and connection state;
- `browser://instances/{browserId}/tabs` — live tabs from a connected browser;
- `browser://instances/{browserId}/capabilities` — runtime capabilities and permissions;
- `browser://artifacts/{artifactId}` — temporary text or binary command output;
- `browser://artifacts/{artifactId}/metadata` — size, expiry, MIME type, and redaction rules.

Browser instance resources use `application/json`. Browser-specific URIs
always address the explicit instance in the URI and never depend on MCP session
selection. Artifact content preserves its original MIME type and is returned as
MCP text or base64 blob content. The pinned MCP SDK does not implement resource
subscriptions, so update notifications are not advertised yet; clients can
read a resource again to refresh it.

Artifact IDs contain 256 bits of cryptographic randomness. Artifacts expire
automatically, and the oldest entries are evicted when the configured quota is
needed. The store never includes original secret values in redaction metadata.

## Available MCP Tools

The generated [complete tool reference](docs/tool-reference.md) documents the
purpose, exact input schema, result, MCP profile, extension capability,
permissions, errors, and an example for every registered tool. Regenerate it
after changing a tool definition with `make tool-reference`; `make check`
rejects stale output.

### Browser discovery

- `browser_list`
- `browser_get`
- `browser_select`
- `browser_get_selected`
- `browser_select_tab`
- `browser_rename`
- `browser_get_capabilities`
- `browser_ping`

### Implemented extension commands

- `browser_get_windows`
- `browser_get_window`
- `browser_create_window`
- `browser_update_window`
- `browser_focus_window`
- `browser_close_window`
- `browser_get_tabs`
- `browser_get_tab`
- `browser_create_tab`
- `browser_activate_tab`
- `browser_navigate_tab`
- `browser_reload_tab`
- `browser_stop_tab`
- `browser_go_back`
- `browser_go_forward`
- `browser_move_tab`
- `browser_duplicate_tab`
- `browser_close_tab`
- `browser_pin_tab`
- `browser_mute_tab`
- `browser_get_tab_zoom`
- `browser_set_tab_zoom`
- `browser_group_tabs`
- `browser_ungroup_tabs`
- `browser_update_tab_group`
- `browser_get_recently_closed`
- `browser_restore_session`
- `browser_page_info`
- `browser_get_html`
- `browser_get_html_by_selector`
- `browser_get_text`
- `browser_query`
- `browser_get_element`
- `browser_snapshot`
- `browser_click_element`
- `browser_input_data`
- `browser_double_click`
- `browser_context_click`
- `browser_hover`
- `browser_focus`
- `browser_blur`
- `browser_type`
- `browser_clear`
- `browser_press`
- `browser_select_option`
- `browser_set_checked`
- `browser_scroll`
- `browser_drag_and_drop`
- `browser_dispatch_event`
- `browser_submit`
- `browser_wait`
- `browser_screenshot`
- `browser_print_to_pdf`
- `browser_get_accessibility_tree`
- `browser_set_emulation`
- `browser_get_emulation_state`
- `browser_reset_emulation`
- `browser_evaluate_javascript`
- `browser_send_cdp_command`
- `browser_get_performance_metrics`
- `browser_capture_performance`
- `browser_start_console_capture`
- `browser_stop_console_capture`
- `browser_clear_console_log`
- `browser_get_console_log`
- `browser_start_network_capture`
- `browser_stop_network_capture`
- `browser_clear_network_log`
- `browser_get_network_log`
- `browser_get_network_body`
- `browser_export_network_har`
- `browser_list_cookies`
- `browser_get_cookie`
- `browser_set_cookie`
- `browser_remove_cookie`
- `browser_list_storage_items`
- `browser_get_storage_item`
- `browser_set_storage_item`
- `browser_remove_storage_item`
- `browser_get_cache_metadata`
- `browser_get_indexeddb_metadata`
- `browser_clear_origin_storage`
- `browser_list_downloads`
- `browser_get_download`
- `browser_create_download`
- `browser_pause_download`
- `browser_resume_download`
- `browser_cancel_download`
- `browser_erase_download_history`
- `browser_search_history`
- `browser_get_history_visits`
- `browser_delete_history_url`
- `browser_delete_history_range`
- `browser_clear_history`
- `browser_list_bookmarks`
- `browser_create_bookmark`
- `browser_update_bookmark`
- `browser_move_bookmark`
- `browser_remove_bookmark`
- `browser_list_reading_list`
- `browser_add_reading_list_entry`
- `browser_update_reading_list_entry`
- `browser_remove_reading_list_entry`
- `browser_batch`

All target tools accept optional `browserId` and `timeoutMs`. Page tools also
accept optional `tabId`, `frameId`, and `documentId`; when `tabId` is omitted,
the browser-scoped selected tab or active tab in the last focused window is
used. A supplied document ID prevents commands from running after navigation.

Click and fill accept a legacy CSS `selector`, viewport `coordinates`, or a
structured `locator`. Locators support CSS, XPath, normalized text, ARIA
role/name, label, placeholder, alt, title, test ID, coordinates, and temporary
element references. Actions use strict matching by default; use `nth` to pick a
specific match or `strict: false` to intentionally accept the first match.
Set `includeShadowDOM: true` to traverse open shadow roots.

```json
{
  "locator": {
    "role": "button",
    "name": "Save",
    "includeShadowDOM": true
  }
}
```

Element results include a reference scoped to the current Chrome document.
References have a sliding 60-second lifetime and fail with `STALE_TARGET` after
navigation, detachment, or expiry. Locator failures include the match count and
bounded candidate diagnostics; actionability checks cover attachment,
visibility, disabled state, viewport placement, and pointer obstruction.

Interaction tools use the content backend by default. Click/hover, text
fill/type/clear, key chords, checkbox/radio clicks, and wheel scrolling can opt
into root-document trusted input with `backend: "cdp"`; this requires the Debug
permission and an exact managed `Input.*` lease. DOM-semantic operations stay
content-only and explicit CDP requests never silently fall back. Actions that
can navigate accept `waitForNavigation: true` and wait for the addressed frame
to complete either a document or same-document navigation within the command
deadline. Password values remain redacted in every interaction result. See
[`docs/trusted-input.md`](docs/trusted-input.md).

`browser_wait` shares the command deadline and supports delay, document ready
state, exact or wildcard URL, element state, text, value, match count,
navigation, network idle, and a safe attribute predicate. `mode` can be
`polling`, `event`, or `auto`, which combines DOM/browser events with bounded
polling. Cancellation propagates into the content script and releases timers,
listeners, and observers. Network idle uses the optional Observe
`webRequest` grant, ignores long-lived WebSockets, and requires an idle interval
after tracked requests finish. Wait results report match metadata but never
echo field values; value waits on sensitive fields and predicates on
sensitive-looking attribute names are rejected.

`browser_screenshot` captures the viewport, full page, or one located element
of the addressed root tab as PNG or JPEG. JPEG quality is configurable from 0
to 100. Before viewport capture on a tab or newly navigated origin, open that
tab and click the extension action once to grant Chrome's temporary `activeTab`
access. This required Core permission has no install warning and avoids the
broader `<all_urls>` permission. Viewport mode serializes captures per window,
temporarily activates an inactive target when necessary, and restores the
previously active tab.
Full-page and element modes require Debug and use a managed exact-method CDP
lease with `captureBeyondViewport`; they never scroll or resize the page and
recheck tab/document identity after capture. Encoded images are limited to
2,000,000 bytes and 16,384 pixels per dimension by default; callers can request
stricter limits. The extension and server independently validate type, size,
and dimensions. Inline base64 is removed and stored as a temporary
`browser://artifacts/{artifactId}` resource. Pixel content is not redacted, so
the result includes an explicit sensitive-content warning. See
[`docs/screenshots.md`](docs/screenshots.md).

`browser_print_to_pdf` uses a managed CDP lease and the optional Debug
permission. It supports one-based page ranges, portrait/landscape orientation,
backgrounds, scale, paper dimensions, margins, and CSS page size preference.
Header/footer HTML is deliberately not exposed. PDF output is limited to
2,000,000 bytes, validated independently by the extension and server, and
returned only as a temporary artifact with a sensitive-content warning. The
tool is available only in the MCP `full` profile and is excluded from batch.

`browser_get_accessibility_tree` provides bounded full and partial CDP AX trees
with role/name/ignored filters, frame association, normalized properties, and
optional locator hints. Unambiguous root-frame role/name matches can include a
temporary document-scoped element reference. The tool independently enforces
depth, node, property, reference, string, scan, frame, and byte limits in the
extension and validates the result again on the Go server. It requires Observe,
Debug, and the MCP `full` profile and is excluded from batch. See
[`docs/accessibility-tree.md`](docs/accessibility-tree.md).

The full-profile emulation tools replace, inspect, and reset one tab's managed
CDP overrides. Supported groups cover viewport/device scale/mobile mode, touch,
offline and bounded network throttling, User-Agent/platform/language, locale,
timezone, geolocation, and fixed CSS media preferences. Every set begins from a
known reset state and rolls back on partial failure. Overrides persist across
navigation until explicit reset or debugger detach; reset remains available
without target-origin access so cleanup cannot be blocked by navigation. The
tools are excluded from batch. See [`docs/emulation.md`](docs/emulation.md).

`browser_evaluate_javascript` runs one bounded expression in the root frame's
isolated world. It requires Observe, Debug, MCP `full`, and the separately
enabled extension feature flag. Results are restricted to bounded JSON-safe
values or explicit unsupported/unserializable/exception metadata; CDP object
handles never leave the extension. Main-world and persistent evaluation are
not exposed, and the tool is excluded from batch. See
[`docs/javascript-evaluation.md`](docs/javascript-evaluation.md).

`browser_send_cdp_command` is an advanced, disabled-by-default read-only CDP
tool. Its fixed initial subset covers bounded accessibility queries, DOM node
description and box geometry, layout metrics, and performance metrics. The Go
server, command router, and extension handler independently validate the exact
method, method-specific parameters, root document, result shape, traversal
limits, and serialized size. Runtime evaluation, cookies, storage, network
access or modification, interception, Target, streams, and browser mutation
are not exposed. Calls require Observe, Debug, MCP `full`, and the separate raw
CDP feature flag; they are audited without parameters or result values and are
excluded from batch. See [`docs/raw-cdp.md`](docs/raw-cdp.md).

`browser_get_performance_metrics` returns at most 200 finite numeric runtime
metrics for one root document. `browser_capture_performance` runs exactly one
100 ms–10 s trace, precise coverage, CPU profile, or audit capture with fixed
CDP settings and a caller-selected artifact limit between 64 KiB and 2 MB.
Captures automatically stop and release their managed CDP lease on success,
error, timeout, or cancellation. Their JSON content is stored only as a
temporary owner-only artifact because URLs and script/page activity cannot be
reliably redacted; normal results and audit logs contain metadata only. Heap
snapshots, caller-controlled trace categories, raw stream handles, continuous
profiling, and batch/generic-command access are prohibited. See
[`docs/performance-diagnostics.md`](docs/performance-diagnostics.md).

Console capture injects packaged, versioned bridges into the selected document's
MAIN and ISOLATED worlds; this baseline does not require the optional debugger
permission. When Debug is granted for a root-frame capture, one managed CDP
lease also collects bounded `Runtime`, `Log`, and failed `Network` events. Both
backends feed the same per-document ring buffer and cursor, with `backend` and
`scope` fields identifying their origin. Start, stop, clear, and paginated read
operations support level, kind, timestamp, and cursor filters. Serialization
avoids getters, limits depth and breadth, handles cycles, and redacts credentials,
authorization values, and sensitive URL query parameters. Capture is released
after navigation, stop, debugger detach, permission revocation, or disconnect.
See [`docs/console-capture.md`](docs/console-capture.md).

Network diagnostics use a separate document-scoped managed CDP consumer.
Start, stop, clear, and paginated read tools retain capture state for at most
eight tabs, 5,000 entries and 2 MB per tab, link redirects with public entry
IDs, and report request/response headers, timing, status, initiator, cache,
completion, and failure metadata.
Sensitive headers, URL credentials/query tokens, and error text are redacted;
opaque CDP request IDs never leave the extension. Stopped captures expire after
ten minutes, while navigation removes stale document state immediately.

`browser_get_network_body` stores one same-origin textual request or response
body as a temporary artifact while capture is active. MIME types use a fixed
text/JSON/XML/JavaScript/form allowlist, decoded bytes are limited to 1 MB, and
both extension and server redact the content. `browser_export_network_har`
stores up to 2 MB of bounded HAR 1.2-like metadata without bodies. Both tools
return metadata only, require Observe, Debug, and MCP `full`, and warn that the
artifact is sensitive. Network tools cannot be reached through the generic
command entry point; body/HAR/stateful capture operations are excluded from
batch. Interception, request modification, cache mutation, and raw body handles
remain prohibited. See [`docs/network-capture.md`](docs/network-capture.md).

Cookie tools list, get, set, and remove cookies visible to an exact URL on the
selected tab origin and in the store containing that tab. Reads are paginated
and return bounded attributes plus `[MASKED]` values by default; setting never
echoes the supplied value. Unmasked reads require an explicit per-call option
and the disabled-by-default **Sensitive data** extension setting. Cookie tools
require Personal data, Observe, and MCP `full`; repeat origin/document/store
checks before returning; and are excluded from generic command and batch paths.
Partition metadata is supported when the browser API exposes it. See
[`docs/cookies.md`](docs/cookies.md).

Web Storage tools list/get/set/remove `localStorage` and `sessionStorage` for
one exact selected root-document origin. Values are masked by default; unmasked
reads require both an explicit per-call option and the same disabled-by-default
**Sensitive data** setting used by cookie reads. Cache Storage exposes bounded
cache names only, while IndexedDB exposes bounded database names and versions
only—never requests, responses, records, handles, or blobs. Confirmed clearing
accepts an explicit subset of origin storage types, performs complete bounded
preflight before mutation, and reports counts and partial browser-API failures.
All storage tools require Personal data, Observe, and MCP `full`, and are
excluded from generic command and batch paths. See
[`docs/web-storage.md`](docs/web-storage.md).

Download tools create, list, inspect, pause, resume, cancel, and erase one
terminal history record through the optional Personal data permission. Results
contain bounded status metadata, strip URL credentials/query/fragment, and
replace Chrome's absolute local path with a basename. Creation accepts only one
policy-allowed HTTP(S) URL and fixes browser filename conflict handling; no
custom filename, path, headers, method, or body is accepted. History erase
requires `confirm: true` and never deletes the file. File reading, `removeFile`,
danger acceptance, bulk erase, generic command, and batch paths are prohibited.
See [`docs/downloads.md`](docs/downloads.md).

History, bookmark, and reading-list tools are browser-scoped Personal data
operations available only in MCP `full`. Reads scan at most 10,000 entries and
return offset-paginated pages of at most 200. History deletion is split into
exact-URL, range, and all-history tools and always requires `confirm: true`;
recursive bookmark-folder removal also requires confirmation. Returned URLs
drop credentials and fragments and replace sensitive query values with
`[REDACTED]`. Audits contain metadata only, and none of these tools can be
reached through generic commands or batch execution. Reading List is
capability-gated because the extension API requires Chrome 120 or newer. See
[`docs/personal-data.md`](docs/personal-data.md).

`browser_batch` runs up to 25 typed commands sequentially against one resolved
browser. It uses a shared 30-second deadline by default (configurable up to 120
seconds), stops on the first failed step unless `stopOnError` is false, and
returns ordered nested tool envelopes within the configured MCP result limit.
Every step passes through the same tool-profile, extension-capability, action
policy, redaction, timeout, and result-size checks as a direct call. Discovery,
selection, generic command dispatch, raw CDP, performance diagnostics, and
recursive batches are not batchable.
Execution is not transactional and completed side effects are never rolled back.

Page inspection never returns unrestricted raw DOM by default. HTML defaults to
100,000 characters and depth 50, supports include/exclude CSS filters, and has
hard limits of 1,000,000 characters and depth 200. Visible text and element
queries use numeric cursors; query pages contain at most 100 elements. Results
report truncation and redaction warnings, and password, secret, token, and
credential field values are replaced before leaving the content script.

`browser_snapshot` returns a compact flat semantic tree with parent links,
roles, accessible names, direct text, states, and temporary references. It
supports interactive-only mode and open shadow roots, defaults to depth 20 and
1,000 nodes, and reports truncation at configurable hard limits of depth 50
and 5,000 nodes.

Grouping and ungrouping use the Core tabs API. Updating a group and reading or
restoring recently closed sessions require the optional Personal data profile;
unsupported Chromium APIs are omitted from the browser's capabilities.

`browser_close_window` requires `confirm: true`. This prevents an implicit
multi-tab close; an omitted or false confirmation is rejected before any
browser command is sent.

The server applies `toolProfile` as a fail-closed allowlist. `minimal` exposes
discovery, selection, browser labels, ping, and read-only window/tab metadata;
`standard` adds normal browser and page automation; and `full` adds
personal-data/debug tools and the expert raw entry point. Filtering applies to
both discovery and direct calls.

Browser action policy is configured separately with exact
`pageOriginAllowlist` and `pageOriginDenylist` values. Deny entries win,
restricted browser schemes and browser extension stores are always blocked,
and incognito is disabled unless `allowIncognito` is set. Targeted actions are
preflighted against current tab metadata, while navigation and creation check
their destination URLs before dispatch. Policy denials are audited without URL
paths, queries, arguments, or result data.

`browser_send_command` is an expert extension-command entry point, but
the extension still enforces its command allowlist and the server rejects
dedicated-only evaluation, network, cookie, storage, download, and raw CDP capabilities. Reviewed raw CDP has its
own typed `browser_send_cdp_command` tool and cannot be used as an arbitrary
DevTools Protocol escape hatch. Performance metrics and captures likewise have
dedicated typed tools and cannot be reached through the generic command tool.

## Tool Result Envelope

Every tool returns one JSON object with `success`, the resolved `target` when
applicable, safe `data`, a `warnings` array, and an RFC 3339 `timestamp`.
Routed browser commands also include `durationMs`. Paginated or artifact-backed
responses promote `nextCursor` and `artifactUri` to the same top-level envelope.
`browserId` remains as a compatibility alias for `target.browserId`.

The pinned MCP SDK does not yet expose structured content, so this object is
currently carried in the tool result's text content instead of being encoded a
second time as an ambiguous nested string.

## Protocol v1

The versioned Draft 2020-12 contract is maintained in
[`protocol/schema/v1.schema.json`](protocol/schema/v1.schema.json). Shared
golden fixtures are validated by both Go and JavaScript tests. Compatibility
rules are documented in [`protocol/COMPATIBILITY.md`](protocol/COMPATIBILITY.md).

The extension starts with a `hello` envelope:

```json
{
  "protocolVersion": "1.0",
  "type": "hello",
  "browserId": "7ec37ee7-...",
  "params": {
    "displayName": "Work Chrome",
    "extensionVersion": "0.3.0",
    "pairingCode": "1234-5678",
    "capabilities": ["browser.ping", "tabs.list"]
  }
}
```

The first handshake contains `pairingCode`; later handshakes contain the issued
`credential`. Authentication failures use `auth_error`, and `revoke` is an
acknowledged credential revocation exchange. Browser commands use
`request`/`response` envelopes with a unique `requestId`. `cancel`, `ping`,
`pong`, and `capabilities_changed` are also supported.

## Security Baseline

- HTTP and WebSocket listeners bind to loopback by default.
- MCP HTTP validates `Host` and `Origin` to reduce DNS rebinding risk.
- MCP HTTP requires a cryptographically random Bearer token stored with
  owner-only permissions.
- Extension WebSocket endpoints must be loopback addresses.
- Browser registration requires a valid credential or an unexpired one-time
  pairing code.
- Pairing codes are rate-limited, expire after ten minutes by default, and are
  rotated after use to prevent replay.
- MCP requests are rate-limited per client session, and inbound extension
  messages are rate-limited independently per browser connection.
- Only credential hashes are persisted by the server; the extension stores the
  raw credential locally.
- Website access is optional and user-granted.
- Restricted browser pages are rejected.
- Password values are redacted from page interaction results.
- Browser results are redacted again at the server boundary and limited to
  2 MiB of sanitized JSON by default.
- WebSocket messages and command deadlines are bounded.

Loopback binding remains mandatory as a defense-in-depth boundary.

The threat model and binding decisions for CDP, evaluation, personal data,
clipboard, file input, performance artifacts, and other sensitive features are
documented in [docs/security-review.md](docs/security-review.md). Features not
approved there remain disabled even when a browser permission exists.
The focused [clipboard and file-input design](docs/clipboard-file-input-design.md)
defines the required one-shot user gesture and server-owned artifact boundary;
it does not enable either runtime feature.
The separate [proxy, content-settings, and browsing-data design](docs/proxy-content-settings-browsing-data-design.md)
keeps proxy and content settings prohibited and defines the disabled-by-default,
two-phase boundary for any future origin-scoped data cleanup.

## Development

Build the server:

```bash
make build
./bin/mcp-browser-control
```

Run the full local verification suite:

```bash
make verify
```

Build and compare two byte-identical cross-platform release bundles:

```bash
make release-check
```

The bundle includes versioned Go binaries, a deterministic extension ZIP,
checksums, generated release notes, a release manifest, and a CycloneDX SBOM.
See [docs/releasing.md](docs/releasing.md) for supported targets, required
tooling, version updates, embedded metadata, and checksum verification.

Individual Go checks remain available:

```bash
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
golangci-lint run ./...
```

Run extension checks:

```bash
make extension-check
make extension-build
```

Run the real two-profile browser E2E with Chromium or Chrome for Testing 116+:

```bash
make e2e CHROME_BIN=/path/to/chrome-for-testing
```

The E2E target starts two isolated headless profiles, loads the unpacked MV3
extension, pairs both with an in-process server, exercises the complete MVP tab,
page-inspection, interaction, wait, and screenshot flow, and restarts one service
worker to verify credential reconnect. It uses a generated test-only extension
manifest with access only to loopback HTTP pages and promotes the Debug permission
for deterministic full-page screenshot coverage. The production manifest and
optional permission flow are unchanged.
Branded Chrome 137+ no longer accepts `--load-extension`, so use
[Chrome for Testing](https://developer.chrome.com/docs/automation-and-testing/download-test-binaries)
or a Chromium build for this target.

Verify the routing and `browser_list` latency budgets, then run the short
reconnect/event soak used by CI:

```bash
make performance
make soak-smoke
```

Release candidates additionally require the configurable eight-hour
qualification (`make soak`). See
[docs/performance-soak.md](docs/performance-soak.md) for budgets, report fields,
and evidence collection.

Run the dependency, license, and repository secret scans after installing
`govulncheck` and `gitleaks`:

```bash
make security-check
```

GitHub Actions runs these checks for pushes, pull requests, manual runs, and a
weekly security schedule. Successful runs publish the server binary, coverage
profile, and unpacked production extension as short-lived CI artifacts.

Core packages:

```text
cmd/server/               executable entry point
internal/
├── app/                  process assembly and transport lifecycle
├── mcpsession/           secure Streamable HTTP session IDs
├── netguard/             local Host and Origin checks
├── protocol/             versioned extension protocol and errors
├── registry/             connected browser registry
├── router/               targeted request/response correlation
├── security/pairing/     one-time codes and browser credentials
├── selection/            per-MCP-session browser selection
├── tools/                MCP tool definitions and handlers
├── transport/websocket/  browser WebSocket transport
├── integration/          multi-browser end-to-end component tests
├── e2e/                  real two-profile Chromium tests (e2e build tag)
└── soak/                 reconnect/event long-run tests (soak build tag)
protocol/                 versioned schema, fixtures, and compatibility policy
```

The product requirements and execution plan are maintained in Russian in
[PRD.md](PRD.md) and [TASKS.md](TASKS.md). All product code, UI, logs, and other
documentation are maintained in English.

## License

MIT License
