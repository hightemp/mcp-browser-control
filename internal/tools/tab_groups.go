package tools

import (
	"context"
	"strings"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const maxTabGroupSize = 100

type tabGroupArgs struct {
	BrowserID string `json:"browserId,omitempty"`
	TabIDs    []int  `json:"tabIds"`
	GroupID   *int   `json:"groupId,omitempty"`
	WindowID  *int   `json:"windowId,omitempty"`
	TimeoutMS *int   `json:"timeoutMs,omitempty"`
}

type tabUngroupArgs struct {
	BrowserID string `json:"browserId,omitempty"`
	TabIDs    []int  `json:"tabIds"`
	TimeoutMS *int   `json:"timeoutMs,omitempty"`
}

type tabGroupUpdateArgs struct {
	BrowserID string  `json:"browserId,omitempty"`
	GroupID   int     `json:"groupId"`
	Title     *string `json:"title,omitempty"`
	Color     string  `json:"color,omitempty"`
	Collapsed *bool   `json:"collapsed,omitempty"`
	TimeoutMS *int    `json:"timeoutMs,omitempty"`
}

type recentlyClosedArgs struct {
	BrowserID  string `json:"browserId,omitempty"`
	MaxResults *int   `json:"maxResults,omitempty"`
	TimeoutMS  *int   `json:"timeoutMs,omitempty"`
}

type restoreSessionArgs struct {
	BrowserID string  `json:"browserId,omitempty"`
	SessionID *string `json:"sessionId,omitempty"`
	TimeoutMS *int    `json:"timeoutMs,omitempty"`
}

func (s *Service) registerTabGroupAndSessionTools(mcpServer *server.MCPServer) {
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_group_tabs",
			mcp.WithDescription("Add tabs to a new or existing browser tab group"),
			optionalBrowserID(),
			tabIDsOption(),
			mcp.WithNumber("groupId", mcp.Description("Existing destination group ID"), mcp.Min(0)),
			mcp.WithNumber("windowId", mcp.Description("Window for a newly created group"), mcp.Min(0)),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserGroupTabsHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_ungroup_tabs",
			mcp.WithDescription("Remove tabs from their browser tab groups"),
			optionalBrowserID(),
			tabIDsOption(),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserUngroupTabsHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_update_tab_group",
			mcp.WithDescription("Update the title, color, or collapsed state of a tab group"),
			optionalBrowserID(),
			mcp.WithNumber("groupId", mcp.Required(), mcp.Description("Browser tab group ID"), mcp.Min(0)),
			mcp.WithString("title", mcp.Description("Group title; an empty string clears it")),
			mcp.WithString(
				"color",
				mcp.Description("Group color"),
				mcp.Enum(tabGroupColors()...),
			),
			mcp.WithBoolean("collapsed", mcp.Description("Collapse or expand the group")),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserUpdateTabGroupHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_get_recently_closed",
			mcp.WithDescription("List recently closed tabs and windows"),
			optionalBrowserID(),
			mcp.WithNumber(
				"maxResults",
				mcp.Description("Maximum sessions to return"),
				mcp.Min(1),
				mcp.Max(25),
			),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserGetRecentlyClosedHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_restore_session",
			mcp.WithDescription("Restore a recently closed tab or window"),
			optionalBrowserID(),
			mcp.WithString(
				"sessionId",
				mcp.Description("Session ID; omit to restore the most recently closed entry"),
				mcp.MinLength(1),
			),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserRestoreSessionHandler),
	)
}

func (s *Service) browserGroupTabsHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args tabGroupArgs,
) (*mcp.CallToolResult, error) {
	if err := validateTabIDs(args.TabIDs); err != nil {
		return errorResult(err)
	}
	if args.GroupID != nil && args.WindowID != nil {
		return errorResult(protocol.NewError(
			protocol.CodeInvalidMessage,
			"groupId and windowId cannot be used together",
			false,
		))
	}
	if args.GroupID != nil && *args.GroupID < 0 {
		return errorResult(protocol.NewError(protocol.CodeInvalidMessage, "groupId must not be negative", false))
	}
	if args.WindowID != nil && *args.WindowID < 0 {
		return errorResult(protocol.NewError(protocol.CodeInvalidMessage, "windowId must not be negative", false))
	}
	params := map[string]any{"tabIds": args.TabIDs}
	addInt(params, "groupId", args.GroupID)
	addInt(params, "windowId", args.WindowID)
	return s.send(ctx, args.BrowserID, protocol.CommandTabsGroup, nil, params, args.TimeoutMS)
}

func (s *Service) browserUngroupTabsHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args tabUngroupArgs,
) (*mcp.CallToolResult, error) {
	if err := validateTabIDs(args.TabIDs); err != nil {
		return errorResult(err)
	}
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandTabsUngroup,
		nil,
		map[string]any{"tabIds": args.TabIDs},
		args.TimeoutMS,
	)
}

func (s *Service) browserUpdateTabGroupHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args tabGroupUpdateArgs,
) (*mcp.CallToolResult, error) {
	if args.GroupID < 0 {
		return errorResult(protocol.NewError(protocol.CodeInvalidMessage, "groupId must not be negative", false))
	}
	if args.Title == nil && args.Color == "" && args.Collapsed == nil {
		return errorResult(protocol.NewError(protocol.CodeInvalidMessage, "at least one tab group update is required", false))
	}
	if args.Color != "" && !contains(tabGroupColors(), args.Color) {
		return errorResult(protocol.NewError(protocol.CodeInvalidMessage, "color is invalid", false))
	}
	params := map[string]any{"groupId": args.GroupID}
	if args.Title != nil {
		params["title"] = *args.Title
	}
	addString(params, "color", args.Color)
	addBool(params, "collapsed", args.Collapsed)
	return s.send(ctx, args.BrowserID, protocol.CommandTabGroupsUpdate, nil, params, args.TimeoutMS)
}

func (s *Service) browserGetRecentlyClosedHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args recentlyClosedArgs,
) (*mcp.CallToolResult, error) {
	if args.MaxResults != nil && (*args.MaxResults < 1 || *args.MaxResults > 25) {
		return errorResult(protocol.NewError(protocol.CodeInvalidMessage, "maxResults must be between 1 and 25", false))
	}
	params := make(map[string]any)
	addInt(params, "maxResults", args.MaxResults)
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandSessionsRecentlyClosed,
		nil,
		params,
		args.TimeoutMS,
	)
}

func (s *Service) browserRestoreSessionHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args restoreSessionArgs,
) (*mcp.CallToolResult, error) {
	params := make(map[string]any)
	if args.SessionID != nil {
		if strings.TrimSpace(*args.SessionID) == "" {
			return errorResult(protocol.NewError(protocol.CodeInvalidMessage, "sessionId must not be empty", false))
		}
		params["sessionId"] = *args.SessionID
	}
	return s.send(ctx, args.BrowserID, protocol.CommandSessionsRestore, nil, params, args.TimeoutMS)
}

func tabIDsOption() mcp.ToolOption {
	return mcp.WithArray(
		"tabIds",
		mcp.Required(),
		mcp.Description("Unique browser tab IDs"),
		mcp.Items(map[string]any{"type": "integer", "minimum": 0}),
		mcp.MinItems(1),
		mcp.MaxItems(maxTabGroupSize),
	)
}

func validateTabIDs(tabIDs []int) error {
	if len(tabIDs) == 0 || len(tabIDs) > maxTabGroupSize {
		return protocol.NewError(protocol.CodeInvalidMessage, "tabIds must contain between 1 and 100 entries", false)
	}
	seen := make(map[int]struct{}, len(tabIDs))
	for _, tabID := range tabIDs {
		if tabID < 0 {
			return protocol.NewError(protocol.CodeInvalidMessage, "tabIds must not contain negative IDs", false)
		}
		if _, exists := seen[tabID]; exists {
			return protocol.NewError(protocol.CodeInvalidMessage, "tabIds must be unique", false)
		}
		seen[tabID] = struct{}{}
	}
	return nil
}

func tabGroupColors() []string {
	return []string{"grey", "blue", "red", "yellow", "green", "pink", "purple", "cyan", "orange"}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
