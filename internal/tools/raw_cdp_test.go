package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestRawCDPHandlerPreservesTargetBoundsAndSafeAudit(t *testing.T) {
	t.Parallel()

	service, connection, otherConnection := newTestService(t)
	var audit bytes.Buffer
	service.auditLogger = log.New(&audit, "", 0)
	tabID := 7
	documentID := "document-1"
	maxDepth := 8
	timeoutMS := 2_000
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserSendRawCDPHandler(
			context.Background(),
			mcp.CallToolRequest{},
			rawCDPArgs{
				BrowserID:  "browser-a",
				TabID:      &tabID,
				DocumentID: documentID,
				Method:     rawCDPAccessibilityQuery,
				MethodParams: map[string]any{
					"backendNodeId":  17,
					"accessibleName": "audit-secret-value",
					"role":           "button",
				},
				MaxDepth:  &maxDepth,
				TimeoutMS: &timeoutMS,
			},
		)
		resultChannel <- result
	}()

	request := receiveToolMessage(t, connection.messages)
	select {
	case other := <-otherConnection.messages:
		t.Fatalf("raw CDP request leaked to another browser: %#v", other)
	default:
	}
	if request.Command != protocol.CommandCDPSendReadOnly || request.Target == nil ||
		request.Target.TabID == nil || *request.Target.TabID != tabID ||
		request.Target.FrameID == nil || *request.Target.FrameID != 0 ||
		request.Target.DocumentID != documentID || request.TimeoutMS < 1 ||
		request.TimeoutMS > int64(timeoutMS) {
		t.Fatalf("raw CDP request = %#v", request)
	}
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatalf("decode raw CDP params: %v", err)
	}
	methodParams, _ := params["params"].(map[string]any)
	if params["method"] != rawCDPAccessibilityQuery ||
		params["maxDepth"] != float64(maxDepth) ||
		params["maxNodes"] != float64(defaultRawCDPMaxNodes) ||
		methodParams["backendNodeId"] != float64(17) ||
		methodParams["accessibleName"] != "audit-secret-value" {
		t.Fatalf("raw CDP params = %#v", params)
	}

	wire := rawCDPWireResult{
		Method:     rawCDPAccessibilityQuery,
		TabID:      tabID,
		DocumentID: documentID,
		Result:     json.RawMessage(`{"nodes":[{"nodeId":"ax-1","name":{"value":"Save"},"authorization":"Bearer secret-result"}]}`),
		NodeCount:  7,
		Warnings:   []string{},
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
		t.Fatalf("browserSendRawCDPHandler() returned error: %s", toolText(t, result))
	}
	text := toolText(t, result)
	for _, expected := range []string{
		`"documentId":"document-1"`,
		`"method":"Accessibility.queryAXTree"`,
		`"nodeId":"ax-1"`,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("tool result does not contain %s: %s", expected, text)
		}
	}
	if strings.Contains(text, "secret-result") {
		t.Fatalf("raw CDP result was not redacted: %s", text)
	}
	auditText := audit.String()
	for _, expected := range []string{
		"operation=raw_cdp",
		`method="Accessibility.queryAXTree"`,
		`browserId="browser-a"`,
		"tabId=7",
		"outcome=OK",
	} {
		if !strings.Contains(auditText, expected) {
			t.Errorf("audit does not contain %q: %s", expected, auditText)
		}
	}
	for _, secret := range []string{"audit-secret-value", "secret-result", "backendNodeId"} {
		if strings.Contains(auditText, secret) {
			t.Errorf("audit contains request or result value %q: %s", secret, auditText)
		}
	}
}

func TestValidateRawCDPArgsAllowsOnlyReviewedMethodShapes(t *testing.T) {
	t.Parallel()

	valid := []rawCDPArgs{
		{Method: rawCDPAccessibilityFull, MethodParams: map[string]any{"depth": 50}},
		{Method: rawCDPAccessibilityPartial, MethodParams: map[string]any{"backendNodeId": 1, "fetchRelatives": true}},
		{Method: rawCDPAccessibilityQuery, MethodParams: map[string]any{"backendNodeId": 1, "accessibleName": "", "role": "button"}},
		{Method: rawCDPDOMDescribeNode, MethodParams: map[string]any{"backendNodeId": 1, "depth": 10}},
		{Method: rawCDPDOMGetBoxModel, MethodParams: map[string]any{"backendNodeId": json.Number("42")}},
		{Method: rawCDPPageGetLayoutMetrics},
		{Method: rawCDPPerformanceMetrics, MethodParams: map[string]any{}},
	}
	for _, args := range valid {
		params, settings, err := validateRawCDPArgs(args)
		if err != nil {
			t.Fatalf("validateRawCDPArgs(%#v) error = %v", args, err)
		}
		if settings.Method != args.Method || settings.MaxDepth != defaultRawCDPMaxDepth ||
			settings.MaxNodes != defaultRawCDPMaxNodes || params == nil {
			t.Fatalf("validateRawCDPArgs(%#v) = (%#v, %#v)", args, params, settings)
		}
	}

	zero := 0
	one := 1
	tooDeep := maxRawCDPMaxDepth + 1
	tooManyNodes := maxRawCDPMaxNodes + 1
	tooManyBytes := maxRawCDPMaxBytes + 1
	tooLongTimeout := maxRawCDPTimeoutMS + 1
	invalid := []rawCDPArgs{
		{},
		{Method: "Runtime.evaluate"},
		{Method: "Network.getCookies"},
		{Method: "Target.getTargets"},
		{Method: "Page.captureSnapshot"},
		{Method: rawCDPAccessibilityFull, MethodParams: map[string]any{"frameId": "root"}},
		{Method: rawCDPAccessibilityFull, MethodParams: map[string]any{"depth": 51}},
		{Method: rawCDPAccessibilityPartial, MethodParams: map[string]any{"backendNodeId": 0}},
		{Method: rawCDPAccessibilityPartial, MethodParams: map[string]any{"backendNodeId": 1, "fetchRelatives": "yes"}},
		{Method: rawCDPAccessibilityQuery, MethodParams: map[string]any{"backendNodeId": 1, "role": strings.Repeat("x", maxRawCDPRoleChars+1)}},
		{Method: rawCDPDOMDescribeNode, MethodParams: map[string]any{"backendNodeId": 1, "depth": 11}},
		{Method: rawCDPDOMGetBoxModel, MethodParams: map[string]any{"nodeId": 1}},
		{Method: rawCDPPerformanceMetrics, MaxDepth: &zero},
		{Method: rawCDPPerformanceMetrics, MaxNodes: &one},
		{Method: rawCDPPerformanceMetrics, MaxDepth: &tooDeep},
		{Method: rawCDPPerformanceMetrics, MaxNodes: &tooManyNodes},
		{Method: rawCDPPerformanceMetrics, MaxBytes: &tooManyBytes},
		{Method: rawCDPPerformanceMetrics, TimeoutMS: &tooLongTimeout},
	}
	for _, args := range invalid {
		if _, _, err := validateRawCDPArgs(args); err == nil {
			t.Fatalf("validateRawCDPArgs(%#v) error = nil", args)
		}
	}

	for _, method := range []string{"Runtime.evaluate", "Network.getCookies", "Page.captureSnapshot"} {
		err := validateRawCDPMethod(method)
		if protocol.ErrorFrom(err).Code != protocol.CodeInvalidCommand {
			t.Fatalf("validateRawCDPMethod(%q) error = %#v", method, err)
		}
	}
}

func TestRawCDPInvalidAuditDoesNotLogCallerControlledFields(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	var audit bytes.Buffer
	service.auditLogger = log.New(&audit, "", 0)
	secret := "caller-secret-value"
	result, err := service.browserSendRawCDPHandler(
		context.Background(),
		mcp.CallToolRequest{},
		rawCDPArgs{
			BrowserID: secret,
			Method:    "Secret." + secret,
		},
	)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("browserSendRawCDPHandler() = (%#v, %v), want tool error", result, err)
	}
	if strings.Contains(audit.String(), secret) || !strings.Contains(audit.String(), "unreviewed:Secret") {
		t.Fatalf("invalid audit contains caller value or omits safe domain: %s", audit.String())
	}
}

func TestDecodeRawCDPResultRejectsMalformedOrHandleBearingValues(t *testing.T) {
	t.Parallel()

	settings := rawCDPSettings{
		Method:         rawCDPPerformanceMetrics,
		MaxDepth:       5,
		MaxNodes:       20,
		MaxStringChars: 100,
		MaxBytes:       defaultRawCDPMaxBytes,
	}
	valid := rawCDPWireResult{
		Method:     rawCDPPerformanceMetrics,
		TabID:      1,
		DocumentID: "document-1",
		Result:     json.RawMessage(`{"metrics":[{"name":"Timestamp","value":1.5}]}`),
		NodeCount:  5,
		Warnings:   []string{},
	}
	encode := func(value rawCDPWireResult) json.RawMessage {
		t.Helper()
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		return payload
	}
	if _, err := decodeRawCDPResult(encode(valid), settings); err != nil {
		t.Fatalf("decodeRawCDPResult(valid) error = %v", err)
	}

	wrongMethod := valid
	wrongMethod.Method = rawCDPPageGetLayoutMetrics
	wrongCount := valid
	wrongCount.NodeCount = 4
	wrongShape := valid
	wrongShape.Result = json.RawMessage(`{"nodes":[]}`)
	wrongShape.NodeCount = 2
	handle := valid
	handle.Result = json.RawMessage(`{"metrics":[{"name":"Timestamp","value":1.5,"objectId":"remote"}]}`)
	handle.NodeCount = 6
	tooLong := valid
	tooLong.Result = json.RawMessage(`{"metrics":[{"name":"` + strings.Repeat("x", 101) + `","value":1.5}]}`)
	tooLong.NodeCount = 5

	for _, raw := range []json.RawMessage{
		json.RawMessage(`null`),
		json.RawMessage(`{"method":"Performance.getMetrics","tabId":1,"documentId":"document-1","result":{"metrics":[]},"truncated":false,"nodeCount":2,"warnings":[],"unexpected":true}`),
		encode(wrongMethod),
		encode(wrongCount),
		encode(wrongShape),
		encode(handle),
		encode(tooLong),
	} {
		if _, err := decodeRawCDPResult(raw, settings); err == nil {
			t.Fatalf("decodeRawCDPResult(%s) error = nil", raw)
		}
	}
}

func TestGenericCommandCannotBypassRawCDPTool(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	result, err := service.browserSendCommandHandler(
		context.Background(),
		mcp.CallToolRequest{},
		sendCommandArgs{
			BrowserID: "browser-a",
			Command:   protocol.CommandCDPSendReadOnly,
			Data:      map[string]any{"method": "Runtime.evaluate"},
		},
	)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("browserSendCommandHandler() = (%#v, %v), want tool error", result, err)
	}
	response := decodeToolResponse(t, result)
	if response.Error == nil || response.Error.Code != protocol.CodeInvalidCommand {
		t.Fatalf("error = %#v, want %s", response.Error, protocol.CodeInvalidCommand)
	}
	select {
	case message := <-connection.messages:
		t.Fatalf("generic command reached browser: %#v", message)
	default:
	}
}
