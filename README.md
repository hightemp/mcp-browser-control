# MCP Browser Control

MCP Browser Control is a local Go server and Manifest V3 extension for
controlling one or more Chromium browser profiles through the Model Context
Protocol.

## Current Status

The first multi-browser vertical slice is implemented:

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
- window, tab, tab-group, session, bounded page inspection, semantic snapshot,
  locator, DOM interaction, waits, viewport screenshots, and basic console
  diagnostics work through the extension;
- Go race tests, real WebSocket tests, extension protocol tests, and a
  two-browser/two-MCP-session integration test are included.

Full-page and element screenshots, trusted CDP input, CDP-enriched diagnostics,
and full network capture are planned but not complete. Remote mode is not
implemented; do not expose either server port outside the local machine.

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
- `browser_start_console_capture`
- `browser_stop_console_capture`
- `browser_clear_console_log`
- `browser_get_console_log`

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

Interaction tools use the synthetic content-script backend when `backend` is
`auto` or `content`. Requesting `cdp` currently returns
`CAPABILITY_UNAVAILABLE`; trusted input will be enabled by the CDP session
manager. Actions that can navigate accept `waitForNavigation: true` and then
wait for the addressed frame to complete either a document or same-document
navigation within the command deadline. Password values remain redacted in
every interaction result.

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

`browser_screenshot` captures the viewport of the addressed tab as PNG or
JPEG. JPEG quality is configurable from 0 to 100. The command serializes
captures per window, temporarily activates an inactive target when necessary,
and restores the previously active tab. Encoded images are limited to
2,000,000 bytes and 16,384 pixels per dimension by default; callers can request
stricter limits. The server validates the image type and dimensions, removes
inline base64 from the result, and stores the bytes as a temporary
`browser://artifacts/{artifactId}` resource. Pixel content is not redacted, so
the result includes an explicit sensitive-content warning. Full-page and
element captures remain planned with the CDP-backed P1 implementation.

Console capture injects packaged, versioned bridges into the selected document's
MAIN and ISOLATED worlds; it does not require the optional debugger permission.
Start, stop, clear, and paginated read operations are available. Reads support
level, kind, timestamp, and cursor filters. Console calls, JavaScript exceptions,
unhandled rejections, and failed resource loads enter a per-document ring buffer
bounded by both entry count and serialized size. Object serialization avoids
getters, limits depth and breadth, handles cycles, and redacts credentials,
authorization values, and sensitive URL query parameters in both bridge worlds.
Capture is document-scoped and must be started again after navigation. CDP
Runtime and Log enrichment remains part of the P1 debugger implementation.

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

### Registered but not implemented by the current extension

- `browser_get_network_log`

This returns `CAPABILITY_UNAVAILABLE` until its event-driven implementation is
added. `browser_send_command` is an expert entry point, but the extension still
enforces its command allowlist.

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
    "extensionVersion": "0.1.0",
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
- Only credential hashes are persisted by the server; the extension stores the
  raw credential locally.
- Website access is optional and user-granted.
- Restricted browser pages are rejected.
- Password values are redacted from page interaction results.
- WebSocket messages and command deadlines are bounded.

Loopback binding remains mandatory as a defense-in-depth boundary.

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
extension, pairs both with an in-process server, exercises selected and parallel
page commands, and restarts one service worker to verify credential reconnect.
It uses a generated test-only extension manifest with access only to loopback
HTTP pages; the production manifest and optional permission flow are unchanged.
Branded Chrome 137+ no longer accepts `--load-extension`, so use
[Chrome for Testing](https://developer.chrome.com/docs/automation-and-testing/download-test-binaries)
or a Chromium build for this target.

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
└── e2e/                  real two-profile Chromium tests (e2e build tag)
protocol/                 versioned schema, fixtures, and compatibility policy
```

The product requirements and execution plan are maintained in Russian in
[PRD.md](PRD.md) and [TASKS.md](TASKS.md). All product code, UI, logs, and other
documentation are maintained in English.

## License

MIT License
