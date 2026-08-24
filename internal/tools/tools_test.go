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
		map[string]any{
			"tabs":        []map[string]any{{"id": 7}},
			"warnings":    []string{"one tab was omitted"},
			"nextCursor":  "cursor-2",
			"artifactUri": "browser://artifacts/tabs-1",
		},
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
		if response.Target == nil || response.Target.BrowserID != "browser-a" {
			t.Errorf("target = %#v, want browser-a", response.Target)
		}
		if !reflect.DeepEqual(response.Warnings, []string{"one tab was omitted"}) {
			t.Errorf("warnings = %#v", response.Warnings)
		}
		if response.NextCursor != "cursor-2" ||
			response.ArtifactURI != "browser://artifacts/tabs-1" {
			t.Errorf("result links = (%q, %q)", response.NextCursor, response.ArtifactURI)
		}
		if response.DurationMS == nil || *response.DurationMS < 0 {
			t.Errorf("durationMs = %#v", response.DurationMS)
		}
		if _, err := time.Parse(time.RFC3339Nano, response.Timestamp); err != nil {
			t.Errorf("timestamp = %q: %v", response.Timestamp, err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool result")
	}
}

func TestServiceUsesBrowserScopedSelectedTab(t *testing.T) {
	t.Parallel()

	service, connectionA, _ := newTestService(t)
	selected, err := service.browserSelectTabHandler(
		context.Background(),
		mcp.CallToolRequest{},
		browserSelectTabArgs{BrowserID: "browser-a", TabID: 17},
	)
	if err != nil || selected.IsError {
		t.Fatalf("browserSelectTabHandler() = (%v, %v)", selected, err)
	}

	result := make(chan *mcp.CallToolResult, 1)
	go func() {
		callResult, _ := service.browserGetHTMLHandler(
			context.Background(),
			mcp.CallToolRequest{},
			pageHTMLArgs{BrowserID: "browser-a"},
		)
		result <- callResult
	}()
	request := receiveToolMessage(t, connectionA.messages)
	if request.Target == nil || request.Target.TabID == nil || *request.Target.TabID != 17 {
		t.Fatalf("selected target = %#v, want tab 17", request.Target)
	}
	response, err := protocol.NewResponse(request.RequestID, "browser-a", map[string]bool{"ok": true}, nil)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	if !service.router.HandleResponse("browser-a", connectionA.ID(), response) {
		t.Fatal("HandleResponse() = false")
	}
	if callResult := <-result; callResult == nil || callResult.IsError {
		t.Fatalf("browserGetHTMLHandler() result = %#v", callResult)
	}

	explicitTabID := 23
	result = make(chan *mcp.CallToolResult, 1)
	go func() {
		callResult, _ := service.browserGetHTMLHandler(
			context.Background(),
			mcp.CallToolRequest{},
			pageHTMLArgs{BrowserID: "browser-a", TabID: &explicitTabID},
		)
		result <- callResult
	}()
	request = receiveToolMessage(t, connectionA.messages)
	if request.Target == nil || request.Target.TabID == nil || *request.Target.TabID != explicitTabID {
		t.Fatalf("explicit target = %#v, want tab %d", request.Target, explicitTabID)
	}
	response, err = protocol.NewResponse(request.RequestID, "browser-a", map[string]bool{"ok": true}, nil)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	service.router.HandleResponse("browser-a", connectionA.ID(), response)
	<-result
	stored, ok := service.selections.GetTab(directSessionID, "browser-a")
	if !ok || stored.TabID != 17 {
		t.Fatalf("explicit tab replaced selection: %#v", stored)
	}

	frameID := 3
	result = make(chan *mcp.CallToolResult, 1)
	go func() {
		callResult, _ := service.browserGetHTMLHandler(
			context.Background(),
			mcp.CallToolRequest{},
			pageHTMLArgs{
				BrowserID: "browser-a", FrameID: &frameID, DocumentID: "document-3",
			},
		)
		result <- callResult
	}()
	request = receiveToolMessage(t, connectionA.messages)
	if request.Target == nil || request.Target.TabID == nil || *request.Target.TabID != 17 ||
		request.Target.FrameID == nil || *request.Target.FrameID != frameID ||
		request.Target.DocumentID != "document-3" {
		t.Fatalf("selected frame target = %#v", request.Target)
	}
	response, err = protocol.NewResponse(request.RequestID, "browser-a", map[string]bool{"ok": true}, nil)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	service.router.HandleResponse("browser-a", connectionA.ID(), response)
	<-result
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
	} else {
		if response.Target != nil {
			t.Errorf("browser_list target = %#v, want nil", response.Target)
		}
		if response.Warnings == nil || len(response.Warnings) != 0 {
			t.Errorf("browser_list warnings = %#v, want empty array", response.Warnings)
		}
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
	} else if response.Target == nil || response.Target.BrowserID != "browser-b" {
		t.Fatalf("target = %#v, want browser-b", response.Target)
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
	inputSelector := "#name"
	clearField := false
	timeoutMS := 500
	frameID := 2
	maxChars := 5_000
	maxDepth := 12
	limit := 10
	maxHTMLChars := 2_000
	interactiveOnly := true
	includeShadowDOM := true
	maxNodes := 500
	delayMS := 15
	checked := true
	waitForNavigation := true
	waitExpected := "Saved"
	waitPollInterval := 50
	roleLocator := &protocol.Locator{Role: "button", Name: "Save", IncludeShadowDOM: true}
	inputLocator := protocol.Locator{CSS: "#name"}
	targetLocator := &protocol.Locator{CSS: "#target"}
	targetCoordinates := &protocol.Coordinates{X: 80, Y: 120}

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
			name:        "page info",
			wantCommand: protocol.CommandPageInfo,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams:  map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserPageInfoHandler(ctx, mcp.CallToolRequest{}, targetedArgs{BrowserID: "browser-a", TabID: &tabID})
			},
		},
		{
			name:        "html",
			wantCommand: protocol.CommandPageGetHTML,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams: map[string]any{
				"maxChars": float64(maxChars), "maxDepth": float64(maxDepth),
				"includeSelectors": []any{"main"}, "excludeSelectors": []any{"script"},
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGetHTMLHandler(ctx, mcp.CallToolRequest{}, pageHTMLArgs{
					BrowserID: "browser-a", TabID: &tabID, MaxChars: &maxChars, MaxDepth: &maxDepth,
					IncludeSelectors: []string{"main"}, ExcludeSelectors: []string{"script"},
				})
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
			name:        "visible text",
			wantCommand: protocol.CommandPageGetText,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams: map[string]any{
				"maxChars": float64(maxChars), "cursor": "100",
				"includeSelectors": []any{"main"}, "excludeSelectors": []any{"nav"},
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGetTextHandler(ctx, mcp.CallToolRequest{}, pageTextArgs{
					BrowserID: "browser-a", TabID: &tabID, MaxChars: &maxChars, Cursor: "100",
					IncludeSelectors: []string{"main"}, ExcludeSelectors: []string{"nav"},
				})
			},
		},
		{
			name:        "query",
			wantCommand: protocol.CommandPageQuery,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams: map[string]any{
				"locator": map[string]any{
					"role": "button", "name": "Save", "includeShadowDOM": true,
				},
				"cursor": "20", "limit": float64(limit),
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserQueryHandler(ctx, mcp.CallToolRequest{}, pageQueryArgs{
					BrowserID: "browser-a", TabID: &tabID, Locator: *roleLocator,
					Cursor: "20", Limit: &limit,
				})
			},
		},
		{
			name:        "element details",
			wantCommand: protocol.CommandPageGetElement,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams: map[string]any{
				"locator": map[string]any{
					"role": "button", "name": "Save", "includeShadowDOM": true,
				},
				"maxHTMLChars": float64(maxHTMLChars),
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGetElementHandler(ctx, mcp.CallToolRequest{}, pageElementArgs{
					BrowserID: "browser-a", TabID: &tabID, Locator: *roleLocator,
					MaxHTMLChars: &maxHTMLChars,
				})
			},
		},
		{
			name:        "semantic snapshot",
			wantCommand: protocol.CommandPageSnapshot,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams: map[string]any{
				"interactiveOnly":  true,
				"includeShadowDOM": true,
				"maxDepth":         float64(maxDepth),
				"maxNodes":         float64(maxNodes),
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserSnapshotHandler(ctx, mcp.CallToolRequest{}, pageSnapshotArgs{
					BrowserID: "browser-a", TabID: &tabID,
					InteractiveOnly: &interactiveOnly, IncludeShadowDOM: &includeShadowDOM,
					MaxDepth: &maxDepth, MaxNodes: &maxNodes,
				})
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
			name:        "locator click in frame",
			wantCommand: protocol.CommandPageClick,
			wantTarget: &protocol.Target{
				BrowserID: "browser-a", TabID: &tabID, FrameID: &frameID, DocumentID: "document-1",
			},
			wantParams: map[string]any{
				"locator": map[string]any{
					"role": "button", "name": "Save", "includeShadowDOM": true,
				},
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserClickHandler(ctx, mcp.CallToolRequest{}, clickArgs{
					BrowserID: "browser-a", TabID: &tabID, FrameID: &frameID,
					DocumentID: "document-1", Locator: roleLocator,
				})
			},
		},
		{
			name:        "input",
			wantCommand: protocol.CommandPageFill,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams:  map[string]any{"selector": "#name", "value": "Ada", "index": float64(index), "clear": false},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserInputHandler(ctx, mcp.CallToolRequest{}, inputArgs{BrowserID: "browser-a", TabID: &tabID, Selector: &inputSelector, Value: "Ada", Index: &index, Clear: &clearField, TimeoutMS: &timeoutMS})
			},
		},
		{
			name:        "type",
			wantCommand: protocol.CommandPageType,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams: map[string]any{
				"locator": map[string]any{"css": "#name"}, "text": " Lovelace",
				"delayMs": float64(delayMS), "backend": "content", "waitForNavigation": true,
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserTypeHandler(ctx, mcp.CallToolRequest{}, interactionTypeArgs{
					interactionTargetArgs: interactionTargetArgs{
						BrowserID: "browser-a", TabID: &tabID, Locator: inputLocator,
						Backend: "content", WaitForNavigation: &waitForNavigation,
					},
					Text: " Lovelace", DelayMS: &delayMS,
				})
			},
		},
		{
			name:        "press",
			wantCommand: protocol.CommandPagePress,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams: map[string]any{
				"locator": map[string]any{"css": "#name"}, "key": "Enter",
				"modifiers": []any{"Control", "Shift"},
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserPressHandler(ctx, mcp.CallToolRequest{}, interactionPressArgs{
					interactionTargetArgs: interactionTargetArgs{
						BrowserID: "browser-a", TabID: &tabID, Locator: inputLocator,
					},
					Key: "Enter", Modifiers: []string{"Control", "Shift"},
				})
			},
		},
		{
			name:        "select option",
			wantCommand: protocol.CommandPageSelect,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams: map[string]any{
				"locator": map[string]any{"css": "#name"}, "values": []any{"US", "Canada"},
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserSelectOptionHandler(ctx, mcp.CallToolRequest{}, interactionSelectArgs{
					interactionTargetArgs: interactionTargetArgs{
						BrowserID: "browser-a", TabID: &tabID, Locator: inputLocator,
					},
					Values: []string{"US", "Canada"},
				})
			},
		},
		{
			name:        "set checked",
			wantCommand: protocol.CommandPageSetChecked,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams: map[string]any{
				"locator": map[string]any{"css": "#name"}, "checked": true,
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserSetCheckedHandler(ctx, mcp.CallToolRequest{}, interactionCheckedArgs{
					interactionTargetArgs: interactionTargetArgs{
						BrowserID: "browser-a", TabID: &tabID, Locator: inputLocator,
					},
					Checked: &checked,
				})
			},
		},
		{
			name:        "scroll",
			wantCommand: protocol.CommandPageScroll,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams: map[string]any{
				"locator": map[string]any{"css": "#name"}, "deltaX": float64(0),
				"deltaY": float64(500), "behavior": "smooth",
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserScrollHandler(ctx, mcp.CallToolRequest{}, interactionScrollArgs{
					BrowserID: "browser-a", TabID: &tabID, Locator: &inputLocator,
					DeltaY: 500, Behavior: "smooth",
				})
			},
		},
		{
			name:        "drag to locator",
			wantCommand: protocol.CommandPageDrag,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams: map[string]any{
				"source":        map[string]any{"css": "#name"},
				"targetLocator": map[string]any{"css": "#target"},
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserDragHandler(ctx, mcp.CallToolRequest{}, interactionDragArgs{
					BrowserID: "browser-a", TabID: &tabID, Source: inputLocator,
					TargetLocator: targetLocator,
				})
			},
		},
		{
			name:        "drag to coordinates",
			wantCommand: protocol.CommandPageDrag,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams: map[string]any{
				"source":            map[string]any{"css": "#name"},
				"targetCoordinates": map[string]any{"x": float64(80), "y": float64(120)},
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserDragHandler(ctx, mcp.CallToolRequest{}, interactionDragArgs{
					BrowserID: "browser-a", TabID: &tabID, Source: inputLocator,
					TargetCoordinates: targetCoordinates,
				})
			},
		},
		{
			name:        "dispatch event",
			wantCommand: protocol.CommandPageDispatch,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams: map[string]any{
				"locator": map[string]any{"css": "#name"}, "eventType": "app:save",
				"detail": map[string]any{"source": "test"},
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserDispatchHandler(ctx, mcp.CallToolRequest{}, interactionDispatchArgs{
					interactionTargetArgs: interactionTargetArgs{
						BrowserID: "browser-a", TabID: &tabID, Locator: inputLocator,
					},
					EventType: "app:save", Detail: map[string]any{"source": "test"},
				})
			},
		},
		{
			name:        "wait for text",
			wantCommand: protocol.CommandPageWait,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams: map[string]any{
				"condition": "text", "mode": "event", "pollIntervalMs": float64(waitPollInterval),
				"locator": map[string]any{"css": "#name"}, "expected": "Saved",
				"matchOperator": "contains", "caseSensitive": false,
			},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				caseSensitive := false
				return service.browserWaitHandler(ctx, mcp.CallToolRequest{}, waitArgs{
					BrowserID: "browser-a", TabID: &tabID, Condition: "text", Mode: "event",
					PollIntervalMS: &waitPollInterval, Locator: &inputLocator,
					Expected: &waitExpected, MatchOperator: "contains", CaseSensitive: &caseSensitive,
				})
			},
		},
		{
			name:        "console",
			wantCommand: protocol.CommandConsoleRead,
			wantTarget:  &protocol.Target{BrowserID: "browser-a", TabID: &tabID},
			wantParams:  map[string]any{},
			call: func(ctx context.Context) (*mcp.CallToolResult, error) {
				return service.browserGetConsoleHandler(ctx, mcp.CallToolRequest{}, consoleReadArgs{
					consoleTargetArgs: consoleTargetArgs{BrowserID: "browser-a", TabID: &tabID},
				})
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
				toolResponse := decodeToolResponse(t, result)
				wantResultTarget := test.wantTarget
				if wantResultTarget == nil {
					wantResultTarget = &protocol.Target{BrowserID: "browser-a"}
				}
				if !reflect.DeepEqual(toolResponse.Target, wantResultTarget) {
					t.Errorf("result target = %#v, want %#v", toolResponse.Target, wantResultTarget)
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
		t.Fatalf("click without an element address = (%v, %v), want tool error", missingTarget, err)
	}

	selector := "button"
	invalidLocator := &protocol.Locator{CSS: "button", Text: "Save"}
	invalidAddress, err := service.browserClickHandler(
		context.Background(),
		mcp.CallToolRequest{},
		clickArgs{BrowserID: "browser-a", Selector: &selector, Locator: invalidLocator},
	)
	if err != nil || !invalidAddress.IsError {
		t.Fatalf("click with multiple addresses = (%v, %v), want tool error", invalidAddress, err)
	}

	locator := protocol.Locator{CSS: "button"}
	invalidInteractions := []struct {
		name string
		call func() (*mcp.CallToolResult, error)
	}{
		{
			name: "backend",
			call: func() (*mcp.CallToolResult, error) {
				return service.browserTypeHandler(context.Background(), mcp.CallToolRequest{}, interactionTypeArgs{
					interactionTargetArgs: interactionTargetArgs{
						BrowserID: "browser-a", Locator: locator, Backend: "native",
					},
					Text: "hello",
				})
			},
		},
		{
			name: "empty text",
			call: func() (*mcp.CallToolResult, error) {
				return service.browserTypeHandler(context.Background(), mcp.CallToolRequest{}, interactionTypeArgs{
					interactionTargetArgs: interactionTargetArgs{
						BrowserID: "browser-a", Locator: locator,
					},
					Text: " ",
				})
			},
		},
		{
			name: "zero scroll",
			call: func() (*mcp.CallToolResult, error) {
				return service.browserScrollHandler(context.Background(), mcp.CallToolRequest{}, interactionScrollArgs{
					BrowserID: "browser-a",
				})
			},
		},
		{
			name: "missing drag target",
			call: func() (*mcp.CallToolResult, error) {
				return service.browserDragHandler(context.Background(), mcp.CallToolRequest{}, interactionDragArgs{
					BrowserID: "browser-a", Source: locator,
				})
			},
		},
		{
			name: "event type",
			call: func() (*mcp.CallToolResult, error) {
				return service.browserDispatchHandler(context.Background(), mcp.CallToolRequest{}, interactionDispatchArgs{
					interactionTargetArgs: interactionTargetArgs{
						BrowserID: "browser-a", Locator: locator,
					},
					EventType: "bad event",
				})
			},
		},
	}
	for _, test := range invalidInteractions {
		t.Run(test.name, func(t *testing.T) {
			result, callErr := test.call()
			if callErr != nil || result == nil || !result.IsError {
				t.Fatalf("invalid interaction = (%v, %v), want tool error", result, callErr)
			}
			response := decodeToolResponse(t, result)
			if response.Error == nil || response.Error.Code != protocol.CodeInvalidMessage {
				t.Fatalf("error = %#v, want %s", response.Error, protocol.CodeInvalidMessage)
			}
		})
	}

	invalidWaits := []waitArgs{
		{BrowserID: "browser-a", Condition: "delay"},
		{BrowserID: "browser-a", Condition: "url"},
		{BrowserID: "browser-a", Condition: "element", Locator: &locator},
		{BrowserID: "browser-a", Condition: "networkIdle"},
		{BrowserID: "browser-a", Condition: "attribute", Locator: &locator, Attribute: "bad name", AttributeState: "present"},
	}
	for index, args := range invalidWaits {
		result, callErr := service.browserWaitHandler(context.Background(), mcp.CallToolRequest{}, args)
		if callErr != nil || result == nil || !result.IsError {
			t.Fatalf("invalid wait %d = (%v, %v), want tool error", index, result, callErr)
		}
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
			registry.Registration{
				BrowserID:   browserID,
				DisplayName: browserID,
				Capabilities: []string{
					protocol.CommandBrowserPing,
					protocol.CommandConsoleRead,
					protocol.CommandConsoleStart,
					protocol.CommandConsoleStop,
					protocol.CommandConsoleClear,
					protocol.CommandNetworkRead,
					protocol.CommandPageClick,
					protocol.CommandPageFill,
					protocol.CommandPageHover,
					protocol.CommandPageFocus,
					protocol.CommandPageBlur,
					protocol.CommandPageType,
					protocol.CommandPageClear,
					protocol.CommandPagePress,
					protocol.CommandPageSelect,
					protocol.CommandPageSetChecked,
					protocol.CommandPageScroll,
					protocol.CommandPageDrag,
					protocol.CommandPageDispatch,
					protocol.CommandPageSubmit,
					protocol.CommandPageWait,
					protocol.CommandPageScreenshot,
					protocol.CommandPagePrintToPDF,
					protocol.CommandAccessibilityGetTree,
					protocol.CommandPageInfo,
					protocol.CommandPageGetText,
					protocol.CommandPageQuery,
					protocol.CommandPageGetElement,
					protocol.CommandPageSnapshot,
					protocol.CommandPageGetHTML,
					protocol.CommandPageGetHTMLBySelector,
					protocol.CommandTabsList,
					protocol.CommandTabsActivate,
					protocol.CommandTabsBack,
					protocol.CommandTabsClose,
					protocol.CommandTabsCreate,
					protocol.CommandTabsDuplicate,
					protocol.CommandTabsForward,
					protocol.CommandTabsGet,
					protocol.CommandTabsGetZoom,
					protocol.CommandTabsMove,
					protocol.CommandTabsMute,
					protocol.CommandTabsNavigate,
					protocol.CommandTabsPin,
					protocol.CommandTabsReload,
					protocol.CommandTabsSetZoom,
					protocol.CommandTabsStop,
					protocol.CommandTabsGroup,
					protocol.CommandTabsUngroup,
					protocol.CommandTabGroupsUpdate,
					protocol.CommandSessionsRecentlyClosed,
					protocol.CommandSessionsRestore,
					protocol.CommandWindowsClose,
					protocol.CommandWindowsCreate,
					protocol.CommandWindowsFocus,
					protocol.CommandWindowsGet,
					protocol.CommandWindowsList,
					protocol.CommandWindowsUpdate,
					"page.custom",
				},
			},
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
