package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestTabCommandHandlersBuildExpectedRequests(t *testing.T) {
	service, connection, _ := newTestService(t)
	tabID := 23
	windowID := 4
	index := -1
	active := false
	pinned := true
	muted := false
	bypassCache := true

	tests := []struct {
		name        string
		wantCommand string
		wantTarget  *protocol.Target
		wantParams  map[string]any
		call        func(context.Context) (*mcp.CallToolResult, error)
	}{
		{
			name: "get", wantCommand: protocol.CommandTabsGet,
			wantTarget: tabTarget("browser-a", &tabID), wantParams: map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGetTabHandler(ctx, mcp.CallToolRequest{}, targetedArgs{BrowserID: "browser-a", TabID: &tabID})
			},
		},
		{
			name: "create", wantCommand: protocol.CommandTabsCreate,
			wantParams: map[string]any{
				"windowId": float64(windowID), "url": "https://example.com", "index": float64(2),
				"active": false, "pinned": true,
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				createIndex := 2
				return service.browserCreateTabHandler(ctx, mcp.CallToolRequest{}, tabCreateArgs{
					BrowserID: "browser-a", WindowID: &windowID, URL: "https://example.com",
					Index: &createIndex, Active: &active, Pinned: &pinned,
				})
			},
		},
		{
			name: "activate", wantCommand: protocol.CommandTabsActivate,
			wantTarget: tabTarget("browser-a", &tabID), wantParams: map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserActivateTabHandler(ctx, mcp.CallToolRequest{}, targetedArgs{BrowserID: "browser-a", TabID: &tabID})
			},
		},
		{
			name: "navigate", wantCommand: protocol.CommandTabsNavigate,
			wantTarget: tabTarget("browser-a", &tabID), wantParams: map[string]any{"url": "https://example.org"},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserNavigateTabHandler(ctx, mcp.CallToolRequest{}, tabNavigateArgs{BrowserID: "browser-a", TabID: &tabID, URL: "https://example.org"})
			},
		},
		{
			name: "reload", wantCommand: protocol.CommandTabsReload,
			wantTarget: tabTarget("browser-a", &tabID), wantParams: map[string]any{"bypassCache": true},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserReloadTabHandler(ctx, mcp.CallToolRequest{}, tabReloadArgs{BrowserID: "browser-a", TabID: &tabID, BypassCache: &bypassCache})
			},
		},
		{
			name: "stop", wantCommand: protocol.CommandTabsStop,
			wantTarget: tabTarget("browser-a", &tabID), wantParams: map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserStopTabHandler(ctx, mcp.CallToolRequest{}, targetedArgs{BrowserID: "browser-a", TabID: &tabID})
			},
		},
		{
			name: "back", wantCommand: protocol.CommandTabsBack,
			wantTarget: tabTarget("browser-a", &tabID), wantParams: map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGoBackHandler(ctx, mcp.CallToolRequest{}, targetedArgs{BrowserID: "browser-a", TabID: &tabID})
			},
		},
		{
			name: "forward", wantCommand: protocol.CommandTabsForward,
			wantTarget: tabTarget("browser-a", &tabID), wantParams: map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGoForwardHandler(ctx, mcp.CallToolRequest{}, targetedArgs{BrowserID: "browser-a", TabID: &tabID})
			},
		},
		{
			name: "move", wantCommand: protocol.CommandTabsMove,
			wantTarget: tabTarget("browser-a", &tabID),
			wantParams: map[string]any{"windowId": float64(windowID), "index": float64(index)},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserMoveTabHandler(ctx, mcp.CallToolRequest{}, tabMoveArgs{BrowserID: "browser-a", TabID: &tabID, WindowID: &windowID, Index: &index})
			},
		},
		{
			name: "duplicate", wantCommand: protocol.CommandTabsDuplicate,
			wantTarget: tabTarget("browser-a", &tabID), wantParams: map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserDuplicateTabHandler(ctx, mcp.CallToolRequest{}, targetedArgs{BrowserID: "browser-a", TabID: &tabID})
			},
		},
		{
			name: "close", wantCommand: protocol.CommandTabsClose,
			wantTarget: tabTarget("browser-a", &tabID), wantParams: map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserCloseTabHandler(ctx, mcp.CallToolRequest{}, targetedArgs{BrowserID: "browser-a", TabID: &tabID})
			},
		},
		{
			name: "pin", wantCommand: protocol.CommandTabsPin,
			wantTarget: tabTarget("browser-a", &tabID), wantParams: map[string]any{"pinned": true},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserPinTabHandler(ctx, mcp.CallToolRequest{}, tabBooleanArgs{BrowserID: "browser-a", TabID: &tabID, Pinned: &pinned})
			},
		},
		{
			name: "mute", wantCommand: protocol.CommandTabsMute,
			wantTarget: tabTarget("browser-a", &tabID), wantParams: map[string]any{"muted": false},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserMuteTabHandler(ctx, mcp.CallToolRequest{}, tabBooleanArgs{BrowserID: "browser-a", TabID: &tabID, Muted: &muted})
			},
		},
		{
			name: "get zoom", wantCommand: protocol.CommandTabsGetZoom,
			wantTarget: tabTarget("browser-a", &tabID), wantParams: map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGetTabZoomHandler(ctx, mcp.CallToolRequest{}, targetedArgs{BrowserID: "browser-a", TabID: &tabID})
			},
		},
		{
			name: "set zoom", wantCommand: protocol.CommandTabsSetZoom,
			wantTarget: tabTarget("browser-a", &tabID), wantParams: map[string]any{"factor": 1.25},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserSetTabZoomHandler(ctx, mcp.CallToolRequest{}, tabZoomArgs{BrowserID: "browser-a", TabID: &tabID, Factor: 1.25})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resultChannel := make(chan *mcp.CallToolResult, 1)
			go func() {
				result, _ := test.call(context.Background())
				resultChannel <- result
			}()

			request := receiveToolMessage(t, connection.messages)
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

			response, err := protocol.NewResponse(request.RequestID, "browser-a", map[string]bool{"ok": true}, nil)
			if err != nil {
				t.Fatalf("NewResponse() error = %v", err)
			}
			if !service.router.HandleResponse("browser-a", connection.ID(), response) {
				t.Fatal("HandleResponse() = false")
			}
			select {
			case result := <-resultChannel:
				if result == nil || result.IsError {
					t.Fatalf("handler result = %#v", result)
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for handler result")
			}
		})
	}
}

func TestTabCommandHandlerValidation(t *testing.T) {
	t.Parallel()
	service, _, _ := newTestService(t)
	invalidIndex := -2

	tests := []func() (*mcp.CallToolResult, error){
		func() (*mcp.CallToolResult, error) {
			return service.browserNavigateTabHandler(context.Background(), mcp.CallToolRequest{}, tabNavigateArgs{BrowserID: "browser-a", URL: " "})
		},
		func() (*mcp.CallToolResult, error) {
			return service.browserMoveTabHandler(context.Background(), mcp.CallToolRequest{}, tabMoveArgs{BrowserID: "browser-a", Index: &invalidIndex})
		},
		func() (*mcp.CallToolResult, error) {
			return service.browserPinTabHandler(context.Background(), mcp.CallToolRequest{}, tabBooleanArgs{BrowserID: "browser-a"})
		},
		func() (*mcp.CallToolResult, error) {
			return service.browserSetTabZoomHandler(context.Background(), mcp.CallToolRequest{}, tabZoomArgs{BrowserID: "browser-a", Factor: 6})
		},
	}

	for index, call := range tests {
		result, err := call()
		if err != nil || result == nil || !result.IsError {
			t.Fatalf("validation case %d = (%#v, %v), want tool error", index, result, err)
		}
		response := decodeToolResponse(t, result)
		if response.Error == nil || response.Error.Code != protocol.CodeInvalidMessage {
			t.Fatalf("validation case %d error = %#v", index, response.Error)
		}
	}
}

func tabTarget(browserID string, tabID *int) *protocol.Target {
	return &protocol.Target{BrowserID: browserID, TabID: tabID}
}
