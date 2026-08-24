package tools

import (
	"context"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var interactionEventTypePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9:_-]{0,99}$`)

const (
	maxInteractionTextChars = 100_000
	maxDelayedTypeChars     = 10_000
	maxInteractionKeyChars  = 100
)

type interactionTargetArgs struct {
	BrowserID         string           `json:"browserId,omitempty"`
	TabID             *int             `json:"tabId,omitempty"`
	FrameID           *int             `json:"frameId,omitempty"`
	DocumentID        string           `json:"documentId,omitempty"`
	Locator           protocol.Locator `json:"locator"`
	Backend           string           `json:"backend,omitempty"`
	WaitForNavigation *bool            `json:"waitForNavigation,omitempty"`
	TimeoutMS         *int             `json:"timeoutMs,omitempty"`
}

type interactionTypeArgs struct {
	interactionTargetArgs
	Text    string `json:"text"`
	DelayMS *int   `json:"delayMs,omitempty"`
}

type interactionPressArgs struct {
	interactionTargetArgs
	Key       string   `json:"key"`
	Modifiers []string `json:"modifiers,omitempty"`
}

type interactionSelectArgs struct {
	interactionTargetArgs
	Values []string `json:"values"`
}

type interactionCheckedArgs struct {
	interactionTargetArgs
	Checked *bool `json:"checked,omitempty"`
}

type interactionScrollArgs struct {
	BrowserID         string            `json:"browserId,omitempty"`
	TabID             *int              `json:"tabId,omitempty"`
	FrameID           *int              `json:"frameId,omitempty"`
	DocumentID        string            `json:"documentId,omitempty"`
	Locator           *protocol.Locator `json:"locator,omitempty"`
	DeltaX            float64           `json:"deltaX,omitempty"`
	DeltaY            float64           `json:"deltaY,omitempty"`
	Behavior          string            `json:"behavior,omitempty"`
	Backend           string            `json:"backend,omitempty"`
	WaitForNavigation *bool             `json:"waitForNavigation,omitempty"`
	TimeoutMS         *int              `json:"timeoutMs,omitempty"`
}

type interactionDragArgs struct {
	BrowserID         string                `json:"browserId,omitempty"`
	TabID             *int                  `json:"tabId,omitempty"`
	FrameID           *int                  `json:"frameId,omitempty"`
	DocumentID        string                `json:"documentId,omitempty"`
	Source            protocol.Locator      `json:"source"`
	TargetLocator     *protocol.Locator     `json:"targetLocator,omitempty"`
	TargetCoordinates *protocol.Coordinates `json:"targetCoordinates,omitempty"`
	Backend           string                `json:"backend,omitempty"`
	WaitForNavigation *bool                 `json:"waitForNavigation,omitempty"`
	TimeoutMS         *int                  `json:"timeoutMs,omitempty"`
}

type interactionDispatchArgs struct {
	interactionTargetArgs
	EventType string         `json:"eventType"`
	Detail    map[string]any `json:"detail,omitempty"`
}

func (s *Service) registerInteractionTools(mcpServer *server.MCPServer) {
	for _, registration := range []struct {
		name        string
		description string
		command     string
	}{
		{"browser_double_click", "Double-click an actionable element", protocol.CommandPageClick},
		{"browser_context_click", "Open an element's context menu", protocol.CommandPageClick},
		{"browser_hover", "Hover over an actionable element", protocol.CommandPageHover},
		{"browser_focus", "Focus an actionable element", protocol.CommandPageFocus},
		{"browser_blur", "Blur an element", protocol.CommandPageBlur},
		{"browser_clear", "Clear an editable element", protocol.CommandPageClear},
		{"browser_submit", "Submit a form or an element's owning form", protocol.CommandPageSubmit},
	} {
		command := registration.command
		name := registration.name
		mcpServer.AddTool(
			newLocatorActionTool(name, registration.description),
			mcp.NewTypedToolHandler(func(
				ctx context.Context,
				_ mcp.CallToolRequest,
				args interactionTargetArgs,
			) (*mcp.CallToolResult, error) {
				if err := validateToolInteractionBackend(name, args.Backend); err != nil {
					return errorResult(err)
				}
				params, target, err := interactionParams(args)
				if err != nil {
					return errorResult(err)
				}
				if name == "browser_double_click" {
					params["clickCount"] = 2
				}
				if name == "browser_context_click" {
					params["button"] = "right"
				}
				return s.send(ctx, args.BrowserID, command, target, params, args.TimeoutMS)
			}),
		)
	}

	mcpServer.AddTool(newLocatorActionTool(
		"browser_type", "Append text to an editable element",
		mcp.WithString("text", mcp.Required(), mcp.Description("Text to type"), mcp.MaxLength(maxInteractionTextChars)),
		mcp.WithNumber("delayMs", mcp.Description("Delay per character in milliseconds"), mcp.Min(0), mcp.Max(1_000)),
	), mcp.NewTypedToolHandler(s.browserTypeHandler))
	mcpServer.AddTool(newLocatorActionTool(
		"browser_press", "Dispatch a keyboard chord to an element",
		mcp.WithString("key", mcp.Required(), mcp.Description("KeyboardEvent key value"), mcp.MaxLength(maxInteractionKeyChars)),
		mcp.WithArray("modifiers", mcp.Description("Alt, Control, Meta, or Shift"),
			mcp.Items(map[string]any{"type": "string", "enum": []string{"Alt", "Control", "Meta", "Shift"}}),
			mcp.MaxItems(4)),
	), mcp.NewTypedToolHandler(s.browserPressHandler))
	mcpServer.AddTool(newLocatorActionTool(
		"browser_select_option", "Select one or more option values or labels",
		mcp.WithArray("values", mcp.Required(), mcp.Description("Option values or labels"),
			mcp.Items(map[string]any{"type": "string"}), mcp.MinItems(1), mcp.MaxItems(100)),
	), mcp.NewTypedToolHandler(s.browserSelectOptionHandler))
	mcpServer.AddTool(newLocatorActionTool(
		"browser_set_checked", "Set or toggle a checkbox or radio input",
		mcp.WithBoolean("checked", mcp.Description("Desired state; omit to toggle")),
	), mcp.NewTypedToolHandler(s.browserSetCheckedHandler))
	mcpServer.AddTool(newPageActionTool(
		"browser_scroll", "Scroll the page or a located element",
		optionalLocator(),
		mcp.WithNumber("deltaX", mcp.Description("Horizontal CSS pixel delta"), mcp.Min(-1_000_000), mcp.Max(1_000_000)),
		mcp.WithNumber("deltaY", mcp.Description("Vertical CSS pixel delta"), mcp.Min(-1_000_000), mcp.Max(1_000_000)),
		mcp.WithString("behavior", mcp.Description("Scroll behavior"), mcp.Enum("auto", "smooth")),
	), mcp.NewTypedToolHandler(s.browserScrollHandler))
	mcpServer.AddTool(newPageActionTool(
		"browser_drag_and_drop", "Drag an element to another element or viewport point",
		locatorNamedOption("source", true),
		locatorNamedOption("targetLocator", false),
		mcp.WithObject("targetCoordinates", mcp.Description("Viewport drop coordinates"),
			mcp.Properties(map[string]any{
				"x": map[string]any{"type": "number", "minimum": 0, "maximum": protocol.MaxCoordinate},
				"y": map[string]any{"type": "number", "minimum": 0, "maximum": protocol.MaxCoordinate},
			}), requiredObjectProperties("x", "y"), mcp.AdditionalProperties(false)),
	), mcp.NewTypedToolHandler(s.browserDragHandler))
	mcpServer.AddTool(newLocatorActionTool(
		"browser_dispatch_event", "Dispatch a custom DOM event on an element",
		mcp.WithString("eventType", mcp.Required(), mcp.Description("DOM event type")),
		mcp.WithObject("detail", mcp.Description("JSON-compatible CustomEvent detail")),
	), mcp.NewTypedToolHandler(s.browserDispatchHandler))
}

func newLocatorActionTool(name, description string, extra ...mcp.ToolOption) mcp.Tool {
	return newPageActionTool(name, description, append([]mcp.ToolOption{requiredLocator()}, extra...)...)
}

func newPageActionTool(name, description string, extra ...mcp.ToolOption) mcp.Tool {
	options := []mcp.ToolOption{
		mcp.WithDescription(description), optionalBrowserID(), optionalTabID(),
		optionalFrameID(), optionalDocumentID(),
	}
	options = append(options, extra...)
	backends := []string{"auto", "content"}
	backendDescription := "DOM interaction backend"
	if trustedCDPBackendTool(name) {
		backends = append(backends, "cdp")
		backendDescription = "Input backend; cdp requires the root document and Debug permission"
	}
	options = append(options,
		mcp.WithString("backend", mcp.Description(backendDescription), mcp.Enum(backends...)),
		mcp.WithBoolean("waitForNavigation", mcp.Description("Wait for navigation completion after the action")),
		optionalTimeout(),
	)
	return mcp.NewTool(name, options...)
}

func (s *Service) browserTypeHandler(ctx context.Context, _ mcp.CallToolRequest, args interactionTypeArgs) (*mcp.CallToolResult, error) {
	params, target, err := interactionParams(args.interactionTargetArgs)
	if err != nil {
		return errorResult(err)
	}
	if strings.TrimSpace(args.Text) == "" {
		return errorResult(invalidInteraction("text must not be empty"))
	}
	if utf8.RuneCountInString(args.Text) > maxInteractionTextChars {
		return errorResult(invalidInteraction("text must contain at most 100000 characters"))
	}
	if args.DelayMS != nil && (*args.DelayMS < 0 || *args.DelayMS > 1_000) {
		return errorResult(invalidInteraction("delayMs must be between 0 and 1000"))
	}
	if args.DelayMS != nil && *args.DelayMS > 0 && utf8.RuneCountInString(args.Text) > maxDelayedTypeChars {
		return errorResult(invalidInteraction("delayed text must contain at most 10000 characters"))
	}
	params["text"] = args.Text
	putOptional(params, "delayMs", args.DelayMS)
	return s.send(ctx, args.BrowserID, protocol.CommandPageType, target, params, args.TimeoutMS)
}

func (s *Service) browserPressHandler(ctx context.Context, _ mcp.CallToolRequest, args interactionPressArgs) (*mcp.CallToolResult, error) {
	params, target, err := interactionParams(args.interactionTargetArgs)
	if err != nil {
		return errorResult(err)
	}
	if strings.TrimSpace(args.Key) == "" || utf8.RuneCountInString(args.Key) > maxInteractionKeyChars {
		return errorResult(invalidInteraction("key must contain between 1 and 100 characters"))
	}
	if err := validateModifiers(args.Modifiers); err != nil {
		return errorResult(err)
	}
	params["key"] = args.Key
	if args.Modifiers != nil {
		params["modifiers"] = args.Modifiers
	}
	return s.send(ctx, args.BrowserID, protocol.CommandPagePress, target, params, args.TimeoutMS)
}

func (s *Service) browserSelectOptionHandler(ctx context.Context, _ mcp.CallToolRequest, args interactionSelectArgs) (*mcp.CallToolResult, error) {
	if err := validateToolInteractionBackend("browser_select_option", args.Backend); err != nil {
		return errorResult(err)
	}
	params, target, err := interactionParams(args.interactionTargetArgs)
	if err != nil {
		return errorResult(err)
	}
	if len(args.Values) == 0 || len(args.Values) > 100 {
		return errorResult(invalidInteraction("values must contain between 1 and 100 entries"))
	}
	for _, value := range args.Values {
		if strings.TrimSpace(value) == "" {
			return errorResult(invalidInteraction("values must not contain empty entries"))
		}
	}
	params["values"] = args.Values
	return s.send(ctx, args.BrowserID, protocol.CommandPageSelect, target, params, args.TimeoutMS)
}

func (s *Service) browserSetCheckedHandler(ctx context.Context, _ mcp.CallToolRequest, args interactionCheckedArgs) (*mcp.CallToolResult, error) {
	params, target, err := interactionParams(args.interactionTargetArgs)
	if err != nil {
		return errorResult(err)
	}
	putOptional(params, "checked", args.Checked)
	return s.send(ctx, args.BrowserID, protocol.CommandPageSetChecked, target, params, args.TimeoutMS)
}

func (s *Service) browserScrollHandler(ctx context.Context, _ mcp.CallToolRequest, args interactionScrollArgs) (*mcp.CallToolResult, error) {
	target := pageTarget(args.TabID, args.FrameID, args.DocumentID)
	if err := validateInteractionBackend(args.Backend); err != nil {
		return errorResult(err)
	}
	params := interactionOptions(args.Backend, args.WaitForNavigation)
	if args.Locator != nil {
		if err := args.Locator.Validate(target); err != nil {
			return errorResult(err)
		}
		params["locator"] = args.Locator
	}
	if !validScrollDelta(args.DeltaX) || !validScrollDelta(args.DeltaY) {
		return errorResult(invalidInteraction("scroll deltas must be finite and between -1000000 and 1000000"))
	}
	if args.DeltaX == 0 && args.DeltaY == 0 {
		return errorResult(invalidInteraction("a non-zero scroll delta is required"))
	}
	if args.Behavior != "" && args.Behavior != "auto" && args.Behavior != "smooth" {
		return errorResult(invalidInteraction("behavior must be auto or smooth"))
	}
	if args.Backend == "cdp" && args.Behavior == "smooth" {
		return errorResult(invalidInteraction("smooth behavior is unavailable with the cdp backend"))
	}
	params["deltaX"], params["deltaY"] = args.DeltaX, args.DeltaY
	if args.Behavior != "" {
		params["behavior"] = args.Behavior
	}
	return s.send(ctx, args.BrowserID, protocol.CommandPageScroll, target, params, args.TimeoutMS)
}

func (s *Service) browserDragHandler(ctx context.Context, _ mcp.CallToolRequest, args interactionDragArgs) (*mcp.CallToolResult, error) {
	if err := validateToolInteractionBackend("browser_drag_and_drop", args.Backend); err != nil {
		return errorResult(err)
	}
	target := pageTarget(args.TabID, args.FrameID, args.DocumentID)
	if err := validateInteractionBackend(args.Backend); err != nil {
		return errorResult(err)
	}
	if err := args.Source.Validate(target); err != nil {
		return errorResult(err)
	}
	if (args.TargetLocator == nil) == (args.TargetCoordinates == nil) {
		return errorResult(invalidInteraction("exactly one drag target is required"))
	}
	if args.TargetLocator != nil {
		if err := args.TargetLocator.Validate(target); err != nil {
			return errorResult(err)
		}
	}
	if args.TargetCoordinates != nil {
		if err := args.TargetCoordinates.Validate(); err != nil {
			return errorResult(err)
		}
	}
	params := interactionOptions(args.Backend, args.WaitForNavigation)
	params["source"] = args.Source
	if args.TargetLocator != nil {
		params["targetLocator"] = args.TargetLocator
	}
	if args.TargetCoordinates != nil {
		params["targetCoordinates"] = args.TargetCoordinates
	}
	return s.send(ctx, args.BrowserID, protocol.CommandPageDrag, target, params, args.TimeoutMS)
}

func (s *Service) browserDispatchHandler(ctx context.Context, _ mcp.CallToolRequest, args interactionDispatchArgs) (*mcp.CallToolResult, error) {
	if err := validateToolInteractionBackend("browser_dispatch_event", args.Backend); err != nil {
		return errorResult(err)
	}
	params, target, err := interactionParams(args.interactionTargetArgs)
	if err != nil {
		return errorResult(err)
	}
	if !interactionEventTypePattern.MatchString(args.EventType) {
		return errorResult(invalidInteraction("eventType is invalid"))
	}
	params["eventType"] = args.EventType
	if args.Detail != nil {
		params["detail"] = args.Detail
	}
	return s.send(ctx, args.BrowserID, protocol.CommandPageDispatch, target, params, args.TimeoutMS)
}

func interactionParams(args interactionTargetArgs) (map[string]any, *protocol.Target, error) {
	target := pageTarget(args.TabID, args.FrameID, args.DocumentID)
	if err := validateInteractionBackend(args.Backend); err != nil {
		return nil, nil, err
	}
	if err := args.Locator.Validate(target); err != nil {
		return nil, nil, err
	}
	params := interactionOptions(args.Backend, args.WaitForNavigation)
	params["locator"] = args.Locator
	return params, target, nil
}

func validateInteractionBackend(backend string) error {
	if backend == "" || backend == "auto" || backend == "content" || backend == "cdp" {
		return nil
	}
	return invalidInteraction("backend must be auto, content, or cdp")
}

func validateToolInteractionBackend(toolName, backend string) error {
	if err := validateInteractionBackend(backend); err != nil {
		return err
	}
	if backend == "cdp" && !trustedCDPBackendTool(toolName) {
		return invalidInteraction("the cdp backend is unavailable for this DOM-semantic action")
	}
	return nil
}

func trustedCDPBackendTool(name string) bool {
	switch name {
	case "browser_click_element", "browser_double_click", "browser_context_click",
		"browser_input_data", "browser_hover", "browser_type", "browser_clear",
		"browser_press", "browser_set_checked", "browser_scroll":
		return true
	default:
		return false
	}
}

func validateModifiers(modifiers []string) error {
	if len(modifiers) > 4 {
		return invalidInteraction("modifiers must contain at most four entries")
	}
	allowed := map[string]bool{"Alt": true, "Control": true, "Meta": true, "Shift": true}
	seen := make(map[string]bool, len(modifiers))
	for _, modifier := range modifiers {
		if !allowed[modifier] || seen[modifier] {
			return invalidInteraction("modifiers must be unique Alt, Control, Meta, or Shift values")
		}
		seen[modifier] = true
	}
	return nil
}

func validScrollDelta(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && math.Abs(value) <= 1_000_000
}

func invalidInteraction(message string) error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}

func interactionOptions(backend string, waitForNavigation *bool) map[string]any {
	params := map[string]any{}
	if backend != "" {
		params["backend"] = backend
	}
	putOptional(params, "waitForNavigation", waitForNavigation)
	return params
}
