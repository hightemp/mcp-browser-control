package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/redaction"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type batchToolResponse struct {
	Success   bool            `json:"success"`
	BrowserID string          `json:"browserId"`
	Data      batchResult     `json:"data"`
	Warnings  []string        `json:"warnings"`
	Error     *protocol.Error `json:"error,omitempty"`
}

func TestBrowserBatchRunsSequentiallyAgainstOneBrowser(t *testing.T) {
	t.Parallel()

	service, connectionA, connectionB := newTestService(t)
	mcpServer := server.NewMCPServer("test", "1.0.0")
	service.Register(mcpServer)
	resultChannel := make(chan mcp.CallToolResult, 1)
	errorChannel := make(chan error, 1)
	go func() {
		result, err := invokeMCPTool(context.Background(), mcpServer, "browser_batch", map[string]any{
			"browserId": "browser-a",
			"steps": []map[string]any{
				{"tool": "browser_get_tabs"},
				{"tool": "browser_ping"},
			},
		})
		if err != nil {
			errorChannel <- err
			return
		}
		resultChannel <- result
	}()

	first := receiveToolMessage(t, connectionA.messages)
	if first.Command != protocol.CommandTabsList {
		t.Fatalf("first command = %q, want %q", first.Command, protocol.CommandTabsList)
	}
	respondToToolRequest(t, service, connectionA, first, map[string]any{
		"tabs": []map[string]any{{"id": 1}},
	})
	second := receiveToolMessage(t, connectionA.messages)
	if second.Command != protocol.CommandBrowserPing {
		t.Fatalf("second command = %q, want %q", second.Command, protocol.CommandBrowserPing)
	}
	respondToToolRequest(t, service, connectionA, second, map[string]any{"pong": true})

	select {
	case err := <-errorChannel:
		t.Fatalf("browser_batch call error = %v", err)
	case result := <-resultChannel:
		if result.IsError {
			t.Fatalf("browser_batch returned error: %s", toolText(t, &result))
		}
		response := decodeBatchToolResponse(t, &result)
		if response.BrowserID != "browser-a" || response.Data.Completed != 2 ||
			response.Data.Requested != 2 || response.Data.StoppedOnError ||
			response.Data.DeadlineExceeded || response.Data.Transactional {
			t.Fatalf("batch response = %#v", response)
		}
		for index, step := range response.Data.Steps {
			if step.Index != index || !step.Success || step.DurationMS < 0 {
				t.Errorf("step %d = %#v", index, step)
			}
			nested := decodeNestedToolResponse(t, step.Result)
			if !nested.Success || nested.BrowserID != "browser-a" {
				t.Errorf("nested step %d = %#v", index, nested)
			}
		}
		if len(response.Warnings) == 0 {
			t.Fatal("batch response omitted the no-rollback warning")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for browser_batch result")
	}
	select {
	case message := <-connectionB.messages:
		t.Fatalf("browser B received unexpected request: %#v", message)
	default:
	}
}

func TestBrowserBatchReappliesToolProfileAndStopsOnError(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	mcpServer := server.NewMCPServer(
		"test",
		"1.0.0",
		server.WithToolHandlerMiddleware(ToolProfileMiddleware(
			"standard",
			log.New(io.Discard, "", 0),
		)),
	)
	service.Register(mcpServer)
	result, err := invokeMCPTool(context.Background(), mcpServer, "browser_batch", map[string]any{
		"browserId": "browser-a",
		"steps": []map[string]any{
			{"tool": "browser_get_recently_closed"},
			{"tool": "browser_ping"},
		},
	})
	if err != nil {
		t.Fatalf("invoke browser_batch: %v", err)
	}
	response := decodeBatchToolResponse(t, &result)
	if result.IsError || !response.Success || response.Data.Completed != 1 ||
		!response.Data.StoppedOnError {
		t.Fatalf("batch response = %#v, isError = %v", response, result.IsError)
	}
	nested := decodeNestedToolResponse(t, response.Data.Steps[0].Result)
	if response.Data.Steps[0].Success || nested.Error == nil ||
		nested.Error.Code != protocol.CodePermissionRequired {
		t.Fatalf("nested policy result = %#v", nested)
	}
	select {
	case message := <-connection.messages:
		t.Fatalf("browser received profile-denied request: %#v", message)
	default:
	}
}

func TestBrowserBatchContinuesAfterArgumentErrorWhenRequested(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	mcpServer := server.NewMCPServer("test", "1.0.0")
	service.Register(mcpServer)
	stopOnError := false
	resultChannel := make(chan mcp.CallToolResult, 1)
	errorChannel := make(chan error, 1)
	go func() {
		result, err := invokeMCPTool(context.Background(), mcpServer, "browser_batch", map[string]any{
			"browserId":   "browser-a",
			"stopOnError": stopOnError,
			"steps": []map[string]any{
				{"tool": "browser_get_tab", "arguments": map[string]any{"tabId": "invalid"}},
				{"tool": "browser_ping"},
			},
		})
		if err != nil {
			errorChannel <- err
			return
		}
		resultChannel <- result
	}()

	request := receiveToolMessage(t, connection.messages)
	if request.Command != protocol.CommandBrowserPing {
		t.Fatalf("dispatched command = %q, want ping", request.Command)
	}
	respondToToolRequest(t, service, connection, request, map[string]any{"pong": true})
	select {
	case err := <-errorChannel:
		t.Fatalf("browser_batch call error = %v", err)
	case result := <-resultChannel:
		response := decodeBatchToolResponse(t, &result)
		if result.IsError || response.Data.Completed != 2 || response.Data.StoppedOnError {
			t.Fatalf("batch response = %#v, isError = %v", response, result.IsError)
		}
		first := decodeNestedToolResponse(t, response.Data.Steps[0].Result)
		if response.Data.Steps[0].Success || first.Error == nil ||
			first.Error.Code != protocol.CodeInvalidMessage {
			t.Fatalf("first nested result = %#v", first)
		}
		if !response.Data.Steps[1].Success {
			t.Fatalf("second step = %#v", response.Data.Steps[1])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for browser_batch result")
	}
}

func TestBrowserBatchReappliesActionPolicy(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	service.actionPolicy = mustActionPolicy(t, []string{"https://allowed.example"}, nil, false, nil)
	mcpServer := server.NewMCPServer("test", "1.0.0")
	service.Register(mcpServer)
	result, err := invokeMCPTool(context.Background(), mcpServer, "browser_batch", map[string]any{
		"browserId": "browser-a",
		"steps": []map[string]any{{
			"tool":      "browser_navigate_tab",
			"arguments": map[string]any{"url": "https://blocked.example/private"},
		}},
	})
	if err != nil {
		t.Fatalf("invoke browser_batch: %v", err)
	}
	response := decodeBatchToolResponse(t, &result)
	nested := decodeNestedToolResponse(t, response.Data.Steps[0].Result)
	if result.IsError || response.Data.Steps[0].Success || nested.Error == nil ||
		nested.Error.Code != protocol.CodeRestrictedURL {
		t.Fatalf("nested action policy result = %#v", nested)
	}
	select {
	case message := <-connection.messages:
		t.Fatalf("browser received action-policy-denied request: %#v", message)
	default:
	}
}

func TestBrowserBatchReappliesExtensionCapabilityChecks(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	if !service.registry.UpdateCapabilities(
		"browser-a",
		connection.ID(),
		[]string{protocol.CommandBrowserPing},
		nil,
	) {
		t.Fatal("UpdateCapabilities() = false")
	}
	mcpServer := server.NewMCPServer("test", "1.0.0")
	service.Register(mcpServer)
	result, err := invokeMCPTool(context.Background(), mcpServer, "browser_batch", map[string]any{
		"browserId": "browser-a",
		"steps":     []map[string]any{{"tool": "browser_get_tabs"}},
	})
	if err != nil {
		t.Fatalf("invoke browser_batch: %v", err)
	}
	response := decodeBatchToolResponse(t, &result)
	nested := decodeNestedToolResponse(t, response.Data.Steps[0].Result)
	if result.IsError || response.Data.Steps[0].Success || nested.Error == nil ||
		nested.Error.Code != protocol.CodeCapabilityUnavailable {
		t.Fatalf("nested capability result = %#v", nested)
	}
	select {
	case message := <-connection.messages:
		t.Fatalf("browser received capability-denied request: %#v", message)
	default:
	}
}

func TestBrowserBatchUsesSharedDeadline(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	mcpServer := server.NewMCPServer("test", "1.0.0")
	service.Register(mcpServer)
	startedAt := time.Now()
	result, err := invokeMCPTool(context.Background(), mcpServer, "browser_batch", map[string]any{
		"browserId": "browser-a",
		"timeoutMs": 25,
		"steps": []map[string]any{
			{"tool": "browser_ping"},
			{"tool": "browser_get_tabs"},
		},
	})
	if err != nil {
		t.Fatalf("invoke browser_batch: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("batch deadline took %s", elapsed)
	}
	response := decodeBatchToolResponse(t, &result)
	if result.IsError || response.Data.Completed != 1 || !response.Data.DeadlineExceeded {
		t.Fatalf("batch deadline response = %#v, isError = %v", response, result.IsError)
	}
	nested := decodeNestedToolResponse(t, response.Data.Steps[0].Result)
	if nested.Error == nil || nested.Error.Code != protocol.CodeTimeout {
		t.Fatalf("nested deadline error = %#v", nested.Error)
	}
	request := receiveToolMessage(t, connection.messages)
	if request.Command != protocol.CommandBrowserPing {
		t.Fatalf("deadline command = %q, want ping", request.Command)
	}
}

func TestBrowserBatchRejectsUnsafeOrCrossBrowserSteps(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	mcpServer := server.NewMCPServer("test", "1.0.0")
	service.Register(mcpServer)
	tooMany := make([]map[string]any, maxBatchSteps+1)
	for index := range tooMany {
		tooMany[index] = map[string]any{"tool": "browser_ping"}
	}
	tests := []struct {
		name     string
		steps    []map[string]any
		wantCode protocol.ErrorCode
	}{
		{name: "server local", steps: []map[string]any{{"tool": "browser_list"}}, wantCode: protocol.CodeInvalidCommand},
		{name: "raw command", steps: []map[string]any{{"tool": "browser_send_command"}}, wantCode: protocol.CodeInvalidCommand},
		{name: "raw CDP", steps: []map[string]any{{"tool": "browser_send_cdp_command"}}, wantCode: protocol.CodeInvalidCommand},
		{name: "performance metrics", steps: []map[string]any{{"tool": "browser_get_performance_metrics"}}, wantCode: protocol.CodeInvalidCommand},
		{name: "performance capture", steps: []map[string]any{{"tool": "browser_capture_performance"}}, wantCode: protocol.CodeInvalidCommand},
		{name: "network start", steps: []map[string]any{{"tool": "browser_start_network_capture"}}, wantCode: protocol.CodeInvalidCommand},
		{name: "network body", steps: []map[string]any{{"tool": "browser_get_network_body"}}, wantCode: protocol.CodeInvalidCommand},
		{name: "network HAR", steps: []map[string]any{{"tool": "browser_export_network_har"}}, wantCode: protocol.CodeInvalidCommand},
		{name: "recursive", steps: []map[string]any{{"tool": "browser_batch"}}, wantCode: protocol.CodeInvalidCommand},
		{
			name: "different browser",
			steps: []map[string]any{{
				"tool":      "browser_ping",
				"arguments": map[string]any{"browserId": "browser-b"},
			}},
			wantCode: protocol.CodeInvalidMessage,
		},
		{name: "too many", steps: tooMany, wantCode: protocol.CodeInvalidMessage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := invokeMCPTool(context.Background(), mcpServer, "browser_batch", map[string]any{
				"browserId": "browser-a",
				"steps":     test.steps,
			})
			if err != nil {
				t.Fatalf("invoke browser_batch: %v", err)
			}
			response := decodeToolResponse(t, &result)
			if !result.IsError || response.Error == nil || response.Error.Code != test.wantCode {
				t.Fatalf("batch error = %#v, want %s", response.Error, test.wantCode)
			}
		})
	}
	select {
	case message := <-connection.messages:
		t.Fatalf("browser received rejected batch request: %#v", message)
	default:
	}
}

func TestBrowserBatchLimitsCombinedResultSize(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	service.resultLimits = redaction.DefaultLimits(300)
	mcpServer := server.NewMCPServer("test", "1.0.0")
	service.Register(mcpServer)
	resultChannel := make(chan mcp.CallToolResult, 1)
	errorChannel := make(chan error, 1)
	go func() {
		result, err := invokeMCPTool(context.Background(), mcpServer, "browser_batch", map[string]any{
			"browserId": "browser-a",
			"steps":     []map[string]any{{"tool": "browser_ping"}},
		})
		if err != nil {
			errorChannel <- err
			return
		}
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	respondToToolRequest(t, service, connection, request, map[string]any{"pong": true})
	select {
	case err := <-errorChannel:
		t.Fatalf("browser_batch call error = %v", err)
	case result := <-resultChannel:
		response := decodeToolResponse(t, &result)
		if !result.IsError || response.Error == nil ||
			response.Error.Code != protocol.CodePayloadTooLarge {
			t.Fatalf("batch size error = %#v", response.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for browser_batch size result")
	}
}

func TestBatchAllowedToolsAreRegisteredAndClassified(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	mcpServer := server.NewMCPServer("test", "1.0.0")
	service.Register(mcpServer)
	message := mcpServer.HandleMessage(
		context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`),
	)
	response, ok := message.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("tools/list response = %T", message)
	}
	list, ok := response.Result.(mcp.ListToolsResult)
	if !ok {
		t.Fatalf("tools/list result = %T", response.Result)
	}
	registered := make(map[string]bool, len(list.Tools))
	for _, tool := range list.Tools {
		registered[tool.Name] = true
	}
	for name := range batchAllowedTools {
		if !registered[name] {
			t.Errorf("batch tool %q is not registered", name)
		}
		if _, classified := browserToolLevels[name]; !classified {
			t.Errorf("batch tool %q is not profile-classified", name)
		}
	}
	for _, denied := range []string{
		"browser_list", "browser_select", "browser_print_to_pdf", "browser_get_accessibility_tree",
		"browser_set_emulation", "browser_get_emulation_state", "browser_reset_emulation",
		"browser_evaluate_javascript",
		"browser_send_cdp_command",
		"browser_get_performance_metrics", "browser_capture_performance",
		"browser_start_network_capture", "browser_stop_network_capture", "browser_clear_network_log",
		"browser_get_network_body", "browser_export_network_har",
		"browser_send_command", "browser_batch",
	} {
		if _, allowed := batchAllowedTools[denied]; allowed {
			t.Errorf("unsafe nested tool %q is allowed", denied)
		}
	}
}

func invokeMCPTool(
	ctx context.Context,
	mcpServer *server.MCPServer,
	name string,
	arguments map[string]any,
) (mcp.CallToolResult, error) {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": arguments,
		},
	})
	if err != nil {
		return mcp.CallToolResult{}, err
	}
	message := mcpServer.HandleMessage(ctx, payload)
	response, ok := message.(mcp.JSONRPCResponse)
	if !ok {
		return mcp.CallToolResult{}, fmt.Errorf("tools/call returned %T", message)
	}
	result, ok := response.Result.(mcp.CallToolResult)
	if !ok {
		return mcp.CallToolResult{}, fmt.Errorf("tools/call result is %T", response.Result)
	}
	return result, nil
}

func decodeBatchToolResponse(t *testing.T, result *mcp.CallToolResult) batchToolResponse {
	t.Helper()
	var response batchToolResponse
	if err := json.Unmarshal([]byte(toolText(t, result)), &response); err != nil {
		t.Fatalf("unmarshal batch response: %v", err)
	}
	return response
}

func decodeNestedToolResponse(t *testing.T, payload json.RawMessage) toolResponse {
	t.Helper()
	var response toolResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("unmarshal nested tool response: %v", err)
	}
	return response
}
