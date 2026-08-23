package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// WebSocket upgrader
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow connections from any origin
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// WebSocketManager stores and manages WebSocket connections.
type WebSocketManager struct {
	connections     map[string]*ConnectionInfo
	pendingRequests map[string]*PendingRequest
	mutex           sync.RWMutex
	logger          *log.Logger
}

// ConnectionInfo stores information about a connection.
type ConnectionInfo struct {
	conn     *websocket.Conn
	lastPing time.Time
	stopChan chan bool
}

// PendingRequest tracks a request awaiting a response.
type PendingRequest struct {
	ID       string
	Response chan WebSocketMessage
	Timeout  time.Time
}

// Global WebSocket connection manager.
var wsManager *WebSocketManager

// Message structures.
type WebSocketMessage struct {
	Command string                 `json:"Command"`
	Params  map[string]interface{} `json:"Params,omitempty"`
	Data    interface{}            `json:"Data,omitempty"`
	TabID   int                    `json:"TabID,omitempty"`
	ID      string                 `json:"ID,omitempty"`
}

type BrowserResponse struct {
	Success   bool        `json:"success"`
	Data      interface{} `json:"data,omitempty"`
	Error     string      `json:"error,omitempty"`
	Command   string      `json:"command"`
	Timestamp string      `json:"timestamp"`
}

// Typed tool argument structures.
type GetHtmlArgs struct {
	TabID *int `json:"tabId,omitempty"`
}

type GetHtmlBySelectorArgs struct {
	Selector string `json:"selector"`
	TabID    *int   `json:"tabId,omitempty"`
}

type ClickElementArgs struct {
	Selector    *string                 `json:"selector,omitempty"`
	Index       *int                    `json:"index,omitempty"`
	Coordinates *map[string]interface{} `json:"coordinates,omitempty"`
	TabID       *int                    `json:"tabId,omitempty"`
}

type InputDataArgs struct {
	Selector string `json:"selector"`
	Value    string `json:"value"`
	Index    *int   `json:"index,omitempty"`
	Clear    *bool  `json:"clear,omitempty"`
	TabID    *int   `json:"tabId,omitempty"`
}

type GetConsoleLogArgs struct {
	TabID *int `json:"tabId,omitempty"`
}

type GetNetworkLogArgs struct {
	TabID *int `json:"tabId,omitempty"`
}

type SendCommandArgs struct {
	Command string                 `json:"command"`
	Data    map[string]interface{} `json:"data,omitempty"`
	TabID   *int                   `json:"tabId,omitempty"`
}

type GetTabsArgs struct {
	// Empty because this command takes no parameters.
}

func main() {
	var transport string
	var host string
	var port string
	var wsPort string
	flag.StringVar(&transport, "t", "sse", "Transport type (stdio or sse)")
	flag.StringVar(&host, "h", "0.0.0.0", "Host of sse server")
	flag.StringVar(&wsPort, "ws_port", "8090", "Port of web socket server")
	flag.StringVar(&port, "p", "8896", "Port of sse server")
	flag.Parse()

	// Initialize the WebSocket manager.
	wsManager = &WebSocketManager{
		connections:     make(map[string]*ConnectionInfo),
		pendingRequests: make(map[string]*PendingRequest),
		logger:          log.New(log.Writer(), "[WebSocket] ", log.LstdFlags),
	}

	// Start the WebSocket server in a separate goroutine.
	go startWebSocketServer(wsPort)

	// Create the MCP server.
	mcpServer := server.NewMCPServer(
		"go_mcp_browser_ext_tool",
		"1.0.0",
	)

	// Register all browser tools.
	registerBrowserTools(mcpServer)

	log.Printf("MCP Browser Extension Tool Server started")
	log.Printf("WebSocket server: ws://localhost:%s/ws", wsPort)

	if transport == "sse" {
		sseServer := server.NewSSEServer(mcpServer, server.WithBaseURL(fmt.Sprintf("http://localhost:%s", port)))
		log.Printf("MCP SSE server: http://127.0.0.1:%s/sse", port)
		if err := sseServer.Start(fmt.Sprintf("%s:%s", host, port)); err != nil {
			log.Fatalf("SSE server error: %v", err)
		}
	} else {
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("STDIO server error: %v", err)
		}
	}
}

// startWebSocketServer starts the WebSocket server.
func startWebSocketServer(port string) {
	http.HandleFunc("/ws", handleWebSocket)

	wsManager.logger.Printf("WebSocket server started on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Failed to start WebSocket server: %v", err)
	}
}

// handleWebSocket handles WebSocket connections.
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		wsManager.logger.Printf("Failed to upgrade connection: %v", err)
		return
	}
	defer conn.Close()

	clientID := fmt.Sprintf("client_%d", time.Now().UnixNano())
	wsManager.addConnection(clientID, conn)
	defer wsManager.removeConnection(clientID)

	wsManager.logger.Printf("New WebSocket connection: %s", clientID)

	// Set up ping/pong handlers.
	conn.SetPingHandler(func(appData string) error {
		wsManager.logger.Printf("Received ping from %s", clientID)
		return conn.WriteMessage(websocket.PongMessage, []byte{})
	})

	conn.SetPongHandler(func(appData string) error {
		wsManager.logger.Printf("Received pong from %s", clientID)
		return nil
	})

	// Read messages from the client.
	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				wsManager.logger.Printf("Failed to read message from %s: %v", clientID, err)
			}
			break
		}

		// Handle ping messages.
		if messageType == websocket.TextMessage && string(data) == "ping" {
			wsManager.logger.Printf("Received ping from %s", clientID)
			err = conn.WriteMessage(websocket.TextMessage, []byte("pong"))
			if err != nil {
				wsManager.logger.Printf("Failed to send pong to client %s: %v", clientID, err)
				break
			}
			continue
		}

		// Handle regular JSON messages.
		if messageType == websocket.TextMessage {
			var msg WebSocketMessage
			err := json.Unmarshal(data, &msg)
			if err != nil {
				wsManager.logger.Printf("Failed to parse JSON from %s: %v", clientID, err)
				continue
			}

			wsManager.logger.Printf("Received message from %s: %+v", clientID, msg)

			// Handle responses from the extension.
			if msg.ID != "" {
				wsManager.handleResponse(msg)
			}
		}
	}

	wsManager.logger.Printf("WebSocket connection closed: %s", clientID)
}

// WebSocketManager methods.
func (wsm *WebSocketManager) addConnection(id string, conn *websocket.Conn) {
	wsm.mutex.Lock()
	defer wsm.mutex.Unlock()
	wsm.connections[id] = &ConnectionInfo{
		conn:     conn,
		lastPing: time.Now(),
		stopChan: make(chan bool),
	}
}

func (wsm *WebSocketManager) removeConnection(id string) {
	wsm.mutex.Lock()
	defer wsm.mutex.Unlock()
	delete(wsm.connections, id)
}

func (wsm *WebSocketManager) sendToAll(message WebSocketMessage) error {
	wsm.mutex.RLock()
	defer wsm.mutex.RUnlock()

	if len(wsm.connections) == 0 {
		return errors.New("no active WebSocket connections")
	}

	var lastError error
	sentCount := 0

	for clientID, connInfo := range wsm.connections {
		err := connInfo.conn.WriteJSON(message)
		if err != nil {
			wsm.logger.Printf("Failed to send message to client %s: %v", clientID, err)
			lastError = err
		} else {
			sentCount++
			wsm.logger.Printf("Message sent to client %s: %+v", clientID, message)
		}
	}

	if sentCount == 0 {
		return fmt.Errorf("failed to send message to any client: %v", lastError)
	}

	return nil
}

func (wsm *WebSocketManager) handleResponse(msg WebSocketMessage) {
	wsm.mutex.Lock()
	defer wsm.mutex.Unlock()

	if pendingReq, exists := wsm.pendingRequests[msg.ID]; exists {
		select {
		case pendingReq.Response <- msg:
			wsm.logger.Printf("Response delivered for request %s", msg.ID)
		default:
			wsm.logger.Printf("Response channel blocked for request %s", msg.ID)
		}
		delete(wsm.pendingRequests, msg.ID)
	} else {
		wsm.logger.Printf("Received response for unknown request %s", msg.ID)
	}
}

func (wsm *WebSocketManager) sendAndWait(message WebSocketMessage, timeout time.Duration) (*WebSocketMessage, error) {
	if message.ID == "" {
		message.ID = generateMessageID()
	}

	// Create the response channel.
	responseChan := make(chan WebSocketMessage, 1)
	pendingReq := &PendingRequest{
		ID:       message.ID,
		Response: responseChan,
		Timeout:  time.Now().Add(timeout),
	}

	wsm.mutex.Lock()
	wsm.pendingRequests[message.ID] = pendingReq
	wsm.mutex.Unlock()

	// Send the message.
	err := wsm.sendToAll(message)
	if err != nil {
		wsm.mutex.Lock()
		delete(wsm.pendingRequests, message.ID)
		wsm.mutex.Unlock()
		return nil, err
	}

	// Wait for a response until the timeout expires.
	select {
	case response := <-responseChan:
		return &response, nil
	case <-time.After(timeout):
		wsm.mutex.Lock()
		delete(wsm.pendingRequests, message.ID)
		wsm.mutex.Unlock()
		return nil, fmt.Errorf("timed out waiting for a response from the extension")
	}
}

// registerBrowserTools registers all browser tools.
func registerBrowserTools(mcpServer *server.MCPServer) {
	// 1. Get page HTML.
	getHtmlTool := mcp.NewTool("browser_get_html",
		mcp.WithDescription("Get the HTML content of the current browser page"),
		mcp.WithNumber("tabId",
			mcp.Description("Browser tab ID (optional)"),
		),
	)
	mcpServer.AddTool(getHtmlTool, mcp.NewTypedToolHandler(browserGetHtmlHandler))

	// 2. Get HTML by selector.
	getHtmlBySelectorTool := mcp.NewTool("browser_get_html_by_selector",
		mcp.WithDescription("Get the HTML of elements matching a CSS selector"),
		mcp.WithString("selector",
			mcp.Required(),
			mcp.Description("CSS selector used to find elements"),
		),
		mcp.WithNumber("tabId",
			mcp.Description("Browser tab ID (optional)"),
		),
	)
	mcpServer.AddTool(getHtmlBySelectorTool, mcp.NewTypedToolHandler(browserGetHtmlBySelectorHandler))

	// 3. Click an element.
	clickElementTool := mcp.NewTool("browser_click_element",
		mcp.WithDescription("Click an element on the page"),
		mcp.WithString("selector",
			mcp.Description("CSS selector of the element to click"),
		),
		mcp.WithNumber("index",
			mcp.Description("Element index when multiple matches are found (default: 0)"),
		),
		mcp.WithObject("coordinates",
			mcp.Description("Click coordinates"),
			mcp.Properties(map[string]any{
				"x": map[string]any{
					"type":        "number",
					"description": "X coordinate",
				},
				"y": map[string]any{
					"type":        "number",
					"description": "Y coordinate",
				},
			}),
		),
		mcp.WithNumber("tabId",
			mcp.Description("Browser tab ID (optional)"),
		),
	)
	mcpServer.AddTool(clickElementTool, mcp.NewTypedToolHandler(browserClickElementHandler))

	// 4. Enter data.
	inputDataTool := mcp.NewTool("browser_input_data",
		mcp.WithDescription("Enter data into an input field on the page"),
		mcp.WithString("selector",
			mcp.Required(),
			mcp.Description("CSS selector of the input field"),
		),
		mcp.WithString("value",
			mcp.Required(),
			mcp.Description("Value to enter"),
		),
		mcp.WithNumber("index",
			mcp.Description("Element index when multiple matches are found (default: 0)"),
		),
		mcp.WithBoolean("clear",
			mcp.Description("Clear the field before entering data (default: true)"),
			mcp.DefaultBool(true),
		),
		mcp.WithNumber("tabId",
			mcp.Description("Browser tab ID (optional)"),
		),
	)
	mcpServer.AddTool(inputDataTool, mcp.NewTypedToolHandler(browserInputDataHandler))

	// 5. Get console logs.
	getConsoleLogTool := mcp.NewTool("browser_get_console_log",
		mcp.WithDescription("Get browser console logs"),
		mcp.WithNumber("tabId",
			mcp.Description("Browser tab ID (optional)"),
		),
	)
	mcpServer.AddTool(getConsoleLogTool, mcp.NewTypedToolHandler(browserGetConsoleLogHandler))

	// 6. Get network logs.
	getNetworkLogTool := mcp.NewTool("browser_get_network_log",
		mcp.WithDescription("Get browser network logs"),
		mcp.WithNumber("tabId",
			mcp.Description("Browser tab ID (optional)"),
		),
	)
	mcpServer.AddTool(getNetworkLogTool, mcp.NewTypedToolHandler(browserGetNetworkLogHandler))

	// 7. Send an arbitrary command.
	sendCommandTool := mcp.NewTool("browser_send_command",
		mcp.WithDescription("Send an arbitrary command to the browser extension"),
		mcp.WithString("command",
			mcp.Required(),
			mcp.Description("Command name"),
		),
		mcp.WithObject("data",
			mcp.Description("Command data"),
		),
		mcp.WithNumber("tabId",
			mcp.Description("Browser tab ID (optional)"),
		),
	)
	mcpServer.AddTool(sendCommandTool, mcp.NewTypedToolHandler(browserSendCommandHandler))

	// 8. Get the list of tabs.
	getTabsTool := mcp.NewTool("browser_get_tabs",
		mcp.WithDescription("Get all open browser tabs and identify the active tab"),
	)
	mcpServer.AddTool(getTabsTool, mcp.NewTypedToolHandler(browserGetTabsHandler))
}

// Typed tool handlers.

func browserGetHtmlHandler(ctx context.Context, request mcp.CallToolRequest, args GetHtmlArgs) (*mcp.CallToolResult, error) {
	params := make(map[string]interface{})
	if args.TabID != nil {
		params["tabId"] = *args.TabID
	}

	message := WebSocketMessage{
		Command: "GET_HTML",
		Params:  params,
		ID:      generateMessageID(),
	}

	// Send the command and wait for a response.
	response, err := wsManager.sendAndWait(message, 10*time.Second)
	if err != nil {
		return createErrorResult("browser_get_html", err.Error()), nil
	}

	return createSuccessResult("browser_get_html", "HTML retrieved successfully", response.Data), nil
}

func browserGetHtmlBySelectorHandler(ctx context.Context, request mcp.CallToolRequest, args GetHtmlBySelectorArgs) (*mcp.CallToolResult, error) {
	params := map[string]interface{}{
		"selector": args.Selector,
	}
	if args.TabID != nil {
		params["tabId"] = *args.TabID
	}

	message := WebSocketMessage{
		Command: "GET_HTML_BY_SELECTOR",
		Params:  params,
		ID:      generateMessageID(),
	}

	// Send the command and wait for a response.
	response, err := wsManager.sendAndWait(message, 10*time.Second)
	if err != nil {
		return createErrorResult("browser_get_html_by_selector", err.Error()), nil
	}

	return createSuccessResult("browser_get_html_by_selector", "HTML retrieved successfully by selector", response.Data), nil
}

func browserClickElementHandler(ctx context.Context, request mcp.CallToolRequest, args ClickElementArgs) (*mcp.CallToolResult, error) {
	params := make(map[string]interface{})

	if args.Selector != nil {
		params["selector"] = *args.Selector
	}
	if args.Index != nil {
		params["index"] = *args.Index
	}
	if args.Coordinates != nil {
		params["coordinates"] = *args.Coordinates
	}
	if args.TabID != nil {
		params["tabId"] = *args.TabID
	}

	// Require either a selector or coordinates.
	if args.Selector == nil && args.Coordinates == nil {
		return createErrorResult("browser_click_element", "either 'selector' or 'coordinates' must be provided"), nil
	}

	message := WebSocketMessage{
		Command: "CLICK_ELEMENT",
		Params:  params,
		ID:      generateMessageID(),
	}

	// Send the command and wait for a response.
	response, err := wsManager.sendAndWait(message, 10*time.Second)
	if err != nil {
		return createErrorResult("browser_click_element", err.Error()), nil
	}

	return createSuccessResult("browser_click_element", "Element clicked successfully", response.Data), nil
}

func browserInputDataHandler(ctx context.Context, request mcp.CallToolRequest, args InputDataArgs) (*mcp.CallToolResult, error) {
	params := map[string]interface{}{
		"selector": args.Selector,
		"value":    args.Value,
	}

	if args.Index != nil {
		params["index"] = *args.Index
	}
	if args.Clear != nil {
		params["clear"] = *args.Clear
	} else {
		params["clear"] = true // Default value.
	}
	if args.TabID != nil {
		params["tabId"] = *args.TabID
	}

	message := WebSocketMessage{
		Command: "INPUT_DATA",
		Params:  params,
		ID:      generateMessageID(),
	}

	// Send the command and wait for a response.
	response, err := wsManager.sendAndWait(message, 10*time.Second)
	if err != nil {
		return createErrorResult("browser_input_data", err.Error()), nil
	}

	return createSuccessResult("browser_input_data", "Data entered successfully", response.Data), nil
}

func browserGetConsoleLogHandler(ctx context.Context, request mcp.CallToolRequest, args GetConsoleLogArgs) (*mcp.CallToolResult, error) {
	params := make(map[string]interface{})
	if args.TabID != nil {
		params["tabId"] = *args.TabID
	}

	message := WebSocketMessage{
		Command: "GET_CONSOLE_LOG",
		Params:  params,
		ID:      generateMessageID(),
	}

	// Send the command and wait for a response.
	response, err := wsManager.sendAndWait(message, 10*time.Second)
	if err != nil {
		return createErrorResult("browser_get_console_log", err.Error()), nil
	}

	return createSuccessResult("browser_get_console_log", "Console logs retrieved successfully", response.Data), nil
}

func browserGetNetworkLogHandler(ctx context.Context, request mcp.CallToolRequest, args GetNetworkLogArgs) (*mcp.CallToolResult, error) {
	params := make(map[string]interface{})
	if args.TabID != nil {
		params["tabId"] = *args.TabID
	}

	message := WebSocketMessage{
		Command: "GET_NETWORK_LOG",
		Params:  params,
		ID:      generateMessageID(),
	}

	// Send the command and wait for a response.
	response, err := wsManager.sendAndWait(message, 10*time.Second)
	if err != nil {
		return createErrorResult("browser_get_network_log", err.Error()), nil
	}

	return createSuccessResult("browser_get_network_log", "Network logs retrieved successfully", response.Data), nil
}

func browserSendCommandHandler(ctx context.Context, request mcp.CallToolRequest, args SendCommandArgs) (*mcp.CallToolResult, error) {
	params := map[string]interface{}{
		"command": args.Command,
	}

	if args.Data != nil {
		params["data"] = args.Data
	}
	if args.TabID != nil {
		params["tabId"] = *args.TabID
	}

	message := WebSocketMessage{
		Command: args.Command,
		Params:  params,
		ID:      generateMessageID(),
	}

	// Send the command and wait for a response.
	response, err := wsManager.sendAndWait(message, 10*time.Second)
	if err != nil {
		return createErrorResult("browser_send_command", err.Error()), nil
	}

	return createSuccessResult("browser_send_command", fmt.Sprintf("Command '%s' completed successfully", args.Command), response.Data), nil
}

func browserGetTabsHandler(ctx context.Context, request mcp.CallToolRequest, args GetTabsArgs) (*mcp.CallToolResult, error) {
	message := WebSocketMessage{
		Command: "GET_TABS",
		Params:  make(map[string]interface{}),
		ID:      generateMessageID(),
	}

	// Send the command and wait for a response.
	response, err := wsManager.sendAndWait(message, 10*time.Second)
	if err != nil {
		return createErrorResult("browser_get_tabs", err.Error()), nil
	}

	return createSuccessResult("browser_get_tabs", "Tab list retrieved successfully", response.Data), nil
}

// Helper functions.

func createSuccessResult(tool, message string, data interface{}) *mcp.CallToolResult {
	result := BrowserResponse{
		Success:   true,
		Data:      data,
		Command:   tool,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	jsonResult, _ := json.Marshal(map[string]interface{}{
		"message": message,
		"result":  result,
	})

	return mcp.NewToolResultText(string(jsonResult))
}

func createErrorResult(tool, errorMsg string) *mcp.CallToolResult {
	result := BrowserResponse{
		Success:   false,
		Error:     errorMsg,
		Command:   tool,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	jsonResult, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(jsonResult))
}

func generateMessageID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}
