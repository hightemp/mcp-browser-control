package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestAccessibilityTreeHandlerPreservesTargetAndValidatedLinks(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	tabID := 7
	documentID := "document-1"
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserAccessibilityTreeHandler(
			context.Background(),
			mcp.CallToolRequest{},
			accessibilityTreeArgs{
				BrowserID:  "browser-a",
				TabID:      &tabID,
				DocumentID: documentID,
				Roles:      []string{"Button"},
			},
		)
		resultChannel <- result
	}()

	request := receiveToolMessage(t, connection.messages)
	if request.Command != protocol.CommandAccessibilityGetTree || request.Target == nil ||
		request.Target.TabID == nil || *request.Target.TabID != tabID ||
		request.Target.FrameID == nil || *request.Target.FrameID != 0 ||
		request.Target.DocumentID != documentID {
		t.Fatalf("accessibility request = %#v", request)
	}
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatalf("decode accessibility params: %v", err)
	}
	if params["mode"] != "full" || params["maxDepth"] != float64(defaultAccessibilityMaxDepth) ||
		params["maxNodes"] != float64(defaultAccessibilityMaxNodes) {
		t.Fatalf("accessibility params = %#v", params)
	}
	roles, _ := params["roles"].([]any)
	if len(roles) != 1 || roles[0] != "button" {
		t.Fatalf("normalized roles = %#v", params["roles"])
	}

	strict := true
	backendNodeID := 17
	wire := accessibilityWireResult{
		Mode:              "full",
		TabID:             tabID,
		DocumentID:        documentID,
		RootFrameID:       "frame-root",
		FrameCount:        1,
		Frames:            []accessibilityFrame{{FrameID: "frame-root", URL: "https://example.com/"}},
		TotalNodeCount:    1,
		MatchingNodeCount: 1,
		ReturnedNodeCount: 1,
		Nodes: []accessibilityNode{{
			NodeID:        "ax-1",
			Depth:         0,
			Role:          "button",
			Name:          "Save",
			Properties:    []accessibilityNodeProperty{},
			BackendNodeID: &backendNodeID,
			FrameID:       "frame-root",
			Locator:       &protocol.Locator{Role: "button", Name: "Save", Strict: &strict},
			Reference: &protocol.ElementReference{
				ElementID:  "element-1",
				DocumentID: documentID,
			},
		}},
		Warnings: []string{},
	}
	response, err := protocol.NewResponse(request.RequestID, "browser-a", wire, nil)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	if !service.router.HandleResponse("browser-a", connection.ID(), response) {
		t.Fatal("HandleResponse() = false")
	}

	result := <-resultChannel
	if result.IsError {
		t.Fatalf("browserAccessibilityTreeHandler() returned error: %s", toolText(t, result))
	}
	text := toolText(t, result)
	for _, expected := range []string{
		`"documentId":"document-1"`,
		`"backendNodeId":17`,
		`"elementId":"element-1"`,
		`"role":"button"`,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("tool result does not contain %s: %s", expected, text)
		}
	}
}

func TestValidateAccessibilityArgsModesAndBounds(t *testing.T) {
	t.Parallel()

	backendNodeID := 42
	includeReferences := false
	params, settings, err := validateAccessibilityArgs(accessibilityTreeArgs{
		Mode:                     " PARTIAL ",
		BackendNodeID:            &backendNodeID,
		IncludeElementReferences: &includeReferences,
	})
	if err != nil {
		t.Fatalf("validateAccessibilityArgs() error = %v", err)
	}
	if settings.Mode != "partial" || settings.BackendNodeID != backendNodeID ||
		settings.MaxElementReferences != 0 || params["fetchRelatives"] != true {
		t.Fatalf("settings = %#v, params = %#v", settings, params)
	}

	tooManyNodes := maxAccessibilityMaxNodes + 1
	whitespace := " "
	for _, args := range []accessibilityTreeArgs{
		{Mode: "partial"},
		{Mode: "full", BackendNodeID: &backendNodeID},
		{Mode: "unknown"},
		{MaxNodes: &tooManyNodes},
		{NameContains: whitespace},
		{Roles: []string{"button", "BUTTON"}},
		{IncludeElementReferences: &includeReferences, MaxElementReferences: &backendNodeID},
	} {
		if _, _, err := validateAccessibilityArgs(args); err == nil {
			t.Fatalf("validateAccessibilityArgs(%#v) error = nil", args)
		}
	}
}

func TestDecodeAccessibilityResultRejectsMalformedTrees(t *testing.T) {
	t.Parallel()

	settings := accessibilitySettings{
		Mode:          "full",
		MaxNodes:      10,
		MaxProperties: 2,
		MaxValueChars: 100,
		MaxBytes:      defaultAccessibilityMaxBytes,
	}
	valid := accessibilityWireResult{
		Mode:              "full",
		TabID:             1,
		DocumentID:        "document-1",
		RootFrameID:       "frame-root",
		FrameCount:        1,
		Frames:            []accessibilityFrame{{FrameID: "frame-root"}},
		TotalNodeCount:    1,
		MatchingNodeCount: 1,
		ReturnedNodeCount: 1,
		Nodes: []accessibilityNode{{
			NodeID:     "node-1",
			Properties: []accessibilityNodeProperty{},
		}},
		Warnings: []string{},
	}
	encode := func(value accessibilityWireResult) json.RawMessage {
		t.Helper()
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		return payload
	}

	duplicateNode := valid
	duplicateNode.ReturnedNodeCount = 2
	duplicateNode.MatchingNodeCount = 2
	duplicateNode.TotalNodeCount = 2
	duplicateNode.Nodes = append(append([]accessibilityNode(nil), valid.Nodes...), valid.Nodes[0])
	badReference := valid
	badReference.Nodes = append([]accessibilityNode(nil), valid.Nodes...)
	badReference.Nodes[0].Reference = &protocol.ElementReference{
		ElementID:  "element-1",
		DocumentID: "other-document",
	}
	badCount := valid
	badCount.ReturnedNodeCount = 2

	for _, raw := range []json.RawMessage{
		json.RawMessage(`null`),
		encode(duplicateNode),
		encode(badReference),
		encode(badCount),
	} {
		if _, err := decodeAccessibilityResult(raw, settings); err == nil {
			t.Fatalf("decodeAccessibilityResult(%s) error = nil", raw)
		}
	}
}
