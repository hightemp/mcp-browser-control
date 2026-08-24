package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	maxRawCDPParamBytes          = 16 * 1_024
	defaultRawCDPMaxDepth        = 12
	maxRawCDPMaxDepth            = 20
	defaultRawCDPMaxNodes        = 2_000
	maxRawCDPMaxNodes            = 5_000
	defaultRawCDPMaxStringChars  = 2_000
	maxRawCDPMaxStringChars      = 10_000
	minRawCDPMaxBytes            = 64 * 1_024
	defaultRawCDPMaxBytes        = 512 * 1_024
	maxRawCDPMaxBytes            = 1_000_000
	defaultRawCDPTimeoutMS       = 10_000
	maxRawCDPTimeoutMS           = 30_000
	maxRawCDPKeyChars            = 256
	maxRawCDPWarnings            = 4
	maxRawCDPAccessibilityDepth  = 50
	maxRawCDPDescribeDepth       = 10
	maxRawCDPAccessibleNameChars = 500
	maxRawCDPRoleChars           = 100
)

const (
	rawCDPAccessibilityFull    = "Accessibility.getFullAXTree"
	rawCDPAccessibilityPartial = "Accessibility.getPartialAXTree"
	rawCDPAccessibilityQuery   = "Accessibility.queryAXTree"
	rawCDPDOMDescribeNode      = "DOM.describeNode"
	rawCDPDOMGetBoxModel       = "DOM.getBoxModel"
	rawCDPPageGetLayoutMetrics = "Page.getLayoutMetrics"
	rawCDPPerformanceMetrics   = "Performance.getMetrics"
)

var rawCDPAllowedMethods = []string{
	rawCDPAccessibilityFull,
	rawCDPAccessibilityPartial,
	rawCDPAccessibilityQuery,
	rawCDPDOMDescribeNode,
	rawCDPDOMGetBoxModel,
	rawCDPPageGetLayoutMetrics,
	rawCDPPerformanceMetrics,
}

var rawCDPAllowedMethodSet = func() map[string]struct{} {
	result := make(map[string]struct{}, len(rawCDPAllowedMethods))
	for _, method := range rawCDPAllowedMethods {
		result[method] = struct{}{}
	}
	return result
}()

var rawCDPDeniedMethods = map[string]struct{}{
	"Browser.close":                         {},
	"DOM.setFileInputFiles":                 {},
	"Page.addScriptToEvaluateOnNewDocument": {},
	"Runtime.callFunctionOn":                {},
	"Runtime.evaluate":                      {},
	"Storage.clearDataForOrigin":            {},
	"Network.clearBrowserCache":             {},
	"Network.clearBrowserCookies":           {},
	"Network.setCookie":                     {},
	"Network.setCookies":                    {},
}

var rawCDPDeniedDomains = map[string]struct{}{
	"Browser":      {},
	"Fetch":        {},
	"HeapProfiler": {},
	"IO":           {},
	"Network":      {},
	"Runtime":      {},
	"Security":     {},
	"Storage":      {},
	"SystemInfo":   {},
	"Target":       {},
}

var rawCDPProhibitedResultKeys = map[string]struct{}{
	"executionContextId": {},
	"objectId":           {},
	"scriptId":           {},
	"stream":             {},
}

type rawCDPArgs struct {
	BrowserID      string         `json:"browserId,omitempty"`
	TabID          *int           `json:"tabId,omitempty"`
	DocumentID     string         `json:"documentId,omitempty"`
	Method         string         `json:"method"`
	MethodParams   map[string]any `json:"params,omitempty"`
	MaxDepth       *int           `json:"maxDepth,omitempty"`
	MaxNodes       *int           `json:"maxNodes,omitempty"`
	MaxStringChars *int           `json:"maxStringChars,omitempty"`
	MaxBytes       *int           `json:"maxBytes,omitempty"`
	TimeoutMS      *int           `json:"timeoutMs,omitempty"`
}

type rawCDPSettings struct {
	Method         string
	MaxDepth       int
	MaxNodes       int
	MaxStringChars int
	MaxBytes       int
	TimeoutMS      int
}

type rawCDPWireResult struct {
	Method     string          `json:"method"`
	TabID      int             `json:"tabId"`
	DocumentID string          `json:"documentId"`
	Result     json.RawMessage `json:"result"`
	Truncated  bool            `json:"truncated"`
	NodeCount  int             `json:"nodeCount"`
	Warnings   []string        `json:"warnings"`
}

func (s *Service) registerRawCDPTool(mcpServer *server.MCPServer) {
	methodParams := mcp.WithObject(
		"params",
		mcp.Description("Method-specific parameters; identifiers are restricted to backendNodeId"),
		mcp.Properties(map[string]any{
			"backendNodeId":  map[string]any{"type": "integer", "minimum": 1},
			"fetchRelatives": map[string]any{"type": "boolean"},
			"accessibleName": map[string]any{"type": "string", "maxLength": maxRawCDPAccessibleNameChars},
			"role":           map[string]any{"type": "string", "maxLength": maxRawCDPRoleChars},
			"depth":          map[string]any{"type": "integer", "minimum": 0, "maximum": maxRawCDPAccessibilityDepth},
		}),
		mcp.AdditionalProperties(false),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_send_cdp_command",
			mcp.WithDescription("Send one reviewed read-only CDP method through a bounded managed session"),
			optionalBrowserID(),
			optionalTabID(),
			optionalDocumentID(),
			mcp.WithString(
				"method",
				mcp.Required(),
				mcp.Description("Reviewed read-only Chrome DevTools Protocol method"),
				mcp.Enum(rawCDPAllowedMethods...),
			),
			methodParams,
			mcp.WithNumber("maxDepth", mcp.Description("Maximum normalized response depth"), mcp.Min(1), mcp.Max(maxRawCDPMaxDepth), mcp.DefaultNumber(defaultRawCDPMaxDepth)),
			mcp.WithNumber("maxNodes", mcp.Description("Maximum normalized response values"), mcp.Min(2), mcp.Max(maxRawCDPMaxNodes), mcp.DefaultNumber(defaultRawCDPMaxNodes)),
			mcp.WithNumber("maxStringChars", mcp.Description("Maximum characters per response string"), mcp.Min(1), mcp.Max(maxRawCDPMaxStringChars), mcp.DefaultNumber(defaultRawCDPMaxStringChars)),
			mcp.WithNumber("maxBytes", mcp.Description("Maximum extension response bytes"), mcp.Min(minRawCDPMaxBytes), mcp.Max(maxRawCDPMaxBytes), mcp.DefaultNumber(defaultRawCDPMaxBytes)),
			mcp.WithNumber("timeoutMs", mcp.Description("Command timeout in milliseconds"), mcp.Min(100), mcp.Max(maxRawCDPTimeoutMS), mcp.DefaultNumber(defaultRawCDPTimeoutMS)),
		),
		mcp.NewTypedToolHandler(s.browserSendRawCDPHandler),
	)
}

func (s *Service) browserSendRawCDPHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args rawCDPArgs,
) (*mcp.CallToolResult, error) {
	startedAt := time.Now()
	params, settings, err := validateRawCDPArgs(args)
	if err != nil {
		s.auditRawCDP(args.Method, "", nil, protocol.ErrorFrom(err).Code, 0, time.Since(startedAt))
		return errorResult(err)
	}
	rootFrameID := 0
	browserID, target, raw, duration, err := s.sendRaw(
		ctx,
		args.BrowserID,
		protocol.CommandCDPSendReadOnly,
		pageTarget(args.TabID, &rootFrameID, args.DocumentID),
		map[string]any{
			"method":         settings.Method,
			"params":         params,
			"maxDepth":       settings.MaxDepth,
			"maxNodes":       settings.MaxNodes,
			"maxStringChars": settings.MaxStringChars,
			"maxBytes":       settings.MaxBytes,
		},
		&settings.TimeoutMS,
	)
	if err != nil {
		s.auditRawCDP(settings.Method, browserID, target, protocol.ErrorFrom(err).Code, 0, duration)
		return errorResultWithDuration(err, duration)
	}
	result, err := decodeRawCDPResult(raw, settings)
	if err != nil {
		s.auditRawCDP(settings.Method, browserID, target, protocol.ErrorFrom(err).Code, len(raw), duration)
		return errorResultWithDuration(err, duration)
	}
	if target == nil {
		tabID := result.TabID
		frameID := 0
		target = &protocol.Target{BrowserID: browserID, TabID: &tabID, FrameID: &frameID}
	}
	if target.TabID == nil || *target.TabID != result.TabID ||
		(target.DocumentID != "" && target.DocumentID != result.DocumentID) {
		err = invalidRawCDPResult()
		s.auditRawCDP(settings.Method, browserID, target, protocol.ErrorFrom(err).Code, len(raw), duration)
		return errorResultWithDuration(err, duration)
	}
	if target.DocumentID == "" {
		target.DocumentID = result.DocumentID
	}
	sanitized, report, err := s.sanitizeBrowserResult(raw)
	if err != nil {
		s.auditRawCDP(settings.Method, browserID, target, protocol.ErrorFrom(err).Code, len(raw), duration)
		return errorResultWithDuration(err, duration)
	}
	s.auditRawCDP(settings.Method, browserID, target, "OK", len(sanitized), duration)
	return successResultWithTargetWarningsLimited(
		browserID,
		target,
		sanitized,
		duration,
		report.Warnings(),
		s.resultLimits.MaxOutputBytes,
	)
}

func validateRawCDPArgs(args rawCDPArgs) (map[string]any, rawCDPSettings, error) {
	settings := rawCDPSettings{
		Method:         strings.TrimSpace(args.Method),
		MaxDepth:       defaultRawCDPMaxDepth,
		MaxNodes:       defaultRawCDPMaxNodes,
		MaxStringChars: defaultRawCDPMaxStringChars,
		MaxBytes:       defaultRawCDPMaxBytes,
		TimeoutMS:      defaultRawCDPTimeoutMS,
	}
	assignInt(&settings.MaxDepth, args.MaxDepth)
	assignInt(&settings.MaxNodes, args.MaxNodes)
	assignInt(&settings.MaxStringChars, args.MaxStringChars)
	assignInt(&settings.MaxBytes, args.MaxBytes)
	assignInt(&settings.TimeoutMS, args.TimeoutMS)
	if err := validateRawCDPMethod(settings.Method); err != nil {
		return nil, settings, err
	}
	if settings.MaxDepth < 1 || settings.MaxDepth > maxRawCDPMaxDepth ||
		settings.MaxNodes < 2 || settings.MaxNodes > maxRawCDPMaxNodes ||
		settings.MaxStringChars < 1 || settings.MaxStringChars > maxRawCDPMaxStringChars ||
		settings.MaxBytes < minRawCDPMaxBytes || settings.MaxBytes > maxRawCDPMaxBytes ||
		settings.TimeoutMS < 100 || settings.TimeoutMS > maxRawCDPTimeoutMS {
		return nil, settings, invalidRawCDP("raw CDP limits are outside the supported bounds")
	}
	params, err := normalizeRawCDPMethodParams(settings.Method, args.MethodParams)
	if err != nil {
		return nil, settings, err
	}
	payload, err := json.Marshal(params)
	if err != nil || len(payload) > maxRawCDPParamBytes {
		return nil, settings, invalidRawCDP("raw CDP parameters exceed the supported size")
	}
	return params, settings, nil
}

func validateRawCDPMethod(method string) error {
	if _, allowed := rawCDPAllowedMethodSet[method]; allowed {
		return nil
	}
	if _, denied := rawCDPDeniedMethods[method]; denied {
		return protocol.NewError(protocol.CodeInvalidCommand, "the CDP method is explicitly prohibited", false)
	}
	domain, _, ok := strings.Cut(method, ".")
	if ok {
		if _, denied := rawCDPDeniedDomains[domain]; denied {
			return protocol.NewError(protocol.CodeInvalidCommand, "the CDP domain is explicitly prohibited", false)
		}
	}
	return protocol.NewError(protocol.CodeInvalidCommand, "the CDP method is not allowlisted", false)
}

func normalizeRawCDPMethodParams(method string, input map[string]any) (map[string]any, error) {
	if input == nil {
		input = map[string]any{}
	}
	allowed := map[string]struct{}{}
	requiredBackend := false
	switch method {
	case rawCDPAccessibilityFull:
		allowed["depth"] = struct{}{}
	case rawCDPAccessibilityPartial:
		allowed["backendNodeId"] = struct{}{}
		allowed["fetchRelatives"] = struct{}{}
		requiredBackend = true
	case rawCDPAccessibilityQuery:
		allowed["backendNodeId"] = struct{}{}
		allowed["accessibleName"] = struct{}{}
		allowed["role"] = struct{}{}
		requiredBackend = true
	case rawCDPDOMDescribeNode:
		allowed["backendNodeId"] = struct{}{}
		allowed["depth"] = struct{}{}
		requiredBackend = true
	case rawCDPDOMGetBoxModel:
		allowed["backendNodeId"] = struct{}{}
		requiredBackend = true
	case rawCDPPageGetLayoutMetrics, rawCDPPerformanceMetrics:
	default:
		return nil, protocol.NewError(protocol.CodeInvalidCommand, "the CDP method is not allowlisted", false)
	}
	for key := range input {
		if _, ok := allowed[key]; !ok {
			return nil, invalidRawCDP(fmt.Sprintf("params.%s is not allowed for %s", key, method))
		}
	}
	result := make(map[string]any, len(input))
	if value, ok := input["backendNodeId"]; ok {
		identifier, valid := rawCDPInteger(value, 1, int64(1<<53-1))
		if !valid {
			return nil, invalidRawCDP("params.backendNodeId must be a positive safe integer")
		}
		result["backendNodeId"] = identifier
	} else if requiredBackend {
		return nil, invalidRawCDP("params.backendNodeId is required for this method")
	}
	if value, ok := input["fetchRelatives"]; ok {
		fetchRelatives, valid := value.(bool)
		if !valid {
			return nil, invalidRawCDP("params.fetchRelatives must be a boolean")
		}
		result["fetchRelatives"] = fetchRelatives
	}
	for _, field := range []struct {
		name string
		max  int
	}{
		{name: "accessibleName", max: maxRawCDPAccessibleNameChars},
		{name: "role", max: maxRawCDPRoleChars},
	} {
		if value, ok := input[field.name]; ok {
			text, valid := value.(string)
			if !valid || len([]rune(text)) > field.max {
				return nil, invalidRawCDP(fmt.Sprintf("params.%s exceeds its string limit", field.name))
			}
			result[field.name] = text
		}
	}
	if value, ok := input["depth"]; ok {
		maximum := int64(maxRawCDPAccessibilityDepth)
		if method == rawCDPDOMDescribeNode {
			maximum = maxRawCDPDescribeDepth
		}
		depth, valid := rawCDPInteger(value, 0, maximum)
		if !valid {
			return nil, invalidRawCDP("params.depth is outside the method-specific bound")
		}
		result["depth"] = depth
	}
	return result, nil
}

func rawCDPInteger(value any, minimum, maximum int64) (int64, bool) {
	var integer int64
	switch typed := value.(type) {
	case int:
		integer = int64(typed)
	case int64:
		integer = typed
	case float64:
		if math.Trunc(typed) != typed || typed < math.MinInt64 || typed > math.MaxInt64 {
			return 0, false
		}
		integer = int64(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		integer = parsed
	default:
		return 0, false
	}
	return integer, integer >= minimum && integer <= maximum
}

func decodeRawCDPResult(raw json.RawMessage, settings rawCDPSettings) (rawCDPWireResult, error) {
	var result rawCDPWireResult
	if len(raw) > settings.MaxBytes {
		return result, invalidRawCDPResult()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, invalidRawCDPResult()
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF || result.Method != settings.Method || result.TabID < 0 ||
		strings.TrimSpace(result.DocumentID) == "" || len(result.DocumentID) > 256 ||
		len(result.Result) == 0 || result.NodeCount < 1 || result.NodeCount > settings.MaxNodes ||
		len(result.Warnings) > maxRawCDPWarnings {
		return result, invalidRawCDPResult()
	}
	for _, warning := range result.Warnings {
		if strings.TrimSpace(warning) == "" || len(warning) > 1_000 {
			return result, invalidRawCDPResult()
		}
	}
	valueDecoder := json.NewDecoder(bytes.NewReader(result.Result))
	valueDecoder.UseNumber()
	var value any
	if err := valueDecoder.Decode(&value); err != nil || valueDecoder.Decode(new(any)) != io.EOF {
		return result, invalidRawCDPResult()
	}
	nodes := 0
	if err := validateRawCDPJSON(value, 0, settings, &nodes); err != nil || nodes != result.NodeCount ||
		validateRawCDPResultShape(settings.Method, value) != nil {
		return result, invalidRawCDPResult()
	}
	return result, nil
}

func validateRawCDPJSON(value any, depth int, settings rawCDPSettings, nodes *int) error {
	if depth > settings.MaxDepth || *nodes >= settings.MaxNodes {
		return invalidRawCDPResult()
	}
	(*nodes)++
	switch typed := value.(type) {
	case nil, bool:
		return nil
	case json.Number:
		number, err := typed.Float64()
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return invalidRawCDPResult()
		}
		return nil
	case string:
		if len([]rune(typed)) > settings.MaxStringChars {
			return invalidRawCDPResult()
		}
		return nil
	case []any:
		for _, item := range typed {
			if err := validateRawCDPJSON(item, depth+1, settings, nodes); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for key, item := range typed {
			if len([]rune(key)) > maxRawCDPKeyChars {
				return invalidRawCDPResult()
			}
			if _, prohibited := rawCDPProhibitedResultKeys[key]; prohibited {
				return invalidRawCDPResult()
			}
			if err := validateRawCDPJSON(item, depth+1, settings, nodes); err != nil {
				return err
			}
		}
		return nil
	default:
		return invalidRawCDPResult()
	}
}

func validateRawCDPResultShape(method string, value any) error {
	result, ok := value.(map[string]any)
	if !ok {
		return invalidRawCDPResult()
	}
	switch method {
	case rawCDPAccessibilityFull, rawCDPAccessibilityPartial, rawCDPAccessibilityQuery:
		if !onlyRawCDPKeys(result, "nodes") {
			return invalidRawCDPResult()
		}
		if _, ok := result["nodes"].([]any); !ok {
			return invalidRawCDPResult()
		}
	case rawCDPDOMDescribeNode:
		if !onlyRawCDPKeys(result, "node") {
			return invalidRawCDPResult()
		}
		if _, ok := result["node"].(map[string]any); !ok {
			return invalidRawCDPResult()
		}
	case rawCDPDOMGetBoxModel:
		if !onlyRawCDPKeys(result, "model") {
			return invalidRawCDPResult()
		}
		if _, ok := result["model"].(map[string]any); !ok {
			return invalidRawCDPResult()
		}
	case rawCDPPageGetLayoutMetrics:
		allowed := map[string]struct{}{
			"layoutViewport": {}, "visualViewport": {}, "contentSize": {},
			"cssLayoutViewport": {}, "cssVisualViewport": {}, "cssContentSize": {},
		}
		if len(result) == 0 {
			return invalidRawCDPResult()
		}
		for key, item := range result {
			if _, ok := allowed[key]; !ok {
				return invalidRawCDPResult()
			}
			if _, ok := item.(map[string]any); !ok {
				return invalidRawCDPResult()
			}
		}
	case rawCDPPerformanceMetrics:
		if !onlyRawCDPKeys(result, "metrics") {
			return invalidRawCDPResult()
		}
		metrics, ok := result["metrics"].([]any)
		if !ok || len(metrics) > 1_000 {
			return invalidRawCDPResult()
		}
		for _, item := range metrics {
			metric, ok := item.(map[string]any)
			if !ok || !onlyRawCDPKeys(metric, "name", "value") {
				return invalidRawCDPResult()
			}
			name, nameOK := metric["name"].(string)
			if !nameOK || strings.TrimSpace(name) == "" || len(name) > 200 {
				return invalidRawCDPResult()
			}
			if _, valueOK := metric["value"].(json.Number); !valueOK {
				return invalidRawCDPResult()
			}
		}
	default:
		return invalidRawCDPResult()
	}
	return nil
}

func onlyRawCDPKeys(value map[string]any, keys ...string) bool {
	if len(value) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := value[key]; !ok {
			return false
		}
	}
	return true
}

func (s *Service) auditRawCDP(
	method, browserID string,
	target *protocol.Target,
	outcome any,
	resultBytes int,
	duration time.Duration,
) {
	if s.auditLogger == nil {
		return
	}
	tabID := -1
	if target != nil && target.TabID != nil {
		tabID = *target.TabID
	}
	s.auditLogger.Printf(
		"operation=raw_cdp method=%q browserId=%q tabId=%d outcome=%s resultBytes=%d duration=%s",
		boundedRawCDPAuditMethod(method),
		boundedRawCDPAudit(browserID),
		tabID,
		fmt.Sprint(outcome),
		resultBytes,
		duration.Round(time.Microsecond),
	)
}

func boundedRawCDPAuditMethod(method string) string {
	if _, allowed := rawCDPAllowedMethodSet[method]; allowed {
		return method
	}
	if _, denied := rawCDPDeniedMethods[method]; denied {
		return method
	}
	domain, _, ok := strings.Cut(method, ".")
	if !ok || domain == "" || len(domain) > 64 {
		return "invalid"
	}
	for _, character := range domain {
		switch {
		case character >= 'A' && character <= 'Z':
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		case character == '_':
		default:
			return "invalid"
		}
	}
	return "unreviewed:" + domain
}

func boundedRawCDPAudit(value string) string {
	value = strings.Map(func(character rune) rune {
		if character < 32 || character == 127 {
			return -1
		}
		return character
	}, value)
	characters := []rune(value)
	if len(characters) > 128 {
		characters = characters[:128]
	}
	return string(characters)
}

func invalidRawCDP(message string) *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}

func invalidRawCDPResult() *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, "the browser returned an invalid raw CDP result", false)
}
