// Package tools exposes browser registry and routed browser commands as MCP
// tools.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/artifacts"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/policy"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/redaction"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/router"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/selection"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	directSessionID       = "direct"
	maxToolTimeout        = 2 * time.Minute
	defaultMaxResultBytes = 2 << 20
)

// Service owns MCP browser tool handlers.
type Service struct {
	registry     *registry.Registry
	router       *router.Router
	selections   *selection.Store
	artifacts    *artifacts.Store
	actionPolicy *policy.Action
	auditLogger  *log.Logger
	resultLimits redaction.Limits
}

// ServiceOption configures a browser MCP tool service.
type ServiceOption func(*Service)

// WithArtifactStore enables tools that materialize binary browser results.
func WithArtifactStore(store *artifacts.Store) ServiceOption {
	return func(service *Service) {
		service.artifacts = store
	}
}

// WithMaxResultBytes changes the sanitized MCP tool and resource result bound.
func WithMaxResultBytes(maxBytes int64) ServiceOption {
	return func(service *Service) {
		if maxBytes > 0 && maxBytes <= 64<<20 {
			service.resultLimits = redaction.DefaultLimits(int(maxBytes))
		}
	}
}

// WithActionPolicy enables server-side URL and incognito checks before
// commands are sent to a browser extension.
func WithActionPolicy(actionPolicy *policy.Action) ServiceOption {
	return func(service *Service) {
		service.actionPolicy = actionPolicy
	}
}

// WithAuditLogger records safe metadata for sensitive browser operations.
func WithAuditLogger(logger *log.Logger) ServiceOption {
	return func(service *Service) {
		service.auditLogger = logger
	}
}

// NewService creates a browser MCP tool service.
func NewService(
	browserRegistry *registry.Registry,
	requestRouter *router.Router,
	selections *selection.Store,
	options ...ServiceOption,
) *Service {
	service := &Service{
		registry:     browserRegistry,
		router:       requestRouter,
		selections:   selections,
		resultLimits: redaction.DefaultLimits(defaultMaxResultBytes),
	}
	for _, option := range options {
		option(service)
	}
	return service
}

// Register adds browser resources, discovery tools, and browser command tools
// to an MCP server.
func (s *Service) Register(mcpServer *server.MCPServer) {
	s.registerResources(mcpServer)
	s.registerDiscoveryTools(mcpServer)
	s.registerBrowserCommandTools(mcpServer)
	s.registerBatchTool(mcpServer)
}

type emptyArgs struct{}

type browserIDArgs struct {
	BrowserID string `json:"browserId,omitempty"`
}

type browserSelectArgs struct {
	BrowserID string `json:"browserId"`
}

type browserSelectTabArgs struct {
	BrowserID string `json:"browserId,omitempty"`
	TabID     int    `json:"tabId"`
}

type browserRenameArgs struct {
	BrowserID   string `json:"browserId"`
	DisplayName string `json:"displayName"`
}

type targetedArgs struct {
	BrowserID  string `json:"browserId,omitempty"`
	TabID      *int   `json:"tabId,omitempty"`
	FrameID    *int   `json:"frameId,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
	TimeoutMS  *int   `json:"timeoutMs,omitempty"`
}

type getHTMLBySelectorArgs struct {
	BrowserID  string `json:"browserId,omitempty"`
	TabID      *int   `json:"tabId,omitempty"`
	FrameID    *int   `json:"frameId,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
	Selector   string `json:"selector"`
	TimeoutMS  *int   `json:"timeoutMs,omitempty"`
}

type pageHTMLArgs struct {
	BrowserID        string   `json:"browserId,omitempty"`
	TabID            *int     `json:"tabId,omitempty"`
	FrameID          *int     `json:"frameId,omitempty"`
	DocumentID       string   `json:"documentId,omitempty"`
	MaxChars         *int     `json:"maxChars,omitempty"`
	MaxDepth         *int     `json:"maxDepth,omitempty"`
	IncludeSelectors []string `json:"includeSelectors,omitempty"`
	ExcludeSelectors []string `json:"excludeSelectors,omitempty"`
	TimeoutMS        *int     `json:"timeoutMs,omitempty"`
}

type pageTextArgs struct {
	BrowserID        string   `json:"browserId,omitempty"`
	TabID            *int     `json:"tabId,omitempty"`
	FrameID          *int     `json:"frameId,omitempty"`
	DocumentID       string   `json:"documentId,omitempty"`
	MaxChars         *int     `json:"maxChars,omitempty"`
	Cursor           string   `json:"cursor,omitempty"`
	IncludeSelectors []string `json:"includeSelectors,omitempty"`
	ExcludeSelectors []string `json:"excludeSelectors,omitempty"`
	TimeoutMS        *int     `json:"timeoutMs,omitempty"`
}

type pageQueryArgs struct {
	BrowserID  string           `json:"browserId,omitempty"`
	TabID      *int             `json:"tabId,omitempty"`
	FrameID    *int             `json:"frameId,omitempty"`
	DocumentID string           `json:"documentId,omitempty"`
	Locator    protocol.Locator `json:"locator"`
	Cursor     string           `json:"cursor,omitempty"`
	Limit      *int             `json:"limit,omitempty"`
	TimeoutMS  *int             `json:"timeoutMs,omitempty"`
}

type pageElementArgs struct {
	BrowserID    string           `json:"browserId,omitempty"`
	TabID        *int             `json:"tabId,omitempty"`
	FrameID      *int             `json:"frameId,omitempty"`
	DocumentID   string           `json:"documentId,omitempty"`
	Locator      protocol.Locator `json:"locator"`
	MaxHTMLChars *int             `json:"maxHTMLChars,omitempty"`
	TimeoutMS    *int             `json:"timeoutMs,omitempty"`
}

type pageSnapshotArgs struct {
	BrowserID        string `json:"browserId,omitempty"`
	TabID            *int   `json:"tabId,omitempty"`
	FrameID          *int   `json:"frameId,omitempty"`
	DocumentID       string `json:"documentId,omitempty"`
	InteractiveOnly  *bool  `json:"interactiveOnly,omitempty"`
	MaxDepth         *int   `json:"maxDepth,omitempty"`
	MaxNodes         *int   `json:"maxNodes,omitempty"`
	IncludeShadowDOM *bool  `json:"includeShadowDOM,omitempty"`
	TimeoutMS        *int   `json:"timeoutMs,omitempty"`
}

type clickArgs struct {
	BrowserID         string                `json:"browserId,omitempty"`
	TabID             *int                  `json:"tabId,omitempty"`
	FrameID           *int                  `json:"frameId,omitempty"`
	DocumentID        string                `json:"documentId,omitempty"`
	Selector          *string               `json:"selector,omitempty"`
	Index             *int                  `json:"index,omitempty"`
	Coordinates       *protocol.Coordinates `json:"coordinates,omitempty"`
	Locator           *protocol.Locator     `json:"locator,omitempty"`
	Button            string                `json:"button,omitempty"`
	ClickCount        *int                  `json:"clickCount,omitempty"`
	Backend           string                `json:"backend,omitempty"`
	WaitForNavigation *bool                 `json:"waitForNavigation,omitempty"`
	TimeoutMS         *int                  `json:"timeoutMs,omitempty"`
}

type inputArgs struct {
	BrowserID         string                `json:"browserId,omitempty"`
	TabID             *int                  `json:"tabId,omitempty"`
	FrameID           *int                  `json:"frameId,omitempty"`
	DocumentID        string                `json:"documentId,omitempty"`
	Selector          *string               `json:"selector,omitempty"`
	Index             *int                  `json:"index,omitempty"`
	Coordinates       *protocol.Coordinates `json:"coordinates,omitempty"`
	Locator           *protocol.Locator     `json:"locator,omitempty"`
	Value             string                `json:"value"`
	Clear             *bool                 `json:"clear,omitempty"`
	Backend           string                `json:"backend,omitempty"`
	WaitForNavigation *bool                 `json:"waitForNavigation,omitempty"`
	TimeoutMS         *int                  `json:"timeoutMs,omitempty"`
}

type sendCommandArgs struct {
	BrowserID string         `json:"browserId,omitempty"`
	TabID     *int           `json:"tabId,omitempty"`
	Command   string         `json:"command"`
	Data      map[string]any `json:"data,omitempty"`
	TimeoutMS *int           `json:"timeoutMs,omitempty"`
}

type toolResponse struct {
	Success     bool             `json:"success"`
	BrowserID   string           `json:"browserId,omitempty"`
	Target      *protocol.Target `json:"target,omitempty"`
	Data        any              `json:"data,omitempty"`
	Warnings    []string         `json:"warnings"`
	NextCursor  string           `json:"nextCursor,omitempty"`
	ArtifactURI string           `json:"artifactUri,omitempty"`
	DurationMS  *float64         `json:"durationMs,omitempty"`
	Error       *protocol.Error  `json:"error,omitempty"`
	Timestamp   string           `json:"timestamp"`
}

func (s *Service) registerDiscoveryTools(mcpServer *server.MCPServer) {
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_list",
			mcp.WithDescription("List connected browser extension instances"),
		),
		mcp.NewTypedToolHandler(s.browserListHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_get",
			mcp.WithDescription("Get details for a connected browser instance"),
			optionalBrowserID(),
		),
		mcp.NewTypedToolHandler(s.browserGetHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_select",
			mcp.WithDescription("Select the default browser for the current MCP session"),
			mcp.WithString(
				"browserId",
				mcp.Required(),
				mcp.Description("Stable browser extension instance ID"),
			),
		),
		mcp.NewTypedToolHandler(s.browserSelectHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_get_selected",
			mcp.WithDescription("Get the browser selected for the current MCP session"),
		),
		mcp.NewTypedToolHandler(s.browserGetSelectedHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_select_tab",
			mcp.WithDescription("Select the default tab for one browser in the current MCP session"),
			optionalBrowserID(),
			mcp.WithNumber("tabId", mcp.Required(), mcp.Description("Browser tab ID")),
		),
		mcp.NewTypedToolHandler(s.browserSelectTabHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_rename",
			mcp.WithDescription("Rename a connected browser instance"),
			mcp.WithString("browserId", mcp.Required(), mcp.Description("Stable browser instance ID")),
			mcp.WithString("displayName", mcp.Required(), mcp.Description("New browser display name")),
		),
		mcp.NewTypedToolHandler(s.browserRenameHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_get_capabilities",
			mcp.WithDescription("Get capabilities and granted permissions for a browser"),
			optionalBrowserID(),
		),
		mcp.NewTypedToolHandler(s.browserGetCapabilitiesHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_ping",
			mcp.WithDescription("Check whether a browser extension responds"),
			optionalBrowserID(),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserPingHandler),
	)
}

func (s *Service) registerBrowserCommandTools(mcpServer *server.MCPServer) {
	s.registerWindowTools(mcpServer)
	s.registerTabTools(mcpServer)
	s.registerTabGroupAndSessionTools(mcpServer)
	s.registerInteractionTools(mcpServer)
	s.registerWaitTool(mcpServer)
	s.registerScreenshotTool(mcpServer)
	s.registerPrintToPDFTool(mcpServer)
	s.registerAccessibilityTool(mcpServer)
	s.registerEmulationTools(mcpServer)
	s.registerEvaluationTool(mcpServer)
	s.registerRawCDPTool(mcpServer)
	s.registerPerformanceTools(mcpServer)
	s.registerConsoleTools(mcpServer)
	s.registerNetworkTools(mcpServer)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_get_tabs",
			mcp.WithDescription("List tabs in one browser instance"),
			optionalBrowserID(),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserGetTabsHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_page_info",
			mcp.WithDescription("Get page, viewport, scroll, and frame metadata"),
			optionalBrowserID(),
			optionalTabID(),
			optionalFrameID(),
			optionalDocumentID(),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserPageInfoHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_get_html",
			mcp.WithDescription("Get bounded, redacted HTML from a browser page"),
			optionalBrowserID(),
			optionalTabID(),
			optionalFrameID(),
			optionalDocumentID(),
			mcp.WithNumber("maxChars", mcp.Description("Maximum returned characters"), mcp.Min(1), mcp.Max(1_000_000)),
			mcp.WithNumber("maxDepth", mcp.Description("Maximum serialized DOM depth"), mcp.Min(0), mcp.Max(200)),
			optionalSelectorArray("includeSelectors", "Only serialize elements matching these CSS selectors"),
			optionalSelectorArray("excludeSelectors", "Exclude elements matching these CSS selectors"),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserGetHTMLHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_get_html_by_selector",
			mcp.WithDescription("Get HTML for elements matching a CSS selector"),
			optionalBrowserID(),
			optionalTabID(),
			optionalFrameID(),
			optionalDocumentID(),
			mcp.WithString("selector", mcp.Required(), mcp.Description("CSS selector")),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserGetHTMLBySelectorHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_get_text",
			mcp.WithDescription("Get paginated normalized visible text with sensitive values redacted"),
			optionalBrowserID(),
			optionalTabID(),
			optionalFrameID(),
			optionalDocumentID(),
			mcp.WithNumber("maxChars", mcp.Description("Maximum returned characters"), mcp.Min(1), mcp.Max(1_000_000)),
			mcp.WithString("cursor", mcp.Description("Numeric cursor returned by a previous call")),
			optionalSelectorArray("includeSelectors", "Only read elements matching these CSS selectors"),
			optionalSelectorArray("excludeSelectors", "Exclude elements matching these CSS selectors"),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserGetTextHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_query",
			mcp.WithDescription("Query elements with a locator and return a bounded result page"),
			optionalBrowserID(),
			optionalTabID(),
			optionalFrameID(),
			optionalDocumentID(),
			requiredLocator(),
			mcp.WithString("cursor", mcp.Description("Numeric cursor returned by a previous call")),
			mcp.WithNumber("limit", mcp.Description("Maximum matching elements to return"), mcp.Min(1), mcp.Max(100)),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserQueryHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_get_element",
			mcp.WithDescription("Get normalized details for one strictly matched element"),
			optionalBrowserID(),
			optionalTabID(),
			optionalFrameID(),
			optionalDocumentID(),
			requiredLocator(),
			mcp.WithNumber("maxHTMLChars", mcp.Description("Maximum element HTML characters"), mcp.Min(1), mcp.Max(100_000)),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserGetElementHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_snapshot",
			mcp.WithDescription("Get a compact semantic tree with document-scoped element references"),
			optionalBrowserID(),
			optionalTabID(),
			optionalFrameID(),
			optionalDocumentID(),
			mcp.WithBoolean("interactiveOnly", mcp.Description("Include only actionable or focusable elements")),
			mcp.WithNumber("maxDepth", mcp.Description("Maximum DOM traversal depth"), mcp.Min(0), mcp.Max(50)),
			mcp.WithNumber("maxNodes", mcp.Description("Maximum semantic nodes"), mcp.Min(1), mcp.Max(5_000)),
			mcp.WithBoolean("includeShadowDOM", mcp.Description("Traverse open shadow roots")),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserSnapshotHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_click_element",
			mcp.WithDescription("Click one actionable element using a strict locator by default"),
			optionalBrowserID(),
			optionalTabID(),
			optionalFrameID(),
			optionalDocumentID(),
			mcp.WithString("selector", mcp.Description("CSS selector")),
			mcp.WithNumber("index", mcp.Description("Legacy zero-based CSS match index")),
			optionalCoordinates(),
			optionalLocator(),
			mcp.WithString("button", mcp.Description("Mouse button"), mcp.Enum("left", "middle", "right")),
			mcp.WithNumber("clickCount", mcp.Description("One or two clicks"), mcp.Min(1), mcp.Max(2)),
			mcp.WithString("backend", mcp.Description("Input backend"), mcp.Enum("auto", "content", "cdp")),
			mcp.WithBoolean("waitForNavigation", mcp.Description("Wait for navigation completion")),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserClickHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_input_data",
			mcp.WithDescription("Fill one actionable input using a strict locator by default"),
			optionalBrowserID(),
			optionalTabID(),
			optionalFrameID(),
			optionalDocumentID(),
			mcp.WithString("selector", mcp.Description("CSS selector")),
			mcp.WithString("value", mcp.Required(), mcp.Description("Value to enter")),
			mcp.WithNumber("index", mcp.Description("Legacy zero-based CSS match index")),
			optionalCoordinates(),
			optionalLocator(),
			mcp.WithBoolean(
				"clear",
				mcp.Description("Clear the field before entering the value"),
				mcp.DefaultBool(true),
			),
			mcp.WithString("backend", mcp.Description("Input backend"), mcp.Enum("auto", "content", "cdp")),
			mcp.WithBoolean("waitForNavigation", mcp.Description("Wait for navigation completion")),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserInputHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_send_command",
			mcp.WithDescription("Send an advanced command supported by the browser extension"),
			optionalBrowserID(),
			optionalTabID(),
			mcp.WithString("command", mcp.Required(), mcp.Description("Versioned extension command name")),
			mcp.WithObject("data", mcp.Description("Command parameters")),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserSendCommandHandler),
	)
}

func (s *Service) browserListHandler(
	_ context.Context,
	_ mcp.CallToolRequest,
	_ emptyArgs,
) (*mcp.CallToolResult, error) {
	browsers := s.registry.ListAll()
	return successResult("", map[string]any{
		"browsers":       browsers,
		"connectedCount": s.registry.Count(),
	})
}

func (s *Service) browserGetHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args browserIDArgs,
) (*mcp.CallToolResult, error) {
	if args.BrowserID != "" {
		browser, ok := s.registry.Get(args.BrowserID)
		if !ok {
			return errorResult(protocol.NewError(protocol.CodeBrowserNotFound, "browser not found", false))
		}
		return successResult(args.BrowserID, browser)
	}
	browserID, err := s.resolveBrowser(ctx, args.BrowserID)
	if err != nil {
		return errorResult(err)
	}
	browser, ok := s.registry.Get(browserID)
	if !ok || !browser.Connected {
		if ok {
			return errorResult(protocol.NewError(protocol.CodeBrowserDisconnected, "browser is disconnected", true))
		}
		return errorResult(protocol.NewError(protocol.CodeBrowserNotFound, "browser not found", false))
	}
	return successResult(browserID, browser)
}

func (s *Service) browserSelectHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args browserSelectArgs,
) (*mcp.CallToolResult, error) {
	browser, ok := s.registry.Get(args.BrowserID)
	if !ok {
		return errorResult(protocol.NewError(protocol.CodeBrowserNotFound, "browser not found", false))
	}
	if !browser.Connected {
		return errorResult(protocol.NewError(protocol.CodeBrowserDisconnected, "browser is disconnected", true))
	}
	if err := s.selections.Set(sessionID(ctx), args.BrowserID); err != nil {
		return errorResult(err)
	}
	return successResult(args.BrowserID, map[string]any{
		"selected": true,
		"browser":  browser,
	})
}

func (s *Service) browserGetSelectedHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	_ emptyArgs,
) (*mcp.CallToolResult, error) {
	selected, ok := s.selections.Get(sessionID(ctx))
	if !ok || selected.BrowserID == "" {
		if ok {
			return successResult("", map[string]any{
				"selected":  false,
				"selection": selected,
			})
		}
		return successResult("", map[string]any{"selected": false})
	}
	browser, known := s.registry.Get(selected.BrowserID)
	connected := known && browser.Connected
	return successResult(selected.BrowserID, map[string]any{
		"selected":  true,
		"connected": connected,
		"selection": selected,
		"browser":   browser,
	})
}

func (s *Service) browserSelectTabHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args browserSelectTabArgs,
) (*mcp.CallToolResult, error) {
	browserID, err := s.resolveBrowser(ctx, args.BrowserID)
	if err != nil {
		return errorResult(err)
	}
	if err := s.selections.SetTab(sessionID(ctx), browserID, args.TabID); err != nil {
		return errorResult(err)
	}
	selection, _ := s.selections.GetTab(sessionID(ctx), browserID)
	return successResult(browserID, map[string]any{
		"selected": true,
		"tab":      selection,
	})
}

func (s *Service) browserRenameHandler(
	_ context.Context,
	_ mcp.CallToolRequest,
	args browserRenameArgs,
) (*mcp.CallToolResult, error) {
	browser, err := s.registry.Rename(args.BrowserID, args.DisplayName)
	if err != nil {
		return errorResult(err)
	}
	return successResult(args.BrowserID, browser)
}

func (s *Service) browserGetCapabilitiesHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args browserIDArgs,
) (*mcp.CallToolResult, error) {
	browserID, err := s.resolveBrowser(ctx, args.BrowserID)
	if err != nil {
		return errorResult(err)
	}
	browser, ok := s.registry.Get(browserID)
	if !ok {
		return errorResult(protocol.NewError(protocol.CodeBrowserNotFound, "browser not found", false))
	}
	return successResult(browserID, map[string]any{
		"capabilities": browser.Capabilities,
		"permissions":  browser.Permissions,
		"browser":      browser.Browser,
	})
}

func (s *Service) browserPingHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args targetedArgs,
) (*mcp.CallToolResult, error) {
	return s.send(ctx, args.BrowserID, protocol.CommandBrowserPing, nil, map[string]any{}, args.TimeoutMS)
}

func (s *Service) browserGetTabsHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args targetedArgs,
) (*mcp.CallToolResult, error) {
	return s.send(ctx, args.BrowserID, protocol.CommandTabsList, nil, map[string]any{}, args.TimeoutMS)
}

func (s *Service) browserPageInfoHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args targetedArgs,
) (*mcp.CallToolResult, error) {
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandPageInfo,
		pageTarget(args.TabID, args.FrameID, args.DocumentID),
		map[string]any{},
		args.TimeoutMS,
	)
}

func (s *Service) browserGetHTMLHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args pageHTMLArgs,
) (*mcp.CallToolResult, error) {
	params := map[string]any{}
	putOptional(params, "maxChars", args.MaxChars)
	putOptional(params, "maxDepth", args.MaxDepth)
	if args.IncludeSelectors != nil {
		params["includeSelectors"] = args.IncludeSelectors
	}
	if args.ExcludeSelectors != nil {
		params["excludeSelectors"] = args.ExcludeSelectors
	}
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandPageGetHTML,
		pageTarget(args.TabID, args.FrameID, args.DocumentID),
		params,
		args.TimeoutMS,
	)
}

func (s *Service) browserGetHTMLBySelectorHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args getHTMLBySelectorArgs,
) (*mcp.CallToolResult, error) {
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandPageGetHTMLBySelector,
		pageTarget(args.TabID, args.FrameID, args.DocumentID),
		map[string]any{"selector": args.Selector},
		args.TimeoutMS,
	)
}

func (s *Service) browserGetTextHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args pageTextArgs,
) (*mcp.CallToolResult, error) {
	params := map[string]any{}
	putOptional(params, "maxChars", args.MaxChars)
	if args.Cursor != "" {
		params["cursor"] = args.Cursor
	}
	if args.IncludeSelectors != nil {
		params["includeSelectors"] = args.IncludeSelectors
	}
	if args.ExcludeSelectors != nil {
		params["excludeSelectors"] = args.ExcludeSelectors
	}
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandPageGetText,
		pageTarget(args.TabID, args.FrameID, args.DocumentID),
		params,
		args.TimeoutMS,
	)
}

func (s *Service) browserQueryHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args pageQueryArgs,
) (*mcp.CallToolResult, error) {
	target := pageTarget(args.TabID, args.FrameID, args.DocumentID)
	if err := args.Locator.Validate(target); err != nil {
		return errorResult(err)
	}
	params := map[string]any{"locator": args.Locator}
	if args.Cursor != "" {
		params["cursor"] = args.Cursor
	}
	putOptional(params, "limit", args.Limit)
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandPageQuery,
		target,
		params,
		args.TimeoutMS,
	)
}

func (s *Service) browserGetElementHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args pageElementArgs,
) (*mcp.CallToolResult, error) {
	target := pageTarget(args.TabID, args.FrameID, args.DocumentID)
	if err := args.Locator.Validate(target); err != nil {
		return errorResult(err)
	}
	params := map[string]any{"locator": args.Locator}
	putOptional(params, "maxHTMLChars", args.MaxHTMLChars)
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandPageGetElement,
		target,
		params,
		args.TimeoutMS,
	)
}

func (s *Service) browserSnapshotHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args pageSnapshotArgs,
) (*mcp.CallToolResult, error) {
	params := map[string]any{}
	putOptional(params, "interactiveOnly", args.InteractiveOnly)
	putOptional(params, "maxDepth", args.MaxDepth)
	putOptional(params, "maxNodes", args.MaxNodes)
	putOptional(params, "includeShadowDOM", args.IncludeShadowDOM)
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandPageSnapshot,
		pageTarget(args.TabID, args.FrameID, args.DocumentID),
		params,
		args.TimeoutMS,
	)
}

func (s *Service) browserClickHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args clickArgs,
) (*mcp.CallToolResult, error) {
	target := pageTarget(args.TabID, args.FrameID, args.DocumentID)
	if err := validateElementArgs(args.Selector, args.Coordinates, args.Locator, args.Index, target); err != nil {
		return errorResult(err)
	}
	if err := validateInteractionBackend(args.Backend); err != nil {
		return errorResult(err)
	}
	if args.Button != "" && args.Button != "left" && args.Button != "middle" && args.Button != "right" {
		return errorResult(invalidInteraction("button must be left, middle, or right"))
	}
	if args.ClickCount != nil && (*args.ClickCount < 1 || *args.ClickCount > 2) {
		return errorResult(invalidInteraction("clickCount must be one or two"))
	}

	params := make(map[string]any)
	if args.Selector != nil {
		params["selector"] = *args.Selector
	}
	if args.Index != nil {
		params["index"] = *args.Index
	}
	if args.Coordinates != nil {
		params["coordinates"] = args.Coordinates
	}
	if args.Locator != nil {
		params["locator"] = args.Locator
	}
	if args.Button != "" {
		params["button"] = args.Button
	}
	putOptional(params, "clickCount", args.ClickCount)
	if args.Backend != "" {
		params["backend"] = args.Backend
	}
	putOptional(params, "waitForNavigation", args.WaitForNavigation)
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandPageClick,
		target,
		params,
		args.TimeoutMS,
	)
}

func (s *Service) browserInputHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args inputArgs,
) (*mcp.CallToolResult, error) {
	target := pageTarget(args.TabID, args.FrameID, args.DocumentID)
	if err := validateElementArgs(args.Selector, args.Coordinates, args.Locator, args.Index, target); err != nil {
		return errorResult(err)
	}
	if err := validateInteractionBackend(args.Backend); err != nil {
		return errorResult(err)
	}
	params := map[string]any{
		"value": args.Value,
		"clear": true,
	}
	if args.Selector != nil {
		params["selector"] = *args.Selector
	}
	if args.Index != nil {
		params["index"] = *args.Index
	}
	if args.Coordinates != nil {
		params["coordinates"] = args.Coordinates
	}
	if args.Locator != nil {
		params["locator"] = args.Locator
	}
	if args.Backend != "" {
		params["backend"] = args.Backend
	}
	putOptional(params, "waitForNavigation", args.WaitForNavigation)
	if args.Clear != nil {
		params["clear"] = *args.Clear
	}
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandPageFill,
		target,
		params,
		args.TimeoutMS,
	)
}

func (s *Service) browserSendCommandHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args sendCommandArgs,
) (*mcp.CallToolResult, error) {
	if args.Command == protocol.CommandRuntimeEvaluateIsolated ||
		args.Command == protocol.CommandCDPSendReadOnly ||
		args.Command == protocol.CommandPerformanceMetrics ||
		args.Command == protocol.CommandPerformanceCapture ||
		isDedicatedNetworkCommand(args.Command) {
		return errorResult(protocol.NewError(
			protocol.CodeInvalidCommand,
			"the command requires its dedicated MCP tool",
			false,
		))
	}
	return s.send(
		ctx,
		args.BrowserID,
		args.Command,
		targetWithTab(args.TabID),
		args.Data,
		args.TimeoutMS,
	)
}

func (s *Service) send(
	ctx context.Context,
	explicitBrowserID string,
	command string,
	target *protocol.Target,
	params any,
	timeoutMS *int,
) (*mcp.CallToolResult, error) {
	browserID, resolvedTarget, result, duration, err := s.sendRaw(
		ctx,
		explicitBrowserID,
		command,
		target,
		params,
		timeoutMS,
	)
	if err != nil {
		return errorResultWithDuration(err, duration)
	}
	sanitized, report, err := s.sanitizeBrowserResult(result)
	if err != nil {
		return errorResultWithDuration(err, duration)
	}
	return successResultWithTargetWarningsLimited(
		browserID,
		resolvedTarget,
		sanitized,
		duration,
		report.Warnings(),
		s.resultLimits.MaxOutputBytes,
	)
}

func (s *Service) sanitizeBrowserResult(
	result json.RawMessage,
) (json.RawMessage, redaction.Report, error) {
	sanitized, report, err := redaction.JSON(result, s.resultLimits)
	if err == nil {
		return sanitized, report, nil
	}
	if errors.Is(err, redaction.ErrInputTooLarge) || errors.Is(err, redaction.ErrOutputTooLarge) {
		return nil, report, protocol.NewError(
			protocol.CodePayloadTooLarge,
			"the sanitized browser result exceeds the configured MCP result limit",
			false,
		)
	}
	return nil, report, protocol.NewError(
		protocol.CodeInvalidMessage,
		"the browser returned an invalid result payload",
		false,
	)
}

func (s *Service) sendRaw(
	ctx context.Context,
	explicitBrowserID string,
	command string,
	target *protocol.Target,
	params any,
	timeoutMS *int,
) (string, *protocol.Target, json.RawMessage, time.Duration, error) {
	startedAt := time.Now()
	browserID, err := s.resolveBrowser(ctx, explicitBrowserID)
	if err != nil {
		return "", target, nil, time.Since(startedAt), err
	}
	if err := s.enforceCommandPolicy(command, browserID, params); err != nil {
		return browserID, target, nil, time.Since(startedAt), err
	}
	if commandUsesTab(command) {
		target, err = s.resolveTarget(ctx, browserID, target)
		if err != nil {
			return browserID, target, nil, time.Since(startedAt), err
		}
	}
	requestCtx, cancel, err := toolContext(ctx, timeoutMS)
	if err != nil {
		return browserID, target, nil, time.Since(startedAt), err
	}
	defer cancel()
	if s.actionPolicy != nil && command == protocol.CommandTabsCreate {
		if err := s.enforceTabCreationPolicy(requestCtx, browserID, params); err != nil {
			return browserID, target, nil, time.Since(startedAt), err
		}
	}
	if s.actionPolicy != nil && commandUsesTab(command) &&
		command != protocol.CommandTabsGet && command != protocol.CommandEmulationReset {
		if err := s.enforceTargetPolicy(requestCtx, browserID, command, target); err != nil {
			return browserID, target, nil, time.Since(startedAt), err
		}
	}

	result, err := s.router.Send(requestCtx, browserID, command, target, params)
	if err != nil {
		return browserID, target, nil, time.Since(startedAt), err
	}
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	return browserID, target, result, time.Since(startedAt), nil
}

func (s *Service) enforceTabCreationPolicy(
	ctx context.Context,
	browserID string,
	params any,
) error {
	values, _ := params.(map[string]any)
	windowID, explicitWindow := values["windowId"].(int)
	if explicitWindow {
		result, err := s.router.Send(
			ctx,
			browserID,
			protocol.CommandWindowsGet,
			targetWithWindow(windowID),
			map[string]any{},
		)
		if err != nil {
			return err
		}
		var payload struct {
			Window struct {
				Incognito *bool `json:"incognito"`
			} `json:"window"`
		}
		if err := json.Unmarshal(result, &payload); err != nil || payload.Window.Incognito == nil {
			return invalidPolicyMetadata("window")
		}
		if err := s.actionPolicy.CheckIncognito(
			protocol.CommandTabsCreate,
			browserID,
			*payload.Window.Incognito,
		); err != nil {
			return err
		}
		return nil
	}

	result, err := s.router.Send(
		ctx,
		browserID,
		protocol.CommandWindowsList,
		nil,
		map[string]any{},
	)
	if err != nil {
		return err
	}
	var payload struct {
		Windows []struct {
			Focused   bool  `json:"focused"`
			Incognito *bool `json:"incognito"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return invalidPolicyMetadata("window list")
	}
	for _, window := range payload.Windows {
		if window.Focused {
			if window.Incognito == nil {
				return invalidPolicyMetadata("focused window")
			}
			if err := s.actionPolicy.CheckIncognito(
				protocol.CommandTabsCreate,
				browserID,
				*window.Incognito,
			); err != nil {
				return err
			}
			return nil
		}
	}
	return invalidPolicyMetadata("focused window")
}

func (s *Service) enforceCommandPolicy(command, browserID string, params any) error {
	if s.actionPolicy == nil {
		return nil
	}
	values, ok := params.(map[string]any)
	if !ok {
		return nil
	}
	switch command {
	case protocol.CommandTabsCreate, protocol.CommandTabsNavigate:
		if destination, ok := values["url"].(string); ok && destination != "" {
			if err := s.actionPolicy.CheckURL(command, browserID, destination); err != nil {
				return err
			}
		}
	case protocol.CommandWindowsCreate:
		if incognito, ok := values["incognito"].(bool); ok {
			if err := s.actionPolicy.CheckIncognito(command, browserID, incognito); err != nil {
				return err
			}
		}
		if destinations, ok := values["urls"].([]string); ok {
			for _, destination := range destinations {
				if err := s.actionPolicy.CheckURL(command, browserID, destination); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Service) enforceTargetPolicy(
	ctx context.Context,
	browserID string,
	command string,
	target *protocol.Target,
) error {
	result, err := s.router.Send(ctx, browserID, protocol.CommandTabsGet, target, map[string]any{})
	if err != nil {
		return err
	}
	var payload struct {
		Tab struct {
			URL       string `json:"url"`
			Incognito *bool  `json:"incognito"`
		} `json:"tab"`
	}
	if err := json.Unmarshal(result, &payload); err != nil ||
		payload.Tab.URL == "" || payload.Tab.Incognito == nil {
		return invalidPolicyMetadata("tab")
	}
	if err := s.actionPolicy.CheckIncognito(command, browserID, *payload.Tab.Incognito); err != nil {
		return err
	}
	if err := s.actionPolicy.CheckURL(command, browserID, payload.Tab.URL); err != nil {
		return err
	}
	return nil
}

func invalidPolicyMetadata(kind string) *protocol.Error {
	return protocol.NewError(
		protocol.CodeInvalidMessage,
		"the browser returned invalid "+kind+" metadata for action policy",
		false,
	)
}

func (s *Service) resolveTarget(
	ctx context.Context,
	browserID string,
	target *protocol.Target,
) (*protocol.Target, error) {
	var explicitTabID *int
	if target != nil {
		explicitTabID = target.TabID
	}
	tabID, err := s.selections.ResolveTab(sessionID(ctx), browserID, explicitTabID)
	if err != nil {
		return nil, err
	}
	if target == nil && tabID == nil {
		return nil, nil
	}
	resolved := &protocol.Target{BrowserID: browserID, TabID: tabID}
	if target != nil {
		*resolved = *target
		if resolved.BrowserID == "" {
			resolved.BrowserID = browserID
		}
		resolved.TabID = tabID
	}
	return protocol.ResolveTarget(browserID, resolved)
}

func commandUsesTab(command string) bool {
	switch command {
	case protocol.CommandBrowserPing,
		protocol.CommandWindowsList,
		protocol.CommandWindowsGet,
		protocol.CommandWindowsCreate,
		protocol.CommandWindowsUpdate,
		protocol.CommandWindowsFocus,
		protocol.CommandWindowsClose,
		protocol.CommandTabsCreate,
		protocol.CommandTabsList,
		protocol.CommandTabsGroup,
		protocol.CommandTabsUngroup,
		protocol.CommandTabGroupsUpdate,
		protocol.CommandSessionsRecentlyClosed,
		protocol.CommandSessionsRestore:
		return false
	default:
		return true
	}
}

func (s *Service) resolveBrowser(ctx context.Context, explicitBrowserID string) (string, error) {
	return s.selections.Resolve(sessionID(ctx), explicitBrowserID, s.registry.List())
}

func sessionID(ctx context.Context) string {
	session := server.ClientSessionFromContext(ctx)
	if session == nil || session.SessionID() == "" {
		return directSessionID
	}
	return session.SessionID()
}

func toolContext(ctx context.Context, timeoutMS *int) (context.Context, context.CancelFunc, error) {
	if timeoutMS == nil {
		requestCtx, cancel := context.WithCancel(ctx)
		return requestCtx, cancel, nil
	}
	timeout := time.Duration(*timeoutMS) * time.Millisecond
	if timeout <= 0 || timeout > maxToolTimeout {
		return nil, nil, protocol.NewError(
			protocol.CodeInvalidMessage,
			fmt.Sprintf("timeoutMs must be between 1 and %d", maxToolTimeout.Milliseconds()),
			false,
		)
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	return requestCtx, cancel, nil
}

func targetWithTab(tabID *int) *protocol.Target {
	if tabID == nil {
		return nil
	}
	return &protocol.Target{TabID: tabID}
}

func pageTarget(tabID, frameID *int, documentID string) *protocol.Target {
	if tabID == nil && frameID == nil && documentID == "" {
		return nil
	}
	return &protocol.Target{
		TabID:      tabID,
		FrameID:    frameID,
		DocumentID: documentID,
	}
}

func validateElementArgs(
	selector *string,
	coordinates *protocol.Coordinates,
	locator *protocol.Locator,
	index *int,
	target *protocol.Target,
) error {
	addressCount := 0
	if selector != nil {
		addressCount++
		if strings.TrimSpace(*selector) == "" {
			return protocol.NewError(protocol.CodeInvalidMessage, "selector must not be empty", false)
		}
	}
	if coordinates != nil {
		addressCount++
		if err := coordinates.Validate(); err != nil {
			return err
		}
	}
	if locator != nil {
		addressCount++
		if err := locator.Validate(target); err != nil {
			return err
		}
	}
	if addressCount != 1 {
		return protocol.NewError(
			protocol.CodeInvalidMessage,
			"exactly one of selector, coordinates, or locator must be provided",
			false,
		)
	}
	if index != nil && (*index < 0 || selector == nil) {
		return protocol.NewError(
			protocol.CodeInvalidMessage,
			"index must be a non-negative integer and can only be used with selector",
			false,
		)
	}
	return nil
}

func putOptional[T any](params map[string]any, name string, value *T) {
	if value != nil {
		params[name] = *value
	}
}

func successResult(browserID string, data any) (*mcp.CallToolResult, error) {
	return successResultWithTarget(browserID, nil, data, 0)
}

func successResultWithTarget(
	browserID string,
	target *protocol.Target,
	data any,
	duration time.Duration,
) (*mcp.CallToolResult, error) {
	return successResultWithTargetWarnings(browserID, target, data, duration, nil)
}

func successResultWithTargetWarnings(
	browserID string,
	target *protocol.Target,
	data any,
	duration time.Duration,
	additionalWarnings []string,
) (*mcp.CallToolResult, error) {
	return successResultWithTargetWarningsLimited(
		browserID,
		target,
		data,
		duration,
		additionalWarnings,
		0,
	)
}

func successResultWithTargetWarningsLimited(
	browserID string,
	target *protocol.Target,
	data any,
	duration time.Duration,
	additionalWarnings []string,
	maxBytes int,
) (*mcp.CallToolResult, error) {
	warnings, nextCursor, artifactURI := resultHints(data)
	warnings = appendUnique(warnings, additionalWarnings...)
	response := toolResponse{
		Success:     true,
		BrowserID:   browserID,
		Target:      resolvedResultTarget(browserID, target),
		Data:        data,
		Warnings:    warnings,
		NextCursor:  nextCursor,
		ArtifactURI: artifactURI,
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	if duration > 0 {
		durationMS := float64(duration.Microseconds()) / 1000
		response.DurationMS = &durationMS
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("marshal MCP tool result: %w", err)
	}
	if maxBytes > 0 && len(payload) > maxBytes {
		return errorResult(protocol.NewError(
			protocol.CodePayloadTooLarge,
			"the sanitized MCP tool result exceeds the configured result limit",
			false,
		))
	}
	return mcp.NewToolResultText(string(payload)), nil
}

func errorResult(err error) (*mcp.CallToolResult, error) {
	return errorResultWithDuration(err, 0)
}

func errorResultWithDuration(err error, duration time.Duration) (*mcp.CallToolResult, error) {
	resultError := sanitizedProtocolError(err)
	response := toolResponse{
		Success:   false,
		Target:    resolvedResultTarget("", resultError.Target),
		Warnings:  []string{},
		Error:     resultError,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if response.Target != nil {
		response.BrowserID = response.Target.BrowserID
	}
	if duration > 0 {
		durationMS := float64(duration.Microseconds()) / 1000
		response.DurationMS = &durationMS
	}
	payload, marshalErr := json.Marshal(response)
	if marshalErr != nil {
		return nil, fmt.Errorf("marshal MCP tool error: %w", marshalErr)
	}
	return mcp.NewToolResultError(string(payload)), nil
}

func sanitizedProtocolError(err error) *protocol.Error {
	result := protocol.ErrorFrom(err)
	if result == nil {
		return nil
	}
	result.Message, _ = redaction.String(result.Message, 4_000)
	if result.Details == nil {
		return result
	}
	limits := redaction.DefaultLimits(64 << 10)
	limits.MaxInputBytes = 64 << 10
	limits.MaxStringBytes = 8_000
	sanitized, _, sanitizeErr := redaction.Value(result.Details, limits)
	if sanitizeErr != nil {
		result.Details = nil
		return result
	}
	details, ok := sanitized.(map[string]any)
	if !ok {
		result.Details = nil
		return result
	}
	encoded, marshalErr := json.Marshal(details)
	if marshalErr != nil || len(encoded) > limits.MaxOutputBytes {
		result.Details = nil
		return result
	}
	result.Details = details
	return result
}

func appendUnique(values []string, additional ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additional))
	result := make([]string, 0, len(values)+len(additional))
	for _, value := range append(append([]string(nil), values...), additional...) {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func resolvedResultTarget(browserID string, target *protocol.Target) *protocol.Target {
	if target == nil {
		if browserID == "" {
			return nil
		}
		return &protocol.Target{BrowserID: browserID}
	}
	if browserID == "" {
		browserID = target.BrowserID
	}
	resolved, err := protocol.ResolveTarget(browserID, target)
	if err != nil {
		return nil
	}
	return resolved
}

func resultHints(data any) ([]string, string, string) {
	warnings := []string{}
	payload, err := json.Marshal(data)
	if err != nil {
		return warnings, "", ""
	}
	var hints struct {
		Warnings    []string `json:"warnings"`
		NextCursor  string   `json:"nextCursor"`
		ArtifactURI string   `json:"artifactUri"`
	}
	if err := json.Unmarshal(payload, &hints); err != nil {
		return warnings, "", ""
	}
	if hints.Warnings != nil {
		warnings = append(warnings, hints.Warnings...)
	}
	return warnings, hints.NextCursor, hints.ArtifactURI
}

func optionalBrowserID() mcp.ToolOption {
	return mcp.WithString(
		"browserId",
		mcp.Description("Browser instance ID; omit to use the current MCP session selection"),
	)
}

func optionalTabID() mcp.ToolOption {
	return mcp.WithNumber(
		"tabId",
		mcp.Description("Browser tab ID; omit to use the active tab"),
	)
}

func optionalFrameID() mcp.ToolOption {
	return mcp.WithNumber(
		"frameId",
		mcp.Description("Frame ID; omit for the top-level frame"),
		mcp.Min(0),
	)
}

func optionalDocumentID() mcp.ToolOption {
	return mcp.WithString(
		"documentId",
		mcp.Description("Current document ID used to reject stale targets"),
	)
}

func optionalCoordinates() mcp.ToolOption {
	return mcp.WithObject(
		"coordinates",
		mcp.Description("Viewport coordinates in CSS pixels"),
		mcp.Properties(map[string]any{
			"x": map[string]any{"type": "number", "minimum": 0, "maximum": protocol.MaxCoordinate},
			"y": map[string]any{"type": "number", "minimum": 0, "maximum": protocol.MaxCoordinate},
		}),
		requiredObjectProperties("x", "y"),
		mcp.AdditionalProperties(false),
	)
}

func optionalLocator() mcp.ToolOption {
	return locatorOption(false)
}

func requiredLocator() mcp.ToolOption {
	return locatorOption(true)
}

func locatorOption(required bool) mcp.ToolOption {
	return locatorNamedOption("locator", required)
}

func locatorNamedOption(name string, required bool) mcp.ToolOption {
	propertyOptions := []mcp.PropertyOption{
		mcp.Description("Element locator; exactly one primary strategy is required"),
		mcp.Properties(map[string]any{
			"css":         map[string]any{"type": "string", "minLength": 1},
			"xpath":       map[string]any{"type": "string", "minLength": 1},
			"text":        map[string]any{"type": "string", "minLength": 1},
			"role":        map[string]any{"type": "string", "minLength": 1},
			"name":        map[string]any{"type": "string", "minLength": 1},
			"label":       map[string]any{"type": "string", "minLength": 1},
			"placeholder": map[string]any{"type": "string", "minLength": 1},
			"alt":         map[string]any{"type": "string", "minLength": 1},
			"title":       map[string]any{"type": "string", "minLength": 1},
			"testId":      map[string]any{"type": "string", "minLength": 1},
			"coordinates": map[string]any{
				"type":     "object",
				"required": []string{"x", "y"},
				"properties": map[string]any{
					"x": map[string]any{"type": "number", "minimum": 0, "maximum": protocol.MaxCoordinate},
					"y": map[string]any{"type": "number", "minimum": 0, "maximum": protocol.MaxCoordinate},
				},
				"additionalProperties": false,
			},
			"element": map[string]any{
				"type":     "object",
				"required": []string{"elementId", "documentId"},
				"properties": map[string]any{
					"elementId":  map[string]any{"type": "string", "minLength": 1},
					"documentId": map[string]any{"type": "string", "minLength": 1},
				},
				"additionalProperties": false,
			},
			"nth":              map[string]any{"type": "integer", "minimum": 0, "maximum": protocol.MaxLocatorNth},
			"strict":           map[string]any{"type": "boolean", "default": true},
			"includeShadowDOM": map[string]any{"type": "boolean", "default": false},
		}),
		locatorObjectConstraints(),
		mcp.AdditionalProperties(false),
	}
	if required {
		propertyOptions = append(propertyOptions, mcp.Required())
	}
	return mcp.WithObject(name, propertyOptions...)
}

func optionalSelectorArray(name, description string) mcp.ToolOption {
	return mcp.WithArray(
		name,
		mcp.Description(description),
		mcp.Items(map[string]any{"type": "string", "minLength": 1}),
		mcp.MaxItems(50),
	)
}

func requiredObjectProperties(names ...string) mcp.PropertyOption {
	return func(schema map[string]any) {
		schema["required"] = names
	}
}

func locatorObjectConstraints() mcp.PropertyOption {
	return func(schema map[string]any) {
		strategies := []string{
			"css", "xpath", "text", "role", "label", "placeholder",
			"alt", "title", "testId", "coordinates", "element",
		}
		oneOf := make([]map[string]any, 0, len(strategies))
		for _, strategy := range strategies {
			oneOf = append(oneOf, map[string]any{"required": []string{strategy}})
		}
		schema["oneOf"] = oneOf
		schema["dependentRequired"] = map[string]any{"name": []string{"role"}}
	}
}

func optionalTimeout() mcp.ToolOption {
	return mcp.WithNumber(
		"timeoutMs",
		mcp.Description("Command timeout in milliseconds"),
	)
}
