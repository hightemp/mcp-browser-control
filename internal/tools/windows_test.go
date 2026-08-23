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

func TestWindowCommandHandlersBuildExpectedRequests(t *testing.T) {
	service, connection, _ := newTestService(t)
	windowID := 7
	left := -100
	width := 900
	focused := false
	incognito := true
	drawAttention := true

	tests := []struct {
		name        string
		wantCommand string
		wantTarget  *protocol.Target
		wantParams  map[string]any
		call        func(context.Context) (*mcp.CallToolResult, error)
	}{
		{
			name:        "list",
			wantCommand: protocol.CommandWindowsList,
			wantParams:  map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGetWindowsHandler(
					ctx,
					mcp.CallToolRequest{},
					windowListArgs{BrowserID: "browser-a"},
				)
			},
		},
		{
			name:        "get",
			wantCommand: protocol.CommandWindowsGet,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", WindowID: &windowID},
			wantParams:  map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGetWindowHandler(
					ctx,
					mcp.CallToolRequest{},
					windowTargetArgs{BrowserID: "browser-a", WindowID: windowID},
				)
			},
		},
		{
			name:        "create popup",
			wantCommand: protocol.CommandWindowsCreate,
			wantParams: map[string]any{
				"urls":      []any{"https://example.com", "https://example.org"},
				"type":      "popup",
				"state":     "normal",
				"focused":   false,
				"incognito": true,
				"left":      float64(left),
				"width":     float64(width),
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserCreateWindowHandler(
					ctx,
					mcp.CallToolRequest{},
					windowCreateArgs{
						BrowserID: "browser-a",
						URLs:      []string{"https://example.com", "https://example.org"},
						Type:      "popup",
						State:     "normal",
						Focused:   &focused,
						Incognito: &incognito,
						Left:      &left,
						Width:     &width,
					},
				)
			},
		},
		{
			name:        "update",
			wantCommand: protocol.CommandWindowsUpdate,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", WindowID: &windowID},
			wantParams: map[string]any{
				"state":         "minimized",
				"drawAttention": true,
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserUpdateWindowHandler(
					ctx,
					mcp.CallToolRequest{},
					windowUpdateArgs{
						BrowserID:     "browser-a",
						WindowID:      windowID,
						State:         "minimized",
						DrawAttention: &drawAttention,
					},
				)
			},
		},
		{
			name:        "focus",
			wantCommand: protocol.CommandWindowsFocus,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", WindowID: &windowID},
			wantParams:  map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserFocusWindowHandler(
					ctx,
					mcp.CallToolRequest{},
					windowTargetArgs{BrowserID: "browser-a", WindowID: windowID},
				)
			},
		},
		{
			name:        "close",
			wantCommand: protocol.CommandWindowsClose,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", WindowID: &windowID},
			wantParams:  map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserCloseWindowHandler(
					ctx,
					mcp.CallToolRequest{},
					windowTargetArgs{BrowserID: "browser-a", WindowID: windowID},
				)
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

			response, err := protocol.NewResponse(
				request.RequestID,
				"browser-a",
				map[string]any{"ok": true},
				nil,
			)
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

func TestWindowCommandHandlerValidation(t *testing.T) {
	t.Parallel()
	service, _, _ := newTestService(t)
	width := 800

	tests := []struct {
		name string
		call func() (*mcp.CallToolResult, error)
	}{
		{
			name: "empty URL",
			call: func() (*mcp.CallToolResult, error) {
				return service.browserCreateWindowHandler(
					context.Background(),
					mcp.CallToolRequest{},
					windowCreateArgs{BrowserID: "browser-a", URLs: []string{" "}},
				)
			},
		},
		{
			name: "state with bounds",
			call: func() (*mcp.CallToolResult, error) {
				return service.browserUpdateWindowHandler(
					context.Background(),
					mcp.CallToolRequest{},
					windowUpdateArgs{
						BrowserID: "browser-a",
						WindowID:  1,
						State:     "fullscreen",
						Width:     &width,
					},
				)
			},
		},
		{
			name: "empty update",
			call: func() (*mcp.CallToolResult, error) {
				return service.browserUpdateWindowHandler(
					context.Background(),
					mcp.CallToolRequest{},
					windowUpdateArgs{BrowserID: "browser-a", WindowID: 1},
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.call()
			if err != nil || result == nil || !result.IsError {
				t.Fatalf("handler result = (%#v, %v), want tool error", result, err)
			}
			response := decodeToolResponse(t, result)
			if response.Error == nil || response.Error.Code != protocol.CodeInvalidMessage {
				t.Fatalf("error = %#v, want %s", response.Error, protocol.CodeInvalidMessage)
			}
		})
	}
}
