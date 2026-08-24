# Install the Chromium Extension

This guide covers unpacked installation, permissions, pairing, updates,
diagnostics, revocation, and removal for Chrome and Microsoft Edge.

## Requirements

- Chrome or Microsoft Edge version 116 or newer
- The MCP Browser Control repository on the same machine as the browser
- The MCP Browser Control server running on a loopback address
- Node.js 20.19 or newer when building the production extension directory

See the project [README](../README.md) for server build and startup instructions.

## Build the Extension

From the repository root, install the locked development dependencies, run the
extension checks, and create the production directory:

```bash
npm ci --prefix chrome-extension
make extension-check extension-build
```

Load `chrome-extension/dist/extension` into the browser. This directory contains
only the manifest and runtime files. Extension developers may load the source
`chrome-extension` directory instead, but should still run `make
extension-check` before reloading it.

## Install in Chrome

1. Open `chrome://extensions`.
2. Enable **Developer mode** in the upper-right corner.
3. Click **Load unpacked**.
4. Select `chrome-extension/dist/extension`, the directory that directly
   contains `manifest.json`.
5. Confirm that **MCP Browser Control** appears without an error badge.
6. Pin the extension from Chrome's Extensions menu for easier access.

## Install in Microsoft Edge

1. Open `edge://extensions`.
2. Enable **Developer mode** in the left sidebar.
3. Click **Load unpacked**.
4. Select `chrome-extension/dist/extension`, the directory that directly
   contains `manifest.json`.
5. Confirm that **MCP Browser Control** appears without an error badge.
6. Pin the extension from Edge's Extensions menu.

Each browser profile is a separate browser instance. Installing the same build
in two profiles creates two stable browser IDs, so the MCP server can address
and select them independently.

## Understand Permissions

The extension separates permissions into profiles. Optional permissions are
requested only after a user clicks the corresponding button in settings.

| Profile           | Access                                                                                     | When to enable it                                                                                                |
| ----------------- | ------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------- |
| **Core**          | Alarms, scripting, storage, tabs, and navigation metadata                                  | Installed automatically for connection, window, and tab management                                               |
| **Observe**       | HTTP/HTTPS site access and `webRequest` metadata                                           | Page inspection, actions, waits, screenshots, and base console capture                                           |
| **Debug**         | Chrome debugger backend                                                                    | PDF, accessibility, emulation, opt-in isolated evaluation, reviewed raw CDP, and bounded performance diagnostics |
| **Personal data** | Cookies, downloads, sessions, tab groups, bookmarks, history, clipboard, and browsing data | Only when a required personal-data tool is implemented and explicitly needed                                     |

The Personal data profile also enables its Observe dependency. Removing one
optional profile removes only that profile's grants. The server immediately
receives updated capabilities; a browser reload or reconnect is not required.
Chrome and Edge always show their own confirmation prompt before granting
optional access.

## Pair the First Browser

1. Start the server. The browser endpoint defaults to
   `ws://127.0.0.1:8090/ws`.
2. Copy the current one-time pairing code from the server log.
3. Open the extension popup.
4. Enter a recognizable browser name, such as `Work Chrome`.
5. Keep the default endpoint unless the local server uses a different port.
6. Click **Save**.
7. Enter the pairing code and click **Pair**.
8. Wait for the popup status to become **Connected**.
9. Grant the **Observe** profile before using page inspection or interaction
   tools.
10. For isolated JavaScript evaluation only, grant **Debug** and enable the
    separate advanced evaluation checkbox in settings. It is off by default.
11. For the reviewed read-only CDP tool only, grant **Debug** and enable the
    separate advanced raw CDP checkbox. It is also off by default.
12. For performance metrics or bounded trace, coverage, CPU-profile, and audit
    artifacts, grant **Debug**. Treat every generated artifact as sensitive.

A pairing code expires after ten minutes by default and is consumed by one
successful pairing. Later connections use the browser credential stored in
`chrome.storage.local`; the server stores only its hash.

## Pair Additional Browsers

1. Open another Chrome/Edge profile or another supported Chromium browser.
2. Install the extension in that profile.
3. Use the newest pairing code printed after the previous pairing succeeded.
4. Give the profile a distinct display name and complete pairing.
5. Run `browser_list` from the MCP client and verify both browser IDs.
6. Use `browser_select` before commands that omit `browserId`, or pass an
   explicit `browserId` to each tool.

Never reuse a consumed code. Pair profiles sequentially so each uses the newest
code shown by the server.

## Update an Unpacked Installation

1. Update the repository checkout.
2. Run `npm ci --prefix chrome-extension` when `package-lock.json` changed.
3. Run `make extension-check extension-build`.
4. Open `chrome://extensions` or `edge://extensions` in every installed profile.
5. Click **Reload** on the **MCP Browser Control** card.
6. Open the popup and confirm that it reconnects. Click **Connect or retry** in
   settings if automatic reconnection does not occur.
7. Reload target pages if diagnostics report **Page bridge incompatible**.

The stable browser ID, settings, grants, and pairing credential normally survive
an unpacked extension reload. Console capture is document-scoped and must be
started again after a target page navigation or reload.

## Revoke Pairing or Reset Identity

To revoke the active credential, connect the browser, open the popup, and click
**Revoke pairing**. The extension deletes its local credential only after the
server confirms revocation. The browser then requires a new one-time code.

Use **Reset identity** only when this installation must appear as a new browser.
The confirmation action creates a new browser ID, removes the local credential,
and preserves other settings. Revoke the current pairing first whenever
possible so the previous server-side credential is removed too.

## Use Diagnostics

Open the popup and select **Open settings and diagnostics**. The page shows:

- runtime browser name and version;
- stable browser ID and current connection ID;
- configured endpoint and connection status;
- last successful connection and heartbeat latency;
- enabled, partial, and disabled permission profiles;
- advertised capabilities and currently granted permissions;
- the most recent safe connection error.

Click **Refresh** after changing permissions or the endpoint. `browser_list`,
`browser_get_capabilities`, and the
`browser://instances/{browserId}/capabilities` MCP resource provide the
server-side view for comparison.

## Uninstall

1. While the server is reachable, click **Revoke pairing** and wait for its
   acknowledgement.
2. Open `chrome://extensions` or `edge://extensions`.
3. Click **Remove** on **MCP Browser Control** and confirm.
4. Repeat these steps for every browser profile that has the extension.

Browser removal deletes the extension's local storage and permission grants. If
the extension is removed before revocation, the old credential hash can remain
in the server credential store, but its raw credential is no longer available
to authenticate; a later installation must pair as a new browser.

## Security Notes

- Only loopback WebSocket endpoints are accepted.
- Website access is optional and user-granted.
- Restricted pages such as `chrome://`, `edge://`, and extension pages cannot
  be controlled.
- Do not expose the MCP or browser WebSocket ports outside the local machine.
- Grant Debug or Personal data only for a specific tool that needs it.
- Review the displayed browser ID before running destructive browser actions.

## Troubleshooting

- **Manifest file is missing:** select the directory that directly contains
  `manifest.json`, normally `chrome-extension/dist/extension`.
- **Unsupported extension or APIs:** update Chrome or Edge to version 116 or
  newer.
- **Disconnected:** verify that the server is running and local port 8090 is
  free, then click **Connect or retry**.
- **Connection refused:** confirm that the popup endpoint matches `-ws_host`
  and `-ws_port`; `ws://` and `wss://` endpoints are accepted only for
  `127.0.0.1`, `localhost`, or IPv6 loopback.
- **Pairing required:** enter the latest code printed by the server. Older codes
  may be expired or already consumed.
- **Revocation unavailable:** reconnect the paired browser before revoking its
  credential.
- **Permission required:** enable Observe or the named profile in settings and
  accept the browser prompt.
- **Capability unavailable:** compare the extension diagnostics with
  `browser_get_capabilities`; the browser API, feature flag, or permission may
  be unavailable.
- **Restricted URL:** switch to an HTTP or HTTPS page.
- **Page bridge incompatible:** reload the target tab after updating the
  extension.
- **Stale diagnostics:** click **Refresh** and wait for the next heartbeat before
  evaluating latency.
