package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	gorilla "github.com/gorilla/websocket"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/mcpsession"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/netguard"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/router"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/security/pairing"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/selection"
	browsertools "github.com/hightemp/go_mcp_browser_ext_tool/internal/tools"
	websockettransport "github.com/hightemp/go_mcp_browser_ext_tool/internal/transport/websocket"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestTwoMCPSessionsRouteToDifferentBrowsers(t *testing.T) {
	browserRegistry := registry.New()
	requestRouter := router.New(
		browserRegistry,
		router.WithDefaultTimeout(2*time.Second),
		router.WithLogger(log.New(io.Discard, "", 0)),
	)
	selections := selection.NewStore()

	hooks := &server.Hooks{}
	hooks.AddOnUnregisterSession(func(_ context.Context, session server.ClientSession) {
		selections.Delete(session.SessionID())
	})
	mcpServer := server.NewMCPServer(
		"integration-test",
		"0.1.0",
		server.WithHooks(hooks),
		server.WithRecovery(),
	)
	browsertools.NewService(browserRegistry, requestRouter, selections).Register(mcpServer)

	pairingManager := mustPairingManager(t)
	websocketHandler := websockettransport.NewServer(
		browserRegistry,
		requestRouter,
		websockettransport.WithLogger(log.New(io.Discard, "", 0)),
		websockettransport.WithAuthenticator(pairingManager),
	)
	websocketMux := http.NewServeMux()
	websocketMux.Handle(websockettransport.DefaultPath, websocketHandler)
	websocketServer := httptest.NewServer(websocketMux)
	t.Cleanup(websocketServer.Close)

	browserA := connectFakeBrowser(t, websocketServer.URL, "Browser A", currentPairingCode(t, pairingManager))
	browserB := connectFakeBrowser(t, websocketServer.URL, "Browser B", currentPairingCode(t, pairingManager))
	t.Cleanup(func() {
		if err := browserA.socket.Close(); err != nil {
			t.Logf("close browser A: %v", err)
		}
		if err := browserB.socket.Close(); err != nil {
			t.Logf("close browser B: %v", err)
		}
	})

	sessionManager, err := mcpsession.NewManager()
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	streamable := server.NewStreamableHTTPServer(
		mcpServer,
		server.WithSessionIdManager(sessionManager),
	)
	mcpMux := http.NewServeMux()
	mcpMux.Handle("/mcp", netguard.LocalOnly(streamable))
	httpServer := httptest.NewServer(mcpMux)
	t.Cleanup(httpServer.Close)

	clientA := newMCPClient(t, httpServer.URL+"/mcp", "client-a")
	clientB := newMCPClient(t, httpServer.URL+"/mcp", "client-b")

	listResult := callTool(t, clientA, "browser_list", map[string]any{})
	listPayload := decodeToolPayload(t, listResult)
	listData, ok := listPayload["data"].(map[string]any)
	if !ok {
		t.Fatalf("browser_list data type = %T", listPayload["data"])
	}
	if count := int(listData["connectedCount"].(float64)); count != 2 {
		t.Fatalf("connectedCount = %d, want 2", count)
	}

	ambiguous := callTool(t, clientA, "browser_get_tabs", map[string]any{})
	if !ambiguous.IsError {
		t.Fatal("browser_get_tabs without selection did not return an error")
	}
	ambiguousPayload := decodeToolPayload(t, ambiguous)
	errorPayload, ok := ambiguousPayload["error"].(map[string]any)
	if !ok || errorPayload["code"] != string(protocol.CodeAmbiguousBrowser) {
		t.Fatalf("ambiguous error = %#v", ambiguousPayload["error"])
	}

	selectBrowser(t, clientA, browserA.id)
	selectBrowser(t, clientB, browserB.id)
	selectTab(t, clientA, browserA.id, 101)
	selectTab(t, clientB, browserB.id, 202)

	responseErrors := make(chan error, 2)
	var responderWG sync.WaitGroup
	for browser, tabID := range map[*fakeBrowser]int{browserA: 101, browserB: 202} {
		browser := browser
		tabID := tabID
		responderWG.Add(1)
		go func() {
			defer responderWG.Done()
			responseErrors <- browser.respondToOneRequest(protocol.CommandPageGetHTML, tabID)
		}()
	}

	type toolCallResult struct {
		owner  string
		result *mcp.CallToolResult
		err    error
	}
	results := make(chan toolCallResult, 2)
	var callerWG sync.WaitGroup
	for owner, mcpClient := range map[string]*client.Client{
		"A": clientA,
		"B": clientB,
	} {
		owner := owner
		mcpClient := mcpClient
		callerWG.Add(1)
		go func() {
			defer callerWG.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			request := mcp.CallToolRequest{}
			request.Params.Name = "browser_get_html"
			request.Params.Arguments = map[string]any{}
			result, callErr := mcpClient.CallTool(ctx, request)
			results <- toolCallResult{
				owner:  owner,
				result: result,
				err:    callErr,
			}
		}()
	}

	responderWG.Wait()
	close(responseErrors)
	for responseErr := range responseErrors {
		if responseErr != nil {
			t.Fatalf("fake browser responder: %v", responseErr)
		}
	}
	callerWG.Wait()
	close(results)

	for result := range results {
		if result.err != nil {
			t.Fatalf("client %s CallTool() error = %v", result.owner, result.err)
		}
		if result.result.IsError {
			t.Fatalf("client %s result is error: %s", result.owner, toolResultText(t, result.result))
		}
		payload := decodeToolPayload(t, result.result)
		data, ok := payload["data"].(map[string]any)
		if !ok {
			t.Fatalf("client %s data type = %T", result.owner, payload["data"])
		}
		if data["owner"] != result.owner {
			t.Errorf("client %s received owner %v", result.owner, data["owner"])
		}
	}

	timeoutObservation := make(chan error, 1)
	go func() {
		timeoutObservation <- browserA.expectRequestTimeout(protocol.CommandPageGetHTML, 101)
	}()
	timedOut := callTool(t, clientA, "browser_get_html", map[string]any{"timeoutMs": 25})
	if !timedOut.IsError {
		t.Fatal("browser_get_html timeout result is not an error")
	}
	timeoutPayload := decodeToolPayload(t, timedOut)
	timeoutError, ok := timeoutPayload["error"].(map[string]any)
	if !ok || timeoutError["code"] != string(protocol.CodeTimeout) {
		t.Fatalf("timeout error = %#v", timeoutPayload["error"])
	}
	if err := <-timeoutObservation; err != nil {
		t.Fatalf("fake browser timeout observer: %v", err)
	}

	if err := browserA.socket.Close(); err != nil {
		t.Fatalf("close browser A before reconnect: %v", err)
	}
	waitForRegistryCount(t, browserRegistry, 1)
	browserA = reconnectFakeBrowser(t, websocketServer.URL, browserA)
	waitForRegistryCount(t, browserRegistry, 2)

	reconnectResponse := make(chan error, 1)
	go func() {
		reconnectResponse <- browserA.respondToOneRequest(protocol.CommandPageGetHTML, 101)
	}()
	reconnectedResult := callTool(t, clientA, "browser_get_html", map[string]any{})
	if err := <-reconnectResponse; err != nil {
		t.Fatalf("reconnected fake browser responder: %v", err)
	}
	if reconnectedResult.IsError {
		t.Fatalf("reconnected browser result is error: %s", toolResultText(t, reconnectedResult))
	}
	reconnectedPayload := decodeToolPayload(t, reconnectedResult)
	reconnectedData, ok := reconnectedPayload["data"].(map[string]any)
	if !ok || reconnectedData["owner"] != "A" {
		t.Fatalf("reconnected browser data = %#v", reconnectedPayload["data"])
	}
}

type fakeBrowser struct {
	id          string
	displayName string
	credential  string
	socket      *gorilla.Conn
}

func connectFakeBrowser(t *testing.T, serverURL, displayName, pairingCode string) *fakeBrowser {
	t.Helper()
	return openFakeBrowser(
		t,
		serverURL,
		uuid.NewString(),
		displayName,
		protocol.HelloParams{PairingCode: pairingCode},
	)
}

func reconnectFakeBrowser(t *testing.T, serverURL string, previous *fakeBrowser) *fakeBrowser {
	t.Helper()
	if previous.credential == "" {
		t.Fatal("fake browser credential is empty")
	}
	return openFakeBrowser(
		t,
		serverURL,
		previous.id,
		previous.displayName,
		protocol.HelloParams{Credential: previous.credential},
	)
}

func openFakeBrowser(
	t *testing.T,
	serverURL string,
	browserID string,
	displayName string,
	helloParams protocol.HelloParams,
) *fakeBrowser {
	t.Helper()

	headers := http.Header{"Origin": []string{"chrome-extension://integration-test"}}
	socket, response, err := gorilla.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(serverURL, "http")+websockettransport.DefaultPath,
		headers,
	)
	if err != nil {
		if response != nil {
			t.Fatalf("dial fake browser: %v (%s)", err, response.Status)
		}
		t.Fatalf("dial fake browser: %v", err)
	}

	browser := &fakeBrowser{
		id:          browserID,
		displayName: displayName,
		credential:  helloParams.Credential,
		socket:      socket,
	}
	helloParams.DisplayName = displayName
	helloParams.ExtensionVersion = "0.1.0-test"
	helloParams.Browser = protocol.BrowserMetadata{Name: "Fake Chromium", Version: "116"}
	helloParams.Capabilities = []string{
		protocol.CommandBrowserPing,
		protocol.CommandPageGetHTML,
		protocol.CommandTabsList,
	}
	hello := protocol.NewMessage(protocol.TypeHello)
	hello.BrowserID = browser.id
	hello.Params, err = json.Marshal(helloParams)
	if err != nil {
		t.Fatalf("marshal fake browser hello: %v", err)
	}
	if err := socket.WriteJSON(hello); err != nil {
		t.Fatalf("write fake browser hello: %v", err)
	}

	var welcome protocol.Message
	if err := socket.ReadJSON(&welcome); err != nil {
		t.Fatalf("read fake browser welcome: %v", err)
	}
	if welcome.Type != protocol.TypeWelcome || welcome.BrowserID != browser.id {
		t.Fatalf("invalid fake browser welcome: %#v", welcome)
	}
	var welcomeResult protocol.WelcomeResult
	if err := json.Unmarshal(welcome.Result, &welcomeResult); err != nil {
		t.Fatalf("decode fake browser welcome: %v", err)
	}
	if welcomeResult.BrowserID != browser.id || welcomeResult.ConnectionID == "" {
		t.Fatalf("invalid fake browser welcome result: %#v", welcomeResult)
	}
	if welcomeResult.Credential != "" {
		browser.credential = welcomeResult.Credential
	}
	return browser
}

func currentPairingCode(t *testing.T, manager *pairing.Manager) string {
	t.Helper()
	code, _, err := manager.CurrentCode()
	if err != nil {
		t.Fatalf("CurrentCode() error = %v", err)
	}
	return code
}

func mustPairingManager(t *testing.T) *pairing.Manager {
	t.Helper()
	manager, err := pairing.NewManager()
	if err != nil {
		t.Fatalf("pairing.NewManager() error = %v", err)
	}
	return manager
}

func (b *fakeBrowser) respondToOneRequest(command string, tabID int) error {
	if err := b.socket.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return fmt.Errorf("set read deadline: %w", err)
	}
	for {
		var request protocol.Message
		if err := b.socket.ReadJSON(&request); err != nil {
			return fmt.Errorf("read request: %w", err)
		}
		if request.Type == protocol.TypePing {
			pong := protocol.NewMessage(protocol.TypePong)
			pong.BrowserID = b.id
			if err := b.socket.WriteJSON(pong); err != nil {
				return fmt.Errorf("write pong: %w", err)
			}
			continue
		}
		if request.Type != protocol.TypeRequest || request.Command != command {
			return fmt.Errorf("unexpected request: %#v", request)
		}
		if request.BrowserID != b.id {
			return fmt.Errorf("request targeted browser %s, want %s", request.BrowserID, b.id)
		}
		if request.Target == nil || request.Target.TabID == nil || *request.Target.TabID != tabID {
			return fmt.Errorf("request target = %#v, want tab %d", request.Target, tabID)
		}

		owner := strings.TrimPrefix(b.displayName, "Browser ")
		response, err := protocol.NewResponse(
			request.RequestID,
			b.id,
			map[string]any{"owner": owner, "tabs": []any{}},
			nil,
		)
		if err != nil {
			return fmt.Errorf("create response: %w", err)
		}
		if err := b.socket.WriteJSON(response); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
		return nil
	}
}

func (b *fakeBrowser) expectRequestTimeout(command string, tabID int) error {
	if err := b.socket.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return fmt.Errorf("set timeout observer deadline: %w", err)
	}
	defer func() { _ = b.socket.SetReadDeadline(time.Time{}) }()
	requestID := ""
	for {
		var message protocol.Message
		if err := b.socket.ReadJSON(&message); err != nil {
			return fmt.Errorf("read timeout sequence: %w", err)
		}
		if message.Type == protocol.TypePing {
			pong := protocol.NewMessage(protocol.TypePong)
			pong.BrowserID = b.id
			if err := b.socket.WriteJSON(pong); err != nil {
				return fmt.Errorf("write timeout sequence pong: %w", err)
			}
			continue
		}
		if requestID == "" {
			if message.Type != protocol.TypeRequest || message.Command != command {
				return fmt.Errorf("unexpected timeout request: %#v", message)
			}
			if message.BrowserID != b.id || message.Target == nil ||
				message.Target.TabID == nil || *message.Target.TabID != tabID {
				return fmt.Errorf("timeout request target = %#v, want browser %s tab %d", message.Target, b.id, tabID)
			}
			requestID = message.RequestID
			continue
		}
		if message.Type != protocol.TypeCancel || message.RequestID != requestID ||
			message.BrowserID != b.id {
			return fmt.Errorf("timeout cancellation = %#v, want request %s", message, requestID)
		}
		return nil
	}
}

func waitForRegistryCount(t *testing.T, browserRegistry *registry.Registry, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for browserRegistry.Count() != want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := browserRegistry.Count(); got != want {
		t.Fatalf("registry count = %d, want %d", got, want)
	}
}

func newMCPClient(t *testing.T, endpoint, name string) *client.Client {
	t.Helper()

	mcpClient, err := client.NewStreamableHttpClient(endpoint)
	if err != nil {
		t.Fatalf("NewStreamableHttpClient() error = %v", err)
	}
	// mcp-go v0.32.0 sends session DELETE asynchronously from Close, after
	// Close has returned. The httptest server owns the session lifecycle here,
	// so deliberately leave this short-lived client to that server cleanup.
	if err := mcpClient.Start(context.Background()); err != nil {
		t.Fatalf("Start(%s) error = %v", name, err)
	}
	initialize := mcp.InitializeRequest{}
	initialize.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initialize.Params.ClientInfo = mcp.Implementation{Name: name, Version: "0.1.0"}
	if _, err := mcpClient.Initialize(context.Background(), initialize); err != nil {
		t.Fatalf("Initialize(%s) error = %v", name, err)
	}
	return mcpClient
}

func selectBrowser(t *testing.T, mcpClient *client.Client, browserID string) {
	t.Helper()
	result := callTool(t, mcpClient, "browser_select", map[string]any{"browserId": browserID})
	if result.IsError {
		t.Fatalf("browser_select(%s) error: %s", browserID, toolResultText(t, result))
	}
}

func selectTab(t *testing.T, mcpClient *client.Client, browserID string, tabID int) {
	t.Helper()
	result := callTool(t, mcpClient, "browser_select_tab", map[string]any{
		"browserId": browserID,
		"tabId":     tabID,
	})
	if result.IsError {
		t.Fatalf("browser_select_tab(%s, %d) error: %s", browserID, tabID, toolResultText(t, result))
	}
}

func callTool(
	t *testing.T,
	mcpClient *client.Client,
	name string,
	arguments map[string]any,
) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	request := mcp.CallToolRequest{}
	request.Params.Name = name
	request.Params.Arguments = arguments
	result, err := mcpClient.CallTool(ctx, request)
	if err != nil {
		t.Fatalf("CallTool(%s) error = %v", name, err)
	}
	return result
}

func decodeToolPayload(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(toolResultText(t, result)), &payload); err != nil {
		t.Fatalf("unmarshal tool payload: %v", err)
	}
	return payload
}

func toolResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("tool content length = %d, want 1", len(result.Content))
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("tool content type = %T, want text", result.Content[0])
	}
	return text.Text
}
