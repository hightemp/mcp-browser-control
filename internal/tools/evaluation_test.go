package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestEvaluateJavaScriptHandlerPreservesTargetAndBounds(t *testing.T) {
	t.Parallel()

	service, connection, otherConnection := newTestService(t)
	tabID := 7
	documentID := "document-1"
	maxDepth := 3
	timeoutMS := 2_000
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserEvaluateJavaScriptHandler(
			context.Background(),
			mcp.CallToolRequest{},
			evaluationArgs{
				BrowserID:  "browser-a",
				TabID:      &tabID,
				DocumentID: documentID,
				Expression: "({ title: document.title, values: [1, 2] })",
				MaxDepth:   &maxDepth,
				TimeoutMS:  &timeoutMS,
			},
		)
		resultChannel <- result
	}()

	request := receiveToolMessage(t, connection.messages)
	select {
	case other := <-otherConnection.messages:
		t.Fatalf("evaluation leaked to another browser: %#v", other)
	default:
	}
	if request.Command != protocol.CommandRuntimeEvaluateIsolated || request.Target == nil ||
		request.Target.TabID == nil || *request.Target.TabID != tabID ||
		request.Target.FrameID == nil || *request.Target.FrameID != 0 ||
		request.Target.DocumentID != documentID || request.TimeoutMS < 1 ||
		request.TimeoutMS > int64(timeoutMS) {
		t.Fatalf("evaluation request = %#v", request)
	}
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatalf("decode evaluation params: %v", err)
	}
	if params["expression"] != "({ title: document.title, values: [1, 2] })" ||
		params["awaitPromise"] != true || params["maxDepth"] != float64(maxDepth) ||
		params["maxNodes"] != float64(defaultEvaluationMaxNodes) ||
		params["maxBytes"] != float64(defaultEvaluationMaxBytes) {
		t.Fatalf("evaluation params = %#v", params)
	}

	wire := evaluationWireResult{
		Completed:  true,
		TabID:      tabID,
		DocumentID: documentID,
		World:      "isolated",
		ValueType:  "object",
		Value:      json.RawMessage(`{"title":"Example","values":[1,2],"authorization":"Bearer secret-value"}`),
		Truncated:  false,
		NodeCount:  6,
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
		t.Fatalf("browserEvaluateJavaScriptHandler() returned error: %s", toolText(t, result))
	}
	text := toolText(t, result)
	for _, expected := range []string{
		`"documentId":"document-1"`,
		`"world":"isolated"`,
		`"title":"Example"`,
		`"nodeCount":6`,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("tool result does not contain %s: %s", expected, text)
		}
	}
	if strings.Contains(text, "secret-value") {
		t.Fatalf("evaluation result was not redacted: %s", text)
	}
}

func TestValidateEvaluationArgsDefaultsAndBounds(t *testing.T) {
	t.Parallel()

	params, settings, err := validateEvaluationArgs(evaluationArgs{Expression: "document.title"})
	if err != nil {
		t.Fatalf("validateEvaluationArgs() error = %v", err)
	}
	if !settings.AwaitPromise || settings.MaxDepth != defaultEvaluationMaxDepth ||
		settings.MaxNodes != defaultEvaluationMaxNodes ||
		settings.MaxStringChars != defaultEvaluationMaxStringChars ||
		settings.MaxBytes != defaultEvaluationMaxBytes || settings.TimeoutMS != defaultEvaluationTimeoutMS {
		t.Fatalf("settings = %#v", settings)
	}
	if params["expression"] != "document.title" || params["awaitPromise"] != true {
		t.Fatalf("params = %#v", params)
	}

	negative := -1
	zero := 0
	tooManyNodes := maxEvaluationMaxNodes + 1
	tooManyBytes := maxEvaluationMaxBytes + 1
	tooLongTimeout := maxEvaluationTimeoutMS + 1
	for _, args := range []evaluationArgs{
		{},
		{Expression: "   "},
		{Expression: strings.Repeat("x", maxEvaluationExpressionChars+1)},
		{Expression: "1", MaxDepth: &negative},
		{Expression: "1", MaxNodes: &zero},
		{Expression: "1", MaxNodes: &tooManyNodes},
		{Expression: "1", MaxBytes: &tooManyBytes},
		{Expression: "1", TimeoutMS: &tooLongTimeout},
	} {
		if _, _, err := validateEvaluationArgs(args); err == nil {
			t.Fatalf("validateEvaluationArgs(%#v) error = nil", args)
		}
	}
}

func TestGenericCommandCannotBypassEvaluationTool(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	result, err := service.browserSendCommandHandler(
		context.Background(),
		mcp.CallToolRequest{},
		sendCommandArgs{
			BrowserID: "browser-a",
			Command:   protocol.CommandRuntimeEvaluateIsolated,
			Data:      map[string]any{"expression": "document.cookie"},
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

func TestDecodeEvaluationResultRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	settings := evaluationSettings{
		MaxDepth:       2,
		MaxNodes:       5,
		MaxStringChars: 10,
		MaxBytes:       defaultEvaluationMaxBytes,
	}
	valid := evaluationWireResult{
		Completed:  true,
		TabID:      1,
		DocumentID: "document-1",
		World:      "isolated",
		ValueType:  "object",
		Value:      json.RawMessage(`{"value":[true,null]}`),
		NodeCount:  4,
		Warnings:   []string{},
	}
	encode := func(value evaluationWireResult) json.RawMessage {
		t.Helper()
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		return payload
	}

	if _, err := decodeEvaluationResult(encode(valid), settings); err != nil {
		t.Fatalf("decodeEvaluationResult(valid) error = %v", err)
	}
	validException := evaluationWireResult{
		Completed:  false,
		TabID:      1,
		DocumentID: "document-1",
		World:      "isolated",
		ValueType:  "undefined",
		Exception:  &evaluationException{Text: "Uncaught", LineNumber: 1, ColumnNumber: 2},
		Warnings:   []string{},
	}
	if _, err := decodeEvaluationResult(encode(validException), settings); err != nil {
		t.Fatalf("decodeEvaluationResult(exception) error = %v", err)
	}

	wrongCount := valid
	wrongCount.NodeCount = 3
	wrongType := valid
	wrongType.ValueType = "array"
	tooDeep := valid
	tooDeep.Value = json.RawMessage(`{"value":[{"nested":true}]}`)
	tooDeep.NodeCount = 5
	tooLong := valid
	tooLong.Value = json.RawMessage(`{"value":"01234567890"}`)
	tooLong.NodeCount = 2
	badException := validException
	badException.Exception = &evaluationException{}

	for _, raw := range []json.RawMessage{
		json.RawMessage(`null`),
		json.RawMessage(`{"completed":true,"tabId":1,"documentId":"document-1","world":"isolated","valueType":"undefined","truncated":false,"nodeCount":1,"warnings":[],"unexpected":true}`),
		encode(wrongCount),
		encode(wrongType),
		encode(tooDeep),
		encode(tooLong),
		encode(badException),
		json.RawMessage(`{"completed":true,"tabId":1,"documentId":"document-1","world":"isolated","valueType":"unserializable","unserializableValue":"not-safe","truncated":false,"nodeCount":1,"warnings":[]}`),
	} {
		if _, err := decodeEvaluationResult(raw, settings); err == nil {
			t.Fatalf("decodeEvaluationResult(%s) error = nil", raw)
		}
	}
}
