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
2. Open the extension popup.
3. Give this browser profile a recognizable name.
4. Keep the default endpoint unless the local server uses another port.
5. Click **Save**, then **Connect**.
6. Click **Grant access to websites** before using page inspection or interaction tools.

Each installed browser profile generates its own stable browser ID. Install the
extension in another profile or Chromium browser to connect a second browser.
Use `browser_list` and `browser_select` from the MCP client to choose between
them.

## Security

- The first release accepts loopback WebSocket endpoints only.
- Website access is optional and must be granted from the popup.
- Restricted pages such as `chrome://` pages cannot be controlled.
- Pairing authentication is planned but is not implemented in this first
  vertical increment. Do not expose the server ports outside the local machine.

## Troubleshooting

- **Disconnected:** verify that the server is running and port 8090 is free.
- **Permission required:** click **Grant access to websites** in the popup.
- **Restricted URL:** switch to an HTTP or HTTPS page.
- **Extension updated:** reload it from the extensions page, then reconnect.
