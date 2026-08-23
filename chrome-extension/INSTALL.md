# Install the Chromium Extension

## Requirements

- Chrome or a Chromium-based browser version 116 or newer
- The MCP Browser Control server running locally

## Install

1. Open the browser's extensions page:
   - Chrome: `chrome://extensions`
   - Edge: `edge://extensions`
2. Enable **Developer mode**.
3. Click **Load unpacked**.
4. Select this `chrome-extension` directory.
5. Pin **MCP Browser Control** to the toolbar.

## Connect

1. Start the server. Its browser endpoint defaults to `ws://127.0.0.1:8090/ws`.
2. Copy the current one-time pairing code printed by the server.
3. Open the extension popup.
4. Give this browser profile a recognizable name.
5. Keep the default endpoint unless the local server uses another port.
6. Click **Save**.
7. Enter the code and click **Pair**.
8. Click **Grant access to websites** before using page inspection or interaction tools.

The popup reports the runtime browser, stable browser and current connection
IDs, measured heartbeat latency, last successful connection, and granted
permission profiles. Use **Open settings and diagnostics** for the full
capability and permission lists or to edit the endpoint and browser name.
Page inspection and interaction can be disabled independently in settings;
the extension immediately omits those commands from its advertised capabilities.

The settings page manages four permission profiles: install-time **Core** and
optional **Observe**, **Debug**, and **Personal data**. Each profile shows its
Chrome permissions, host allowlist, related tool domains, warning, and current
state. Permission changes take effect immediately without reloading tabs or
reconnecting the server. Personal data also requests Observe website access
for origin-scoped cookie and storage operations. It includes `sessions` for
recently closed entries and `tabGroups` for group title, color, and collapsed
state management.

Each installed browser profile generates its own stable browser ID. Install the
extension in another profile or Chromium browser to connect a second browser.
Use `browser_list` and `browser_select` from the MCP client to choose between
them.

Each pairing code expires after ten minutes by default and is consumed after
one successful use. Pair browsers sequentially with the newest code printed by
the server. Later connections authenticate automatically with the credential
stored in `chrome.storage.local`.

To revoke a credential, connect the browser and click **Revoke pairing**. The
extension deletes its local credential only after the server confirms the
revocation.

Use **Reset identity** only when this installation must appear as a new browser.
The popup asks for explicit confirmation, creates a new UUID in local storage,
deletes the local credential, and requires pairing again. Revoke the current
pairing first when possible so its server-side credential is also removed.

## Security

- The first release accepts loopback WebSocket endpoints only.
- Website access is optional and must be granted from the popup.
- Restricted pages such as `chrome://` pages cannot be controlled.
- Browser credentials are required, and one-time pairing codes are
  rate-limited and protected against replay.
- Remote mode is not supported. Do not expose the server ports outside the
  local machine.

## Troubleshooting

- **Disconnected:** verify that the server is running and port 8090 is free.
- **Pairing required:** enter the latest code printed by the server; older codes
  may be expired or already consumed.
- **Revocation unavailable:** reconnect the paired browser before revoking its
  credential.
- **Permission required:** click **Grant access to websites** in the popup.
- **Restricted URL:** switch to an HTTP or HTTPS page.
- **Page bridge incompatible:** reload the target tab after updating the extension.
- **Extension updated:** reload it from the extensions page, then reconnect.
- **Stale diagnostics:** open the settings page and click **Refresh**; latency
  appears after the next successful heartbeat.
