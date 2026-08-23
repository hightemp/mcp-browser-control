package tools

import (
	"context"
	"strings"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type waitArgs struct {
	BrowserID      string            `json:"browserId,omitempty"`
	TabID          *int              `json:"tabId,omitempty"`
	FrameID        *int              `json:"frameId,omitempty"`
	DocumentID     string            `json:"documentId,omitempty"`
	Condition      string            `json:"condition"`
	Mode           string            `json:"mode,omitempty"`
	PollIntervalMS *int              `json:"pollIntervalMs,omitempty"`
	DelayMS        *int              `json:"delayMs,omitempty"`
	ReadyState     string            `json:"readyState,omitempty"`
	URL            *string           `json:"url,omitempty"`
	URLPattern     *string           `json:"urlPattern,omitempty"`
	Locator        *protocol.Locator `json:"locator,omitempty"`
	ElementState   string            `json:"elementState,omitempty"`
	Expected       *string           `json:"expected,omitempty"`
	MatchOperator  string            `json:"matchOperator,omitempty"`
	CaseSensitive  *bool             `json:"caseSensitive,omitempty"`
	Count          *int              `json:"count,omitempty"`
	CountOperator  string            `json:"countOperator,omitempty"`
	IdleMS         *int              `json:"idleMs,omitempty"`
	Attribute      string            `json:"attribute,omitempty"`
	AttributeState string            `json:"attributeState,omitempty"`
	TimeoutMS      *int              `json:"timeoutMs,omitempty"`
}

func (s *Service) registerWaitTool(mcpServer *server.MCPServer) {
	mcpServer.AddTool(mcp.NewTool(
		"browser_wait",
		mcp.WithDescription("Wait for a bounded browser or page condition"),
		optionalBrowserID(), optionalTabID(), optionalFrameID(), optionalDocumentID(),
		mcp.WithString("condition", mcp.Required(), mcp.Description("Condition family"),
			mcp.Enum("delay", "loadState", "url", "element", "text", "value", "count", "navigation", "networkIdle", "attribute")),
		mcp.WithString("mode", mcp.Description("Observation mode"), mcp.Enum("auto", "polling", "event")),
		mcp.WithNumber("pollIntervalMs", mcp.Description("Polling interval in milliseconds"), mcp.Min(25), mcp.Max(1_000)),
		mcp.WithNumber("delayMs", mcp.Description("Delay duration in milliseconds"), mcp.Min(0), mcp.Max(float64(protocol.MaxTimeoutMS))),
		mcp.WithString("readyState", mcp.Description("Minimum document ready state"), mcp.Enum("interactive", "complete")),
		mcp.WithString("url", mcp.Description("Exact target URL"), mcp.MinLength(1), mcp.MaxLength(4_096)),
		mcp.WithString("urlPattern", mcp.Description("URL wildcard pattern where * matches any characters"), mcp.MinLength(1), mcp.MaxLength(4_096)),
		optionalLocator(),
		mcp.WithString("elementState", mcp.Description("Desired element state"),
			mcp.Enum("attached", "detached", "visible", "hidden", "enabled", "disabled")),
		mcp.WithString("expected", mcp.Description("Expected text, value, or attribute value"), mcp.MaxLength(100_000)),
		mcp.WithString("matchOperator", mcp.Description("String comparison"), mcp.Enum("equals", "contains")),
		mcp.WithBoolean("caseSensitive", mcp.Description("Use case-sensitive string comparison")),
		mcp.WithNumber("count", mcp.Description("Expected locator match count"), mcp.Min(0), mcp.Max(1_000_000)),
		mcp.WithString("countOperator", mcp.Description("Count comparison"), mcp.Enum("equals", "atLeast", "atMost")),
		mcp.WithNumber("idleMs", mcp.Description("Required quiet network interval in milliseconds"), mcp.Min(100), mcp.Max(30_000)),
		mcp.WithString("attribute", mcp.Description("Safe DOM attribute name"),
			mcp.MinLength(1), mcp.MaxLength(200), mcp.Pattern(`^[A-Za-z0-9:_-]+$`)),
		mcp.WithString("attributeState", mcp.Description("Attribute comparison"),
			mcp.Enum("present", "absent", "equals", "contains")),
		optionalTimeout(),
	), mcp.NewTypedToolHandler(s.browserWaitHandler))
}

func (s *Service) browserWaitHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args waitArgs,
) (*mcp.CallToolResult, error) {
	params, target, err := validateWaitArgs(args)
	if err != nil {
		return errorResult(err)
	}
	return s.send(
		ctx,
		args.BrowserID,
		protocol.CommandPageWait,
		target,
		params,
		args.TimeoutMS,
	)
}

func validateWaitArgs(args waitArgs) (map[string]any, *protocol.Target, error) {
	target := pageTarget(args.TabID, args.FrameID, args.DocumentID)
	params := map[string]any{"condition": args.Condition}
	if args.Mode != "" && args.Mode != "auto" && args.Mode != "polling" && args.Mode != "event" {
		return nil, nil, invalidWait("mode must be auto, polling, or event")
	}
	if args.Mode != "" {
		params["mode"] = args.Mode
	}
	if args.PollIntervalMS != nil {
		if *args.PollIntervalMS < 25 || *args.PollIntervalMS > 1_000 {
			return nil, nil, invalidWait("pollIntervalMs must be between 25 and 1000")
		}
		params["pollIntervalMs"] = *args.PollIntervalMS
	}

	switch args.Condition {
	case "delay":
		if args.DelayMS == nil || *args.DelayMS < 0 || int64(*args.DelayMS) > protocol.MaxTimeoutMS {
			return nil, nil, invalidWait("delayMs must be between 0 and 120000")
		}
		params["delayMs"] = *args.DelayMS
	case "loadState":
		if args.ReadyState != "interactive" && args.ReadyState != "complete" {
			return nil, nil, invalidWait("readyState must be interactive or complete")
		}
		params["readyState"] = args.ReadyState
	case "url":
		if (args.URL == nil) == (args.URLPattern == nil) {
			return nil, nil, invalidWait("exactly one of url or urlPattern is required")
		}
		if args.URL != nil {
			if strings.TrimSpace(*args.URL) == "" || len(*args.URL) > 4_096 {
				return nil, nil, invalidWait("url must contain between 1 and 4096 characters")
			}
			params["url"] = *args.URL
		}
		if args.URLPattern != nil {
			if strings.TrimSpace(*args.URLPattern) == "" || len(*args.URLPattern) > 4_096 {
				return nil, nil, invalidWait("urlPattern must contain between 1 and 4096 characters")
			}
			params["urlPattern"] = *args.URLPattern
		}
	case "element":
		if err := validateWaitLocator(args.Locator, target); err != nil {
			return nil, nil, err
		}
		if !oneOf(args.ElementState, "attached", "detached", "visible", "hidden", "enabled", "disabled") {
			return nil, nil, invalidWait("elementState is invalid")
		}
		params["locator"], params["elementState"] = args.Locator, args.ElementState
	case "text", "value":
		if args.Expected == nil {
			return nil, nil, invalidWait("expected is required")
		}
		if len(*args.Expected) > 100_000 {
			return nil, nil, invalidWait("expected must not exceed 100000 characters")
		}
		if args.Condition == "value" {
			if err := validateWaitLocator(args.Locator, target); err != nil {
				return nil, nil, err
			}
			params["locator"] = args.Locator
		} else if args.Locator != nil {
			if err := args.Locator.Validate(target); err != nil {
				return nil, nil, err
			}
			params["locator"] = args.Locator
		}
		if args.MatchOperator != "" && args.MatchOperator != "equals" && args.MatchOperator != "contains" {
			return nil, nil, invalidWait("matchOperator must be equals or contains")
		}
		params["expected"] = *args.Expected
		if args.MatchOperator != "" {
			params["matchOperator"] = args.MatchOperator
		}
		putOptional(params, "caseSensitive", args.CaseSensitive)
	case "count":
		if err := validateWaitLocator(args.Locator, target); err != nil {
			return nil, nil, err
		}
		if args.Count == nil || *args.Count < 0 || *args.Count > 1_000_000 {
			return nil, nil, invalidWait("count must be between 0 and 1000000")
		}
		if args.CountOperator != "" && !oneOf(args.CountOperator, "equals", "atLeast", "atMost") {
			return nil, nil, invalidWait("countOperator is invalid")
		}
		params["locator"], params["count"] = args.Locator, *args.Count
		if args.CountOperator != "" {
			params["countOperator"] = args.CountOperator
		}
	case "navigation":
	case "networkIdle":
		if args.IdleMS == nil || *args.IdleMS < 100 || *args.IdleMS > 30_000 {
			return nil, nil, invalidWait("idleMs must be between 100 and 30000")
		}
		params["idleMs"] = *args.IdleMS
	case "attribute":
		if err := validateWaitLocator(args.Locator, target); err != nil {
			return nil, nil, err
		}
		if !validAttributeName(args.Attribute) {
			return nil, nil, invalidWait("attribute is invalid")
		}
		if !oneOf(args.AttributeState, "present", "absent", "equals", "contains") {
			return nil, nil, invalidWait("attributeState is invalid")
		}
		if (args.AttributeState == "equals" || args.AttributeState == "contains") && args.Expected == nil {
			return nil, nil, invalidWait("expected is required for the attribute comparison")
		}
		if args.Expected != nil && len(*args.Expected) > 100_000 {
			return nil, nil, invalidWait("expected must not exceed 100000 characters")
		}
		params["locator"], params["attribute"] = args.Locator, args.Attribute
		params["attributeState"] = args.AttributeState
		if args.Expected != nil {
			params["expected"] = *args.Expected
		}
		putOptional(params, "caseSensitive", args.CaseSensitive)
	default:
		return nil, nil, invalidWait("condition is invalid")
	}
	return params, target, nil
}

func validateWaitLocator(locator *protocol.Locator, target *protocol.Target) error {
	if locator == nil {
		return invalidWait("locator is required for this condition")
	}
	return locator.Validate(target)
}

func validAttributeName(value string) bool {
	if value == "" || len(value) > 200 {
		return false
	}
	for _, character := range value {
		allowed := (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == ':' || character == '_' || character == '-'
		if !allowed {
			return false
		}
	}
	lower := strings.ToLower(value)
	for _, sensitive := range []string{
		"password", "secret", "token", "credential", "authorization", "cookie", "apikey", "api-key", "api_key",
	} {
		if strings.Contains(lower, sensitive) {
			return false
		}
	}
	return true
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func invalidWait(message string) error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}
