package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultDownloadLimit     = 50
	maxDownloadLimit         = 200
	maxDownloadScanItems     = 10_000
	maxDownloadResultBytes   = 1_000_000
	maxDownloadURLBytes      = 8_192
	maxDownloadFilenameBytes = 1_024
	maxDownloadTextBytes     = 256
	maxDownloadWarnings      = 4
	maxJavaScriptSafeInteger = int64(9_007_199_254_740_991)
)

var downloadStates = []string{"in_progress", "interrupted", "complete"}

type downloadBaseArgs struct {
	BrowserID string `json:"browserId,omitempty"`
	TimeoutMS *int   `json:"timeoutMs,omitempty"`
}

type downloadListArgs struct {
	downloadBaseArgs
	Cursor string `json:"cursor,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
	State  string `json:"state,omitempty"`
	Paused *bool  `json:"paused,omitempty"`
}

type downloadIDArgs struct {
	downloadBaseArgs
	DownloadID int64 `json:"downloadId"`
}

type downloadCreateArgs struct {
	downloadBaseArgs
	URL string `json:"url"`
}

type downloadEraseArgs struct {
	downloadIDArgs
	Confirm bool `json:"confirm"`
}

type downloadWireItem struct {
	ID               int64  `json:"id"`
	SourceURL        string `json:"sourceUrl"`
	FinalURL         string `json:"finalUrl"`
	FileName         string `json:"fileName"`
	PathRedacted     bool   `json:"pathRedacted"`
	State            string `json:"state"`
	Paused           bool   `json:"paused"`
	CanResume        bool   `json:"canResume"`
	Danger           string `json:"danger"`
	Error            string `json:"error"`
	BytesReceived    int64  `json:"bytesReceived"`
	TotalBytes       int64  `json:"totalBytes"`
	FileSize         int64  `json:"fileSize"`
	Exists           bool   `json:"exists"`
	Incognito        bool   `json:"incognito"`
	MIME             string `json:"mime"`
	StartTime        string `json:"startTime"`
	EndTime          string `json:"endTime"`
	EstimatedEndTime string `json:"estimatedEndTime"`
}

type downloadWireResult struct {
	Kind         string             `json:"kind"`
	Downloads    []downloadWireItem `json:"downloads"`
	DownloadID   int64              `json:"downloadId"`
	TotalMatched int                `json:"totalMatched"`
	NextCursor   string             `json:"nextCursor"`
	Operation    string             `json:"operation"`
	Changed      bool               `json:"changed"`
	ErasedIDs    []int64            `json:"erasedIds"`
	Warnings     []string           `json:"warnings"`
}

func (s *Service) registerDownloadTools(mcpServer *server.MCPServer) {
	base := func(description string, extra ...mcp.ToolOption) []mcp.ToolOption {
		options := []mcp.ToolOption{mcp.WithDescription(description), optionalBrowserID()}
		options = append(options, extra...)
		options = append(options, optionalTimeout())
		return options
	}
	id := func() mcp.ToolOption {
		return mcp.WithNumber("downloadId", mcp.Required(), mcp.Description("Persistent browser download identifier"), mcp.Min(0), mcp.Max(float64(maxJavaScriptSafeInteger)))
	}
	mcpServer.AddTool(
		mcp.NewTool("browser_list_downloads", base(
			"List bounded download status metadata without local paths or file contents",
			mcp.WithString("cursor", mcp.Description("Positive offset cursor from a previous result")),
			mcp.WithNumber("limit", mcp.Description("Maximum downloads to return"), mcp.Min(1), mcp.Max(maxDownloadLimit), mcp.DefaultNumber(defaultDownloadLimit)),
			mcp.WithString("state", mcp.Description("Filter by browser download state"), mcp.Enum(downloadStates...)),
			mcp.WithBoolean("paused", mcp.Description("Filter by paused state")),
		)...),
		mcp.NewTypedToolHandler(s.browserListDownloadsHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool("browser_get_download", base("Get bounded status metadata for one download without its local path", id())...),
		mcp.NewTypedToolHandler(s.browserGetDownloadHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool("browser_create_download", base(
			"Start one HTTP(S) download with a browser-chosen unique filename and no custom headers",
			mcp.WithString("url", mcp.Required(), mcp.Description("HTTP(S) source URL"), mcp.MaxLength(maxDownloadURLBytes)),
		)...),
		mcp.NewTypedToolHandler(s.browserCreateDownloadHandler),
	)
	for _, registration := range []struct {
		name, description, command string
	}{
		{"browser_pause_download", "Pause one active download", protocol.CommandDownloadsPause},
		{"browser_resume_download", "Resume one resumable download", protocol.CommandDownloadsResume},
		{"browser_cancel_download", "Cancel one active download", protocol.CommandDownloadsCancel},
	} {
		registration := registration
		mcpServer.AddTool(
			mcp.NewTool(registration.name, base(registration.description, id())...),
			mcp.NewTypedToolHandler(func(ctx context.Context, _ mcp.CallToolRequest, args downloadIDArgs) (*mcp.CallToolResult, error) {
				return s.sendDownloadIDCommand(ctx, registration.name, registration.command, args)
			}),
		)
	}
	mcpServer.AddTool(
		mcp.NewTool("browser_erase_download_history", base(
			"Erase one terminal download history entry without deleting the downloaded file",
			id(),
			mcp.WithBoolean("confirm", mcp.Required(), mcp.Description("Must be true because this changes browser history")),
		)...),
		mcp.NewTypedToolHandler(s.browserEraseDownloadHistoryHandler),
	)
}

func (s *Service) browserListDownloadsHandler(ctx context.Context, _ mcp.CallToolRequest, args downloadListArgs) (*mcp.CallToolResult, error) {
	limit := defaultDownloadLimit
	if args.Limit != nil {
		limit = *args.Limit
	}
	if limit < 1 || limit > maxDownloadLimit {
		return errorResult(invalidDownload("limit must be between 1 and 200"))
	}
	if args.Cursor != "" {
		offset, err := strconv.Atoi(args.Cursor)
		if err != nil || offset < 1 || offset >= maxDownloadScanItems || strconv.Itoa(offset) != args.Cursor {
			return errorResult(invalidDownload("cursor is invalid"))
		}
	}
	if args.State != "" && !performanceStringAllowed(downloadStates, args.State) {
		return errorResult(invalidDownload("state is unsupported"))
	}
	params := map[string]any{"limit": limit, "allowIncognito": s.downloadsAllowIncognito()}
	if args.Cursor != "" {
		params["cursor"] = args.Cursor
	}
	if args.State != "" {
		params["state"] = args.State
	}
	if args.Paused != nil {
		params["paused"] = *args.Paused
	}
	return s.sendDownloadCommand(ctx, "browser_list_downloads", protocol.CommandDownloadsList, args.downloadBaseArgs, params)
}

func (s *Service) browserGetDownloadHandler(ctx context.Context, _ mcp.CallToolRequest, args downloadIDArgs) (*mcp.CallToolResult, error) {
	return s.sendDownloadIDCommand(ctx, "browser_get_download", protocol.CommandDownloadsGet, args)
}

func (s *Service) browserCreateDownloadHandler(ctx context.Context, _ mcp.CallToolRequest, args downloadCreateArgs) (*mcp.CallToolResult, error) {
	parsed, err := validateCookieURL(args.URL)
	if err != nil || len(args.URL) > maxDownloadURLBytes {
		return errorResult(invalidDownload("url must be a bounded HTTP(S) URL without credentials"))
	}
	params := map[string]any{"url": parsed.String(), "allowIncognito": s.downloadsAllowIncognito()}
	return s.sendDownloadCommand(ctx, "browser_create_download", protocol.CommandDownloadsCreate, args.downloadBaseArgs, params)
}

func (s *Service) browserEraseDownloadHistoryHandler(ctx context.Context, _ mcp.CallToolRequest, args downloadEraseArgs) (*mcp.CallToolResult, error) {
	if !args.Confirm {
		if s.actionPolicy != nil {
			s.actionPolicy.AuditDenied(protocol.CommandDownloadsErase, args.BrowserID, "", "confirmation_required")
		}
		return errorResult(protocol.NewError(protocol.CodeConfirmationRequired, "erasing download history requires confirm: true", false))
	}
	if !validDownloadID(args.DownloadID) {
		return errorResult(invalidDownload("downloadId is invalid"))
	}
	params := map[string]any{"downloadId": args.DownloadID, "confirm": true, "allowIncognito": s.downloadsAllowIncognito()}
	return s.sendDownloadCommand(ctx, "browser_erase_download_history", protocol.CommandDownloadsErase, args.downloadBaseArgs, params)
}

func (s *Service) sendDownloadIDCommand(ctx context.Context, operation, command string, args downloadIDArgs) (*mcp.CallToolResult, error) {
	if !validDownloadID(args.DownloadID) {
		return errorResult(invalidDownload("downloadId is invalid"))
	}
	return s.sendDownloadCommand(ctx, operation, command, args.downloadBaseArgs, map[string]any{
		"downloadId": args.DownloadID, "allowIncognito": s.downloadsAllowIncognito(),
	})
}

func (s *Service) sendDownloadCommand(ctx context.Context, operation, command string, args downloadBaseArgs, params map[string]any) (*mcp.CallToolResult, error) {
	startedAt := time.Now()
	browserID, _, raw, duration, err := s.sendRaw(ctx, args.BrowserID, command, nil, params, args.TimeoutMS)
	if err != nil {
		s.auditDownload(operation, browserID, downloadIDFromParams(params), protocol.ErrorFrom(err).Code, 0, duration)
		return errorResultWithDuration(err, duration)
	}
	result, err := decodeDownloadResult(raw, command, s.downloadsAllowIncognito())
	if err != nil {
		s.auditDownload(operation, browserID, downloadIDFromParams(params), protocol.ErrorFrom(err).Code, 0, duration)
		return errorResultWithDuration(err, duration)
	}
	count := len(result.Downloads) + len(result.ErasedIDs)
	s.auditDownload(operation, browserID, result.DownloadID, "OK", count, time.Since(startedAt))
	return successResultWithTargetWarningsLimited(browserID, nil, result, duration, nil, s.resultLimits.MaxOutputBytes)
}

func decodeDownloadResult(raw json.RawMessage, command string, allowIncognito bool) (downloadWireResult, error) {
	if len(raw) == 0 || len(raw) > maxDownloadResultBytes {
		return downloadWireResult{}, invalidDownloadResult()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var result downloadWireResult
	if err := decoder.Decode(&result); err != nil {
		return downloadWireResult{}, invalidDownloadResult()
	}
	if err := ensureDownloadEOF(decoder); err != nil || !validDownloadResult(result, command, allowIncognito) {
		return downloadWireResult{}, invalidDownloadResult()
	}
	return result, nil
}

func validDownloadResult(result downloadWireResult, command string, allowIncognito bool) bool {
	if result.Kind != downloadResultKind(command) || result.TotalMatched < 0 || result.TotalMatched > maxDownloadScanItems ||
		!validDownloadCursor(result.NextCursor) || len(result.Downloads) > maxDownloadLimit ||
		len(result.ErasedIDs) > 1 || !validDownloadWarnings(result.Warnings) ||
		!validDownloadID(result.DownloadID) {
		return false
	}
	if result.NextCursor != "" {
		next, _ := strconv.Atoi(result.NextCursor)
		if next > result.TotalMatched {
			return false
		}
	}
	seenIDs := make(map[int64]bool, len(result.Downloads))
	for _, item := range result.Downloads {
		if !validDownloadWire(item, allowIncognito) || seenIDs[item.ID] {
			return false
		}
		seenIDs[item.ID] = true
	}
	for _, id := range result.ErasedIDs {
		if !validDownloadID(id) {
			return false
		}
	}
	switch command {
	case protocol.CommandDownloadsList:
		return result.Operation == "" && !result.Changed && result.DownloadID == 0 && len(result.ErasedIDs) == 0 && result.TotalMatched >= len(result.Downloads)
	case protocol.CommandDownloadsGet:
		return result.Operation == "" && !result.Changed && len(result.Downloads) == 1 && result.TotalMatched == 1 && len(result.ErasedIDs) == 0 && result.DownloadID == result.Downloads[0].ID && result.NextCursor == ""
	case protocol.CommandDownloadsCreate:
		return result.Operation == "create" && result.Changed && len(result.Downloads) == 0 && result.TotalMatched == 0 && len(result.ErasedIDs) == 0 && result.NextCursor == ""
	case protocol.CommandDownloadsPause, protocol.CommandDownloadsResume, protocol.CommandDownloadsCancel:
		return result.Operation == strings.TrimPrefix(command, "downloads.") && result.Changed && len(result.Downloads) == 1 && result.TotalMatched == 1 && len(result.ErasedIDs) == 0 && result.DownloadID == result.Downloads[0].ID && result.NextCursor == ""
	case protocol.CommandDownloadsErase:
		return result.Operation == "erase" && result.Changed && len(result.Downloads) == 0 && result.TotalMatched == 0 && len(result.ErasedIDs) == 1 && result.ErasedIDs[0] == result.DownloadID && result.NextCursor == ""
	default:
		return false
	}
}

func validDownloadWire(item downloadWireItem, allowIncognito bool) bool {
	if !validDownloadID(item.ID) || !item.PathRedacted || !performanceStringAllowed(downloadStates, item.State) ||
		item.Incognito && !allowIncognito || !validDownloadURLMetadata(item.SourceURL) ||
		!validDownloadURLMetadata(item.FinalURL) || !validDownloadBaseName(item.FileName) ||
		!validDownloadText(item.Danger) || !validDownloadText(item.Error) || !validDownloadText(item.MIME) ||
		item.BytesReceived < 0 || item.BytesReceived > maxJavaScriptSafeInteger ||
		item.TotalBytes < -1 || item.TotalBytes > maxJavaScriptSafeInteger ||
		item.FileSize < -1 || item.FileSize > maxJavaScriptSafeInteger ||
		!validDownloadTime(item.StartTime, false) || !validDownloadTime(item.EndTime, true) ||
		!validDownloadTime(item.EstimatedEndTime, true) {
		return false
	}
	return true
}

func validDownloadURLMetadata(value string) bool {
	if value == "[REDACTED]" {
		return true
	}
	if len(value) == 0 || len(value) > maxDownloadURLBytes || !utf8.ValidString(value) {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && !containsCookieControl(value)
}

func validDownloadBaseName(value string) bool {
	return value != "" && len(value) <= maxDownloadFilenameBytes && utf8.ValidString(value) &&
		!strings.ContainsAny(value, "/\\") && !containsCookieControl(value)
}

func validDownloadText(value string) bool {
	return len(value) <= maxDownloadTextBytes && utf8.ValidString(value) && !containsCookieControl(value)
}

func validDownloadTime(value string, optional bool) bool {
	if value == "" {
		return optional
	}
	if len(value) > 64 {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

func validDownloadWarnings(warnings []string) bool {
	if len(warnings) > maxDownloadWarnings {
		return false
	}
	seen := make(map[string]bool, len(warnings))
	for _, warning := range warnings {
		if warning == "" || len(warning) > maxDownloadTextBytes || !utf8.ValidString(warning) || containsCookieControl(warning) || seen[warning] {
			return false
		}
		seen[warning] = true
	}
	return true
}

func validDownloadCursor(cursor string) bool {
	if cursor == "" {
		return true
	}
	value, err := strconv.Atoi(cursor)
	return err == nil && value >= 1 && value < maxDownloadScanItems && strconv.Itoa(value) == cursor
}

func validDownloadID(id int64) bool { return id >= 0 && id <= maxJavaScriptSafeInteger }

func downloadResultKind(command string) string {
	switch command {
	case protocol.CommandDownloadsList:
		return "list"
	case protocol.CommandDownloadsGet:
		return "item"
	case protocol.CommandDownloadsCreate:
		return "create"
	case protocol.CommandDownloadsPause, protocol.CommandDownloadsResume, protocol.CommandDownloadsCancel:
		return "mutation"
	case protocol.CommandDownloadsErase:
		return "erase"
	default:
		return ""
	}
}

func isDedicatedDownloadCommand(command string) bool {
	return performanceStringAllowed([]string{
		protocol.CommandDownloadsList, protocol.CommandDownloadsGet, protocol.CommandDownloadsCreate,
		protocol.CommandDownloadsPause, protocol.CommandDownloadsResume, protocol.CommandDownloadsCancel,
		protocol.CommandDownloadsErase,
	}, command)
}

func (s *Service) downloadsAllowIncognito() bool {
	return s.actionPolicy != nil && s.actionPolicy.AllowsIncognito()
}

func downloadIDFromParams(params map[string]any) int64 {
	switch value := params["downloadId"].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	default:
		return 0
	}
}

func ensureDownloadEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return err
	}
	return nil
}

func (s *Service) auditDownload(operation, browserID string, downloadID int64, outcome any, count int, duration time.Duration) {
	if s.auditLogger == nil {
		return
	}
	if !performanceStringAllowed([]string{
		"browser_list_downloads", "browser_get_download", "browser_create_download",
		"browser_pause_download", "browser_resume_download", "browser_cancel_download",
		"browser_erase_download_history",
	}, operation) {
		operation = "invalid"
	}
	s.auditLogger.Printf("operation=downloads tool=%q browserId=%q downloadId=%d count=%d outcome=%s duration=%s", operation, boundedRawCDPAudit(browserID), downloadID, count, fmt.Sprint(outcome), duration.Round(time.Microsecond))
}

func invalidDownload(message string) *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}

func invalidDownloadResult() *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, "the browser returned an invalid download result", false)
}
