# Server Configuration

The server reads configuration in this order, with later sources overriding
earlier ones:

1. secure built-in defaults;
2. a JSON file selected by `-config` or `MCP_BROWSER_CONFIG`;
3. `MCP_BROWSER_*` environment variables;
4. command-line flags.

Both listeners are restricted to loopback hosts. Configuration that attempts
to bind either listener to a non-loopback interface is rejected.

## Example JSON File

```json
{
  "transport": "streamable-http",
  "mcpHost": "127.0.0.1",
  "mcpPort": "8896",
  "webSocketHost": "127.0.0.1",
  "webSocketPort": "8090",
  "commandTimeout": "15s",
  "webSocketHandshakeTimeout": "5s",
  "webSocketWriteTimeout": "5s",
  "webSocketReadTimeout": "60s",
  "webSocketPingInterval": "20s",
  "webSocketSendQueueSize": 64,
  "shutdownTimeout": "5s",
  "webSocketMaxMessageBytes": 4194304,
  "mcpMaxRequestBytes": 4194304,
  "mcpMaxResultBytes": 2097152,
  "mcpRequestsPerSecond": 100,
  "mcpRequestBurst": 200,
  "webSocketMessagesPerSecond": 1000,
  "webSocketMessageBurst": 2000,
  "mcpTokenFile": "/home/user/.config/mcp-browser-control/mcp-token",
  "credentialFile": "/home/user/.config/mcp-browser-control/credentials.json",
  "pairingTTL": "10m",
  "pairingMaxAttempts": 5,
  "pairingAttemptWindow": "1m",
  "originAllowlist": ["chrome-extension://extension-id"],
  "pageOriginAllowlist": ["https://allowed.example"],
  "pageOriginDenylist": ["https://admin.allowed.example"],
  "allowIncognito": false,
  "permissionProfile": "minimal",
  "toolProfile": "standard",
  "artifactDirectory": "/home/user/.cache/mcp-browser-control/artifacts",
  "artifactTTL": "24h",
  "artifactMaxBytes": 536870912,
  "logLevel": "info",
  "redactLogs": true,
  "legacySSEEnabled": false
}
```

Unknown JSON fields, malformed durations, unsafe origins, and invalid limits
cause startup to fail. An empty `credentialFile` selects in-memory credential
storage.

## Environment Variables and Flags

| Setting | Environment variable | Flag |
| --- | --- | --- |
| Config file | `MCP_BROWSER_CONFIG` | `-config` |
| MCP transport | `MCP_BROWSER_TRANSPORT` | `-t` |
| MCP host / port | `MCP_BROWSER_MCP_HOST`, `MCP_BROWSER_MCP_PORT` | `-h`, `-p` |
| WebSocket host / port | `MCP_BROWSER_WS_HOST`, `MCP_BROWSER_WS_PORT` | `-ws_host`, `-ws_port` |
| Command timeout | `MCP_BROWSER_COMMAND_TIMEOUT` | `-command_timeout` |
| WebSocket lifecycle | `MCP_BROWSER_WS_HANDSHAKE_TIMEOUT`, `MCP_BROWSER_WS_WRITE_TIMEOUT`, `MCP_BROWSER_WS_READ_TIMEOUT`, `MCP_BROWSER_WS_PING_INTERVAL`, `MCP_BROWSER_WS_SEND_QUEUE_SIZE` | `-ws_handshake_timeout`, `-ws_write_timeout`, `-ws_read_timeout`, `-ws_ping_interval`, `-ws_send_queue_size` |
| Shutdown timeout | `MCP_BROWSER_SHUTDOWN_TIMEOUT` | `-shutdown_timeout` |
| Payload limits | `MCP_BROWSER_WS_MAX_MESSAGE_BYTES`, `MCP_BROWSER_MCP_MAX_REQUEST_BYTES`, `MCP_BROWSER_MCP_MAX_RESULT_BYTES` | `-ws_max_message_bytes`, `-mcp_max_request_bytes`, `-mcp_max_result_bytes` |
| Rate limits | `MCP_BROWSER_MCP_REQUESTS_PER_SECOND`, `MCP_BROWSER_MCP_REQUEST_BURST`, `MCP_BROWSER_WS_MESSAGES_PER_SECOND`, `MCP_BROWSER_WS_MESSAGE_BURST` | `-mcp_requests_per_second`, `-mcp_request_burst`, `-ws_messages_per_second`, `-ws_message_burst` |
| MCP Bearer token file | `MCP_BROWSER_MCP_TOKEN_FILE` | `-mcp_token_file` |
| Credential store | `MCP_BROWSER_CREDENTIAL_FILE` | `-credential_file` |
| Pairing controls | `MCP_BROWSER_PAIRING_TTL`, `MCP_BROWSER_PAIRING_MAX_ATTEMPTS`, `MCP_BROWSER_PAIRING_WINDOW` | `-pairing_ttl`, `-pairing_max_attempts`, `-pairing_window` |
| MCP/extension client origins | `MCP_BROWSER_ORIGIN_ALLOWLIST` | `-origin_allowlist` |
| Allowed page origins | `MCP_BROWSER_PAGE_ORIGIN_ALLOWLIST` | `-page_origin_allowlist` |
| Denied page origins | `MCP_BROWSER_PAGE_ORIGIN_DENYLIST` | `-page_origin_denylist` |
| Incognito contexts | `MCP_BROWSER_ALLOW_INCOGNITO` | `-allow_incognito` |
| Profiles | `MCP_BROWSER_PERMISSION_PROFILE`, `MCP_BROWSER_TOOL_PROFILE` | `-permission_profile`, `-tool_profile` |
| Artifacts | `MCP_BROWSER_ARTIFACT_DIR`, `MCP_BROWSER_ARTIFACT_TTL`, `MCP_BROWSER_ARTIFACT_MAX_BYTES` | `-artifact_dir`, `-artifact_ttl`, `-artifact_max_bytes` |
| Logging | `MCP_BROWSER_LOG_LEVEL`, `MCP_BROWSER_REDACT_LOGS` | `-log_level`, `-redact_logs` |
| Deprecated SSE opt-in | `MCP_BROWSER_ENABLE_LEGACY_SSE` | `-enable_legacy_sse` |

Comma-separate multiple origins in the environment variable or flag.
`originAllowlist` controls which local MCP pages and extension IDs may connect;
only exact browser-extension origins and HTTP(S) loopback origins are accepted.
Requests without an `Origin` header remain available to native MCP clients.
All Streamable HTTP and legacy SSE requests must also include the token from
`mcpTokenFile` as an `Authorization: Bearer <token>` header. The server creates
the file on first use with owner-only permissions and logs only its path.

The MCP request limit is an independent token bucket for each MCP session and
applies to STDIO, Streamable HTTP, and legacy SSE. Browser message limits are
independent per authenticated WebSocket connection. Buckets permit the
configured burst and then refill at the configured per-second rate. Exceeding
the MCP limit returns a JSON-RPC error; exceeding the browser limit closes that
connection with WebSocket policy-violation code 1008. Session buckets are
removed on normal cleanup and also have bounded count and idle lifetime.

Every browser tool result and JSON browser resource passes through a second,
server-side redaction boundary before reaching the MCP client. Authorization
and cookie headers, cookie values, password/credential fields, form bodies,
clipboard data, sensitive URL query values, URL userinfo, and local filesystem
paths are replaced even if the extension failed to redact them. Traversal has
fixed depth, node, and string budgets. The final sanitized JSON must fit
`mcpMaxResultBytes` (2 MiB by default) or the operation returns
`PAYLOAD_TOO_LARGE`.

`pageOriginAllowlist` and `pageOriginDenylist` control browser actions, not
network clients. They contain exact HTTP(S) origins without paths, queries, or
fragments. The denylist wins. An empty allowlist permits HTTP(S) origins except
denylisted entries; restricted browser schemes and Chrome/Edge extension stores
are always denied. Before a targeted tab action, the server reads current tab
metadata and checks its URL and incognito state. Navigation and tab/window
creation also check every destination before dispatch. Incognito browser
connections and window creation are denied unless `allowIncognito` is true.
Denied actions are audited using the action, browser ID, origin, and reason;
full URLs, arguments, and results are not logged.

Legacy SSE is disabled by default. Both `transport: "sse"` and
`legacySSEEnabled: true` are required to start it.

The accepted permission and tool profiles are `minimal`, `standard`, and
`full`; log levels are `error`, `warn`, `info`, and `debug`. The tool profile is
an enforced allowlist for both `tools/list` and direct calls:

- `minimal` exposes browser discovery/selection, browser labels, ping, and
  read-only window/tab metadata;
- `standard` adds normal tab, window, page, screenshot, wait, and console
  automation; full-page and element screenshot modes additionally require the
  browser's optional Debug permission, as does an explicit trusted `cdp`
  interaction backend;
- `full` also exposes tab-group/session tools, network diagnostics, and the
  expert `browser_send_command` entry point.

Unknown or newly added tools fail closed until assigned to a profile. The
extension still checks its live capability and permission set for every routed
command. Optional browser permissions can only be requested by an explicit
click in the extension settings UI; MCP commands never open a permission
prompt. `browser_close_window` requires `confirm: true` because it closes every
tab in the window.

Artifacts are stored in an owner-only directory, expire after `artifactTTL`,
and share the `artifactMaxBytes` quota. When a new artifact needs space, the
oldest artifacts are evicted first.
