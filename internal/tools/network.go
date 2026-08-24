package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/artifacts"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/redaction"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultNetworkMaxEntries    = 1_000
	maxNetworkMaxEntries        = 5_000
	defaultNetworkReadLimit     = 50
	maxNetworkReadLimit         = 200
	defaultNetworkReadMaxBytes  = 512 * 1_024
	minNetworkReadMaxBytes      = 64 * 1_024
	maxNetworkReadMaxBytes      = 1_000_000
	defaultNetworkBodyMaxBytes  = 256 * 1_024
	minNetworkBodyMaxBytes      = 1_024
	maxNetworkBodyMaxBytes      = 1_000_000
	defaultNetworkHARMaxBytes   = 2_000_000
	minNetworkHARMaxBytes       = 64 * 1_024
	maxNetworkWarnings          = 4
	maxNetworkRedactionRules    = 16
	maxNetworkRedactionRuleSize = 100
)

var networkResourceTypes = []string{
	"Document", "Stylesheet", "Image", "Media", "Font", "Script", "TextTrack",
	"XHR", "Fetch", "Prefetch", "EventSource", "WebSocket", "Manifest",
	"SignedExchange", "Ping", "CSPViolationReport", "Preflight", "Other",
}

type networkTargetArgs struct {
	BrowserID  string `json:"browserId,omitempty"`
	TabID      *int   `json:"tabId,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
	TimeoutMS  *int   `json:"timeoutMs,omitempty"`
}

type networkStartArgs struct {
	networkTargetArgs
	MaxEntries *int `json:"maxEntries,omitempty"`
}

type networkReadArgs struct {
	networkTargetArgs
	Cursor        string   `json:"cursor,omitempty"`
	Limit         *int     `json:"limit,omitempty"`
	ResourceTypes []string `json:"resourceTypes,omitempty"`
	FailedOnly    *bool    `json:"failedOnly,omitempty"`
	StatusMin     *int     `json:"statusMin,omitempty"`
	StatusMax     *int     `json:"statusMax,omitempty"`
	Since         string   `json:"since,omitempty"`
	MaxBytes      *int     `json:"maxBytes,omitempty"`
}

type networkBodyArgs struct {
	networkTargetArgs
	EntryID   string `json:"entryId"`
	Direction string `json:"direction"`
	MaxBytes  *int   `json:"maxBytes,omitempty"`
}

type networkHARArgs struct {
	networkTargetArgs
	MaxBytes *int `json:"maxBytes,omitempty"`
}

type networkArtifactWireResult struct {
	Kind             string   `json:"kind"`
	MIMEType         string   `json:"mimeType"`
	DataBase64       string   `json:"dataBase64"`
	ByteLength       int      `json:"byteLength"`
	TabID            int      `json:"tabId"`
	DocumentID       string   `json:"documentId"`
	EntryID          string   `json:"entryId,omitempty"`
	EntryCount       int      `json:"entryCount,omitempty"`
	Truncated        bool     `json:"truncated"`
	RedactionApplied bool     `json:"redactionApplied"`
	RedactionRules   []string `json:"redactionRules"`
	Warnings         []string `json:"warnings"`
}

func (s *Service) registerNetworkTools(mcpServer *server.MCPServer) {
	mcpServer.AddTool(
		newNetworkTool(
			"browser_start_network_capture",
			"Start bounded network metadata capture in one root document",
			mcp.WithNumber("maxEntries", mcp.Description("Maximum retained request entries"), mcp.Min(1), mcp.Max(maxNetworkMaxEntries), mcp.DefaultNumber(defaultNetworkMaxEntries)),
		),
		mcp.NewTypedToolHandler(s.browserStartNetworkHandler),
	)
	for _, registration := range []struct {
		name        string
		description string
		command     string
	}{
		{"browser_stop_network_capture", "Stop network capture and retain bounded metadata", protocol.CommandNetworkStop},
		{"browser_clear_network_log", "Clear retained network capture metadata", protocol.CommandNetworkClear},
	} {
		registration := registration
		mcpServer.AddTool(
			newNetworkTool(registration.name, registration.description),
			mcp.NewTypedToolHandler(func(ctx context.Context, _ mcp.CallToolRequest, args networkTargetArgs) (*mcp.CallToolResult, error) {
				return s.sendNetworkInline(ctx, registration.name, registration.command, args, map[string]any{})
			}),
		)
	}
	mcpServer.AddTool(
		newNetworkTool(
			"browser_get_network_log",
			"Read filtered paginated network metadata from one root document",
			mcp.WithString("cursor", mcp.Description("Cursor returned by a previous network read")),
			mcp.WithNumber("limit", mcp.Description("Maximum entries to return"), mcp.Min(1), mcp.Max(maxNetworkReadLimit), mcp.DefaultNumber(defaultNetworkReadLimit)),
			mcp.WithArray("resourceTypes", mcp.Description("CDP resource types to include"), mcp.Items(map[string]any{"type": "string", "enum": networkResourceTypes}), mcp.MaxItems(len(networkResourceTypes))),
			mcp.WithBoolean("failedOnly", mcp.Description("Return only failed requests")),
			mcp.WithNumber("statusMin", mcp.Description("Minimum HTTP response status"), mcp.Min(100), mcp.Max(599)),
			mcp.WithNumber("statusMax", mcp.Description("Maximum HTTP response status"), mcp.Min(100), mcp.Max(599)),
			mcp.WithString("since", mcp.Description("RFC 3339 request start lower bound")),
			mcp.WithNumber("maxBytes", mcp.Description("Maximum serialized extension result bytes"), mcp.Min(minNetworkReadMaxBytes), mcp.Max(maxNetworkReadMaxBytes), mcp.DefaultNumber(defaultNetworkReadMaxBytes)),
		),
		mcp.NewTypedToolHandler(s.browserGetNetworkHandler),
	)
	mcpServer.AddTool(
		newNetworkTool(
			"browser_get_network_body",
			"Store one redacted same-origin textual network body as a temporary artifact",
			mcp.WithString("entryId", mcp.Required(), mcp.Description("Public entry ID returned by browser_get_network_log"), mcp.MaxLength(32)),
			mcp.WithString("direction", mcp.Required(), mcp.Description("Request or response body"), mcp.Enum("request", "response")),
			mcp.WithNumber("maxBytes", mcp.Description("Maximum decoded and redacted body bytes"), mcp.Min(minNetworkBodyMaxBytes), mcp.Max(maxNetworkBodyMaxBytes), mcp.DefaultNumber(defaultNetworkBodyMaxBytes)),
		),
		mcp.NewTypedToolHandler(s.browserGetNetworkBodyHandler),
	)
	mcpServer.AddTool(
		newNetworkTool(
			"browser_export_network_har",
			"Store bounded redacted HAR-like network metadata as a temporary artifact",
			mcp.WithNumber("maxBytes", mcp.Description("Maximum redacted HAR artifact bytes"), mcp.Min(minNetworkHARMaxBytes), mcp.Max(defaultNetworkHARMaxBytes), mcp.DefaultNumber(defaultNetworkHARMaxBytes)),
		),
		mcp.NewTypedToolHandler(s.browserExportNetworkHARHandler),
	)
}

func newNetworkTool(name, description string, extra ...mcp.ToolOption) mcp.Tool {
	options := []mcp.ToolOption{
		mcp.WithDescription(description),
		optionalBrowserID(),
		optionalTabID(),
		optionalDocumentID(),
	}
	options = append(options, extra...)
	options = append(options, optionalTimeout())
	return mcp.NewTool(name, options...)
}

func (s *Service) browserStartNetworkHandler(ctx context.Context, _ mcp.CallToolRequest, args networkStartArgs) (*mcp.CallToolResult, error) {
	maxEntries := defaultNetworkMaxEntries
	assignInt(&maxEntries, args.MaxEntries)
	if maxEntries < 1 || maxEntries > maxNetworkMaxEntries {
		return errorResult(invalidNetwork("maxEntries is outside the supported range"))
	}
	return s.sendNetworkInline(ctx, "browser_start_network_capture", protocol.CommandNetworkStart, args.networkTargetArgs, map[string]any{"maxEntries": maxEntries})
}

func (s *Service) browserGetNetworkHandler(ctx context.Context, _ mcp.CallToolRequest, args networkReadArgs) (*mcp.CallToolResult, error) {
	params, err := validateNetworkReadArgs(args)
	if err != nil {
		return errorResult(err)
	}
	return s.sendNetworkInline(ctx, "browser_get_network_log", protocol.CommandNetworkRead, args.networkTargetArgs, params)
}

func (s *Service) sendNetworkInline(ctx context.Context, operation, command string, args networkTargetArgs, params map[string]any) (*mcp.CallToolResult, error) {
	startedAt := time.Now()
	rootFrameID := 0
	browserID, target, raw, duration, err := s.sendRaw(ctx, args.BrowserID, command, pageTarget(args.TabID, &rootFrameID, args.DocumentID), params, args.TimeoutMS)
	if err != nil {
		s.auditNetwork(operation, "", browserID, target, protocol.ErrorFrom(err).Code, 0, duration)
		return errorResultWithDuration(err, duration)
	}
	tabID, documentID, err := validateNetworkInlineTarget(raw, target)
	if err != nil {
		s.auditNetwork(operation, "", browserID, target, protocol.ErrorFrom(err).Code, len(raw), duration)
		return errorResultWithDuration(err, duration)
	}
	if target == nil {
		frameID := 0
		target = &protocol.Target{BrowserID: browserID, TabID: &tabID, FrameID: &frameID, DocumentID: documentID}
	} else if target.DocumentID == "" {
		target.DocumentID = documentID
	}
	sanitized, report, err := s.sanitizeBrowserResult(raw)
	if err != nil {
		s.auditNetwork(operation, "", browserID, target, protocol.ErrorFrom(err).Code, len(raw), duration)
		return errorResultWithDuration(err, duration)
	}
	s.auditNetwork(operation, "", browserID, target, "OK", len(sanitized), time.Since(startedAt))
	return successResultWithTargetWarningsLimited(browserID, target, sanitized, duration, report.Warnings(), s.resultLimits.MaxOutputBytes)
}

func validateNetworkReadArgs(args networkReadArgs) (map[string]any, error) {
	limit := defaultNetworkReadLimit
	maxBytes := defaultNetworkReadMaxBytes
	assignInt(&limit, args.Limit)
	assignInt(&maxBytes, args.MaxBytes)
	if limit < 1 || limit > maxNetworkReadLimit || maxBytes < minNetworkReadMaxBytes || maxBytes > maxNetworkReadMaxBytes {
		return nil, invalidNetwork("network read limits are outside the supported range")
	}
	if args.Cursor != "" {
		cursor, err := strconv.ParseUint(args.Cursor, 10, 53)
		if err != nil || cursor == 0 {
			return nil, invalidNetwork("cursor must be a positive safe integer string")
		}
	}
	if err := validateConsoleFilter(args.ResourceTypes, networkResourceTypes, "resourceTypes"); err != nil {
		return nil, invalidNetwork("resourceTypes contains unsupported or duplicate values")
	}
	if args.StatusMin != nil && (*args.StatusMin < 100 || *args.StatusMin > 599) ||
		args.StatusMax != nil && (*args.StatusMax < 100 || *args.StatusMax > 599) ||
		args.StatusMin != nil && args.StatusMax != nil && *args.StatusMin > *args.StatusMax {
		return nil, invalidNetwork("status bounds are invalid")
	}
	if args.Since != "" {
		if _, err := time.Parse(time.RFC3339Nano, args.Since); err != nil {
			return nil, invalidNetwork("since must be an RFC 3339 timestamp")
		}
	}
	params := map[string]any{"limit": limit, "maxBytes": maxBytes}
	if args.Cursor != "" {
		params["cursor"] = args.Cursor
	}
	if args.ResourceTypes != nil {
		params["resourceTypes"] = args.ResourceTypes
	}
	putOptional(params, "failedOnly", args.FailedOnly)
	putOptional(params, "statusMin", args.StatusMin)
	putOptional(params, "statusMax", args.StatusMax)
	if args.Since != "" {
		params["since"] = args.Since
	}
	return params, nil
}

func (s *Service) browserGetNetworkBodyHandler(ctx context.Context, _ mcp.CallToolRequest, args networkBodyArgs) (*mcp.CallToolResult, error) {
	entryID := strings.TrimSpace(args.EntryID)
	direction := strings.TrimSpace(args.Direction)
	maxBytes := defaultNetworkBodyMaxBytes
	assignInt(&maxBytes, args.MaxBytes)
	if !validNetworkEntryID(entryID) || !performanceStringAllowed([]string{"request", "response"}, direction) ||
		maxBytes < minNetworkBodyMaxBytes || maxBytes > maxNetworkBodyMaxBytes {
		return errorResult(invalidNetwork("network body arguments are invalid"))
	}
	return s.captureNetworkArtifact(ctx, args.networkTargetArgs, protocol.CommandNetworkGetBody, "browser_get_network_body", direction+"Body", map[string]any{"entryId": entryID, "direction": direction, "maxBytes": maxBytes}, maxBytes)
}

func (s *Service) browserExportNetworkHARHandler(ctx context.Context, _ mcp.CallToolRequest, args networkHARArgs) (*mcp.CallToolResult, error) {
	maxBytes := defaultNetworkHARMaxBytes
	assignInt(&maxBytes, args.MaxBytes)
	if maxBytes < minNetworkHARMaxBytes || maxBytes > defaultNetworkHARMaxBytes {
		return errorResult(invalidNetwork("HAR maxBytes is outside the supported range"))
	}
	return s.captureNetworkArtifact(ctx, args.networkTargetArgs, protocol.CommandNetworkExportHAR, "browser_export_network_har", "har", map[string]any{"maxBytes": maxBytes}, maxBytes)
}

func (s *Service) captureNetworkArtifact(ctx context.Context, args networkTargetArgs, command, operation, expectedKind string, params map[string]any, maxBytes int) (*mcp.CallToolResult, error) {
	startedAt := time.Now()
	if s.artifacts == nil {
		return errorResult(protocol.NewError(protocol.CodeCapabilityUnavailable, "network artifact storage is unavailable", false))
	}
	operationCtx, cancel, err := toolContext(ctx, args.TimeoutMS)
	if err != nil {
		return errorResult(err)
	}
	defer cancel()
	rootFrameID := 0
	browserID, target, raw, duration, err := s.sendRaw(operationCtx, args.BrowserID, command, pageTarget(args.TabID, &rootFrameID, args.DocumentID), params, nil)
	if err != nil {
		s.auditNetwork(operation, expectedKind, browserID, target, protocol.ErrorFrom(err).Code, 0, duration)
		return errorResultWithDuration(err, duration)
	}
	wire, data, err := decodeNetworkArtifact(raw, expectedKind, maxBytes, target)
	if err != nil {
		s.auditNetwork(operation, expectedKind, browserID, target, protocol.ErrorFrom(err).Code, len(raw), duration)
		return errorResultWithDuration(err, duration)
	}
	data, report, err := redactNetworkArtifact(data, wire.MIMEType, expectedKind == "har", maxBytes)
	if err != nil {
		s.auditNetwork(operation, expectedKind, browserID, target, protocol.ErrorFrom(err).Code, len(data), duration)
		return errorResultWithDuration(err, duration)
	}
	redactionRules := append([]string(nil), wire.RedactionRules...)
	redactionRules = append(redactionRules, report.Rules...)
	redactionRules = uniqueSortedNetworkStrings(redactionRules)
	storeStarted := time.Now()
	metadata, err := s.artifacts.Put(operationCtx, wire.MIMEType, data, artifacts.RedactionMetadata{Applied: wire.RedactionApplied || report.Applied, Rules: redactionRules})
	duration += time.Since(storeStarted)
	if err != nil {
		s.auditNetwork(operation, expectedKind, browserID, target, protocol.ErrorFrom(err).Code, len(data), duration)
		return errorResultWithDuration(fmt.Errorf("store network artifact: %w", err), duration)
	}
	if target == nil {
		tabID := wire.TabID
		frameID := 0
		target = &protocol.Target{BrowserID: browserID, TabID: &tabID, FrameID: &frameID, DocumentID: wire.DocumentID}
	} else if target.DocumentID == "" {
		target.DocumentID = wire.DocumentID
	}
	warnings := append([]string(nil), wire.Warnings...)
	warnings = append(warnings, report.Warnings()...)
	warnings = append(warnings, "Network artifacts may contain sensitive page activity; inspect and share them carefully")
	result := map[string]any{
		"kind": wire.Kind, "artifactUri": metadata.URI, "artifactMetadataUri": metadata.URI + "/metadata",
		"mimeType": metadata.MIMEType, "size": metadata.Size, "tabId": wire.TabID,
		"documentId": wire.DocumentID, "entryCount": wire.EntryCount, "truncated": wire.Truncated,
		"expiresAt": metadata.ExpiresAt, "redactionApplied": metadata.Redaction.Applied,
		"redactionRules": metadata.Redaction.Rules, "warnings": uniqueSortedNetworkStrings(warnings),
	}
	if wire.EntryID != "" {
		result["entryId"] = wire.EntryID
	}
	s.auditNetwork(operation, expectedKind, browserID, target, "OK", len(data), time.Since(startedAt))
	return successResultWithTarget(browserID, target, result, duration)
}

func decodeNetworkArtifact(raw json.RawMessage, expectedKind string, maxBytes int, target *protocol.Target) (networkArtifactWireResult, []byte, error) {
	var result networkArtifactWireResult
	if len(raw) > base64.StdEncoding.EncodedLen(maxBytes)+8_192 || decodeStrictJSON(raw, &result) != nil ||
		result.Kind != expectedKind || result.TabID < 0 || strings.TrimSpace(result.DocumentID) == "" ||
		len(result.DocumentID) > 256 || result.ByteLength < 0 || result.ByteLength > maxBytes ||
		len(result.Warnings) > maxNetworkWarnings || !validPerformanceWarnings(result.Warnings) ||
		len(result.RedactionRules) > maxNetworkRedactionRules || result.EntryCount < 0 ||
		!networkTargetMatches(target, result.TabID, result.DocumentID) {
		return networkArtifactWireResult{}, nil, invalidNetworkResult()
	}
	for _, rule := range result.RedactionRules {
		if strings.TrimSpace(rule) == "" || len(rule) > maxNetworkRedactionRuleSize {
			return networkArtifactWireResult{}, nil, invalidNetworkResult()
		}
	}
	if expectedKind == "har" {
		if result.MIMEType != "application/json" || result.EntryID != "" || result.ByteLength < 2 {
			return networkArtifactWireResult{}, nil, invalidNetworkResult()
		}
	} else if !validNetworkEntryID(result.EntryID) || !allowedNetworkBodyMIME(result.MIMEType) {
		return networkArtifactWireResult{}, nil, invalidNetworkResult()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(result.DataBase64)
	if err != nil || len(data) != result.ByteLength || !utf8.Valid(data) {
		return networkArtifactWireResult{}, nil, invalidNetworkResult()
	}
	if expectedKind == "har" && (!json.Valid(data) || len(data) > maxBytes) {
		return networkArtifactWireResult{}, nil, invalidNetworkResult()
	}
	return result, data, nil
}

func redactNetworkArtifact(data []byte, mimeType string, requireJSON bool, maxBytes int) ([]byte, redaction.Report, error) {
	if requireJSON || networkJSONMIME(mimeType) && len(data) > 0 && json.Valid(data) {
		limits := redaction.DefaultLimits(maxBytes)
		limits.MaxInputBytes = maxBytes
		limits.MaxStringBytes = maxBytes
		redacted, report, err := redaction.JSON(data, limits)
		if err != nil {
			return nil, report, protocol.NewError(protocol.CodePayloadTooLarge, "the redacted network artifact exceeds maxBytes", false)
		}
		return redacted, report, nil
	}
	value, report := redaction.String(string(data), maxBytes)
	return []byte(value), report, nil
}

func allowedNetworkBodyMIME(value string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(mediaType)
	return strings.HasPrefix(mediaType, "text/") || networkJSONMIME(mediaType) ||
		performanceStringAllowed([]string{"application/xml", "application/xhtml+xml", "application/javascript", "application/x-www-form-urlencoded", "image/svg+xml"}, mediaType)
}

func networkJSONMIME(value string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func validateNetworkInlineTarget(raw json.RawMessage, target *protocol.Target) (int, string, error) {
	var result struct {
		TabID      int    `json:"tabId"`
		DocumentID string `json:"documentId"`
	}
	if json.Unmarshal(raw, &result) != nil || result.TabID < 0 || strings.TrimSpace(result.DocumentID) == "" ||
		!networkTargetMatches(target, result.TabID, result.DocumentID) {
		return 0, "", invalidNetworkResult()
	}
	return result.TabID, result.DocumentID, nil
}

func networkTargetMatches(target *protocol.Target, tabID int, documentID string) bool {
	return target == nil || target.TabID != nil && *target.TabID == tabID && (target.DocumentID == "" || target.DocumentID == documentID)
}

func validNetworkEntryID(value string) bool {
	if value == "" || len(value) > 32 {
		return false
	}
	parsed, err := strconv.ParseUint(value, 10, 53)
	return err == nil && parsed > 0
}

func isDedicatedNetworkCommand(command string) bool {
	return performanceStringAllowed([]string{
		protocol.CommandNetworkStart, protocol.CommandNetworkStop, protocol.CommandNetworkClear,
		protocol.CommandNetworkRead, protocol.CommandNetworkGetBody, protocol.CommandNetworkExportHAR,
	}, command)
}

func uniqueSortedNetworkStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (s *Service) auditNetwork(operation, kind, browserID string, target *protocol.Target, outcome any, resultBytes int, duration time.Duration) {
	if s.auditLogger == nil {
		return
	}
	allowedOperations := []string{"browser_start_network_capture", "browser_stop_network_capture", "browser_clear_network_log", "browser_get_network_log", "browser_get_network_body", "browser_export_network_har"}
	if !performanceStringAllowed(allowedOperations, operation) {
		operation = "invalid"
	}
	if !performanceStringAllowed([]string{"", "requestBody", "responseBody", "har"}, kind) {
		kind = "invalid"
	}
	tabID := -1
	if target != nil && target.TabID != nil {
		tabID = *target.TabID
	}
	s.auditLogger.Printf("operation=network tool=%q kind=%q browserId=%q tabId=%d outcome=%s resultBytes=%d duration=%s", operation, kind, boundedRawCDPAudit(browserID), tabID, fmt.Sprint(outcome), resultBytes, duration.Round(time.Microsecond))
}

func invalidNetwork(message string) *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}

func invalidNetworkResult() *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, "the browser returned an invalid network result", false)
}
