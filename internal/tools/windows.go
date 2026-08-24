package tools

import (
	"context"
	"strings"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type windowListArgs struct {
	BrowserID string `json:"browserId,omitempty"`
	TimeoutMS *int   `json:"timeoutMs,omitempty"`
}

type windowTargetArgs struct {
	BrowserID string `json:"browserId,omitempty"`
	WindowID  int    `json:"windowId"`
	TimeoutMS *int   `json:"timeoutMs,omitempty"`
}

type windowCloseArgs struct {
	BrowserID string `json:"browserId,omitempty"`
	WindowID  int    `json:"windowId"`
	Confirm   bool   `json:"confirm"`
	TimeoutMS *int   `json:"timeoutMs,omitempty"`
}

type windowCreateArgs struct {
	BrowserID string   `json:"browserId,omitempty"`
	URLs      []string `json:"urls,omitempty"`
	Type      string   `json:"type,omitempty"`
	State     string   `json:"state,omitempty"`
	Focused   *bool    `json:"focused,omitempty"`
	Incognito *bool    `json:"incognito,omitempty"`
	Left      *int     `json:"left,omitempty"`
	Top       *int     `json:"top,omitempty"`
	Width     *int     `json:"width,omitempty"`
	Height    *int     `json:"height,omitempty"`
	TimeoutMS *int     `json:"timeoutMs,omitempty"`
}

type windowUpdateArgs struct {
	BrowserID     string `json:"browserId,omitempty"`
	WindowID      int    `json:"windowId"`
	State         string `json:"state,omitempty"`
	Focused       *bool  `json:"focused,omitempty"`
	DrawAttention *bool  `json:"drawAttention,omitempty"`
	Left          *int   `json:"left,omitempty"`
	Top           *int   `json:"top,omitempty"`
	Width         *int   `json:"width,omitempty"`
	Height        *int   `json:"height,omitempty"`
	TimeoutMS     *int   `json:"timeoutMs,omitempty"`
}

func (s *Service) registerWindowTools(mcpServer *server.MCPServer) {
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_get_windows",
			mcp.WithDescription("List normal and popup windows in one browser instance"),
			optionalBrowserID(),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserGetWindowsHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_get_window",
			mcp.WithDescription("Get one browser window"),
			optionalBrowserID(),
			requiredWindowID(),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserGetWindowHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_create_window",
			windowCreateToolOptions()...,
		),
		mcp.NewTypedToolHandler(s.browserCreateWindowHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_update_window",
			windowUpdateToolOptions()...,
		),
		mcp.NewTypedToolHandler(s.browserUpdateWindowHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_focus_window",
			mcp.WithDescription("Focus one browser window"),
			optionalBrowserID(),
			requiredWindowID(),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserFocusWindowHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_close_window",
			mcp.WithDescription("Close one browser window and all of its tabs"),
			optionalBrowserID(),
			requiredWindowID(),
			mcp.WithBoolean(
				"confirm",
				mcp.Required(),
				mcp.Description("Explicitly confirm closing the window and all of its tabs"),
			),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserCloseWindowHandler),
	)
}

func (s *Service) browserGetWindowsHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args windowListArgs,
) (*mcp.CallToolResult, error) {
	return s.send(ctx, args.BrowserID, protocol.CommandWindowsList, nil, map[string]any{}, args.TimeoutMS)
}

func (s *Service) browserGetWindowHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args windowTargetArgs,
) (*mcp.CallToolResult, error) {
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandWindowsGet,
		targetWithWindow(args.WindowID),
		map[string]any{},
		args.TimeoutMS,
	)
}

func (s *Service) browserCreateWindowHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args windowCreateArgs,
) (*mcp.CallToolResult, error) {
	if err := validateWindowCreateArgs(args); err != nil {
		return errorResult(err)
	}
	params := make(map[string]any)
	addString(params, "type", args.Type)
	addString(params, "state", args.State)
	if len(args.URLs) > 0 {
		params["urls"] = args.URLs
	}
	addBool(params, "focused", args.Focused)
	addBool(params, "incognito", args.Incognito)
	addWindowBounds(params, args.Left, args.Top, args.Width, args.Height)
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandWindowsCreate,
		nil,
		params,
		args.TimeoutMS,
	)
}

func (s *Service) browserUpdateWindowHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args windowUpdateArgs,
) (*mcp.CallToolResult, error) {
	if err := validateWindowUpdateArgs(args); err != nil {
		return errorResult(err)
	}
	params := make(map[string]any)
	addString(params, "state", args.State)
	addBool(params, "focused", args.Focused)
	addBool(params, "drawAttention", args.DrawAttention)
	addWindowBounds(params, args.Left, args.Top, args.Width, args.Height)
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandWindowsUpdate,
		targetWithWindow(args.WindowID),
		params,
		args.TimeoutMS,
	)
}

func (s *Service) browserFocusWindowHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args windowTargetArgs,
) (*mcp.CallToolResult, error) {
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandWindowsFocus,
		targetWithWindow(args.WindowID),
		map[string]any{},
		args.TimeoutMS,
	)
}

func (s *Service) browserCloseWindowHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args windowCloseArgs,
) (*mcp.CallToolResult, error) {
	if !args.Confirm {
		if s.actionPolicy != nil {
			s.actionPolicy.AuditDenied(
				protocol.CommandWindowsClose,
				args.BrowserID,
				"",
				"confirmation_required",
			)
		}
		return errorResult(protocol.NewError(
			protocol.CodeConfirmationRequired,
			"closing a window and all of its tabs requires confirm: true",
			false,
		))
	}
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandWindowsClose,
		targetWithWindow(args.WindowID),
		map[string]any{},
		args.TimeoutMS,
	)
}

func requiredWindowID() mcp.ToolOption {
	return mcp.WithNumber(
		"windowId",
		mcp.Required(),
		mcp.Description("Browser window ID"),
		mcp.Min(0),
	)
}

func optionalWindowState() mcp.ToolOption {
	return mcp.WithString(
		"state",
		mcp.Description("Window display state"),
		mcp.Enum("normal", "minimized", "maximized", "fullscreen"),
	)
}

func windowCreateToolOptions() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithDescription("Create a normal or popup browser window"),
		optionalBrowserID(),
		mcp.WithArray("urls", mcp.Description("Initial URLs, one per tab")),
		mcp.WithString("type", mcp.Description("Window type"), mcp.Enum("normal", "popup")),
		optionalWindowState(),
		mcp.WithBoolean("focused", mcp.Description("Focus the new window")),
		mcp.WithBoolean("incognito", mcp.Description("Create an incognito window")),
		mcp.WithNumber("left", mcp.Description("Left screen coordinate")),
		mcp.WithNumber("top", mcp.Description("Top screen coordinate")),
		mcp.WithNumber("width", mcp.Description("Window width"), mcp.Min(1)),
		mcp.WithNumber("height", mcp.Description("Window height"), mcp.Min(1)),
		optionalTimeout(),
	}
}

func windowUpdateToolOptions() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithDescription("Update browser window bounds, focus, or display state"),
		optionalBrowserID(),
		requiredWindowID(),
		optionalWindowState(),
		mcp.WithBoolean("focused", mcp.Description("Focus or unfocus the window")),
		mcp.WithBoolean("drawAttention", mcp.Description("Request user attention")),
		mcp.WithNumber("left", mcp.Description("Left screen coordinate")),
		mcp.WithNumber("top", mcp.Description("Top screen coordinate")),
		mcp.WithNumber("width", mcp.Description("Window width"), mcp.Min(1)),
		mcp.WithNumber("height", mcp.Description("Window height"), mcp.Min(1)),
		optionalTimeout(),
	}
}

func targetWithWindow(windowID int) *protocol.Target {
	return &protocol.Target{WindowID: &windowID}
}

func addWindowBounds(params map[string]any, left, top, width, height *int) {
	addInt(params, "left", left)
	addInt(params, "top", top)
	addInt(params, "width", width)
	addInt(params, "height", height)
}

func addString(params map[string]any, name, value string) {
	if value != "" {
		params[name] = value
	}
}

func addBool(params map[string]any, name string, value *bool) {
	if value != nil {
		params[name] = *value
	}
}

func addInt(params map[string]any, name string, value *int) {
	if value != nil {
		params[name] = *value
	}
}

func validateWindowCreateArgs(args windowCreateArgs) *protocol.Error {
	if args.Type != "" && args.Type != "normal" && args.Type != "popup" {
		return invalidWindowArgs("type must be normal or popup")
	}
	if len(args.URLs) > 50 {
		return invalidWindowArgs("urls must contain at most 50 entries")
	}
	for _, url := range args.URLs {
		if strings.TrimSpace(url) == "" {
			return invalidWindowArgs("urls must not contain empty values")
		}
	}
	return validateWindowConfiguration(args.State, args.Left, args.Top, args.Width, args.Height)
}

func validateWindowUpdateArgs(args windowUpdateArgs) *protocol.Error {
	if args.State == "" && args.Focused == nil && args.DrawAttention == nil &&
		args.Left == nil && args.Top == nil && args.Width == nil && args.Height == nil {
		return invalidWindowArgs("at least one window update is required")
	}
	return validateWindowConfiguration(args.State, args.Left, args.Top, args.Width, args.Height)
}

func validateWindowConfiguration(state string, left, top, width, height *int) *protocol.Error {
	if state != "" && state != "normal" && state != "minimized" &&
		state != "maximized" && state != "fullscreen" {
		return invalidWindowArgs("invalid window state")
	}
	if (width != nil && *width < 1) || (height != nil && *height < 1) {
		return invalidWindowArgs("window width and height must be positive")
	}
	if state != "" && state != "normal" &&
		(left != nil || top != nil || width != nil || height != nil) {
		return invalidWindowArgs("window bounds can only be combined with the normal state")
	}
	return nil
}

func invalidWindowArgs(message string) *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}
