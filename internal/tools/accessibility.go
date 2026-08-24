package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultAccessibilityMaxDepth      = 20
	maxAccessibilityMaxDepth          = 50
	defaultAccessibilityMaxNodes      = 1_000
	maxAccessibilityMaxNodes          = 5_000
	defaultAccessibilityMaxProperties = 20
	maxAccessibilityMaxProperties     = 50
	defaultAccessibilityValueChars    = 500
	maxAccessibilityValueChars        = 2_000
	defaultAccessibilityReferences    = 50
	maxAccessibilityReferences        = 100
	defaultAccessibilityMaxBytes      = 1_500_000
	minAccessibilityMaxBytes          = 64 * 1_024
	maxAccessibilityFrames            = 100
	maxAccessibilityWarnings          = 10
)

type accessibilityTreeArgs struct {
	BrowserID                string   `json:"browserId,omitempty"`
	TabID                    *int     `json:"tabId,omitempty"`
	DocumentID               string   `json:"documentId,omitempty"`
	Mode                     string   `json:"mode,omitempty"`
	BackendNodeID            *int     `json:"backendNodeId,omitempty"`
	FetchRelatives           *bool    `json:"fetchRelatives,omitempty"`
	Roles                    []string `json:"roles,omitempty"`
	NameContains             string   `json:"nameContains,omitempty"`
	IncludeIgnored           *bool    `json:"includeIgnored,omitempty"`
	IncludeLocators          *bool    `json:"includeLocators,omitempty"`
	IncludeElementReferences *bool    `json:"includeElementReferences,omitempty"`
	MaxDepth                 *int     `json:"maxDepth,omitempty"`
	MaxNodes                 *int     `json:"maxNodes,omitempty"`
	MaxProperties            *int     `json:"maxProperties,omitempty"`
	MaxValueChars            *int     `json:"maxValueChars,omitempty"`
	MaxElementReferences     *int     `json:"maxElementReferences,omitempty"`
	MaxBytes                 *int     `json:"maxBytes,omitempty"`
	TimeoutMS                *int     `json:"timeoutMs,omitempty"`
}

type accessibilitySettings struct {
	Mode                     string
	BackendNodeID            int
	FetchRelatives           bool
	Roles                    []string
	NameContains             string
	IncludeIgnored           bool
	IncludeLocators          bool
	IncludeElementReferences bool
	MaxDepth                 int
	MaxNodes                 int
	MaxProperties            int
	MaxValueChars            int
	MaxElementReferences     int
	MaxBytes                 int
}

type accessibilityWireResult struct {
	Mode              string               `json:"mode"`
	TabID             int                  `json:"tabId"`
	DocumentID        string               `json:"documentId"`
	RootFrameID       string               `json:"rootFrameId"`
	FrameCount        int                  `json:"frameCount"`
	Frames            []accessibilityFrame `json:"frames"`
	TotalNodeCount    int                  `json:"totalNodeCount"`
	MatchingNodeCount int                  `json:"matchingNodeCount"`
	ReturnedNodeCount int                  `json:"returnedNodeCount"`
	Nodes             []accessibilityNode  `json:"nodes"`
	Truncated         bool                 `json:"truncated"`
	Warnings          []string             `json:"warnings"`
}

type accessibilityFrame struct {
	FrameID       string `json:"frameId"`
	ParentFrameID string `json:"parentFrameId,omitempty"`
	Name          string `json:"name,omitempty"`
	URL           string `json:"url,omitempty"`
}

type accessibilityNode struct {
	NodeID        string                      `json:"nodeId"`
	ParentID      string                      `json:"parentId,omitempty"`
	Depth         int                         `json:"depth"`
	Ignored       bool                        `json:"ignored"`
	Role          string                      `json:"role,omitempty"`
	Name          string                      `json:"name,omitempty"`
	Description   string                      `json:"description,omitempty"`
	Value         string                      `json:"value,omitempty"`
	Properties    []accessibilityNodeProperty `json:"properties"`
	BackendNodeID *int                        `json:"backendNodeId,omitempty"`
	FrameID       string                      `json:"frameId,omitempty"`
	Locator       *protocol.Locator           `json:"locator,omitempty"`
	Reference     *protocol.ElementReference  `json:"reference,omitempty"`
}

type accessibilityNodeProperty struct {
	Name  string `json:"name"`
	Type  string `json:"type,omitempty"`
	Value string `json:"value,omitempty"`
}

func (s *Service) registerAccessibilityTool(mcpServer *server.MCPServer) {
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_get_accessibility_tree",
			mcp.WithDescription("Get a bounded normalized full or partial accessibility tree"),
			optionalBrowserID(),
			optionalTabID(),
			optionalDocumentID(),
			mcp.WithString("mode", mcp.Description("Tree retrieval mode"), mcp.Enum("full", "partial")),
			mcp.WithNumber("backendNodeId", mcp.Description("Backend DOM node ID required by partial mode"), mcp.Min(1)),
			mcp.WithBoolean("fetchRelatives", mcp.Description("Include ancestors, siblings, and children in partial mode")),
			mcp.WithArray(
				"roles",
				mcp.Description("Case-insensitive accessibility roles to include"),
				mcp.Items(map[string]any{"type": "string", "minLength": 1, "maxLength": 100}),
				mcp.MaxItems(50),
			),
			mcp.WithString("nameContains", mcp.Description("Case-insensitive accessible-name substring"), mcp.MaxLength(500)),
			mcp.WithBoolean("includeIgnored", mcp.Description("Include nodes ignored by the accessibility tree")),
			mcp.WithBoolean("includeLocators", mcp.Description("Attach role/name locator hints when available")),
			mcp.WithBoolean("includeElementReferences", mcp.Description("Resolve bounded unambiguous root-frame element references")),
			mcp.WithNumber("maxDepth", mcp.Description("Maximum full-tree depth"), mcp.Min(0), mcp.Max(maxAccessibilityMaxDepth)),
			mcp.WithNumber("maxNodes", mcp.Description("Maximum returned accessibility nodes"), mcp.Min(1), mcp.Max(maxAccessibilityMaxNodes)),
			mcp.WithNumber("maxProperties", mcp.Description("Maximum normalized properties per node"), mcp.Min(0), mcp.Max(maxAccessibilityMaxProperties)),
			mcp.WithNumber("maxValueChars", mcp.Description("Maximum characters per normalized value"), mcp.Min(1), mcp.Max(maxAccessibilityValueChars)),
			mcp.WithNumber("maxElementReferences", mcp.Description("Maximum root-frame reference lookups"), mcp.Min(0), mcp.Max(maxAccessibilityReferences)),
			mcp.WithNumber("maxBytes", mcp.Description("Maximum normalized extension result bytes"), mcp.Min(minAccessibilityMaxBytes), mcp.Max(defaultAccessibilityMaxBytes)),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserAccessibilityTreeHandler),
	)
}

func (s *Service) browserAccessibilityTreeHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args accessibilityTreeArgs,
) (*mcp.CallToolResult, error) {
	params, settings, err := validateAccessibilityArgs(args)
	if err != nil {
		return errorResult(err)
	}
	rootFrameID := 0
	browserID, target, raw, duration, err := s.sendRaw(
		ctx,
		args.BrowserID,
		protocol.CommandAccessibilityGetTree,
		pageTarget(args.TabID, &rootFrameID, args.DocumentID),
		params,
		args.TimeoutMS,
	)
	if err != nil {
		return errorResultWithDuration(err, duration)
	}
	result, err := decodeAccessibilityResult(raw, settings)
	if err != nil {
		return errorResultWithDuration(err, duration)
	}
	if target == nil {
		tabID := result.TabID
		frameID := 0
		target = &protocol.Target{BrowserID: browserID, TabID: &tabID, FrameID: &frameID}
	}
	if target.TabID == nil || *target.TabID != result.TabID ||
		(target.DocumentID != "" && target.DocumentID != result.DocumentID) {
		return errorResultWithDuration(invalidAccessibilityResult(), duration)
	}
	if target.DocumentID == "" {
		target.DocumentID = result.DocumentID
	}

	sanitized, report, err := s.sanitizeBrowserResult(raw)
	if err != nil {
		return errorResultWithDuration(err, duration)
	}
	return successResultWithTargetWarningsLimited(
		browserID,
		target,
		sanitized,
		duration,
		report.Warnings(),
		s.resultLimits.MaxOutputBytes,
	)
}

func validateAccessibilityArgs(
	args accessibilityTreeArgs,
) (map[string]any, accessibilitySettings, error) {
	settings := accessibilitySettings{
		Mode:                     "full",
		FetchRelatives:           true,
		IncludeLocators:          true,
		IncludeElementReferences: true,
		MaxDepth:                 defaultAccessibilityMaxDepth,
		MaxNodes:                 defaultAccessibilityMaxNodes,
		MaxProperties:            defaultAccessibilityMaxProperties,
		MaxValueChars:            defaultAccessibilityValueChars,
		MaxElementReferences:     defaultAccessibilityReferences,
		MaxBytes:                 defaultAccessibilityMaxBytes,
	}
	if args.Mode != "" {
		settings.Mode = strings.ToLower(strings.TrimSpace(args.Mode))
	}
	assignBool(&settings.FetchRelatives, args.FetchRelatives)
	assignBool(&settings.IncludeIgnored, args.IncludeIgnored)
	assignBool(&settings.IncludeLocators, args.IncludeLocators)
	assignBool(&settings.IncludeElementReferences, args.IncludeElementReferences)
	assignInt(&settings.MaxDepth, args.MaxDepth)
	assignInt(&settings.MaxNodes, args.MaxNodes)
	assignInt(&settings.MaxProperties, args.MaxProperties)
	assignInt(&settings.MaxValueChars, args.MaxValueChars)
	assignInt(&settings.MaxElementReferences, args.MaxElementReferences)
	assignInt(&settings.MaxBytes, args.MaxBytes)

	switch settings.Mode {
	case "full":
		if args.BackendNodeID != nil || args.FetchRelatives != nil {
			return nil, accessibilitySettings{}, invalidAccessibility("backendNodeId and fetchRelatives require partial mode")
		}
	case "partial":
		if args.BackendNodeID == nil || *args.BackendNodeID < 1 {
			return nil, accessibilitySettings{}, invalidAccessibility("partial mode requires a positive backendNodeId")
		}
		if args.MaxDepth != nil {
			return nil, accessibilitySettings{}, invalidAccessibility("maxDepth is only valid in full mode")
		}
		settings.BackendNodeID = *args.BackendNodeID
	default:
		return nil, accessibilitySettings{}, invalidAccessibility("mode must be full or partial")
	}
	if settings.MaxDepth < 0 || settings.MaxDepth > maxAccessibilityMaxDepth ||
		settings.MaxNodes < 1 || settings.MaxNodes > maxAccessibilityMaxNodes ||
		settings.MaxProperties < 0 || settings.MaxProperties > maxAccessibilityMaxProperties ||
		settings.MaxValueChars < 1 || settings.MaxValueChars > maxAccessibilityValueChars ||
		settings.MaxElementReferences < 0 || settings.MaxElementReferences > maxAccessibilityReferences ||
		settings.MaxBytes < minAccessibilityMaxBytes || settings.MaxBytes > defaultAccessibilityMaxBytes {
		return nil, accessibilitySettings{}, invalidAccessibility("one or more accessibility limits are out of range")
	}
	if !settings.IncludeElementReferences && args.MaxElementReferences != nil {
		return nil, accessibilitySettings{}, invalidAccessibility("maxElementReferences requires includeElementReferences")
	}
	if !settings.IncludeElementReferences {
		settings.MaxElementReferences = 0
	}

	settings.NameContains = strings.TrimSpace(args.NameContains)
	if args.NameContains != "" && settings.NameContains == "" {
		return nil, accessibilitySettings{}, invalidAccessibility("nameContains must not contain only whitespace")
	}
	roles := make([]string, 0, len(args.Roles))
	seenRoles := make(map[string]struct{}, len(args.Roles))
	for _, role := range args.Roles {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" || len(role) > 100 {
			return nil, accessibilitySettings{}, invalidAccessibility("roles contain an invalid value")
		}
		if _, exists := seenRoles[role]; exists {
			return nil, accessibilitySettings{}, invalidAccessibility("roles must not contain duplicates")
		}
		seenRoles[role] = struct{}{}
		roles = append(roles, role)
	}
	if len(roles) > 50 {
		return nil, accessibilitySettings{}, invalidAccessibility("roles contain too many values")
	}
	settings.Roles = roles

	params := map[string]any{
		"mode":                     settings.Mode,
		"roles":                    settings.Roles,
		"nameContains":             settings.NameContains,
		"includeIgnored":           settings.IncludeIgnored,
		"includeLocators":          settings.IncludeLocators,
		"includeElementReferences": settings.IncludeElementReferences,
		"maxNodes":                 settings.MaxNodes,
		"maxProperties":            settings.MaxProperties,
		"maxValueChars":            settings.MaxValueChars,
		"maxElementReferences":     settings.MaxElementReferences,
		"maxBytes":                 settings.MaxBytes,
	}
	if settings.Mode == "full" {
		params["maxDepth"] = settings.MaxDepth
	} else {
		params["backendNodeId"] = settings.BackendNodeID
		params["fetchRelatives"] = settings.FetchRelatives
	}
	return params, settings, nil
}

func decodeAccessibilityResult(
	raw json.RawMessage,
	settings accessibilitySettings,
) (accessibilityWireResult, error) {
	var result accessibilityWireResult
	if len(raw) > settings.MaxBytes || json.Unmarshal(raw, &result) != nil {
		return result, invalidAccessibilityResult()
	}
	if result.Mode != settings.Mode || result.TabID < 0 ||
		strings.TrimSpace(result.DocumentID) == "" || len(result.DocumentID) > 256 ||
		strings.TrimSpace(result.RootFrameID) == "" || len(result.RootFrameID) > 256 ||
		result.FrameCount < len(result.Frames) || len(result.Frames) > maxAccessibilityFrames ||
		result.TotalNodeCount < result.MatchingNodeCount ||
		result.MatchingNodeCount < result.ReturnedNodeCount ||
		result.ReturnedNodeCount != len(result.Nodes) || len(result.Nodes) > settings.MaxNodes ||
		len(result.Warnings) > maxAccessibilityWarnings {
		return result, invalidAccessibilityResult()
	}
	for _, warning := range result.Warnings {
		if strings.TrimSpace(warning) == "" || len(warning) > 1_000 {
			return result, invalidAccessibilityResult()
		}
	}
	frameIDs := make(map[string]struct{}, len(result.Frames))
	for _, frame := range result.Frames {
		if strings.TrimSpace(frame.FrameID) == "" || len(frame.FrameID) > 256 ||
			len(frame.ParentFrameID) > 256 || len(frame.Name) > 200 || len(frame.URL) > 1_024 {
			return result, invalidAccessibilityResult()
		}
		if _, exists := frameIDs[frame.FrameID]; exists {
			return result, invalidAccessibilityResult()
		}
		frameIDs[frame.FrameID] = struct{}{}
	}

	nodeIDs := make(map[string]struct{}, len(result.Nodes))
	target := &protocol.Target{TabID: &result.TabID, DocumentID: result.DocumentID}
	allowedRoles := make(map[string]struct{}, len(settings.Roles))
	for _, role := range settings.Roles {
		allowedRoles[role] = struct{}{}
	}
	referenceCount := 0
	for _, node := range result.Nodes {
		if strings.TrimSpace(node.NodeID) == "" || len(node.NodeID) > 256 ||
			len(node.ParentID) > 256 || node.Depth < 0 || node.Depth > maxAccessibilityMaxDepth+1 ||
			len(node.Role) > 100 || len(node.Name) > settings.MaxValueChars ||
			len(node.Description) > settings.MaxValueChars || len(node.Value) > settings.MaxValueChars ||
			len(node.FrameID) > 256 || len(node.Properties) > settings.MaxProperties ||
			(node.BackendNodeID != nil && *node.BackendNodeID < 1) {
			return result, invalidAccessibilityResult()
		}
		if _, exists := nodeIDs[node.NodeID]; exists {
			return result, invalidAccessibilityResult()
		}
		nodeIDs[node.NodeID] = struct{}{}
		if (settings.Mode == "full" && node.Depth > settings.MaxDepth) ||
			(!settings.IncludeIgnored && node.Ignored) ||
			(settings.NameContains != "" && !strings.Contains(
				strings.ToLower(node.Name),
				strings.ToLower(settings.NameContains),
			)) {
			return result, invalidAccessibilityResult()
		}
		if len(allowedRoles) > 0 {
			if _, allowed := allowedRoles[strings.ToLower(node.Role)]; !allowed {
				return result, invalidAccessibilityResult()
			}
		}
		for _, property := range node.Properties {
			if strings.TrimSpace(property.Name) == "" || len(property.Name) > 100 ||
				len(property.Type) > 50 || len(property.Value) > settings.MaxValueChars {
				return result, invalidAccessibilityResult()
			}
		}
		if node.Locator != nil {
			if !settings.IncludeLocators || node.Locator.Role == "" ||
				node.Locator.CSS != "" || node.Locator.XPath != "" ||
				node.Locator.Text != "" || node.Locator.Label != "" || node.Locator.Placeholder != "" ||
				node.Locator.Alt != "" || node.Locator.Title != "" || node.Locator.TestID != "" ||
				node.Locator.Coordinates != nil || node.Locator.Element != nil ||
				node.Locator.Validate(target) != nil {
				return result, invalidAccessibilityResult()
			}
		}
		if node.Reference != nil {
			referenceCount++
			if !settings.IncludeElementReferences ||
				node.Reference.Validate() != nil || node.Reference.DocumentID != result.DocumentID {
				return result, invalidAccessibilityResult()
			}
		}
	}
	if referenceCount > settings.MaxElementReferences {
		return result, invalidAccessibilityResult()
	}
	return result, nil
}

func assignInt(destination *int, value *int) {
	if value != nil {
		*destination = *value
	}
}

func invalidAccessibility(message string) *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}

func invalidAccessibilityResult() *protocol.Error {
	return protocol.NewError(
		protocol.CodeInvalidMessage,
		"the browser returned an invalid accessibility tree",
		false,
	)
}
