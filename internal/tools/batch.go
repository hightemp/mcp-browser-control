package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	maxBatchSteps       = 25
	defaultBatchTimeout = 30 * time.Second
)

type batchArgs struct {
	BrowserID   string      `json:"browserId,omitempty"`
	Steps       []batchStep `json:"steps"`
	StopOnError *bool       `json:"stopOnError,omitempty"`
	TimeoutMS   *int        `json:"timeoutMs,omitempty"`
}

type batchStep struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type batchStepResult struct {
	Index      int             `json:"index"`
	Tool       string          `json:"tool"`
	Success    bool            `json:"success"`
	Result     json.RawMessage `json:"result"`
	DurationMS float64         `json:"durationMs"`
}

type batchResult struct {
	Steps            []batchStepResult `json:"steps"`
	Requested        int               `json:"requested"`
	Completed        int               `json:"completed"`
	StoppedOnError   bool              `json:"stoppedOnError"`
	DeadlineExceeded bool              `json:"deadlineExceeded"`
	Transactional    bool              `json:"transactional"`
}

// Batch is deliberately fail-closed. A new tool is not batchable until it is
// explicitly reviewed and added here. Server-local selection operations, the
// raw command escape hatch, and recursive batches are never eligible.
var batchAllowedTools = map[string]struct{}{
	"browser_ping": {},

	"browser_get_windows":   {},
	"browser_get_window":    {},
	"browser_create_window": {},
	"browser_update_window": {},
	"browser_focus_window":  {},
	"browser_close_window":  {},

	"browser_get_tabs":      {},
	"browser_get_tab":       {},
	"browser_create_tab":    {},
	"browser_activate_tab":  {},
	"browser_navigate_tab":  {},
	"browser_reload_tab":    {},
	"browser_stop_tab":      {},
	"browser_go_back":       {},
	"browser_go_forward":    {},
	"browser_move_tab":      {},
	"browser_duplicate_tab": {},
	"browser_close_tab":     {},
	"browser_pin_tab":       {},
	"browser_mute_tab":      {},
	"browser_get_tab_zoom":  {},
	"browser_set_tab_zoom":  {},

	"browser_group_tabs":          {},
	"browser_ungroup_tabs":        {},
	"browser_update_tab_group":    {},
	"browser_get_recently_closed": {},
	"browser_restore_session":     {},

	"browser_page_info":            {},
	"browser_get_html":             {},
	"browser_get_html_by_selector": {},
	"browser_get_text":             {},
	"browser_query":                {},
	"browser_get_element":          {},
	"browser_snapshot":             {},

	"browser_click_element":  {},
	"browser_input_data":     {},
	"browser_double_click":   {},
	"browser_context_click":  {},
	"browser_hover":          {},
	"browser_focus":          {},
	"browser_blur":           {},
	"browser_type":           {},
	"browser_clear":          {},
	"browser_press":          {},
	"browser_select_option":  {},
	"browser_set_checked":    {},
	"browser_scroll":         {},
	"browser_drag_and_drop":  {},
	"browser_dispatch_event": {},
	"browser_submit":         {},
	"browser_wait":           {},
	"browser_screenshot":     {},

	"browser_start_console_capture": {},
	"browser_stop_console_capture":  {},
	"browser_clear_console_log":     {},
	"browser_get_console_log":       {},
	"browser_get_network_log":       {},
}

func (s *Service) registerBatchTool(mcpServer *server.MCPServer) {
	stepSchema := map[string]any{
		"type":     "object",
		"required": []string{"tool"},
		"properties": map[string]any{
			"tool": map[string]any{
				"type":        "string",
				"minLength":   1,
				"description": "Typed browser tool to call",
			},
			"arguments": map[string]any{
				"type":                 "object",
				"description":          "Arguments for the nested tool; browserId is injected by the batch",
				"additionalProperties": true,
			},
		},
		"additionalProperties": false,
	}
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_batch",
			mcp.WithDescription("Run a bounded sequential list of typed commands against one browser"),
			optionalBrowserID(),
			mcp.WithArray(
				"steps",
				mcp.Required(),
				mcp.Description("Ordered typed browser commands"),
				mcp.Items(stepSchema),
				mcp.MinItems(1),
				mcp.MaxItems(maxBatchSteps),
			),
			mcp.WithBoolean(
				"stopOnError",
				mcp.Description("Stop after the first failed nested command"),
				mcp.DefaultBool(true),
			),
			mcp.WithNumber(
				"timeoutMs",
				mcp.Description("Shared batch deadline in milliseconds"),
				mcp.Min(1),
				mcp.Max(float64(maxToolTimeout.Milliseconds())),
				mcp.DefaultNumber(float64(defaultBatchTimeout.Milliseconds())),
			),
		),
		mcp.NewTypedToolHandler(func(
			ctx context.Context,
			_ mcp.CallToolRequest,
			args batchArgs,
		) (*mcp.CallToolResult, error) {
			return s.browserBatchHandler(ctx, mcpServer, args)
		}),
	)
}

func (s *Service) browserBatchHandler(
	ctx context.Context,
	mcpServer *server.MCPServer,
	args batchArgs,
) (*mcp.CallToolResult, error) {
	startedAt := time.Now()
	if len(args.Steps) == 0 || len(args.Steps) > maxBatchSteps {
		return errorResult(protocol.NewError(
			protocol.CodeInvalidMessage,
			fmt.Sprintf("steps must contain between 1 and %d commands", maxBatchSteps),
			false,
		))
	}
	if err := validateBatchSteps(args.Steps); err != nil {
		return errorResult(err)
	}
	browserID, err := s.resolveBrowser(ctx, args.BrowserID)
	if err != nil {
		return errorResult(err)
	}
	preparedSteps := make([]batchStep, len(args.Steps))
	for index, step := range args.Steps {
		arguments, prepareErr := batchStepArguments(step.Arguments, browserID)
		if prepareErr != nil {
			return errorResult(prepareErr)
		}
		preparedSteps[index] = batchStep{Tool: step.Tool, Arguments: arguments}
	}
	batchCtx, cancel, err := batchContext(ctx, args.TimeoutMS)
	if err != nil {
		return errorResult(err)
	}
	defer cancel()

	stopOnError := args.StopOnError == nil || *args.StopOnError
	data := batchResult{
		Steps:         make([]batchStepResult, 0, len(args.Steps)),
		Requested:     len(args.Steps),
		Transactional: false,
	}
	nestedBytes := 0
	for index, step := range preparedSteps {
		if batchCtx.Err() != nil {
			data.DeadlineExceeded = batchCtx.Err() == context.DeadlineExceeded
			break
		}
		stepStartedAt := time.Now()
		result, dispatchErr := dispatchBatchStep(
			batchCtx,
			mcpServer,
			index,
			step.Tool,
			step.Arguments,
		)
		if dispatchErr != nil {
			return errorResultWithDuration(dispatchErr, time.Since(startedAt))
		}
		nestedBytes += len(result.payload)
		if nestedBytes > s.resultLimits.MaxOutputBytes {
			return errorResultWithDuration(protocol.NewError(
				protocol.CodePayloadTooLarge,
				"the batch results exceed the configured MCP result limit",
				false,
			), time.Since(startedAt))
		}
		data.Steps = append(data.Steps, batchStepResult{
			Index:      index,
			Tool:       step.Tool,
			Success:    result.success,
			Result:     result.payload,
			DurationMS: float64(time.Since(stepStartedAt).Microseconds()) / 1000,
		})
		data.Completed = len(data.Steps)
		if batchCtx.Err() != nil {
			data.DeadlineExceeded = batchCtx.Err() == context.DeadlineExceeded
			break
		}
		if !result.success && stopOnError {
			data.StoppedOnError = true
			break
		}
	}

	warnings := []string{
		"Batch execution is sequential and does not provide transactional rollback",
	}
	if data.DeadlineExceeded {
		warnings = append(warnings, "The shared batch deadline expired before every requested step completed")
	}
	return successResultWithTargetWarningsLimited(
		browserID,
		nil,
		data,
		time.Since(startedAt),
		warnings,
		s.resultLimits.MaxOutputBytes,
	)
}

func validateBatchSteps(steps []batchStep) error {
	for index, step := range steps {
		if _, allowed := batchAllowedTools[step.Tool]; !allowed {
			return protocol.NewError(
				protocol.CodeInvalidCommand,
				fmt.Sprintf("steps[%d].tool is not an allowed typed batch command", index),
				false,
			)
		}
	}
	return nil
}

func batchStepArguments(arguments map[string]any, browserID string) (map[string]any, error) {
	cloned := make(map[string]any, len(arguments)+1)
	for key, value := range arguments {
		cloned[key] = value
	}
	if explicit, exists := cloned["browserId"]; exists {
		explicitBrowserID, ok := explicit.(string)
		if !ok {
			return nil, protocol.NewError(
				protocol.CodeInvalidMessage,
				"a nested browserId must be a string",
				false,
			)
		}
		if explicitBrowserID != "" && explicitBrowserID != browserID {
			return nil, protocol.NewError(
				protocol.CodeInvalidMessage,
				"all batch steps must target the resolved browserId",
				false,
			)
		}
	}
	cloned["browserId"] = browserID
	return cloned, nil
}

type dispatchedBatchStep struct {
	success bool
	payload json.RawMessage
}

func dispatchBatchStep(
	ctx context.Context,
	mcpServer *server.MCPServer,
	index int,
	tool string,
	arguments map[string]any,
) (dispatchedBatchStep, error) {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      fmt.Sprintf("batch-%d", index),
		"method":  "tools/call",
		"params": map[string]any{
			"name":      tool,
			"arguments": arguments,
		},
	})
	if err != nil {
		return dispatchedBatchStep{}, fmt.Errorf("marshal nested MCP tool call: %w", err)
	}
	message := mcpServer.HandleMessage(ctx, payload)
	response, ok := message.(mcp.JSONRPCResponse)
	if !ok {
		return dispatchedBatchStep{}, protocol.NewError(
			protocol.CodeBackpressure,
			"the nested MCP tool call was rejected before execution",
			true,
		)
	}
	result, ok := response.Result.(mcp.CallToolResult)
	if !ok {
		return dispatchedBatchStep{}, protocol.NewError(
			protocol.CodeInternal,
			"the nested MCP tool returned an invalid protocol result",
			false,
		)
	}
	normalized, err := normalizedBatchPayload(result)
	if err != nil {
		return dispatchedBatchStep{}, err
	}
	return dispatchedBatchStep{success: !result.IsError, payload: normalized}, nil
}

func normalizedBatchPayload(result mcp.CallToolResult) (json.RawMessage, error) {
	if len(result.Content) == 1 {
		if content, ok := mcp.AsTextContent(result.Content[0]); ok && json.Valid([]byte(content.Text)) {
			var response toolResponse
			if err := json.Unmarshal([]byte(content.Text), &response); err == nil &&
				response.Success == !result.IsError {
				return json.RawMessage(content.Text), nil
			}
		}
	}
	if !result.IsError {
		return nil, protocol.NewError(
			protocol.CodeInternal,
			"the nested MCP tool returned an invalid result envelope",
			false,
		)
	}
	return protocolErrorPayload(protocol.NewError(
		protocol.CodeInvalidMessage,
		"the nested tool arguments could not be decoded",
		false,
	))
}

func protocolErrorPayload(err error) (json.RawMessage, error) {
	result, resultErr := errorResult(err)
	if resultErr != nil {
		return nil, resultErr
	}
	content, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		return nil, protocol.NewError(
			protocol.CodeInternal,
			"could not encode a nested MCP tool error",
			false,
		)
	}
	return json.RawMessage(content.Text), nil
}

func batchContext(ctx context.Context, timeoutMS *int) (context.Context, context.CancelFunc, error) {
	if timeoutMS == nil {
		requestCtx, cancel := context.WithTimeout(ctx, defaultBatchTimeout)
		return requestCtx, cancel, nil
	}
	return toolContext(ctx, timeoutMS)
}
