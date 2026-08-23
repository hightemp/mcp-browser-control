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

func TestTabGroupAndSessionHandlersBuildExpectedRequests(t *testing.T) {
	service, connection, _ := newTestService(t)
	groupID := 8
	windowID := 4
	title := "Research"
	collapsed := true
	maxResults := 10
	sessionID := "session-1"

	tests := []struct {
		name        string
		wantCommand string
		wantParams  map[string]any
		call        func(context.Context) (*mcp.CallToolResult, error)
	}{
		{
			name:        "group tabs",
			wantCommand: protocol.CommandTabsGroup,
			wantParams: map[string]any{
				"tabIds":  []any{float64(2), float64(3)},
				"groupId": float64(groupID),
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGroupTabsHandler(ctx, mcp.CallToolRequest{}, tabGroupArgs{
					BrowserID: "browser-a", TabIDs: []int{2, 3}, GroupID: &groupID,
				})
			},
		},
		{
			name:        "group tabs in window",
			wantCommand: protocol.CommandTabsGroup,
			wantParams: map[string]any{
				"tabIds":   []any{float64(5)},
				"windowId": float64(windowID),
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGroupTabsHandler(ctx, mcp.CallToolRequest{}, tabGroupArgs{
					BrowserID: "browser-a", TabIDs: []int{5}, WindowID: &windowID,
				})
			},
		},
		{
			name:        "ungroup tabs",
			wantCommand: protocol.CommandTabsUngroup,
			wantParams:  map[string]any{"tabIds": []any{float64(2), float64(3)}},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserUngroupTabsHandler(ctx, mcp.CallToolRequest{}, tabUngroupArgs{
					BrowserID: "browser-a", TabIDs: []int{2, 3},
				})
			},
		},
		{
			name:        "update group",
			wantCommand: protocol.CommandTabGroupsUpdate,
			wantParams: map[string]any{
				"groupId": float64(groupID), "title": title,
				"color": "blue", "collapsed": true,
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserUpdateTabGroupHandler(ctx, mcp.CallToolRequest{}, tabGroupUpdateArgs{
					BrowserID: "browser-a", GroupID: groupID, Title: &title,
					Color: "blue", Collapsed: &collapsed,
				})
			},
		},
		{
			name:        "recently closed",
			wantCommand: protocol.CommandSessionsRecentlyClosed,
			wantParams:  map[string]any{"maxResults": float64(maxResults)},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGetRecentlyClosedHandler(ctx, mcp.CallToolRequest{}, recentlyClosedArgs{
					BrowserID: "browser-a", MaxResults: &maxResults,
				})
			},
		},
		{
			name:        "restore session",
			wantCommand: protocol.CommandSessionsRestore,
			wantParams:  map[string]any{"sessionId": sessionID},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserRestoreSessionHandler(ctx, mcp.CallToolRequest{}, restoreSessionArgs{
					BrowserID: "browser-a", SessionID: &sessionID,
				})
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
			if request.Target != nil {
				t.Errorf("target = %#v, want nil", request.Target)
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
				map[string]bool{"ok": true},
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

func TestTabGroupAndSessionHandlerValidation(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	groupID := 1
	windowID := 2
	blankSessionID := " "
	zeroResults := 0
	tests := []func() (*mcp.CallToolResult, error){
		func() (*mcp.CallToolResult, error) {
			return service.browserGroupTabsHandler(context.Background(), mcp.CallToolRequest{}, tabGroupArgs{
				BrowserID: "browser-a", TabIDs: []int{1}, GroupID: &groupID, WindowID: &windowID,
			})
		},
		func() (*mcp.CallToolResult, error) {
			return service.browserUngroupTabsHandler(context.Background(), mcp.CallToolRequest{}, tabUngroupArgs{
				BrowserID: "browser-a", TabIDs: []int{1, 1},
			})
		},
		func() (*mcp.CallToolResult, error) {
			return service.browserUpdateTabGroupHandler(context.Background(), mcp.CallToolRequest{}, tabGroupUpdateArgs{
				BrowserID: "browser-a", GroupID: groupID,
			})
		},
		func() (*mcp.CallToolResult, error) {
			return service.browserUpdateTabGroupHandler(context.Background(), mcp.CallToolRequest{}, tabGroupUpdateArgs{
				BrowserID: "browser-a", GroupID: groupID, Color: "black",
			})
		},
		func() (*mcp.CallToolResult, error) {
			return service.browserGetRecentlyClosedHandler(context.Background(), mcp.CallToolRequest{}, recentlyClosedArgs{
				BrowserID: "browser-a", MaxResults: &zeroResults,
			})
		},
		func() (*mcp.CallToolResult, error) {
			return service.browserRestoreSessionHandler(context.Background(), mcp.CallToolRequest{}, restoreSessionArgs{
				BrowserID: "browser-a", SessionID: &blankSessionID,
			})
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
