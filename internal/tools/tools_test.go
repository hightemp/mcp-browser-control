package tools

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"reflect"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/router"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/selection"
	"github.com/mark3labs/mcp-go/mcp"
)

type toolTestConnection struct {
	id       string
	messages chan protocol.Message
}

func newToolTestConnection(id string) *toolTestConnection {
	return &toolTestConnection{
		id:       id,
		messages: make(chan protocol.Message, 8),
	}
}

func (c *toolTestConnection) ID() string {
	return c.id
}

func (c *toolTestConnection) Send(ctx context.Context, message protocol.Message) error {
	select {
	case c.messages <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *toolTestConnection) Close() error {
	return nil
}

func TestServiceRequiresSelectionForMultipleBrowsers(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	result, err := service.browserGetTabsHandler(
		context.Background(),
		mcp.CallToolRequest{},
		targetedArgs{},
	)
	if err != nil {
		t.Fatalf("browserGetTabsHandler() error = %v", err)
	}
	if !result.IsError {
		t.Fatal("IsError = false, want true")
	}

	response := decodeToolResponse(t, result)
	if response.Error == nil || response.Error.Code != protocol.CodeAmbiguousBrowser {
		t.Fatalf("error = %#v, want %s", response.Error, protocol.CodeAmbiguousBrowser)
	}
}

func TestServiceRoutesThroughSelectedBrowser(t *testing.T) {
	t.Parallel()

	service, connectionA, connectionB := newTestService(t)
	selectResult, err := service.browserSelectHandler(
		context.Background(),
		mcp.CallToolRequest{},
		browserSelectArgs{BrowserID: "browser-a"},
	)
	if err != nil {
		t.Fatalf("browserSelectHandler() error = %v", err)
	}
	if selectResult.IsError {
		t.Fatalf("browserSelectHandler() returned error: %s", toolText(t, selectResult))
	}

	resultChannel := make(chan *mcp.CallToolResult, 1)
	errorChannel := make(chan error, 1)
	go func() {
		result, handlerErr := service.browserGetTabsHandler(
			context.Background(),
			mcp.CallToolRequest{},
			targetedArgs{},
		)
		if handlerErr != nil {
			errorChannel <- handlerErr
			return
		}
		resultChannel <- result
	}()

	request := receiveToolMessage(t, connectionA.messages)
	select {
	case unexpected := <-connectionB.messages:
		t.Fatalf("browser B received unexpected request: %#v", unexpected)
	default:
	}

	responseMessage, err := protocol.NewResponse(
		request.RequestID,
		"browser-a",
		map[string]any{"tabs": []map[string]any{{"id": 7}}},
		nil,
	)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	if !service.router.HandleResponse("browser-a", connectionA.ID(), responseMessage) {
		t.Fatal("HandleResponse() did not accept browser A response")
	}

	select {
	case handlerErr := <-errorChannel:
		t.Fatalf("browserGetTabsHandler() error = %v", handlerErr)
	case result := <-resultChannel:
		if result.IsError {
			t.Fatalf("browserGetTabsHandler() returned error: %s", toolText(t, result))
		}
		response := decodeToolResponse(t, result)
		if response.BrowserID != "browser-a" {
			t.Errorf("browserId = %q, want browser-a", response.BrowserID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool result")
	}
}

func TestDiscoveryHandlers(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	ctx := context.Background()

	list, err := service.browserListHandler(ctx, mcp.CallToolRequest{}, emptyArgs{})
	if err != nil || list.IsError {
		t.Fatalf("browserListHandler() = (%v, %v)", list, err)
	}
	if response := decodeToolResponse(t, list); !response.Success {
		t.Fatalf("browserListHandler() response = %#v", response)
	}

	selected, err := service.browserGetSelectedHandler(ctx, mcp.CallToolRequest{}, emptyArgs{})
	if err != nil || selected.IsError {
		t.Fatalf("browserGetSelectedHandler() = (%v, %v)", selected, err)
	}

	missingSelection, err := service.browserSelectHandler(
		ctx,
		mcp.CallToolRequest{},
		browserSelectArgs{BrowserID: "missing"},
	)
	if err != nil || !missingSelection.IsError {
		t.Fatalf("select missing browser = (%v, %v), want tool error", missingSelection, err)
	}

	selected, err = service.browserSelectHandler(
		ctx,
		mcp.CallToolRequest{},
		browserSelectArgs{BrowserID: "browser-a"},
	)
	if err != nil || selected.IsError {
		t.Fatalf("browserSelectHandler() = (%v, %v)", selected, err)
	}

	getSelected, err := service.browserGetSelectedHandler(ctx, mcp.CallToolRequest{}, emptyArgs{})
	if err != nil || getSelected.IsError {
		t.Fatalf("browserGetSelectedHandler() = (%v, %v)", getSelected, err)
	}
	if response := decodeToolResponse(t, getSelected); response.BrowserID != "browser-a" {
		t.Fatalf("selected browser = %q, want browser-a", response.BrowserID)
	}

	gotBrowser, err := service.browserGetHandler(
		ctx,
		mcp.CallToolRequest{},
		browserIDArgs{BrowserID: "browser-b"},
	)
	if err != nil || gotBrowser.IsError {
		t.Fatalf("browserGetHandler() = (%v, %v)", gotBrowser, err)
	}
	if response := decodeToolResponse(t, gotBrowser); response.BrowserID != "browser-b" {
		t.Fatalf("browserId = %q, want browser-b", response.BrowserID)
	}

	renamed, err := service.browserRenameHandler(
		ctx,
		mcp.CallToolRequest{},
		browserRenameArgs{BrowserID: "browser-a", DisplayName: "Work Chrome"},
	)
	if err != nil || renamed.IsError {
		t.Fatalf("browserRenameHandler() = (%v, %v)", renamed, err)
	}

	capabilities, err := service.browserGetCapabilitiesHandler(
		ctx,
		mcp.CallToolRequest{},
		browserIDArgs{BrowserID: "browser-a"},
	)
	if err != nil || capabilities.IsError {
		t.Fatalf("browserGetCapabilitiesHandler() = (%v, %v)", capabilities, err)
	}
}

func TestDiscoveryIncludesDisconnectedBrowsers(t *testing.T) {
	t.Parallel()

	service, _, connectionB := newTestService(t)
	if !service.registry.Disconnect("browser-b", connectionB.ID(), "browser closed") {
		t.Fatal("Disconnect() = false")
	}
	result, err := service.browserListHandler(context.Background(), mcp.CallToolRequest{}, emptyArgs{})
	if err != nil || result.IsError {
		t.Fatalf("browserListHandler() = (%v, %v)", result, err)
	}
	response := decodeToolResponse(t, result)
	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type = %T", response.Data)
	}
	if data["connectedCount"] != float64(1) {
		t.Errorf("connectedCount = %#v, want 1", data["connectedCount"])
	}
	browsers, ok := data["browsers"].([]any)
	if !ok || len(browsers) != 2 {
		t.Fatalf("browsers = %#v, want two retained entries", data["browsers"])
	}

	get, err := service.browserGetHandler(
		context.Background(),
		mcp.CallToolRequest{},
		browserIDArgs{BrowserID: "browser-b"},
	)
	if err != nil || get.IsError {
		t.Fatalf("browserGetHandler(disconnected) = (%v, %v)", get, err)
	}
	selected, err := service.browserSelectHandler(
		context.Background(),
		mcp.CallToolRequest{},
		browserSelectArgs{BrowserID: "browser-b"},
	)
	if err != nil || !selected.IsError {
		t.Fatalf("browserSelectHandler(disconnected) = (%v, %v), want tool error", selected, err)
	}
}

func TestCommandHandlersBuildExpectedRequests(t *testing.T) {
	service, connectionA, _ := newTestService(t)
	tabID := 17
	index := 2
	selector := "#submit"
	clearField := false
	timeoutMS := 500

	tests := []struct {
		name        string
		wantCommand string
		wantTarget  *protocol.Target
		wantParams  map[string]any
		call        func(context.Context) (*mcp.CallToolResult, error)
	}{
		{
			name:        "ping",
			wantCommand: protocol.CommandBrowserPing,
			wantParams:  map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserPingHandler(ctx, mcp.CallToolRequest{}, targetedArgs{BrowserID: "browser-a"})
			},
		},
		{
			name:        "tabs",
			wantCommand: protocol.CommandTabsList,
			wantParams:  map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGetTabsHandler(ctx, mcp.CallToolRequest{}, targetedArgs{BrowserID: "browser-a"})
			},
		},
		{
			name:        "html",
			wantCommand: protocol.CommandPageGetHTML,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams:  map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGetHTMLHandler(ctx, mcp.CallToolRequest{}, targetedArgs{BrowserID: "browser-a", TabID: &tabID})
			},
		},
		{
			name:        "selector html",
			wantCommand: protocol.CommandPageGetHTMLBySelector,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams:  map[string]any{"selector": selector},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGetHTMLBySelectorHandler(ctx, mcp.CallToolRequest{}, getHTMLBySelectorArgs{BrowserID: "browser-a", TabID: &tabID, Selector: selector})
			},
		},
		{
			name:        "click",
			wantCommand: protocol.CommandPageClick,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams:  map[string]any{"selector": selector, "index": float64(index)},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserClickHandler(ctx, mcp.CallToolRequest{}, clickArgs{BrowserID: "browser-a", TabID: &tabID, Selector: &selector, Index: &index})
			},
		},
		{
			name:        "input",
			wantCommand: protocol.CommandPageFill,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams:  map[string]any{"selector": "#name", "value": "Ada", "index": float64(index), "clear": false},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserInputHandler(ctx, mcp.CallToolRequest{}, inputArgs{BrowserID: "browser-a", TabID: &tabID, Selector: "#name", Value: "Ada", Index: &index, Clear: &clearField, TimeoutMS: &timeoutMS})
			},
		},
		{
			name:        "console",
			wantCommand: protocol.CommandConsoleRead,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams:  map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGetConsoleHandler(ctx, mcp.CallToolRequest{}, targetedArgs{BrowserID: "browser-a", TabID: &tabID})
			},
		},
		{
			name:        "network",
			wantCommand: protocol.CommandNetworkRead,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams:  map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGetNetworkHandler(ctx, mcp.CallToolRequest{}, targetedArgs{BrowserID: "browser-a", TabID: &tabID})
			},
		},
		{
			name:        "custom",
			wantCommand: "page.custom",
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams:  map[string]any{"key": "value"},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserSendCommandHandler(ctx, mcp.CallToolRequest{}, sendCommandArgs{BrowserID: "browser-a", TabID: &tabID, Command: "page.custom", Data: map[string]any{"key": "value"}})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resultChannel := make(chan *mcp.CallToolResult, 1)
			errorChannel := make(chan error, 1)
			go func() {
				result, err := test.call(context.Background())
				if err != nil {
					errorChannel <- err
					return
				}
				resultChannel <- result
			}()

			request := receiveToolMessage(t, connectionA.messages)
			if request.Command != test.wantCommand {
				t.Errorf("command = %q, want %q", request.Command, test.wantCommand)
			}
			if !reflect.DeepEqual(request.Target, test.wantTarget) {
				t.Errorf("target = %#v, want %#v", request.Target, test.wantTarget)
			}
			var params map[string]any
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatalf("unmarshal params: %v", err)
			}
			if !reflect.DeepEqual(params, test.wantParams) {
				t.Errorf("params = %#v, want %#v", params, test.wantParams)
			}

			response, err := protocol.NewResponse(
				request.RequestID,
				"browser-a",
				map[string]any{"ok": true},
				nil,
			)
			if err != nil {
				t.Fatalf("NewResponse() error = %v", err)
			}
			if !service.router.HandleResponse("browser-a", connectionA.ID(), response) {
				t.Fatal("HandleResponse() = false")
			}

			select {
			case err := <-errorChannel:
				t.Fatalf("handler error = %v", err)
			case result := <-resultChannel:
				if result.IsError {
					t.Fatalf("handler returned tool error: %s", toolText(t, result))
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for handler result")
			}
		})
	}
}

func TestCommandHandlerValidation(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)

	missingTarget, err := service.browserClickHandler(
		context.Background(),
		mcp.CallToolRequest{},
		clickArgs{BrowserID: "browser-a"},
	)
	if err != nil || !missingTarget.IsError {
		t.Fatalf("click without selector or coordinates = (%v, %v), want tool error", missingTarget, err)
	}

	invalidTimeout := 0
	badTimeout, err := service.browserPingHandler(
		context.Background(),
		mcp.CallToolRequest{},
		targetedArgs{BrowserID: "browser-a", TimeoutMS: &invalidTimeout},
	)
	if err != nil || !badTimeout.IsError {
		t.Fatalf("ping with invalid timeout = (%v, %v), want tool error", badTimeout, err)
	}

	missingBrowser, err := service.browserGetHandler(
		context.Background(),
		mcp.CallToolRequest{},
		browserIDArgs{BrowserID: "missing"},
	)
	if err != nil || !missingBrowser.IsError {
		t.Fatalf("get missing browser = (%v, %v), want tool error", missingBrowser, err)
	}
}

func newTestService(
	t *testing.T,
) (*Service, *toolTestConnection, *toolTestConnection) {
	t.Helper()

	browserRegistry := registry.New()
	connectionA := newToolTestConnection("connection-a")
	connectionB := newToolTestConnection("connection-b")
	for browserID, connection := range map[string]*toolTestConnection{
		"browser-a": connectionA,
		"browser-b": connectionB,
	} {
		if _, err := browserRegistry.Register(
			registry.Registration{BrowserID: browserID, DisplayName: browserID},
			connection,
		); err != nil {
			t.Fatalf("Register(%s) error = %v", browserID, err)
		}
	}

	requestRouter := router.New(
		browserRegistry,
		router.WithDefaultTimeout(time.Second),
		router.WithIDGenerator(func() string { return "request-1" }),
		router.WithLogger(log.New(io.Discard, "", 0)),
	)
	return NewService(browserRegistry, requestRouter, selection.NewStore()), connectionA, connectionB
}

func receiveToolMessage(t *testing.T, messages <-chan protocol.Message) protocol.Message {
	t.Helper()
	select {
	case message := <-messages:
		return message
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for browser command")
		return protocol.Message{}
	}
}

func decodeToolResponse(t *testing.T, result *mcp.CallToolResult) toolResponse {
	t.Helper()
	var response toolResponse
	if err := json.Unmarshal([]byte(toolText(t, result)), &response); err != nil {
		t.Fatalf("unmarshal tool response: %v", err)
	}
	return response
}

func toolText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("content length = %d, want 1", len(result.Content))
	}
	content, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("content type = %T, want text", result.Content[0])
	}
	return content.Text
}
