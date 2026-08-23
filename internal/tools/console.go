package tools

import (
	"context"
	"regexp"
	"strconv"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	maxConsoleBufferSize = 5_000
	maxConsoleReadLimit  = 200
)

var consoleCursorPattern = regexp.MustCompile(`^\d+$`)

type consoleStartArgs struct {
	BrowserID      string `json:"browserId,omitempty"`
	TabID          *int   `json:"tabId,omitempty"`
	FrameID        *int   `json:"frameId,omitempty"`
	DocumentID     string `json:"documentId,omitempty"`
	BufferSize     *int   `json:"bufferSize,omitempty"`
	CaptureConsole *bool  `json:"captureConsole,omitempty"`
	CaptureErrors  *bool  `json:"captureErrors,omitempty"`
	TimeoutMS      *int   `json:"timeoutMs,omitempty"`
}

type consoleTargetArgs struct {
	BrowserID  string `json:"browserId,omitempty"`
	TabID      *int   `json:"tabId,omitempty"`
	FrameID    *int   `json:"frameId,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
	TimeoutMS  *int   `json:"timeoutMs,omitempty"`
}

type consoleReadArgs struct {
	consoleTargetArgs
	Levels []string `json:"levels,omitempty"`
	Kinds  []string `json:"kinds,omitempty"`
	Cursor string   `json:"cursor,omitempty"`
	Limit  *int     `json:"limit,omitempty"`
	Since  string   `json:"since,omitempty"`
}

func (s *Service) registerConsoleTools(mcpServer *server.MCPServer) {
	mcpServer.AddTool(
		newConsoleTool(
			"browser_start_console_capture",
			"Start bounded console and page error capture in one document",
			mcp.WithNumber("bufferSize", mcp.Description("Maximum buffered entries"), mcp.Min(1), mcp.Max(maxConsoleBufferSize)),
			mcp.WithBoolean("captureConsole", mcp.Description("Capture console method calls")),
			mcp.WithBoolean("captureErrors", mcp.Description("Capture exceptions, unhandled rejections, and resource errors")),
		),
		mcp.NewTypedToolHandler(s.browserStartConsoleHandler),
	)
	for _, registration := range []struct {
		name        string
		description string
		command     string
	}{
		{
			name: "browser_stop_console_capture", description: "Stop console capture and retain buffered entries",
			command: protocol.CommandConsoleStop,
		},
		{
			name: "browser_clear_console_log", description: "Clear buffered console and page error entries",
			command: protocol.CommandConsoleClear,
		},
	} {
		command := registration.command
		mcpServer.AddTool(
			newConsoleTool(
				registration.name,
				registration.description,
			),
			mcp.NewTypedToolHandler(func(
				ctx context.Context,
				_ mcp.CallToolRequest,
				args consoleTargetArgs,
			) (*mcp.CallToolResult, error) {
				return s.send(
					ctx,
					args.BrowserID,
					command,
					pageTarget(args.TabID, args.FrameID, args.DocumentID),
					map[string]any{},
					args.TimeoutMS,
				)
			}),
		)
	}
	mcpServer.AddTool(
		newConsoleTool(
			"browser_get_console_log",
			"Read filtered console and page error entries from one document",
			mcp.WithArray("levels", mcp.Description("Console levels to include"),
				mcp.Items(map[string]any{"type": "string", "enum": consoleLevels()}), mcp.MaxItems(5)),
			mcp.WithArray("kinds", mcp.Description("Entry kinds to include"),
				mcp.Items(map[string]any{"type": "string", "enum": consoleKinds()}), mcp.MaxItems(4)),
			mcp.WithString("cursor", mcp.Description("Cursor returned by a previous read")),
			mcp.WithNumber("limit", mcp.Description("Maximum entries to return"), mcp.Min(1), mcp.Max(maxConsoleReadLimit)),
			mcp.WithString("since", mcp.Description("RFC 3339 timestamp lower bound")),
		),
		mcp.NewTypedToolHandler(s.browserGetConsoleHandler),
	)
}

func newConsoleTool(name, description string, extra ...mcp.ToolOption) mcp.Tool {
	options := []mcp.ToolOption{
		mcp.WithDescription(description),
		optionalBrowserID(),
		optionalTabID(),
		optionalFrameID(),
		optionalDocumentID(),
	}
	options = append(options, extra...)
	options = append(options, optionalTimeout())
	return mcp.NewTool(name, options...)
}

func (s *Service) browserStartConsoleHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args consoleStartArgs,
) (*mcp.CallToolResult, error) {
	if args.BufferSize != nil && (*args.BufferSize < 1 || *args.BufferSize > maxConsoleBufferSize) {
		return errorResult(invalidConsole("bufferSize must be between 1 and 5000"))
	}
	if args.CaptureConsole != nil && args.CaptureErrors != nil &&
		!*args.CaptureConsole && !*args.CaptureErrors {
		return errorResult(invalidConsole("at least one capture source must be enabled"))
	}
	params := map[string]any{}
	putOptional(params, "bufferSize", args.BufferSize)
	putOptional(params, "captureConsole", args.CaptureConsole)
	putOptional(params, "captureErrors", args.CaptureErrors)
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandConsoleStart,
		pageTarget(args.TabID, args.FrameID, args.DocumentID),
		params,
		args.TimeoutMS,
	)
}

func (s *Service) browserGetConsoleHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args consoleReadArgs,
) (*mcp.CallToolResult, error) {
	if err := validateConsoleFilter(args.Levels, consoleLevels(), "levels"); err != nil {
		return errorResult(err)
	}
	if err := validateConsoleFilter(args.Kinds, consoleKinds(), "kinds"); err != nil {
		return errorResult(err)
	}
	if args.Cursor != "" {
		if !consoleCursorPattern.MatchString(args.Cursor) {
			return errorResult(invalidConsole("cursor must be an unsigned integer string"))
		}
		cursor, err := strconv.ParseUint(args.Cursor, 10, 64)
		if err != nil || cursor > 9_007_199_254_740_991 {
			return errorResult(invalidConsole("cursor is out of range"))
		}
	}
	if args.Limit != nil && (*args.Limit < 1 || *args.Limit > maxConsoleReadLimit) {
		return errorResult(invalidConsole("limit must be between 1 and 200"))
	}
	if args.Since != "" {
		if _, err := time.Parse(time.RFC3339Nano, args.Since); err != nil {
			return errorResult(invalidConsole("since must be an RFC 3339 timestamp"))
		}
	}
	params := map[string]any{}
	if args.Levels != nil {
		params["levels"] = args.Levels
	}
	if args.Kinds != nil {
		params["kinds"] = args.Kinds
	}
	if args.Cursor != "" {
		params["cursor"] = args.Cursor
	}
	putOptional(params, "limit", args.Limit)
	if args.Since != "" {
		params["since"] = args.Since
	}
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandConsoleRead,
		pageTarget(args.TabID, args.FrameID, args.DocumentID),
		params,
		args.TimeoutMS,
	)
}

func validateConsoleFilter(values, allowed []string, name string) error {
	if len(values) > len(allowed) {
		return invalidConsole(name + " contains too many values")
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := allowedSet[value]; !ok {
			return invalidConsole(name + " contains an unsupported value")
		}
		if _, ok := seen[value]; ok {
			return invalidConsole(name + " must not contain duplicates")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func consoleLevels() []string {
	return []string{"debug", "log", "info", "warn", "error"}
}

func consoleKinds() []string {
	return []string{"console", "exception", "unhandledRejection", "resourceError"}
}

func invalidConsole(message string) *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}
