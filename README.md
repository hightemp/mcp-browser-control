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
- STDIO, Streamable HTTP, and legacy SSE transports are available;
- tab listing, HTML inspection, CSS queries, clicking, and input work through
  the extension;
- Go race tests, real WebSocket tests, extension protocol tests, and a
  two-browser/two-MCP-session integration test are included.

Pairing authentication, full tab/window control, semantic snapshots, waits,
screenshots, console capture, and network capture are planned but not complete.
Do not expose either server port outside the local machine.

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

- Go 1.23 or newer
- Chrome or a compatible Chromium browser version 116 or newer
- Node.js only for extension unit tests

## Run the Server

Install dependencies and start the default Streamable HTTP transport:

```bash
go mod tidy
go run .
```

Endpoints:

- MCP: `http://127.0.0.1:8896/mcp`
- Browser extensions: `ws://127.0.0.1:8090/ws`

STDIO:

```bash
go run . -t stdio
```

Legacy SSE:

```bash
go run . -t sse
```

Options:

- `-t` — `streamable-http`, `stdio`, or `sse`
- `-h` — MCP HTTP host; default `127.0.0.1`
- `-p` — MCP HTTP port; default `8896`
- `-ws_host` — extension WebSocket host; default `127.0.0.1`
- `-ws_port` — extension WebSocket port; default `8090`
- `-command_timeout` — default browser command timeout; default `15s`

## Install the Extension

Follow [chrome-extension/INSTALL.md](chrome-extension/INSTALL.md). Install the
extension in each browser profile that should be independently selectable.

The extension requests website host access separately from installation. Tab
discovery works without it; page inspection and interaction require the user to
click **Grant access to websites** in the popup.

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

## Available MCP Tools

### Browser discovery

- `browser_list`
- `browser_get`
- `browser_select`
- `browser_get_selected`
- `browser_rename`
- `browser_get_capabilities`
- `browser_ping`

### Implemented extension commands

- `browser_get_tabs`
- `browser_get_html`
- `browser_get_html_by_selector`
- `browser_click_element`
- `browser_input_data`

All target tools accept optional `browserId` and `timeoutMs`. Page tools also
accept optional `tabId`; when omitted, the active tab in the last focused
window is used.

### Registered but not implemented by the current extension

- `browser_get_console_log`
- `browser_get_network_log`

These return `CAPABILITY_UNAVAILABLE` until their event-driven implementations
are added. `browser_send_command` is an expert entry point, but the extension
still enforces its command allowlist.

## Protocol v1

The extension starts with a `hello` envelope:

```json
{
  "protocolVersion": "1.0",
  "type": "hello",
  "browserId": "7ec37ee7-...",
  "params": {
    "displayName": "Work Chrome",
    "extensionVersion": "0.1.0",
    "capabilities": ["browser.ping", "tabs.list"]
  }
}
```

Browser commands use `request`/`response` envelopes with a unique `requestId`.
`cancel`, `ping`, `pong`, and `capabilities_changed` are also supported.

## Security Baseline

- HTTP and WebSocket listeners bind to loopback by default.
- MCP HTTP validates `Host` and `Origin` to reduce DNS rebinding risk.
- Extension WebSocket endpoints must be loopback addresses.
- Website access is optional and user-granted.
- Restricted browser pages are rejected.
- Password values are redacted from page interaction results.
- WebSocket messages and command deadlines are bounded.

Pairing and persistent extension credentials are the next security milestone.
Until then, loopback binding is mandatory for safe use.

## Development

Run all Go checks:

```bash
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
golangci-lint run ./...
```

Run extension checks:

```bash
node --check chrome-extension/src/service-worker.js
npm test --prefix chrome-extension
```

Core packages:

```text
internal/
├── app/                  process assembly and transport lifecycle
├── mcpsession/           secure Streamable HTTP session IDs
├── netguard/             local Host and Origin checks
├── protocol/             versioned extension protocol and errors
├── registry/             connected browser registry
├── router/               targeted request/response correlation
├── selection/            per-MCP-session browser selection
├── tools/                MCP tool definitions and handlers
├── transport/websocket/  browser WebSocket transport
└── integration/          multi-browser end-to-end component tests
```

The product requirements and execution plan are maintained in Russian in
[PRD.md](PRD.md) and [TASKS.md](TASKS.md). All product code, UI, logs, and other
documentation are maintained in English.

## License

MIT License
