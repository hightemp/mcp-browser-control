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

func TestConsoleHandlersRouteLifecycleAndFilters(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	tabID := 7
	frameID := 2
	bufferSize := 250
	captureConsole := true
	captureErrors := false
	limit := 25
	tests := []struct {
		name        string
		command     string
		wantParams  map[string]any
		callHandler func() (*mcp.CallToolResult, error)
	}{
		{
			name:       "start",
			command:    protocol.CommandConsoleStart,
			wantParams: map[string]any{"bufferSize": float64(250), "captureConsole": true, "captureErrors": false},
			callHandler: func() (*mcp.CallToolResult, error) {
				return service.browserStartConsoleHandler(context.Background(), mcp.CallToolRequest{}, consoleStartArgs{
					BrowserID: "browser-a", TabID: &tabID, FrameID: &frameID,
					BufferSize: &bufferSize, CaptureConsole: &captureConsole, CaptureErrors: &captureErrors,
				})
			},
		},
		{
			name:    "read",
			command: protocol.CommandConsoleRead,
			wantParams: map[string]any{
				"levels": []any{"warn", "error"}, "kinds": []any{"console", "exception"},
				"cursor": "12", "limit": float64(25), "since": "2026-08-24T10:00:00Z",
			},
			callHandler: func() (*mcp.CallToolResult, error) {
				return service.browserGetConsoleHandler(context.Background(), mcp.CallToolRequest{}, consoleReadArgs{
					consoleTargetArgs: consoleTargetArgs{BrowserID: "browser-a", TabID: &tabID, FrameID: &frameID},
					Levels:            []string{"warn", "error"}, Kinds: []string{"console", "exception"},
					Cursor: "12", Limit: &limit, Since: "2026-08-24T10:00:00Z",
				})
			},
		},
		{
			name: "clear", command: protocol.CommandConsoleClear, wantParams: map[string]any{},
			callHandler: func() (*mcp.CallToolResult, error) {
				return service.send(
					context.Background(), "browser-a", protocol.CommandConsoleClear,
					pageTarget(&tabID, &frameID, ""), map[string]any{}, nil,
				)
			},
		},
		{
			name: "stop", command: protocol.CommandConsoleStop, wantParams: map[string]any{},
			callHandler: func() (*mcp.CallToolResult, error) {
				return service.send(
					context.Background(), "browser-a", protocol.CommandConsoleStop,
					pageTarget(&tabID, &frameID, ""), map[string]any{}, nil,
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resultChannel := make(chan *mcp.CallToolResult, 1)
			go func() {
				result, _ := test.callHandler()
				resultChannel <- result
			}()
			request := receiveToolMessage(t, connection.messages)
			if request.Command != test.command || request.Target == nil ||
				request.Target.TabID == nil || *request.Target.TabID != tabID ||
				request.Target.FrameID == nil || *request.Target.FrameID != frameID {
				t.Fatalf("request = %#v", request)
			}
			var params map[string]any
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatalf("unmarshal params: %v", err)
			}
			if !reflect.DeepEqual(params, test.wantParams) {
				t.Fatalf("params = %#v, want %#v", params, test.wantParams)
			}
			response, err := protocol.NewResponse(
				request.RequestID,
				"browser-a",
				map[string]any{"active": test.command == protocol.CommandConsoleStart},
				nil,
			)
			if err != nil {
				t.Fatalf("NewResponse() error = %v", err)
			}
			if !service.router.HandleResponse("browser-a", connection.ID(), response) {
				t.Fatal("HandleResponse() = false")
			}
			if result := <-resultChannel; result.IsError {
				t.Fatalf("handler returned error: %s", toolText(t, result))
			}
		})
	}
}

func TestConsoleHandlersRejectInvalidConfiguration(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	falseValue := false
	invalidBuffer := maxConsoleBufferSize + 1
	invalidLimit := maxConsoleReadLimit + 1
	tests := []func() (*mcp.CallToolResult, error){
		func() (*mcp.CallToolResult, error) {
			return service.browserStartConsoleHandler(context.Background(), mcp.CallToolRequest{}, consoleStartArgs{
				BrowserID: "browser-a", BufferSize: &invalidBuffer,
			})
		},
		func() (*mcp.CallToolResult, error) {
			return service.browserStartConsoleHandler(context.Background(), mcp.CallToolRequest{}, consoleStartArgs{
				BrowserID: "browser-a", CaptureConsole: &falseValue, CaptureErrors: &falseValue,
			})
		},
		func() (*mcp.CallToolResult, error) {
			return service.browserGetConsoleHandler(context.Background(), mcp.CallToolRequest{}, consoleReadArgs{
				consoleTargetArgs: consoleTargetArgs{BrowserID: "browser-a"}, Levels: []string{"error", "error"},
			})
		},
		func() (*mcp.CallToolResult, error) {
			return service.browserGetConsoleHandler(context.Background(), mcp.CallToolRequest{}, consoleReadArgs{
				consoleTargetArgs: consoleTargetArgs{BrowserID: "browser-a"}, Cursor: "-1", Limit: &invalidLimit,
			})
		},
		func() (*mcp.CallToolResult, error) {
			return service.browserGetConsoleHandler(context.Background(), mcp.CallToolRequest{}, consoleReadArgs{
				consoleTargetArgs: consoleTargetArgs{BrowserID: "browser-a"}, Since: time.Now().String(),
			})
		},
	}
	for _, test := range tests {
		result, err := test()
		if err != nil || !result.IsError {
			t.Fatalf("invalid console call = (%#v, %v), want tool error", result, err)
		}
	}
}
