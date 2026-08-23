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
| Payload limits | `MCP_BROWSER_WS_MAX_MESSAGE_BYTES`, `MCP_BROWSER_MCP_MAX_REQUEST_BYTES` | `-ws_max_message_bytes`, `-mcp_max_request_bytes` |
| Rate limits | `MCP_BROWSER_MCP_REQUESTS_PER_SECOND`, `MCP_BROWSER_MCP_REQUEST_BURST`, `MCP_BROWSER_WS_MESSAGES_PER_SECOND`, `MCP_BROWSER_WS_MESSAGE_BURST` | `-mcp_requests_per_second`, `-mcp_request_burst`, `-ws_messages_per_second`, `-ws_message_burst` |
| MCP Bearer token file | `MCP_BROWSER_MCP_TOKEN_FILE` | `-mcp_token_file` |
| Credential store | `MCP_BROWSER_CREDENTIAL_FILE` | `-credential_file` |
| Pairing controls | `MCP_BROWSER_PAIRING_TTL`, `MCP_BROWSER_PAIRING_MAX_ATTEMPTS`, `MCP_BROWSER_PAIRING_WINDOW` | `-pairing_ttl`, `-pairing_max_attempts`, `-pairing_window` |
| Exact origins | `MCP_BROWSER_ORIGIN_ALLOWLIST` | `-origin_allowlist` |
| Profiles | `MCP_BROWSER_PERMISSION_PROFILE`, `MCP_BROWSER_TOOL_PROFILE` | `-permission_profile`, `-tool_profile` |
| Artifacts | `MCP_BROWSER_ARTIFACT_DIR`, `MCP_BROWSER_ARTIFACT_TTL`, `MCP_BROWSER_ARTIFACT_MAX_BYTES` | `-artifact_dir`, `-artifact_ttl`, `-artifact_max_bytes` |
| Logging | `MCP_BROWSER_LOG_LEVEL`, `MCP_BROWSER_REDACT_LOGS` | `-log_level`, `-redact_logs` |
| Deprecated SSE opt-in | `MCP_BROWSER_ENABLE_LEGACY_SSE` | `-enable_legacy_sse` |

Comma-separate multiple origins in the environment variable or flag. Only
exact browser-extension origins and HTTP(S) loopback origins are accepted.
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

Legacy SSE is disabled by default. Both `transport: "sse"` and
`legacySSEEnabled: true` are required to start it.

The accepted permission, tool, and log profiles are `minimal`, `standard`, and
`full` for profiles, and `error`, `warn`, `info`, and `debug` for logging.
Profile-specific enforcement and artifact lifecycle behavior are applied by
their respective subsystems as those capabilities are enabled.

Artifacts are stored in an owner-only directory, expire after `artifactTTL`,
and share the `artifactMaxBytes` quota. When a new artifact needs space, the
oldest artifacts are evicted first.
