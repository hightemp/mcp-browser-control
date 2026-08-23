package tools

import (
	"context"
	"strings"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type tabCreateArgs struct {
	BrowserID string `json:"browserId,omitempty"`
	WindowID  *int   `json:"windowId,omitempty"`
	URL       string `json:"url,omitempty"`
	Index     *int   `json:"index,omitempty"`
	Active    *bool  `json:"active,omitempty"`
	Pinned    *bool  `json:"pinned,omitempty"`
	TimeoutMS *int   `json:"timeoutMs,omitempty"`
}

type tabNavigateArgs struct {
	BrowserID string `json:"browserId,omitempty"`
	TabID     *int   `json:"tabId,omitempty"`
	URL       string `json:"url"`
	TimeoutMS *int   `json:"timeoutMs,omitempty"`
}

type tabReloadArgs struct {
	BrowserID   string `json:"browserId,omitempty"`
	TabID       *int   `json:"tabId,omitempty"`
	BypassCache *bool  `json:"bypassCache,omitempty"`
	TimeoutMS   *int   `json:"timeoutMs,omitempty"`
}

type tabMoveArgs struct {
	BrowserID string `json:"browserId,omitempty"`
	TabID     *int   `json:"tabId,omitempty"`
	WindowID  *int   `json:"windowId,omitempty"`
	Index     *int   `json:"index"`
	TimeoutMS *int   `json:"timeoutMs,omitempty"`
}

type tabBooleanArgs struct {
	BrowserID string `json:"browserId,omitempty"`
	TabID     *int   `json:"tabId,omitempty"`
	Pinned    *bool  `json:"pinned,omitempty"`
	Muted     *bool  `json:"muted,omitempty"`
	TimeoutMS *int   `json:"timeoutMs,omitempty"`
}

type tabZoomArgs struct {
	BrowserID string  `json:"browserId,omitempty"`
	TabID     *int    `json:"tabId,omitempty"`
	Factor    float64 `json:"factor"`
	TimeoutMS *int    `json:"timeoutMs,omitempty"`
}

func (s *Service) registerTabTools(mcpServer *server.MCPServer) {
	for _, registration := range []struct {
		tool    mcp.Tool
		handler server.ToolHandlerFunc
	}{
		{
			tool: mcp.NewTool(
				"browser_get_tab",
				mcp.WithDescription("Get a tab by ID or use the selected or active tab"),
				optionalBrowserID(), optionalTabID(), optionalTimeout(),
			),
			handler: mcp.NewTypedToolHandler(s.browserGetTabHandler),
		},
		{
			tool:    mcp.NewTool("browser_create_tab", tabCreateToolOptions()...),
			handler: mcp.NewTypedToolHandler(s.browserCreateTabHandler),
		},
		{
			tool: mcp.NewTool(
				"browser_activate_tab",
				mcp.WithDescription("Activate a tab by ID or use the selected or active tab"),
				optionalBrowserID(), optionalTabID(), optionalTimeout(),
			),
			handler: mcp.NewTypedToolHandler(s.browserActivateTabHandler),
		},
		{
			tool: mcp.NewTool(
				"browser_navigate_tab",
				mcp.WithDescription("Navigate a tab to a URL"),
				optionalBrowserID(), optionalTabID(),
				mcp.WithString("url", mcp.Required(), mcp.Description("Destination URL"), mcp.MinLength(1)),
				optionalTimeout(),
			),
			handler: mcp.NewTypedToolHandler(s.browserNavigateTabHandler),
		},
		{
			tool: mcp.NewTool(
				"browser_reload_tab",
				mcp.WithDescription("Reload a tab"),
				optionalBrowserID(), optionalTabID(),
				mcp.WithBoolean("bypassCache", mcp.Description("Bypass the browser cache")),
				optionalTimeout(),
			),
			handler: mcp.NewTypedToolHandler(s.browserReloadTabHandler),
		},
		{
			tool:    simpleTabTool("browser_stop_tab", "Stop loading a tab"),
			handler: mcp.NewTypedToolHandler(s.browserStopTabHandler),
		},
		{
			tool:    simpleTabTool("browser_go_back", "Navigate a tab backward in history"),
			handler: mcp.NewTypedToolHandler(s.browserGoBackHandler),
		},
		{
			tool:    simpleTabTool("browser_go_forward", "Navigate a tab forward in history"),
			handler: mcp.NewTypedToolHandler(s.browserGoForwardHandler),
		},
		{
			tool: mcp.NewTool(
				"browser_move_tab",
				mcp.WithDescription("Move a tab within or between windows"),
				optionalBrowserID(), optionalTabID(),
				mcp.WithNumber("windowId", mcp.Description("Destination window ID"), mcp.Min(0)),
				mcp.WithNumber("index", mcp.Required(), mcp.Description("Destination index; -1 appends"), mcp.Min(-1)),
				optionalTimeout(),
			),
			handler: mcp.NewTypedToolHandler(s.browserMoveTabHandler),
		},
		{
			tool:    simpleTabTool("browser_duplicate_tab", "Duplicate a tab"),
			handler: mcp.NewTypedToolHandler(s.browserDuplicateTabHandler),
		},
		{
			tool:    simpleTabTool("browser_close_tab", "Close a tab"),
			handler: mcp.NewTypedToolHandler(s.browserCloseTabHandler),
		},
		{
			tool: mcp.NewTool(
				"browser_pin_tab",
				mcp.WithDescription("Pin or unpin a tab"),
				optionalBrowserID(), optionalTabID(),
				mcp.WithBoolean("pinned", mcp.Required(), mcp.Description("Desired pinned state")),
				optionalTimeout(),
			),
			handler: mcp.NewTypedToolHandler(s.browserPinTabHandler),
		},
		{
			tool: mcp.NewTool(
				"browser_mute_tab",
				mcp.WithDescription("Mute or unmute a tab"),
				optionalBrowserID(), optionalTabID(),
				mcp.WithBoolean("muted", mcp.Required(), mcp.Description("Desired muted state")),
				optionalTimeout(),
			),
			handler: mcp.NewTypedToolHandler(s.browserMuteTabHandler),
		},
		{
			tool:    simpleTabTool("browser_get_tab_zoom", "Get a tab zoom factor"),
			handler: mcp.NewTypedToolHandler(s.browserGetTabZoomHandler),
		},
		{
			tool: mcp.NewTool(
				"browser_set_tab_zoom",
				mcp.WithDescription("Set a tab zoom factor"),
				optionalBrowserID(), optionalTabID(),
				mcp.WithNumber("factor", mcp.Required(), mcp.Description("Zoom factor"), mcp.Min(0.25), mcp.Max(5)),
				optionalTimeout(),
			),
			handler: mcp.NewTypedToolHandler(s.browserSetTabZoomHandler),
		},
	} {
		mcpServer.AddTool(registration.tool, registration.handler)
	}
}

func (s *Service) browserGetTabHandler(ctx context.Context, _ mcp.CallToolRequest, args targetedArgs) (*mcp.CallToolResult, error) {
	return s.send(ctx, args.BrowserID, protocol.CommandTabsGet, targetWithTab(args.TabID), map[string]any{}, args.TimeoutMS)
}

func (s *Service) browserCreateTabHandler(ctx context.Context, _ mcp.CallToolRequest, args tabCreateArgs) (*mcp.CallToolResult, error) {
	if args.URL != "" && strings.TrimSpace(args.URL) == "" {
		return errorResult(protocol.NewError(protocol.CodeInvalidMessage, "url must not be empty", false))
	}
	params := make(map[string]any)
	addString(params, "url", args.URL)
	addInt(params, "windowId", args.WindowID)
	addInt(params, "index", args.Index)
	addBool(params, "active", args.Active)
	addBool(params, "pinned", args.Pinned)
	return s.send(ctx, args.BrowserID, protocol.CommandTabsCreate, nil, params, args.TimeoutMS)
}

func (s *Service) browserActivateTabHandler(ctx context.Context, _ mcp.CallToolRequest, args targetedArgs) (*mcp.CallToolResult, error) {
	return s.send(ctx, args.BrowserID, protocol.CommandTabsActivate, targetWithTab(args.TabID), map[string]any{}, args.TimeoutMS)
}

func (s *Service) browserNavigateTabHandler(ctx context.Context, _ mcp.CallToolRequest, args tabNavigateArgs) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(args.URL) == "" {
		return errorResult(protocol.NewError(protocol.CodeInvalidMessage, "url is required", false))
	}
	return s.send(ctx, args.BrowserID, protocol.CommandTabsNavigate, targetWithTab(args.TabID), map[string]any{"url": args.URL}, args.TimeoutMS)
}

func (s *Service) browserReloadTabHandler(ctx context.Context, _ mcp.CallToolRequest, args tabReloadArgs) (*mcp.CallToolResult, error) {
	params := make(map[string]any)
	addBool(params, "bypassCache", args.BypassCache)
	return s.send(ctx, args.BrowserID, protocol.CommandTabsReload, targetWithTab(args.TabID), params, args.TimeoutMS)
}

func (s *Service) browserStopTabHandler(ctx context.Context, _ mcp.CallToolRequest, args targetedArgs) (*mcp.CallToolResult, error) {
	return s.send(ctx, args.BrowserID, protocol.CommandTabsStop, targetWithTab(args.TabID), map[string]any{}, args.TimeoutMS)
}

func (s *Service) browserGoBackHandler(ctx context.Context, _ mcp.CallToolRequest, args targetedArgs) (*mcp.CallToolResult, error) {
	return s.send(ctx, args.BrowserID, protocol.CommandTabsBack, targetWithTab(args.TabID), map[string]any{}, args.TimeoutMS)
}

func (s *Service) browserGoForwardHandler(ctx context.Context, _ mcp.CallToolRequest, args targetedArgs) (*mcp.CallToolResult, error) {
	return s.send(ctx, args.BrowserID, protocol.CommandTabsForward, targetWithTab(args.TabID), map[string]any{}, args.TimeoutMS)
}

func (s *Service) browserMoveTabHandler(ctx context.Context, _ mcp.CallToolRequest, args tabMoveArgs) (*mcp.CallToolResult, error) {
	if args.Index == nil || *args.Index < -1 {
		return errorResult(protocol.NewError(protocol.CodeInvalidMessage, "index must be at least -1", false))
	}
	params := map[string]any{"index": *args.Index}
	addInt(params, "windowId", args.WindowID)
	return s.send(ctx, args.BrowserID, protocol.CommandTabsMove, targetWithTab(args.TabID), params, args.TimeoutMS)
}

func (s *Service) browserDuplicateTabHandler(ctx context.Context, _ mcp.CallToolRequest, args targetedArgs) (*mcp.CallToolResult, error) {
	return s.send(ctx, args.BrowserID, protocol.CommandTabsDuplicate, targetWithTab(args.TabID), map[string]any{}, args.TimeoutMS)
}

func (s *Service) browserCloseTabHandler(ctx context.Context, _ mcp.CallToolRequest, args targetedArgs) (*mcp.CallToolResult, error) {
	return s.send(ctx, args.BrowserID, protocol.CommandTabsClose, targetWithTab(args.TabID), map[string]any{}, args.TimeoutMS)
}

func (s *Service) browserPinTabHandler(ctx context.Context, _ mcp.CallToolRequest, args tabBooleanArgs) (*mcp.CallToolResult, error) {
	if args.Pinned == nil {
		return errorResult(protocol.NewError(protocol.CodeInvalidMessage, "pinned is required", false))
	}
	return s.send(ctx, args.BrowserID, protocol.CommandTabsPin, targetWithTab(args.TabID), map[string]any{"pinned": *args.Pinned}, args.TimeoutMS)
}

func (s *Service) browserMuteTabHandler(ctx context.Context, _ mcp.CallToolRequest, args tabBooleanArgs) (*mcp.CallToolResult, error) {
	if args.Muted == nil {
		return errorResult(protocol.NewError(protocol.CodeInvalidMessage, "muted is required", false))
	}
	return s.send(ctx, args.BrowserID, protocol.CommandTabsMute, targetWithTab(args.TabID), map[string]any{"muted": *args.Muted}, args.TimeoutMS)
}

func (s *Service) browserGetTabZoomHandler(ctx context.Context, _ mcp.CallToolRequest, args targetedArgs) (*mcp.CallToolResult, error) {
	return s.send(ctx, args.BrowserID, protocol.CommandTabsGetZoom, targetWithTab(args.TabID), map[string]any{}, args.TimeoutMS)
}

func (s *Service) browserSetTabZoomHandler(ctx context.Context, _ mcp.CallToolRequest, args tabZoomArgs) (*mcp.CallToolResult, error) {
	if args.Factor < 0.25 || args.Factor > 5 {
		return errorResult(protocol.NewError(protocol.CodeInvalidMessage, "factor must be between 0.25 and 5", false))
	}
	return s.send(ctx, args.BrowserID, protocol.CommandTabsSetZoom, targetWithTab(args.TabID), map[string]any{"factor": args.Factor}, args.TimeoutMS)
}

func tabCreateToolOptions() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithDescription("Create a tab in the current or specified window"),
		optionalBrowserID(),
		mcp.WithNumber("windowId", mcp.Description("Destination window ID"), mcp.Min(0)),
		mcp.WithString("url", mcp.Description("Initial URL"), mcp.MinLength(1)),
		mcp.WithNumber("index", mcp.Description("Insertion index"), mcp.Min(0)),
		mcp.WithBoolean("active", mcp.Description("Activate the new tab")),
		mcp.WithBoolean("pinned", mcp.Description("Pin the new tab")),
		optionalTimeout(),
	}
}

func simpleTabTool(name, description string) mcp.Tool {
	return mcp.NewTool(
		name,
		mcp.WithDescription(description),
		optionalBrowserID(),
		optionalTabID(),
		optionalTimeout(),
	)
}
