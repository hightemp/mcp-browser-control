# MCP Browser Control Tool

This project is an MCP (Model Context Protocol) server that communicates with a Chrome extension over WebSocket to automate browser actions.

## Architecture

```text
┌─────────────────┐    WebSocket     ┌─────────────────┐    MCP Protocol    ┌─────────────────┐
│  Chrome         │ ◄──────────────► │  Go MCP Server  │ ◄─────────────────► │  MCP Client     │
│  Extension      │   ws://localhost │  (main.go)      │   SSE/STDIO        │  (Claude, etc.) │
└─────────────────┘      :8090/ws    └─────────────────┘                    └─────────────────┘
```

## Components

### 1. Chrome Extension (`chrome-extension/`)

The browser extension:

- Connects to the WebSocket server at `ws://localhost:8090/ws`
- Executes commands on web pages
- Provides a DevTools panel for manual control
- Supports automatic reconnection

Key files:

- `manifest.json` — extension configuration
- `background.js` — WebSocket manager and command handling
- `content.js` — page DOM interaction
- `panel.js` — DevTools panel for manual control
- `popup.js` — extension popup interface

### 2. MCP Server (`main.go`)

The Go server:

- Starts a WebSocket server for communication with the extension
- Provides MCP tools for browser automation
- Supports SSE and STDIO transports for MCP clients
- Logs all operations

## Supported Commands

### 1. `browser_get_html`

Gets the HTML content of the current browser page.

Parameters:

- `tabId` (optional) — browser tab ID

### 2. `browser_get_html_by_selector`

Gets the HTML of elements matching a CSS selector.

Parameters:

- `selector` (required) — CSS selector used to find elements
- `tabId` (optional) — browser tab ID

### 3. `browser_click_element`

Clicks an element on the page.

Parameters:

- `selector` (optional) — CSS selector of the element to click
- `index` (optional) — element index when multiple matches are found (default: `0`)
- `coordinates` (optional) — coordinate object in the form `{x: number, y: number}`
- `tabId` (optional) — browser tab ID

Either `selector` or `coordinates` must be provided.

### 4. `browser_input_data`

Enters data into an input field on the page.

Parameters:

- `selector` (required) — CSS selector of the input field
- `value` (required) — value to enter
- `index` (optional) — element index when multiple matches are found (default: `0`)
- `clear` (optional) — clear the field before entering data (default: `true`)
- `tabId` (optional) — browser tab ID

### 5. `browser_get_console_log`

Gets browser console logs.

Parameters:

- `tabId` (optional) — browser tab ID

### 6. `browser_get_network_log`

Gets browser network logs.

Parameters:

- `tabId` (optional) — browser tab ID

### 7. `browser_send_command`

Sends an arbitrary command to the browser extension.

Parameters:

- `command` (required) — command name
- `data` (optional) — command data
- `tabId` (optional) — browser tab ID

### 8. `browser_get_tabs`

Gets all open browser tabs and identifies the active tab.

Parameters:

- This command takes no parameters.

Returns:

- `tabs` — an array of objects containing tab information:
  - `id` — tab ID
  - `title` — tab title
  - `url` — tab URL
  - `active` — whether the tab is active
  - `windowId` — window ID
  - `index` — tab position in the window
  - `pinned` — whether the tab is pinned
  - `status` — loading status (`loading` or `complete`)
  - `favIconUrl` — site icon URL
  - `incognito` — whether the tab is in incognito mode
- `totalCount` — total number of tabs
- `activeTabId` — active tab ID

## Installation and Startup

### 1. Install the Chrome Extension

Follow the instructions in `chrome-extension/INSTALL.md`.

### 2. Start the MCP Server

```bash
# Install dependencies
go mod tidy

# Start with the SSE transport (default)
go run main.go

# Start with the STDIO transport
go run main.go -t stdio

# Configure ports
go run main.go -ws_port 8090 -p 8896 -h 0.0.0.0
```

Command-line options:

- `-t` — transport type: `sse` (default) or `stdio`
- `-h` — SSE server host (default: `0.0.0.0`)
- `-p` — SSE server port (default: `8896`)
- `-ws_port` — WebSocket server port (default: `8090`)

### 3. Connect an MCP Client

#### SSE Transport

```text
URL: http://localhost:8896/sse
```

#### STDIO Transport

```bash
go run main.go -t stdio
```

## Usage

### With an MCP Client (for example, Claude Desktop)

```json
{
  "mcpServers": {
    "browser-tool": {
      "command": "go",
      "args": ["run", "/path/to/main.go"],
      "env": {}
    }
  }
}
```

### With the DevTools Panel

1. Open Chrome DevTools (F12).
2. Select the **Browser Tool** tab.
3. Click **Connect**.
4. Use the interface to run commands.

## Logging

The server keeps detailed logs of all operations:

- WebSocket connections and disconnections
- Sent commands
- Errors and warnings

Example:

```text
2024/01/01 12:00:00 MCP Browser Extension Tool Server started
2024/01/01 12:00:00 WebSocket server: ws://localhost:8090/ws
2024/01/01 12:00:00 MCP SSE server: http://127.0.0.1:8896/sse
[WebSocket] 2024/01/01 12:00:01 WebSocket server started on port 8090
[WebSocket] 2024/01/01 12:00:05 New WebSocket connection: client_1704110405123456789
[WebSocket] 2024/01/01 12:00:10 Message sent to client client_1704110405123456789: {Command:GET_HTML Params:map[] Data:<nil> TabID:0 ID:msg_1704110410987654321}
```

## Development

### Project Structure

```text
.
├── main.go                    # MCP server
├── go.mod                     # Go module
├── go.sum                     # Go dependencies
├── README.md                  # Documentation
└── chrome-extension/          # Chrome extension
    ├── manifest.json          # Extension configuration
    ├── background.js          # Background script
    ├── content.js             # Content script
    ├── popup.html/js          # Popup interface
    ├── panel.html/js/css      # DevTools panel
    ├── devtools.html/js       # DevTools integration
    ├── icons/                 # Extension icons
    └── INSTALL.md             # Installation instructions
```

### Adding New Commands

1. Add a new argument structure to `main.go`.
2. Create a new tool in `registerBrowserTools()`.
3. Implement a typed handler.
4. Add command support to the extension's `background.js`.
5. Add any required logic to `content.js`.

## Security

- The WebSocket server accepts connections only from localhost.
- The extension runs only on allowed domains.
- All commands are logged for auditing.
- WebSocket connections support CORS.

## License

MIT License
