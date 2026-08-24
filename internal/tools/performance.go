package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/artifacts"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultPerformanceCaptureDurationMS = 1_000
	minPerformanceCaptureDurationMS     = 100
	maxPerformanceCaptureDurationMS     = 10_000
	defaultPerformanceCaptureBytes      = 2_000_000
	minPerformanceCaptureBytes          = 64 * 1_024
	maxPerformanceMetrics               = 200
	maxPerformanceMetricNameChars       = 200
	maxPerformanceWarnings              = 4
)

var performanceCaptureKinds = []string{"trace", "coverage", "cpuProfile", "audits"}

type performanceTargetArgs struct {
	BrowserID  string `json:"browserId,omitempty"`
	TabID      *int   `json:"tabId,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
	TimeoutMS  *int   `json:"timeoutMs,omitempty"`
}

type performanceCaptureArgs struct {
	performanceTargetArgs
	Kind       string `json:"kind"`
	DurationMS *int   `json:"durationMs,omitempty"`
	MaxBytes   *int   `json:"maxBytes,omitempty"`
}

type performanceMetric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

type performanceMetricsWireResult struct {
	TabID      int                 `json:"tabId"`
	DocumentID string              `json:"documentId"`
	Metrics    []performanceMetric `json:"metrics"`
	Warnings   []string            `json:"warnings"`
}

type performanceCaptureWireResult struct {
	Kind       string   `json:"kind"`
	MIMEType   string   `json:"mimeType"`
	DataBase64 string   `json:"dataBase64"`
	ByteLength int      `json:"byteLength"`
	TabID      int      `json:"tabId"`
	DocumentID string   `json:"documentId"`
	DurationMS int      `json:"durationMs"`
	Warnings   []string `json:"warnings"`
}

func (s *Service) registerPerformanceTools(mcpServer *server.MCPServer) {
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_get_performance_metrics",
			mcp.WithDescription("Read bounded runtime performance metrics from one root document"),
			optionalBrowserID(),
			optionalTabID(),
			optionalDocumentID(),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserGetPerformanceMetricsHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_capture_performance",
			mcp.WithDescription("Capture one bounded trace, coverage, CPU profile, or audit artifact"),
			optionalBrowserID(),
			optionalTabID(),
			optionalDocumentID(),
			mcp.WithString(
				"kind",
				mcp.Required(),
				mcp.Description("Bounded diagnostic capture type"),
				mcp.Enum(performanceCaptureKinds...),
			),
			mcp.WithNumber(
				"durationMs",
				mcp.Description("Capture duration in milliseconds"),
				mcp.Min(minPerformanceCaptureDurationMS),
				mcp.Max(maxPerformanceCaptureDurationMS),
				mcp.DefaultNumber(defaultPerformanceCaptureDurationMS),
			),
			mcp.WithNumber(
				"maxBytes",
				mcp.Description("Maximum decoded artifact bytes"),
				mcp.Min(minPerformanceCaptureBytes),
				mcp.Max(defaultPerformanceCaptureBytes),
				mcp.DefaultNumber(defaultPerformanceCaptureBytes),
			),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserCapturePerformanceHandler),
	)
}

func (s *Service) browserGetPerformanceMetricsHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args performanceTargetArgs,
) (*mcp.CallToolResult, error) {
	startedAt := time.Now()
	rootFrameID := 0
	browserID, target, raw, duration, err := s.sendRaw(
		ctx,
		args.BrowserID,
		protocol.CommandPerformanceMetrics,
		pageTarget(args.TabID, &rootFrameID, args.DocumentID),
		map[string]any{},
		args.TimeoutMS,
	)
	if err != nil {
		s.auditPerformance("metrics", browserID, target, protocol.ErrorFrom(err).Code, 0, duration)
		return errorResultWithDuration(err, duration)
	}
	wire, err := decodePerformanceMetrics(raw)
	if err != nil || !performanceTargetMatches(target, wire.TabID, wire.DocumentID) {
		if err == nil {
			err = invalidPerformanceResult()
		}
		s.auditPerformance("metrics", browserID, target, protocol.ErrorFrom(err).Code, len(raw), duration)
		return errorResultWithDuration(err, duration)
	}
	if target == nil {
		tabID := wire.TabID
		frameID := 0
		target = &protocol.Target{
			BrowserID:  browserID,
			TabID:      &tabID,
			FrameID:    &frameID,
			DocumentID: wire.DocumentID,
		}
	} else if target.DocumentID == "" {
		target.DocumentID = wire.DocumentID
	}
	sanitized, report, err := s.sanitizeBrowserResult(raw)
	if err != nil {
		s.auditPerformance("metrics", browserID, target, protocol.ErrorFrom(err).Code, len(raw), duration)
		return errorResultWithDuration(err, duration)
	}
	s.auditPerformance("metrics", browserID, target, "OK", len(sanitized), time.Since(startedAt))
	return successResultWithTargetWarningsLimited(
		browserID,
		target,
		sanitized,
		duration,
		report.Warnings(),
		s.resultLimits.MaxOutputBytes,
	)
}

func (s *Service) browserCapturePerformanceHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args performanceCaptureArgs,
) (*mcp.CallToolResult, error) {
	startedAt := time.Now()
	kind, durationMS, maxBytes, err := validatePerformanceCaptureArgs(args)
	if err != nil {
		s.auditPerformance(kind, "", nil, protocol.ErrorFrom(err).Code, 0, time.Since(startedAt))
		return errorResult(err)
	}
	if s.artifacts == nil {
		err = protocol.NewError(
			protocol.CodeCapabilityUnavailable,
			"performance artifact storage is unavailable",
			false,
		)
		s.auditPerformance(kind, "", nil, protocol.ErrorFrom(err).Code, 0, time.Since(startedAt))
		return errorResult(err)
	}
	if args.TimeoutMS != nil && *args.TimeoutMS < durationMS+1_000 {
		err = invalidPerformance("timeoutMs must exceed durationMs by at least 1000 ms")
		s.auditPerformance(kind, "", nil, protocol.ErrorFrom(err).Code, 0, time.Since(startedAt))
		return errorResult(err)
	}
	operationCtx, cancel, err := toolContext(ctx, args.TimeoutMS)
	if err != nil {
		s.auditPerformance(kind, "", nil, protocol.ErrorFrom(err).Code, 0, time.Since(startedAt))
		return errorResult(err)
	}
	defer cancel()

	rootFrameID := 0
	browserID, target, raw, duration, err := s.sendRaw(
		operationCtx,
		args.BrowserID,
		protocol.CommandPerformanceCapture,
		pageTarget(args.TabID, &rootFrameID, args.DocumentID),
		map[string]any{"kind": kind, "durationMs": durationMS, "maxBytes": maxBytes},
		nil,
	)
	if err != nil {
		s.auditPerformance(kind, browserID, target, protocol.ErrorFrom(err).Code, 0, duration)
		return errorResultWithDuration(err, duration)
	}
	wire, artifactData, err := decodePerformanceCapture(raw, kind, durationMS, maxBytes)
	if err != nil || !performanceTargetMatches(target, wire.TabID, wire.DocumentID) {
		if err == nil {
			err = invalidPerformanceResult()
		}
		s.auditPerformance(kind, browserID, target, protocol.ErrorFrom(err).Code, len(raw), duration)
		return errorResultWithDuration(err, duration)
	}
	if target == nil {
		tabID := wire.TabID
		frameID := 0
		target = &protocol.Target{
			BrowserID:  browserID,
			TabID:      &tabID,
			FrameID:    &frameID,
			DocumentID: wire.DocumentID,
		}
	} else if target.DocumentID == "" {
		target.DocumentID = wire.DocumentID
	}

	storeStarted := time.Now()
	metadata, err := s.artifacts.Put(
		operationCtx,
		wire.MIMEType,
		artifactData,
		artifacts.RedactionMetadata{},
	)
	duration += time.Since(storeStarted)
	if err != nil {
		s.auditPerformance(kind, browserID, target, protocol.ErrorFrom(err).Code, len(artifactData), duration)
		return errorResultWithDuration(fmt.Errorf("store performance artifact: %w", err), duration)
	}

	warnings := append([]string(nil), wire.Warnings...)
	warnings = append(
		warnings,
		"Performance artifacts may contain sensitive URLs, script metadata, and page activity; no content redaction was applied",
	)
	result := map[string]any{
		"kind":                wire.Kind,
		"artifactUri":         metadata.URI,
		"artifactMetadataUri": metadata.URI + "/metadata",
		"mimeType":            metadata.MIMEType,
		"size":                metadata.Size,
		"tabId":               wire.TabID,
		"documentId":          wire.DocumentID,
		"captureDurationMs":   wire.DurationMS,
		"expiresAt":           metadata.ExpiresAt,
		"redactionApplied":    metadata.Redaction.Applied,
		"warnings":            warnings,
	}
	s.auditPerformance(kind, browserID, target, "OK", len(artifactData), duration)
	return successResultWithTarget(browserID, target, result, duration)
}

func validatePerformanceCaptureArgs(args performanceCaptureArgs) (string, int, int, error) {
	kind := strings.TrimSpace(args.Kind)
	if !performanceStringAllowed(performanceCaptureKinds, kind) {
		return kind, 0, 0, protocol.NewError(
			protocol.CodeInvalidCommand,
			"the performance capture kind is not allowlisted",
			false,
		)
	}
	durationMS := defaultPerformanceCaptureDurationMS
	maxBytes := defaultPerformanceCaptureBytes
	assignInt(&durationMS, args.DurationMS)
	assignInt(&maxBytes, args.MaxBytes)
	if durationMS < minPerformanceCaptureDurationMS || durationMS > maxPerformanceCaptureDurationMS {
		return kind, durationMS, maxBytes, invalidPerformance("durationMs is outside the supported range")
	}
	if maxBytes < minPerformanceCaptureBytes || maxBytes > defaultPerformanceCaptureBytes {
		return kind, durationMS, maxBytes, invalidPerformance("maxBytes is outside the supported range")
	}
	return kind, durationMS, maxBytes, nil
}

func decodePerformanceMetrics(raw json.RawMessage) (performanceMetricsWireResult, error) {
	var result performanceMetricsWireResult
	if err := decodeStrictJSON(raw, &result); err != nil || result.TabID < 0 ||
		strings.TrimSpace(result.DocumentID) == "" || len(result.DocumentID) > 256 ||
		len(result.Metrics) > maxPerformanceMetrics || len(result.Warnings) > maxPerformanceWarnings {
		return performanceMetricsWireResult{}, invalidPerformanceResult()
	}
	seen := make(map[string]struct{}, len(result.Metrics))
	for _, metric := range result.Metrics {
		if strings.TrimSpace(metric.Name) == "" || len([]rune(metric.Name)) > maxPerformanceMetricNameChars ||
			math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) {
			return performanceMetricsWireResult{}, invalidPerformanceResult()
		}
		if _, exists := seen[metric.Name]; exists {
			return performanceMetricsWireResult{}, invalidPerformanceResult()
		}
		seen[metric.Name] = struct{}{}
	}
	if !validPerformanceWarnings(result.Warnings) {
		return performanceMetricsWireResult{}, invalidPerformanceResult()
	}
	return result, nil
}

func decodePerformanceCapture(
	raw json.RawMessage,
	kind string,
	durationMS, maxBytes int,
) (performanceCaptureWireResult, []byte, error) {
	var result performanceCaptureWireResult
	if len(raw) > base64.StdEncoding.EncodedLen(maxBytes)+4_096 ||
		decodeStrictJSON(raw, &result) != nil || result.Kind != kind ||
		result.MIMEType != "application/json" || result.TabID < 0 ||
		strings.TrimSpace(result.DocumentID) == "" || len(result.DocumentID) > 256 ||
		result.ByteLength < 2 || result.ByteLength > maxBytes || result.DurationMS < 0 ||
		result.DurationMS != durationMS || len(result.Warnings) > maxPerformanceWarnings ||
		!validPerformanceWarnings(result.Warnings) ||
		len(result.DataBase64) > base64.StdEncoding.EncodedLen(maxBytes) {
		return performanceCaptureWireResult{}, nil, invalidPerformanceResult()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(result.DataBase64)
	if err != nil || len(data) != result.ByteLength || !json.Valid(data) {
		return performanceCaptureWireResult{}, nil, invalidPerformanceResult()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return performanceCaptureWireResult{}, nil, invalidPerformanceResult()
	}
	if _, object := value.(map[string]any); !object {
		return performanceCaptureWireResult{}, nil, invalidPerformanceResult()
	}
	return result, data, nil
}

func decodeStrictJSON(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return invalidPerformanceResult()
	}
	return nil
}

func performanceTargetMatches(target *protocol.Target, tabID int, documentID string) bool {
	if target == nil {
		return tabID >= 0 && strings.TrimSpace(documentID) != ""
	}
	return target.TabID != nil && *target.TabID == tabID &&
		(target.DocumentID == "" || target.DocumentID == documentID)
}

func validPerformanceWarnings(warnings []string) bool {
	for _, warning := range warnings {
		if strings.TrimSpace(warning) == "" || len(warning) > 1_000 {
			return false
		}
	}
	return true
}

func (s *Service) auditPerformance(
	kind, browserID string,
	target *protocol.Target,
	outcome any,
	resultBytes int,
	duration time.Duration,
) {
	if s.auditLogger == nil {
		return
	}
	if !performanceStringAllowed(append([]string{"metrics"}, performanceCaptureKinds...), kind) {
		kind = "invalid"
	}
	tabID := -1
	if target != nil && target.TabID != nil {
		tabID = *target.TabID
	}
	s.auditLogger.Printf(
		"operation=performance kind=%q browserId=%q tabId=%d outcome=%s resultBytes=%d duration=%s",
		kind,
		boundedRawCDPAudit(browserID),
		tabID,
		fmt.Sprint(outcome),
		resultBytes,
		duration.Round(time.Microsecond),
	)
}

func performanceStringAllowed(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func invalidPerformance(message string) *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}

func invalidPerformanceResult() *protocol.Error {
	return protocol.NewError(
		protocol.CodeInvalidMessage,
		"the browser returned an invalid performance result",
		false,
	)
}
