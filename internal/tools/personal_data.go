package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
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
	defaultPersonalDataLimit = 50
	maxPersonalDataLimit     = 200
	maxPersonalDataScan      = 10_000
	maxPersonalDataURLBytes  = 8_192
	maxPersonalDataTitle     = 2_048
	maxPersonalDataID        = 256
	maxPersonalDataQuery     = 1_024
	maxPersonalDataResult    = 2_000_000
)

type personalDataBaseArgs struct {
	BrowserID string `json:"browserId,omitempty"`
	TimeoutMS *int   `json:"timeoutMs,omitempty"`
}

type personalPageArgs struct {
	personalDataBaseArgs
	Cursor string `json:"cursor,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
}

type historySearchArgs struct {
	personalPageArgs
	Text      string   `json:"text,omitempty"`
	StartTime *float64 `json:"startTime,omitempty"`
	EndTime   *float64 `json:"endTime,omitempty"`
}

type historyVisitsArgs struct {
	personalPageArgs
	URL string `json:"url"`
}

type confirmedURLArgs struct {
	personalDataBaseArgs
	URL     string `json:"url"`
	Confirm bool   `json:"confirm"`
}

type historyRangeArgs struct {
	personalDataBaseArgs
	StartTime float64 `json:"startTime"`
	EndTime   float64 `json:"endTime"`
	Confirm   bool    `json:"confirm"`
}

type confirmedPersonalArgs struct {
	personalDataBaseArgs
	Confirm bool `json:"confirm"`
}

type bookmarkListArgs struct {
	personalPageArgs
	Query    string `json:"query,omitempty"`
	ParentID string `json:"parentId,omitempty"`
}

type bookmarkCreateArgs struct {
	personalDataBaseArgs
	Title    string `json:"title"`
	URL      string `json:"url,omitempty"`
	ParentID string `json:"parentId,omitempty"`
	Index    *int   `json:"index,omitempty"`
}

type bookmarkUpdateArgs struct {
	personalDataBaseArgs
	BookmarkID string  `json:"bookmarkId"`
	Title      *string `json:"title,omitempty"`
	URL        *string `json:"url,omitempty"`
}

type bookmarkMoveArgs struct {
	personalDataBaseArgs
	BookmarkID string `json:"bookmarkId"`
	ParentID   string `json:"parentId,omitempty"`
	Index      *int   `json:"index,omitempty"`
}

type bookmarkRemoveArgs struct {
	personalDataBaseArgs
	BookmarkID string `json:"bookmarkId"`
	Recursive  bool   `json:"recursive,omitempty"`
	Confirm    bool   `json:"confirm,omitempty"`
}

type readingListArgs struct {
	personalPageArgs
	Title       string `json:"title,omitempty"`
	URL         string `json:"url,omitempty"`
	HasBeenRead *bool  `json:"hasBeenRead,omitempty"`
}

type readingListAddArgs struct {
	personalDataBaseArgs
	URL         string `json:"url"`
	Title       string `json:"title"`
	HasBeenRead bool   `json:"hasBeenRead"`
}

type readingListUpdateArgs struct {
	personalDataBaseArgs
	URL         string  `json:"url"`
	Title       *string `json:"title,omitempty"`
	HasBeenRead *bool   `json:"hasBeenRead,omitempty"`
}

type readingListRemoveArgs struct {
	personalDataBaseArgs
	URL string `json:"url"`
}

type historyWireItem struct {
	ID            string  `json:"id"`
	URL           string  `json:"url"`
	Title         string  `json:"title"`
	LastVisitTime float64 `json:"lastVisitTime"`
	VisitCount    int64   `json:"visitCount"`
	TypedCount    int64   `json:"typedCount"`
}

type historyWireVisit struct {
	ID               string  `json:"id"`
	VisitID          string  `json:"visitId"`
	ReferringVisitID string  `json:"referringVisitId"`
	VisitTime        float64 `json:"visitTime"`
	Transition       string  `json:"transition"`
}

type historyWireResult struct {
	Kind         string             `json:"kind"`
	Items        []historyWireItem  `json:"items"`
	Visits       []historyWireVisit `json:"visits"`
	TotalMatched int                `json:"totalMatched"`
	NextCursor   string             `json:"nextCursor"`
	Operation    string             `json:"operation"`
	Changed      bool               `json:"changed"`
	DeletedCount int                `json:"deletedCount"`
	Scope        string             `json:"scope"`
	StartTime    float64            `json:"startTime"`
	EndTime      float64            `json:"endTime"`
	Warnings     []string           `json:"warnings"`
}

type bookmarkWireItem struct {
	ID                string  `json:"id"`
	ParentID          string  `json:"parentId"`
	Index             int64   `json:"index"`
	Title             string  `json:"title"`
	URL               string  `json:"url"`
	DateAdded         float64 `json:"dateAdded"`
	DateGroupModified float64 `json:"dateGroupModified"`
	Unmodifiable      string  `json:"unmodifiable"`
	Syncing           bool    `json:"syncing"`
}

type bookmarkWireResult struct {
	Kind         string             `json:"kind"`
	Bookmarks    []bookmarkWireItem `json:"bookmarks"`
	TotalMatched int                `json:"totalMatched"`
	NextCursor   string             `json:"nextCursor"`
	BookmarkID   string             `json:"bookmarkId"`
	Operation    string             `json:"operation"`
	Changed      bool               `json:"changed"`
	RemovedIDs   []string           `json:"removedIds"`
	Warnings     []string           `json:"warnings"`
}

type readingListWireEntry struct {
	URL            string  `json:"url"`
	Title          string  `json:"title"`
	HasBeenRead    bool    `json:"hasBeenRead"`
	CreationTime   float64 `json:"creationTime"`
	LastUpdateTime float64 `json:"lastUpdateTime"`
}

type readingListWireResult struct {
	Kind         string                 `json:"kind"`
	Entries      []readingListWireEntry `json:"entries"`
	TotalMatched int                    `json:"totalMatched"`
	NextCursor   string                 `json:"nextCursor"`
	Operation    string                 `json:"operation"`
	Changed      bool                   `json:"changed"`
	TargetURL    string                 `json:"targetUrl"`
	Warnings     []string               `json:"warnings"`
}

func (s *Service) registerPersonalDataTools(mcpServer *server.MCPServer) {
	base := func(description string, extra ...mcp.ToolOption) []mcp.ToolOption {
		options := []mcp.ToolOption{mcp.WithDescription(description), optionalBrowserID()}
		options = append(options, extra...)
		options = append(options, optionalTimeout())
		return options
	}
	page := func() []mcp.ToolOption {
		return []mcp.ToolOption{
			mcp.WithString("cursor", mcp.Description("Positive offset cursor from a previous result")),
			mcp.WithNumber("limit", mcp.Description("Maximum entries to return"), mcp.Min(1), mcp.Max(maxPersonalDataLimit), mcp.DefaultNumber(defaultPersonalDataLimit)),
		}
	}
	urlOption := func(required bool) mcp.ToolOption {
		options := []mcp.PropertyOption{mcp.Description("Exact HTTP(S) URL without credentials"), mcp.MaxLength(maxPersonalDataURLBytes)}
		if required {
			options = append(options, mcp.Required())
		}
		return mcp.WithString("url", options...)
	}
	confirm := func(description string) mcp.ToolOption {
		return mcp.WithBoolean("confirm", mcp.Required(), mcp.Description(description))
	}

	historyPage := page()
	mcpServer.AddTool(mcp.NewTool("browser_search_history", base(
		"Search bounded paginated browser history metadata",
		append(historyPage,
			mcp.WithString("text", mcp.Description("Browser history full-text query"), mcp.MaxLength(maxPersonalDataQuery)),
			mcp.WithNumber("startTime", mcp.Description("Inclusive epoch time in milliseconds"), mcp.Min(0), mcp.Max(float64(maxJavaScriptSafeInteger))),
			mcp.WithNumber("endTime", mcp.Description("Exclusive epoch time in milliseconds"), mcp.Min(0), mcp.Max(float64(maxJavaScriptSafeInteger))),
		)...,
	)...), mcp.NewTypedToolHandler(s.browserSearchHistoryHandler))
	mcpServer.AddTool(mcp.NewTool("browser_get_history_visits", base(
		"List bounded paginated visits for one exact HTTP(S) URL",
		append(page(), urlOption(true))...,
	)...), mcp.NewTypedToolHandler(s.browserGetHistoryVisitsHandler))
	mcpServer.AddTool(mcp.NewTool("browser_delete_history_url", base(
		"Delete every history visit for one exact HTTP(S) URL",
		urlOption(true), confirm("Must be true because every visit for the URL is deleted"),
	)...), mcp.NewTypedToolHandler(s.browserDeleteHistoryURLHandler))
	mcpServer.AddTool(mcp.NewTool("browser_delete_history_range", base(
		"Delete browser history visits in one explicit time range",
		mcp.WithNumber("startTime", mcp.Required(), mcp.Description("Inclusive epoch time in milliseconds"), mcp.Min(0), mcp.Max(float64(maxJavaScriptSafeInteger))),
		mcp.WithNumber("endTime", mcp.Required(), mcp.Description("Exclusive epoch time in milliseconds"), mcp.Min(0), mcp.Max(float64(maxJavaScriptSafeInteger))),
		confirm("Must be true because this is a bulk deletion"),
	)...), mcp.NewTypedToolHandler(s.browserDeleteHistoryRangeHandler))
	mcpServer.AddTool(mcp.NewTool("browser_clear_history", base(
		"Clear all browser history",
		confirm("Must be true because all browser history is deleted"),
	)...), mcp.NewTypedToolHandler(s.browserClearHistoryHandler))

	mcpServer.AddTool(mcp.NewTool("browser_list_bookmarks", base(
		"List or search bounded paginated bookmark and folder metadata",
		append(page(),
			mcp.WithString("query", mcp.Description("Bookmark title or URL search text"), mcp.MaxLength(maxPersonalDataQuery)),
			mcp.WithString("parentId", mcp.Description("List direct children of this folder"), mcp.MaxLength(maxPersonalDataID)),
		)...,
	)...), mcp.NewTypedToolHandler(s.browserListBookmarksHandler))
	mcpServer.AddTool(mcp.NewTool("browser_create_bookmark", base(
		"Create one HTTP(S) bookmark or a folder when url is omitted",
		mcp.WithString("title", mcp.Required(), mcp.Description("Bookmark or folder title"), mcp.MaxLength(maxPersonalDataTitle)),
		urlOption(false),
		mcp.WithString("parentId", mcp.Description("Destination folder ID"), mcp.MaxLength(maxPersonalDataID)),
		mcp.WithNumber("index", mcp.Description("Zero-based destination index"), mcp.Min(0), mcp.Max(float64(maxJavaScriptSafeInteger))),
	)...), mcp.NewTypedToolHandler(s.browserCreateBookmarkHandler))
	mcpServer.AddTool(mcp.NewTool("browser_update_bookmark", base(
		"Update one bookmark or folder title and optional HTTP(S) URL",
		mcp.WithString("bookmarkId", mcp.Required(), mcp.Description("Bookmark node ID"), mcp.MaxLength(maxPersonalDataID)),
		mcp.WithString("title", mcp.Description("Replacement title"), mcp.MaxLength(maxPersonalDataTitle)),
		urlOption(false),
	)...), mcp.NewTypedToolHandler(s.browserUpdateBookmarkHandler))
	mcpServer.AddTool(mcp.NewTool("browser_move_bookmark", base(
		"Move one bookmark or folder to a parent and/or index",
		mcp.WithString("bookmarkId", mcp.Required(), mcp.Description("Bookmark node ID"), mcp.MaxLength(maxPersonalDataID)),
		mcp.WithString("parentId", mcp.Description("Destination folder ID"), mcp.MaxLength(maxPersonalDataID)),
		mcp.WithNumber("index", mcp.Description("Zero-based destination index"), mcp.Min(0), mcp.Max(float64(maxJavaScriptSafeInteger))),
	)...), mcp.NewTypedToolHandler(s.browserMoveBookmarkHandler))
	mcpServer.AddTool(mcp.NewTool("browser_remove_bookmark", base(
		"Remove one bookmark or empty folder; recursive folder removal requires confirmation",
		mcp.WithString("bookmarkId", mcp.Required(), mcp.Description("Bookmark node ID"), mcp.MaxLength(maxPersonalDataID)),
		mcp.WithBoolean("recursive", mcp.Description("Remove an entire folder tree")),
		mcp.WithBoolean("confirm", mcp.Description("Must be true for recursive removal")),
	)...), mcp.NewTypedToolHandler(s.browserRemoveBookmarkHandler))

	mcpServer.AddTool(mcp.NewTool("browser_list_reading_list", base(
		"List bounded paginated reading-list entries",
		append(page(),
			mcp.WithString("title", mcp.Description("Exact browser reading-list title query"), mcp.MaxLength(maxPersonalDataTitle)),
			urlOption(false),
			mcp.WithBoolean("hasBeenRead", mcp.Description("Filter by read state")),
		)...,
	)...), mcp.NewTypedToolHandler(s.browserListReadingListHandler))
	mcpServer.AddTool(mcp.NewTool("browser_add_reading_list_entry", base(
		"Add one exact HTTP(S) URL to the browser reading list",
		urlOption(true),
		mcp.WithString("title", mcp.Required(), mcp.Description("Entry title"), mcp.MaxLength(maxPersonalDataTitle)),
		mcp.WithBoolean("hasBeenRead", mcp.Required(), mcp.Description("Initial read state")),
	)...), mcp.NewTypedToolHandler(s.browserAddReadingListHandler))
	mcpServer.AddTool(mcp.NewTool("browser_update_reading_list_entry", base(
		"Update one reading-list entry title and/or read state",
		urlOption(true),
		mcp.WithString("title", mcp.Description("Replacement title"), mcp.MaxLength(maxPersonalDataTitle)),
		mcp.WithBoolean("hasBeenRead", mcp.Description("Replacement read state")),
	)...), mcp.NewTypedToolHandler(s.browserUpdateReadingListHandler))
	mcpServer.AddTool(mcp.NewTool("browser_remove_reading_list_entry", base(
		"Remove one exact HTTP(S) URL from the browser reading list",
		urlOption(true),
	)...), mcp.NewTypedToolHandler(s.browserRemoveReadingListHandler))
}

func (s *Service) browserSearchHistoryHandler(ctx context.Context, _ mcp.CallToolRequest, args historySearchArgs) (*mcp.CallToolResult, error) {
	params, err := personalPageParams(args.personalPageArgs)
	if err != nil {
		return errorResult(err)
	}
	if len(args.Text) > maxPersonalDataQuery || !validPersonalText(args.Text, true) {
		return errorResult(invalidPersonalData("text is invalid"))
	}
	if err := validateOptionalPersonalTime(args.StartTime, "startTime"); err != nil {
		return errorResult(err)
	}
	if err := validateOptionalPersonalTime(args.EndTime, "endTime"); err != nil {
		return errorResult(err)
	}
	if args.StartTime != nil && args.EndTime != nil && *args.StartTime >= *args.EndTime {
		return errorResult(invalidPersonalData("startTime must be before endTime"))
	}
	params["text"] = args.Text
	putOptional(params, "startTime", args.StartTime)
	putOptional(params, "endTime", args.EndTime)
	return s.sendPersonalDataCommand(ctx, "browser_search_history", protocol.CommandHistorySearch, args.personalDataBaseArgs, params)
}

func (s *Service) browserGetHistoryVisitsHandler(ctx context.Context, _ mcp.CallToolRequest, args historyVisitsArgs) (*mcp.CallToolResult, error) {
	params, err := personalPageParams(args.personalPageArgs)
	if err != nil {
		return errorResult(err)
	}
	parsed, err := validatePersonalHTTPURL(args.URL)
	if err != nil {
		return errorResult(err)
	}
	params["url"] = parsed.String()
	return s.sendPersonalDataCommand(ctx, "browser_get_history_visits", protocol.CommandHistoryGetVisits, args.personalDataBaseArgs, params)
}

func (s *Service) browserDeleteHistoryURLHandler(ctx context.Context, _ mcp.CallToolRequest, args confirmedURLArgs) (*mcp.CallToolResult, error) {
	if err := s.requirePersonalConfirmation(protocol.CommandHistoryDeleteURL, args.BrowserID, args.Confirm); err != nil {
		return errorResult(err)
	}
	parsed, err := validatePersonalHTTPURL(args.URL)
	if err != nil {
		return errorResult(err)
	}
	return s.sendPersonalDataCommand(ctx, "browser_delete_history_url", protocol.CommandHistoryDeleteURL, args.personalDataBaseArgs, map[string]any{"url": parsed.String(), "confirm": true})
}

func (s *Service) browserDeleteHistoryRangeHandler(ctx context.Context, _ mcp.CallToolRequest, args historyRangeArgs) (*mcp.CallToolResult, error) {
	if err := s.requirePersonalConfirmation(protocol.CommandHistoryDeleteRange, args.BrowserID, args.Confirm); err != nil {
		return errorResult(err)
	}
	if !validPersonalTime(args.StartTime) || !validPersonalTime(args.EndTime) || args.StartTime >= args.EndTime {
		return errorResult(invalidPersonalData("startTime must be before endTime and both must be safe epoch times"))
	}
	return s.sendPersonalDataCommand(ctx, "browser_delete_history_range", protocol.CommandHistoryDeleteRange, args.personalDataBaseArgs, map[string]any{"startTime": args.StartTime, "endTime": args.EndTime, "confirm": true})
}

func (s *Service) browserClearHistoryHandler(ctx context.Context, _ mcp.CallToolRequest, args confirmedPersonalArgs) (*mcp.CallToolResult, error) {
	if err := s.requirePersonalConfirmation(protocol.CommandHistoryDeleteAll, args.BrowserID, args.Confirm); err != nil {
		return errorResult(err)
	}
	return s.sendPersonalDataCommand(ctx, "browser_clear_history", protocol.CommandHistoryDeleteAll, args.personalDataBaseArgs, map[string]any{"confirm": true})
}

func (s *Service) browserListBookmarksHandler(ctx context.Context, _ mcp.CallToolRequest, args bookmarkListArgs) (*mcp.CallToolResult, error) {
	params, err := personalPageParams(args.personalPageArgs)
	if err != nil {
		return errorResult(err)
	}
	if len(args.Query) > maxPersonalDataQuery || !validPersonalText(args.Query, true) {
		return errorResult(invalidPersonalData("query is invalid"))
	}
	if args.ParentID != "" && !validPersonalID(args.ParentID) {
		return errorResult(invalidPersonalData("parentId is invalid"))
	}
	if args.Query != "" && args.ParentID != "" {
		return errorResult(invalidPersonalData("query and parentId are mutually exclusive"))
	}
	if args.Query != "" {
		params["query"] = args.Query
	}
	if args.ParentID != "" {
		params["parentId"] = args.ParentID
	}
	return s.sendPersonalDataCommand(ctx, "browser_list_bookmarks", protocol.CommandBookmarksList, args.personalDataBaseArgs, params)
}

func (s *Service) browserCreateBookmarkHandler(ctx context.Context, _ mcp.CallToolRequest, args bookmarkCreateArgs) (*mcp.CallToolResult, error) {
	if !validPersonalTitle(args.Title) || args.ParentID != "" && !validPersonalID(args.ParentID) || args.Index != nil && (*args.Index < 0) {
		return errorResult(invalidPersonalData("bookmark title, parentId, or index is invalid"))
	}
	params := map[string]any{"title": args.Title}
	if args.URL != "" {
		parsed, err := validatePersonalHTTPURL(args.URL)
		if err != nil {
			return errorResult(err)
		}
		params["url"] = parsed.String()
	}
	if args.ParentID != "" {
		params["parentId"] = args.ParentID
	}
	putOptional(params, "index", args.Index)
	return s.sendPersonalDataCommand(ctx, "browser_create_bookmark", protocol.CommandBookmarksCreate, args.personalDataBaseArgs, params)
}

func (s *Service) browserUpdateBookmarkHandler(ctx context.Context, _ mcp.CallToolRequest, args bookmarkUpdateArgs) (*mcp.CallToolResult, error) {
	if !validPersonalID(args.BookmarkID) || args.Title == nil && args.URL == nil {
		return errorResult(invalidPersonalData("bookmarkId and at least one update field are required"))
	}
	params := map[string]any{"bookmarkId": args.BookmarkID}
	if args.Title != nil {
		if !validPersonalTitle(*args.Title) {
			return errorResult(invalidPersonalData("title is invalid"))
		}
		params["title"] = *args.Title
	}
	if args.URL != nil {
		parsed, err := validatePersonalHTTPURL(*args.URL)
		if err != nil {
			return errorResult(err)
		}
		params["url"] = parsed.String()
	}
	return s.sendPersonalDataCommand(ctx, "browser_update_bookmark", protocol.CommandBookmarksUpdate, args.personalDataBaseArgs, params)
}

func (s *Service) browserMoveBookmarkHandler(ctx context.Context, _ mcp.CallToolRequest, args bookmarkMoveArgs) (*mcp.CallToolResult, error) {
	if !validPersonalID(args.BookmarkID) || args.ParentID != "" && !validPersonalID(args.ParentID) || args.Index != nil && *args.Index < 0 || args.ParentID == "" && args.Index == nil {
		return errorResult(invalidPersonalData("bookmarkId and a valid parentId or index are required"))
	}
	params := map[string]any{"bookmarkId": args.BookmarkID}
	if args.ParentID != "" {
		params["parentId"] = args.ParentID
	}
	putOptional(params, "index", args.Index)
	return s.sendPersonalDataCommand(ctx, "browser_move_bookmark", protocol.CommandBookmarksMove, args.personalDataBaseArgs, params)
}

func (s *Service) browserRemoveBookmarkHandler(ctx context.Context, _ mcp.CallToolRequest, args bookmarkRemoveArgs) (*mcp.CallToolResult, error) {
	if !validPersonalID(args.BookmarkID) {
		return errorResult(invalidPersonalData("bookmarkId is invalid"))
	}
	if args.Recursive && !args.Confirm {
		if s.actionPolicy != nil {
			s.actionPolicy.AuditDenied(protocol.CommandBookmarksRemove, args.BrowserID, "", "confirmation_required")
		}
		return errorResult(protocol.NewError(protocol.CodeConfirmationRequired, "recursive bookmark removal requires confirm: true", false))
	}
	return s.sendPersonalDataCommand(ctx, "browser_remove_bookmark", protocol.CommandBookmarksRemove, args.personalDataBaseArgs, map[string]any{"bookmarkId": args.BookmarkID, "recursive": args.Recursive, "confirm": args.Confirm})
}

func (s *Service) browserListReadingListHandler(ctx context.Context, _ mcp.CallToolRequest, args readingListArgs) (*mcp.CallToolResult, error) {
	params, err := personalPageParams(args.personalPageArgs)
	if err != nil {
		return errorResult(err)
	}
	if args.Title != "" {
		if !validPersonalTitle(args.Title) {
			return errorResult(invalidPersonalData("title is invalid"))
		}
		params["title"] = args.Title
	}
	if args.URL != "" {
		parsed, parseErr := validatePersonalHTTPURL(args.URL)
		if parseErr != nil {
			return errorResult(parseErr)
		}
		params["url"] = parsed.String()
	}
	putOptional(params, "hasBeenRead", args.HasBeenRead)
	return s.sendPersonalDataCommand(ctx, "browser_list_reading_list", protocol.CommandReadingListList, args.personalDataBaseArgs, params)
}

func (s *Service) browserAddReadingListHandler(ctx context.Context, _ mcp.CallToolRequest, args readingListAddArgs) (*mcp.CallToolResult, error) {
	parsed, err := validatePersonalHTTPURL(args.URL)
	if err != nil || !validPersonalTitle(args.Title) {
		return errorResult(invalidPersonalData("url or title is invalid"))
	}
	return s.sendPersonalDataCommand(ctx, "browser_add_reading_list_entry", protocol.CommandReadingListAdd, args.personalDataBaseArgs, map[string]any{"url": parsed.String(), "title": args.Title, "hasBeenRead": args.HasBeenRead})
}

func (s *Service) browserUpdateReadingListHandler(ctx context.Context, _ mcp.CallToolRequest, args readingListUpdateArgs) (*mcp.CallToolResult, error) {
	parsed, err := validatePersonalHTTPURL(args.URL)
	if err != nil || args.Title == nil && args.HasBeenRead == nil {
		return errorResult(invalidPersonalData("url and at least one update field are required"))
	}
	params := map[string]any{"url": parsed.String()}
	if args.Title != nil {
		if !validPersonalTitle(*args.Title) {
			return errorResult(invalidPersonalData("title is invalid"))
		}
		params["title"] = *args.Title
	}
	putOptional(params, "hasBeenRead", args.HasBeenRead)
	return s.sendPersonalDataCommand(ctx, "browser_update_reading_list_entry", protocol.CommandReadingListUpdate, args.personalDataBaseArgs, params)
}

func (s *Service) browserRemoveReadingListHandler(ctx context.Context, _ mcp.CallToolRequest, args readingListRemoveArgs) (*mcp.CallToolResult, error) {
	parsed, err := validatePersonalHTTPURL(args.URL)
	if err != nil {
		return errorResult(err)
	}
	return s.sendPersonalDataCommand(ctx, "browser_remove_reading_list_entry", protocol.CommandReadingListRemove, args.personalDataBaseArgs, map[string]any{"url": parsed.String()})
}

func (s *Service) sendPersonalDataCommand(ctx context.Context, operation, command string, args personalDataBaseArgs, params map[string]any) (*mcp.CallToolResult, error) {
	startedAt := time.Now()
	browserID, _, raw, duration, err := s.sendRaw(ctx, args.BrowserID, command, nil, params, args.TimeoutMS)
	if err != nil {
		s.auditPersonalData(operation, personalDomain(command), browserID, protocol.ErrorFrom(err).Code, 0, duration)
		return errorResultWithDuration(err, duration)
	}
	result, count, err := decodePersonalDataResult(raw, command)
	if err != nil {
		s.auditPersonalData(operation, personalDomain(command), browserID, protocol.ErrorFrom(err).Code, 0, duration)
		return errorResultWithDuration(err, duration)
	}
	s.auditPersonalData(operation, personalDomain(command), browserID, "OK", count, time.Since(startedAt))
	return successResultWithTargetWarningsLimited(browserID, nil, result, duration, nil, s.resultLimits.MaxOutputBytes)
}

func decodePersonalDataResult(raw json.RawMessage, command string) (any, int, error) {
	if len(raw) == 0 || len(raw) > maxPersonalDataResult {
		return nil, 0, invalidPersonalDataResult()
	}
	switch personalDomain(command) {
	case "history":
		var result historyWireResult
		if err := decodePersonalWire(raw, &result); err != nil || !validHistoryResult(result, command) {
			return nil, 0, invalidPersonalDataResult()
		}
		return result, len(result.Items) + len(result.Visits) + result.DeletedCount, nil
	case "bookmarks":
		var result bookmarkWireResult
		if err := decodePersonalWire(raw, &result); err != nil || !validBookmarkResult(result, command) {
			return nil, 0, invalidPersonalDataResult()
		}
		return result, len(result.Bookmarks) + len(result.RemovedIDs), nil
	case "readingList":
		var result readingListWireResult
		if err := decodePersonalWire(raw, &result); err != nil || !validReadingListResult(result, command) {
			return nil, 0, invalidPersonalDataResult()
		}
		return result, len(result.Entries), nil
	default:
		return nil, 0, invalidPersonalDataResult()
	}
}

func validHistoryResult(result historyWireResult, command string) bool {
	if result.TotalMatched < 0 || result.TotalMatched > maxPersonalDataScan || !validPersonalCursor(result.NextCursor) || len(result.Items) > maxPersonalDataLimit || len(result.Visits) > maxPersonalDataLimit || result.DeletedCount < 0 || result.DeletedCount > maxPersonalDataScan || !validPersonalWarnings(result.Warnings) {
		return false
	}
	for _, item := range result.Items {
		if !validPersonalID(item.ID) || !validPersonalBrowserURL(item.URL) || !validPersonalText(item.Title, true) || len(item.Title) > maxPersonalDataTitle || !validPersonalTime(item.LastVisitTime) || item.VisitCount < 0 || item.TypedCount < 0 {
			return false
		}
	}
	for _, visit := range result.Visits {
		if !validPersonalID(visit.ID) || !validPersonalID(visit.VisitID) || visit.ReferringVisitID != "" && !validPersonalID(visit.ReferringVisitID) || !validPersonalTime(visit.VisitTime) || !personalHistoryTransition(visit.Transition) {
			return false
		}
	}
	switch command {
	case protocol.CommandHistorySearch:
		return result.Kind == "history" && !result.Changed && result.Operation == "" && len(result.Visits) == 0 && result.TotalMatched >= len(result.Items)
	case protocol.CommandHistoryGetVisits:
		return result.Kind == "visits" && !result.Changed && result.Operation == "" && len(result.Items) == 0 && result.TotalMatched >= len(result.Visits)
	case protocol.CommandHistoryDeleteURL:
		return result.Kind == "history_mutation" && result.Changed && result.Operation == "delete_url" && result.Scope == "url" && result.NextCursor == "" && len(result.Items) == 0 && len(result.Visits) == 0
	case protocol.CommandHistoryDeleteRange:
		return result.Kind == "history_mutation" && result.Changed && result.Operation == "delete_range" && result.Scope == "range" && validPersonalTime(result.StartTime) && validPersonalTime(result.EndTime) && result.StartTime < result.EndTime
	case protocol.CommandHistoryDeleteAll:
		return result.Kind == "history_mutation" && result.Changed && result.Operation == "delete_all" && result.Scope == "all"
	default:
		return false
	}
}

func validBookmarkResult(result bookmarkWireResult, command string) bool {
	if result.TotalMatched < 0 || result.TotalMatched > maxPersonalDataScan || !validPersonalCursor(result.NextCursor) || len(result.Bookmarks) > maxPersonalDataLimit || len(result.RemovedIDs) > 1 || !validPersonalWarnings(result.Warnings) {
		return false
	}
	for _, item := range result.Bookmarks {
		if !validPersonalID(item.ID) || item.ParentID != "" && !validPersonalID(item.ParentID) || item.Index < 0 || !validPersonalText(item.Title, true) || len(item.Title) > maxPersonalDataTitle || item.URL != "" && !validPersonalBrowserURL(item.URL) || !validOptionalPersonalTime(item.DateAdded) || !validOptionalPersonalTime(item.DateGroupModified) || len(item.Unmodifiable) > 64 || !validPersonalText(item.Unmodifiable, true) {
			return false
		}
	}
	for _, id := range result.RemovedIDs {
		if !validPersonalID(id) {
			return false
		}
	}
	if command == protocol.CommandBookmarksList {
		return result.Kind == "bookmarks" && !result.Changed && result.Operation == "" && result.TotalMatched >= len(result.Bookmarks) && result.BookmarkID == "" && len(result.RemovedIDs) == 0
	}
	expected := map[string]string{
		protocol.CommandBookmarksCreate: "create",
		protocol.CommandBookmarksUpdate: "update",
		protocol.CommandBookmarksMove:   "move",
		protocol.CommandBookmarksRemove: "remove",
	}[command]
	if result.Kind != "bookmark_mutation" || !result.Changed || result.NextCursor != "" {
		return false
	}
	if command == protocol.CommandBookmarksRemove {
		return (result.Operation == "remove" || result.Operation == "remove_tree") && len(result.Bookmarks) == 0 && len(result.RemovedIDs) == 1 && result.BookmarkID == result.RemovedIDs[0]
	}
	return result.Operation == expected && len(result.Bookmarks) == 1 && result.TotalMatched == 1 && len(result.RemovedIDs) == 0 && result.BookmarkID == result.Bookmarks[0].ID
}

func validReadingListResult(result readingListWireResult, command string) bool {
	if result.TotalMatched < 0 || result.TotalMatched > maxPersonalDataScan || !validPersonalCursor(result.NextCursor) || len(result.Entries) > maxPersonalDataLimit || !validPersonalWarnings(result.Warnings) || result.TargetURL != "" && !validPersonalBrowserURL(result.TargetURL) {
		return false
	}
	for _, entry := range result.Entries {
		if !validPersonalBrowserURL(entry.URL) || !validPersonalText(entry.Title, true) || len(entry.Title) > maxPersonalDataTitle || !validPersonalTime(entry.CreationTime) || !validPersonalTime(entry.LastUpdateTime) {
			return false
		}
	}
	if command == protocol.CommandReadingListList {
		return result.Kind == "reading_list" && !result.Changed && result.Operation == "" && result.TotalMatched >= len(result.Entries) && result.TargetURL == ""
	}
	expected := map[string]string{
		protocol.CommandReadingListAdd:    "add",
		protocol.CommandReadingListUpdate: "update",
		protocol.CommandReadingListRemove: "remove",
	}[command]
	if result.Kind != "reading_list_mutation" || !result.Changed || result.Operation != expected || result.NextCursor != "" || result.TargetURL == "" {
		return false
	}
	if command == protocol.CommandReadingListRemove {
		return len(result.Entries) == 0 && result.TotalMatched == 0
	}
	return len(result.Entries) == 1 && result.TotalMatched == 1 && result.Entries[0].URL == result.TargetURL
}

func personalPageParams(args personalPageArgs) (map[string]any, error) {
	limit := defaultPersonalDataLimit
	if args.Limit != nil {
		limit = *args.Limit
	}
	if limit < 1 || limit > maxPersonalDataLimit || !validPersonalCursor(args.Cursor) {
		return nil, invalidPersonalData("limit or cursor is invalid")
	}
	params := map[string]any{"limit": limit}
	if args.Cursor != "" {
		params["cursor"] = args.Cursor
	}
	return params, nil
}

func validatePersonalHTTPURL(value string) (*url.URL, error) {
	if len(value) == 0 || len(value) > maxPersonalDataURLBytes || !utf8.ValidString(value) || containsCookieControl(value) {
		return nil, invalidPersonalData("url must be a bounded HTTP(S) URL without credentials")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return nil, invalidPersonalData("url must be a bounded HTTP(S) URL without credentials")
	}
	return parsed, nil
}

func validateOptionalPersonalTime(value *float64, field string) error {
	if value != nil && !validPersonalTime(*value) {
		return invalidPersonalData(field + " must be a safe non-negative epoch time")
	}
	return nil
}

func validPersonalTime(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= float64(maxJavaScriptSafeInteger)
}

func validOptionalPersonalTime(value float64) bool { return value == 0 || validPersonalTime(value) }

func validPersonalID(value string) bool {
	return value != "" && len(value) <= maxPersonalDataID && validPersonalText(value, false)
}

func validPersonalTitle(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= maxPersonalDataTitle && validPersonalText(value, false)
}

func validPersonalText(value string, allowEmpty bool) bool {
	return (allowEmpty || value != "") && utf8.ValidString(value) && !containsCookieControl(value)
}

func validPersonalBrowserURL(value string) bool {
	if len(value) == 0 || len(value) > maxPersonalDataURLBytes || !validPersonalText(value, false) {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	for key, values := range parsed.Query() {
		if !personalSensitiveQueryName(key) {
			continue
		}
		for _, value := range values {
			if value != "[REDACTED]" {
				return false
			}
		}
	}
	return true
}

func personalSensitiveQueryName(value string) bool {
	normalized := strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(value))
	switch normalized {
	case "password", "passwd", "passphrase", "secret", "token", "credential", "authorization",
		"clientsecret", "idtoken", "cookie", "apikey", "accesstoken", "refreshtoken":
		return true
	default:
		return false
	}
}

func validPersonalCursor(value string) bool {
	if value == "" {
		return true
	}
	offset, err := strconv.Atoi(value)
	return err == nil && offset >= 1 && offset < maxPersonalDataScan && strconv.Itoa(offset) == value
}

func validPersonalWarnings(warnings []string) bool {
	if len(warnings) > 4 {
		return false
	}
	for _, warning := range warnings {
		if len(warning) == 0 || len(warning) > 256 || !validPersonalText(warning, false) {
			return false
		}
	}
	return true
}

func personalHistoryTransition(value string) bool {
	switch value {
	case "link", "typed", "auto_bookmark", "auto_subframe", "manual_subframe", "generated", "auto_toplevel", "form_submit", "reload", "keyword", "keyword_generated":
		return true
	default:
		return false
	}
}

func decodePersonalWire(raw []byte, result any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return err
	}
	return nil
}

func (s *Service) requirePersonalConfirmation(command, browserID string, confirm bool) error {
	if confirm {
		return nil
	}
	if s.actionPolicy != nil {
		s.actionPolicy.AuditDenied(command, browserID, "", "confirmation_required")
	}
	return protocol.NewError(protocol.CodeConfirmationRequired, "the personal-data deletion requires confirm: true", false)
}

func (s *Service) auditPersonalData(operation, domain, browserID string, outcome any, count int, duration time.Duration) {
	if s.auditLogger == nil {
		return
	}
	if !isDedicatedPersonalDataTool(operation) {
		operation = "invalid"
	}
	s.auditLogger.Printf("operation=personal_data domain=%q tool=%q browserId=%q count=%d outcome=%s duration=%s", domain, operation, boundedRawCDPAudit(browserID), count, fmt.Sprint(outcome), duration.Round(time.Microsecond))
}

func personalDomain(command string) string {
	switch {
	case strings.HasPrefix(command, "history."):
		return "history"
	case strings.HasPrefix(command, "bookmarks."):
		return "bookmarks"
	case strings.HasPrefix(command, "readingList."):
		return "readingList"
	default:
		return ""
	}
}

func isDedicatedPersonalDataCommand(command string) bool {
	return personalDomain(command) != ""
}

func isDedicatedPersonalDataTool(tool string) bool {
	for _, candidate := range []string{
		"browser_search_history", "browser_get_history_visits", "browser_delete_history_url",
		"browser_delete_history_range", "browser_clear_history", "browser_list_bookmarks",
		"browser_create_bookmark", "browser_update_bookmark", "browser_move_bookmark",
		"browser_remove_bookmark", "browser_list_reading_list", "browser_add_reading_list_entry",
		"browser_update_reading_list_entry", "browser_remove_reading_list_entry",
	} {
		if tool == candidate {
			return true
		}
	}
	return false
}

func invalidPersonalData(message string) *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}

func invalidPersonalDataResult() *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, "the browser returned an invalid personal-data result", false)
}
