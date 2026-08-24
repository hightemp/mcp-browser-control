package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"regexp"
	"strings"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultEvaluationTimeoutMS      = 5_000
	maxEvaluationTimeoutMS          = 10_000
	maxEvaluationExpressionChars    = 32_768
	defaultEvaluationMaxDepth       = 6
	maxEvaluationMaxDepth           = 10
	defaultEvaluationMaxNodes       = 1_000
	maxEvaluationMaxNodes           = 5_000
	defaultEvaluationMaxStringChars = 10_000
	maxEvaluationMaxStringChars     = 100_000
	minEvaluationMaxBytes           = 64 * 1_024
	defaultEvaluationMaxBytes       = 512 * 1_024
	maxEvaluationMaxBytes           = 1_000_000
	maxEvaluationKeyChars           = 256
	maxEvaluationWarnings           = 4
	maxEvaluationExceptionChars     = 2_000
)

var evaluationUnserializablePattern = regexp.MustCompile(`^(?:NaN|Infinity|-Infinity|-0|-?[0-9]+n)$`)

type evaluationArgs struct {
	BrowserID      string `json:"browserId,omitempty"`
	TabID          *int   `json:"tabId,omitempty"`
	DocumentID     string `json:"documentId,omitempty"`
	Expression     string `json:"expression"`
	AwaitPromise   *bool  `json:"awaitPromise,omitempty"`
	MaxDepth       *int   `json:"maxDepth,omitempty"`
	MaxNodes       *int   `json:"maxNodes,omitempty"`
	MaxStringChars *int   `json:"maxStringChars,omitempty"`
	MaxBytes       *int   `json:"maxBytes,omitempty"`
	TimeoutMS      *int   `json:"timeoutMs,omitempty"`
}

type evaluationSettings struct {
	AwaitPromise   bool
	MaxDepth       int
	MaxNodes       int
	MaxStringChars int
	MaxBytes       int
	TimeoutMS      int
}

type evaluationWireResult struct {
	Completed           bool                 `json:"completed"`
	TabID               int                  `json:"tabId"`
	DocumentID          string               `json:"documentId"`
	World               string               `json:"world"`
	ValueType           string               `json:"valueType"`
	Value               json.RawMessage      `json:"value,omitempty"`
	UnserializableValue string               `json:"unserializableValue,omitempty"`
	Exception           *evaluationException `json:"exception,omitempty"`
	Truncated           bool                 `json:"truncated"`
	NodeCount           int                  `json:"nodeCount"`
	Warnings            []string             `json:"warnings"`
}

type evaluationException struct {
	Text         string `json:"text"`
	Description  string `json:"description,omitempty"`
	LineNumber   int    `json:"lineNumber"`
	ColumnNumber int    `json:"columnNumber"`
}

func (s *Service) registerEvaluationTool(mcpServer *server.MCPServer) {
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_evaluate_javascript",
			mcp.WithDescription("Evaluate one bounded JavaScript expression in an ephemeral isolated world"),
			optionalBrowserID(),
			optionalTabID(),
			optionalDocumentID(),
			mcp.WithString(
				"expression",
				mcp.Required(),
				mcp.Description("JavaScript expression evaluated only in the root frame isolated world"),
				mcp.MinLength(1),
				mcp.MaxLength(maxEvaluationExpressionChars),
			),
			mcp.WithBoolean(
				"awaitPromise",
				mcp.Description("Await a promise returned by the expression"),
				mcp.DefaultBool(true),
			),
			mcp.WithNumber(
				"maxDepth",
				mcp.Description("Maximum JSON result depth"),
				mcp.Min(0),
				mcp.Max(maxEvaluationMaxDepth),
				mcp.DefaultNumber(defaultEvaluationMaxDepth),
			),
			mcp.WithNumber(
				"maxNodes",
				mcp.Description("Maximum values in the normalized result"),
				mcp.Min(1),
				mcp.Max(maxEvaluationMaxNodes),
				mcp.DefaultNumber(defaultEvaluationMaxNodes),
			),
			mcp.WithNumber(
				"maxStringChars",
				mcp.Description("Maximum characters in each result string"),
				mcp.Min(1),
				mcp.Max(maxEvaluationMaxStringChars),
				mcp.DefaultNumber(defaultEvaluationMaxStringChars),
			),
			mcp.WithNumber(
				"maxBytes",
				mcp.Description("Maximum extension result bytes"),
				mcp.Min(minEvaluationMaxBytes),
				mcp.Max(maxEvaluationMaxBytes),
				mcp.DefaultNumber(defaultEvaluationMaxBytes),
			),
			mcp.WithNumber(
				"timeoutMs",
				mcp.Description("Evaluation and command timeout in milliseconds"),
				mcp.Min(100),
				mcp.Max(maxEvaluationTimeoutMS),
				mcp.DefaultNumber(defaultEvaluationTimeoutMS),
			),
		),
		mcp.NewTypedToolHandler(s.browserEvaluateJavaScriptHandler),
	)
}

func (s *Service) browserEvaluateJavaScriptHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args evaluationArgs,
) (*mcp.CallToolResult, error) {
	params, settings, err := validateEvaluationArgs(args)
	if err != nil {
		return errorResult(err)
	}
	rootFrameID := 0
	browserID, target, raw, duration, err := s.sendRaw(
		ctx,
		args.BrowserID,
		protocol.CommandRuntimeEvaluateIsolated,
		pageTarget(args.TabID, &rootFrameID, args.DocumentID),
		params,
		&settings.TimeoutMS,
	)
	if err != nil {
		return errorResultWithDuration(err, duration)
	}
	result, err := decodeEvaluationResult(raw, settings)
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
		return errorResultWithDuration(invalidEvaluationResult(), duration)
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

func validateEvaluationArgs(args evaluationArgs) (map[string]any, evaluationSettings, error) {
	settings := evaluationSettings{
		AwaitPromise:   true,
		MaxDepth:       defaultEvaluationMaxDepth,
		MaxNodes:       defaultEvaluationMaxNodes,
		MaxStringChars: defaultEvaluationMaxStringChars,
		MaxBytes:       defaultEvaluationMaxBytes,
		TimeoutMS:      defaultEvaluationTimeoutMS,
	}
	if args.AwaitPromise != nil {
		settings.AwaitPromise = *args.AwaitPromise
	}
	assignInt(&settings.MaxDepth, args.MaxDepth)
	assignInt(&settings.MaxNodes, args.MaxNodes)
	assignInt(&settings.MaxStringChars, args.MaxStringChars)
	assignInt(&settings.MaxBytes, args.MaxBytes)
	assignInt(&settings.TimeoutMS, args.TimeoutMS)
	if strings.TrimSpace(args.Expression) == "" || len(args.Expression) > maxEvaluationExpressionChars {
		return nil, settings, invalidEvaluation("expression must contain between 1 and 32768 characters")
	}
	if settings.MaxDepth < 0 || settings.MaxDepth > maxEvaluationMaxDepth ||
		settings.MaxNodes < 1 || settings.MaxNodes > maxEvaluationMaxNodes ||
		settings.MaxStringChars < 1 || settings.MaxStringChars > maxEvaluationMaxStringChars ||
		settings.MaxBytes < minEvaluationMaxBytes || settings.MaxBytes > maxEvaluationMaxBytes ||
		settings.TimeoutMS < 100 || settings.TimeoutMS > maxEvaluationTimeoutMS {
		return nil, settings, invalidEvaluation("evaluation limits are outside the supported bounds")
	}
	return map[string]any{
		"expression":     args.Expression,
		"awaitPromise":   settings.AwaitPromise,
		"maxDepth":       settings.MaxDepth,
		"maxNodes":       settings.MaxNodes,
		"maxStringChars": settings.MaxStringChars,
		"maxBytes":       settings.MaxBytes,
	}, settings, nil
}

func decodeEvaluationResult(
	raw json.RawMessage,
	settings evaluationSettings,
) (evaluationWireResult, error) {
	var result evaluationWireResult
	if len(raw) > settings.MaxBytes {
		return result, invalidEvaluationResult()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, invalidEvaluationResult()
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF || result.TabID < 0 ||
		strings.TrimSpace(result.DocumentID) == "" || len(result.DocumentID) > 256 ||
		result.World != "isolated" || result.NodeCount < 0 || result.NodeCount > settings.MaxNodes ||
		len(result.Warnings) > maxEvaluationWarnings {
		return result, invalidEvaluationResult()
	}
	for _, warning := range result.Warnings {
		if strings.TrimSpace(warning) == "" || len(warning) > 1_000 {
			return result, invalidEvaluationResult()
		}
	}
	if result.Completed {
		if result.Exception != nil {
			return result, invalidEvaluationResult()
		}
		if err := validateEvaluationValue(result, settings); err != nil {
			return result, invalidEvaluationResult()
		}
		return result, nil
	}
	if result.Exception == nil || result.ValueType != "undefined" || len(result.Value) != 0 ||
		result.UnserializableValue != "" || result.Truncated || result.NodeCount != 0 ||
		strings.TrimSpace(result.Exception.Text) == "" ||
		len(result.Exception.Text) > maxEvaluationExceptionChars ||
		len(result.Exception.Description) > maxEvaluationExceptionChars ||
		result.Exception.LineNumber < 0 || result.Exception.ColumnNumber < 0 {
		return result, invalidEvaluationResult()
	}
	return result, nil
}

func validateEvaluationValue(result evaluationWireResult, settings evaluationSettings) error {
	switch result.ValueType {
	case "undefined", "unsupported":
		if len(result.Value) != 0 || result.UnserializableValue != "" || result.NodeCount != 1 {
			return invalidEvaluationResult()
		}
		return nil
	case "unserializable":
		if len(result.Value) != 0 || len(result.UnserializableValue) > 1_000 ||
			!evaluationUnserializablePattern.MatchString(result.UnserializableValue) || result.NodeCount != 1 {
			return invalidEvaluationResult()
		}
		return nil
	case "null", "boolean", "number", "string", "array", "object":
		if len(result.Value) == 0 || result.UnserializableValue != "" {
			return invalidEvaluationResult()
		}
	default:
		return invalidEvaluationResult()
	}

	decoder := json.NewDecoder(bytes.NewReader(result.Value))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || decoder.Decode(new(any)) != io.EOF {
		return invalidEvaluationResult()
	}
	nodeCount := 0
	if err := validateEvaluationJSON(value, 0, settings, &nodeCount); err != nil ||
		nodeCount != result.NodeCount || evaluationJSONType(value) != result.ValueType {
		return invalidEvaluationResult()
	}
	return nil
}

func validateEvaluationJSON(value any, depth int, settings evaluationSettings, nodes *int) error {
	if depth > settings.MaxDepth || *nodes >= settings.MaxNodes {
		return invalidEvaluationResult()
	}
	*nodes++
	switch typed := value.(type) {
	case nil, bool:
		return nil
	case json.Number:
		number, err := typed.Float64()
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return invalidEvaluationResult()
		}
		return nil
	case string:
		if len([]rune(typed)) > settings.MaxStringChars {
			return invalidEvaluationResult()
		}
		return nil
	case []any:
		for _, item := range typed {
			if err := validateEvaluationJSON(item, depth+1, settings, nodes); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for key, item := range typed {
			if len([]rune(key)) > maxEvaluationKeyChars {
				return invalidEvaluationResult()
			}
			if err := validateEvaluationJSON(item, depth+1, settings, nodes); err != nil {
				return err
			}
		}
		return nil
	default:
		return invalidEvaluationResult()
	}
}

func evaluationJSONType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return ""
	}
}

func invalidEvaluation(message string) *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}

func invalidEvaluationResult() *protocol.Error {
	return protocol.NewError(
		protocol.CodeInvalidMessage,
		"the browser returned an invalid isolated evaluation result",
		false,
	)
}
