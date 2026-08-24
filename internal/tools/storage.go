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
	defaultStorageLimit     = 50
	maxStorageLimit         = 200
	maxStorageScanItems     = 10_000
	maxStorageResultBytes   = 1_000_000
	maxStorageKeyBytes      = 1_024
	maxStorageValueBytes    = 64 * 1_024
	maxStorageObservedBytes = 1_000_000
	maxStorageNameBytes     = 1_024
	maxStorageWarnings      = 4
	maskedStorageValue      = "[MASKED]"
	omittedStorageValue     = "[OMITTED]"
)

var storageTypes = []string{"localStorage", "sessionStorage"}
var clearableStorageTypes = []string{"localStorage", "sessionStorage", "cacheStorage", "indexedDB"}

type storageTargetArgs struct {
	BrowserID  string `json:"browserId,omitempty"`
	TabID      *int   `json:"tabId,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
	TimeoutMS  *int   `json:"timeoutMs,omitempty"`
}

type storageListArgs struct {
	storageTargetArgs
	Origin        string `json:"origin"`
	StorageType   string `json:"storageType"`
	Cursor        string `json:"cursor,omitempty"`
	Limit         *int   `json:"limit,omitempty"`
	IncludeValues *bool  `json:"includeValues,omitempty"`
}

type storageGetArgs struct {
	storageTargetArgs
	Origin       string `json:"origin"`
	StorageType  string `json:"storageType"`
	Key          string `json:"key"`
	IncludeValue *bool  `json:"includeValue,omitempty"`
}

type storageSetArgs struct {
	storageTargetArgs
	Origin      string `json:"origin"`
	StorageType string `json:"storageType"`
	Key         string `json:"key"`
	Value       string `json:"value"`
}

type storageRemoveArgs struct {
	storageTargetArgs
	Origin      string `json:"origin"`
	StorageType string `json:"storageType"`
	Key         string `json:"key"`
}

type storageMetadataArgs struct {
	storageTargetArgs
	Origin string `json:"origin"`
	Cursor string `json:"cursor,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
}

type storageClearArgs struct {
	storageTargetArgs
	Origin  string   `json:"origin"`
	Types   []string `json:"types"`
	Confirm bool     `json:"confirm"`
}

type storageWireItem struct {
	Key           string `json:"key"`
	Value         string `json:"value"`
	ValueIncluded bool   `json:"valueIncluded"`
	ValueLength   int    `json:"valueLength"`
}

type storageCacheWire struct {
	Name string `json:"name"`
}

type storageDatabaseWire struct {
	Name    string  `json:"name"`
	Version float64 `json:"version"`
}

type storageWireResult struct {
	Kind           string                `json:"kind"`
	TabID          int                   `json:"tabId"`
	DocumentID     string                `json:"documentId"`
	Origin         string                `json:"origin"`
	StorageType    string                `json:"storageType"`
	ValuesIncluded bool                  `json:"valuesIncluded"`
	Items          []storageWireItem     `json:"items"`
	Caches         []storageCacheWire    `json:"caches"`
	Databases      []storageDatabaseWire `json:"databases"`
	TotalMatched   int                   `json:"totalMatched"`
	NextCursor     string                `json:"nextCursor"`
	Operation      string                `json:"operation"`
	Changed        bool                  `json:"changed"`
	Supported      bool                  `json:"supported"`
	RequestedTypes []string              `json:"requestedTypes"`
	ClearedTypes   []string              `json:"clearedTypes"`
	ClearedCounts  map[string]int        `json:"clearedCounts"`
	Warnings       []string              `json:"warnings"`
}

func (s *Service) registerStorageTools(mcpServer *server.MCPServer) {
	storageType := func() mcp.ToolOption {
		return mcp.WithString("storageType", mcp.Required(), mcp.Description("Web Storage area"), mcp.Enum(storageTypes...))
	}
	mcpServer.AddTool(
		newStorageTool(
			"browser_list_storage_items",
			"List bounded localStorage or sessionStorage items with values masked by default",
			storageType(),
			mcp.WithString("cursor", mcp.Description("Positive offset cursor from a previous result")),
			mcp.WithNumber("limit", mcp.Description("Maximum items to return"), mcp.Min(1), mcp.Max(maxStorageLimit), mcp.DefaultNumber(defaultStorageLimit)),
			mcp.WithBoolean("includeValues", mcp.Description("Return values only when Sensitive data is enabled"), mcp.DefaultBool(false)),
		),
		mcp.NewTypedToolHandler(s.browserListStorageItemsHandler),
	)
	mcpServer.AddTool(
		newStorageTool(
			"browser_get_storage_item",
			"Get one localStorage or sessionStorage item with its value masked by default",
			storageType(),
			mcp.WithString("key", mcp.Required(), mcp.Description("Exact storage key"), mcp.MaxLength(maxStorageKeyBytes)),
			mcp.WithBoolean("includeValue", mcp.Description("Return the value only when Sensitive data is enabled"), mcp.DefaultBool(false)),
		),
		mcp.NewTypedToolHandler(s.browserGetStorageItemHandler),
	)
	mcpServer.AddTool(
		newStorageTool(
			"browser_set_storage_item",
			"Set one bounded localStorage or sessionStorage item without echoing its value",
			storageType(),
			mcp.WithString("key", mcp.Required(), mcp.MaxLength(maxStorageKeyBytes)),
			mcp.WithString("value", mcp.Required(), mcp.MaxLength(maxStorageValueBytes)),
		),
		mcp.NewTypedToolHandler(s.browserSetStorageItemHandler),
	)
	mcpServer.AddTool(
		newStorageTool(
			"browser_remove_storage_item",
			"Remove one localStorage or sessionStorage item",
			storageType(),
			mcp.WithString("key", mcp.Required(), mcp.MaxLength(maxStorageKeyBytes)),
		),
		mcp.NewTypedToolHandler(s.browserRemoveStorageItemHandler),
	)
	for _, registration := range []struct {
		name        string
		description string
		command     string
	}{
		{"browser_get_cache_metadata", "List bounded Cache Storage names without requests or response bodies", protocol.CommandStorageCacheMetadata},
		{"browser_get_indexeddb_metadata", "List bounded IndexedDB database names and versions without records or blobs", protocol.CommandStorageIndexedDBMetadata},
	} {
		registration := registration
		mcpServer.AddTool(
			newStorageTool(
				registration.name, registration.description,
				mcp.WithString("cursor", mcp.Description("Positive offset cursor from a previous result")),
				mcp.WithNumber("limit", mcp.Description("Maximum metadata records to return"), mcp.Min(1), mcp.Max(maxStorageLimit), mcp.DefaultNumber(defaultStorageLimit)),
			),
			mcp.NewTypedToolHandler(func(ctx context.Context, _ mcp.CallToolRequest, args storageMetadataArgs) (*mcp.CallToolResult, error) {
				params, err := validateStorageMetadataArgs(args)
				if err != nil {
					return errorResult(err)
				}
				return s.sendStorageCommand(ctx, registration.name, registration.command, args.storageTargetArgs, params, false)
			}),
		)
	}
	mcpServer.AddTool(
		newStorageTool(
			"browser_clear_origin_storage",
			"Clear explicitly selected storage types for one exact origin after confirmation",
			mcp.WithArray("types", mcp.Required(), mcp.Description("Storage types to clear"), mcp.Items(map[string]any{"type": "string", "enum": clearableStorageTypes}), mcp.MinItems(1), mcp.MaxItems(len(clearableStorageTypes))),
			mcp.WithBoolean("confirm", mcp.Required(), mcp.Description("Must be true because this deletes origin data")),
		),
		mcp.NewTypedToolHandler(s.browserClearOriginStorageHandler),
	)
}

func newStorageTool(name, description string, extra ...mcp.ToolOption) mcp.Tool {
	options := []mcp.ToolOption{
		mcp.WithDescription(description), optionalBrowserID(), optionalTabID(), optionalDocumentID(),
		mcp.WithString("origin", mcp.Required(), mcp.Description("Exact HTTP(S) origin of the selected root document"), mcp.MaxLength(8192)),
	}
	options = append(options, extra...)
	options = append(options, optionalTimeout())
	return mcp.NewTool(name, options...)
}

func (s *Service) browserListStorageItemsHandler(ctx context.Context, _ mcp.CallToolRequest, args storageListArgs) (*mcp.CallToolResult, error) {
	params, err := validateStorageListArgs(args)
	if err != nil {
		return errorResult(err)
	}
	includeValues := args.IncludeValues != nil && *args.IncludeValues
	command := protocol.CommandStorageList
	if includeValues {
		command = protocol.CommandStorageListSensitive
	}
	return s.sendStorageCommand(ctx, "browser_list_storage_items", command, args.storageTargetArgs, params, includeValues)
}

func (s *Service) browserGetStorageItemHandler(ctx context.Context, _ mcp.CallToolRequest, args storageGetArgs) (*mcp.CallToolResult, error) {
	params, err := validateStorageItemArgs(args.Origin, args.StorageType, args.Key)
	if err != nil {
		return errorResult(err)
	}
	includeValue := args.IncludeValue != nil && *args.IncludeValue
	command := protocol.CommandStorageGet
	if includeValue {
		command = protocol.CommandStorageGetSensitive
	}
	return s.sendStorageCommand(ctx, "browser_get_storage_item", command, args.storageTargetArgs, params, includeValue)
}

func (s *Service) browserSetStorageItemHandler(ctx context.Context, _ mcp.CallToolRequest, args storageSetArgs) (*mcp.CallToolResult, error) {
	params, err := validateStorageItemArgs(args.Origin, args.StorageType, args.Key)
	if err != nil {
		return errorResult(err)
	}
	if !utf8.ValidString(args.Value) || len(args.Value) > maxStorageValueBytes {
		return errorResult(invalidStorage("value is invalid or exceeds 65536 UTF-8 bytes"))
	}
	params["value"] = args.Value
	return s.sendStorageCommand(ctx, "browser_set_storage_item", protocol.CommandStorageSet, args.storageTargetArgs, params, false)
}

func (s *Service) browserRemoveStorageItemHandler(ctx context.Context, _ mcp.CallToolRequest, args storageRemoveArgs) (*mcp.CallToolResult, error) {
	params, err := validateStorageItemArgs(args.Origin, args.StorageType, args.Key)
	if err != nil {
		return errorResult(err)
	}
	return s.sendStorageCommand(ctx, "browser_remove_storage_item", protocol.CommandStorageRemove, args.storageTargetArgs, params, false)
}

func (s *Service) browserClearOriginStorageHandler(ctx context.Context, _ mcp.CallToolRequest, args storageClearArgs) (*mcp.CallToolResult, error) {
	if !args.Confirm {
		if s.actionPolicy != nil {
			s.actionPolicy.AuditDenied(protocol.CommandStorageClear, args.BrowserID, "", "confirmation_required")
		}
		return errorResult(protocol.NewError(protocol.CodeConfirmationRequired, "clearing origin storage requires confirm: true", false))
	}
	origin, err := validateStorageOrigin(args.Origin)
	if err != nil {
		return errorResult(err)
	}
	if !uniqueAllowedStorageStrings(args.Types, clearableStorageTypes) {
		return errorResult(invalidStorage("types contains an unsupported or duplicate storage type"))
	}
	return s.sendStorageCommand(ctx, "browser_clear_origin_storage", protocol.CommandStorageClear, args.storageTargetArgs, map[string]any{
		"origin": origin, "types": args.Types, "confirm": true,
	}, false)
}

func (s *Service) sendStorageCommand(ctx context.Context, operation, command string, args storageTargetArgs, params map[string]any, includeValues bool) (*mcp.CallToolResult, error) {
	startedAt := time.Now()
	rootFrameID := 0
	browserID, target, raw, duration, err := s.sendRaw(ctx, args.BrowserID, command, pageTarget(args.TabID, &rootFrameID, args.DocumentID), params, args.TimeoutMS)
	if err != nil {
		s.auditStorage(operation, browserID, target, includeValues, protocol.ErrorFrom(err).Code, 0, duration)
		return errorResultWithDuration(err, duration)
	}
	result, err := decodeStorageResult(raw, command, target)
	if err != nil {
		s.auditStorage(operation, browserID, target, includeValues, protocol.ErrorFrom(err).Code, 0, duration)
		return errorResultWithDuration(err, duration)
	}
	if target == nil {
		frameID := 0
		target = &protocol.Target{BrowserID: browserID, TabID: &result.TabID, FrameID: &frameID, DocumentID: result.DocumentID}
	} else if target.DocumentID == "" {
		target.DocumentID = result.DocumentID
	}
	count := len(result.Items) + len(result.Caches) + len(result.Databases)
	s.auditStorage(operation, browserID, target, includeValues, "OK", count, time.Since(startedAt))
	return successResultWithTargetWarningsLimited(browserID, target, result, duration, nil, s.resultLimits.MaxOutputBytes)
}

func validateStorageListArgs(args storageListArgs) (map[string]any, error) {
	origin, err := validateStorageOrigin(args.Origin)
	if err != nil {
		return nil, err
	}
	if !performanceStringAllowed(storageTypes, args.StorageType) {
		return nil, invalidStorage("storageType is unsupported")
	}
	limit, err := validateStoragePage(args.Cursor, args.Limit)
	if err != nil {
		return nil, err
	}
	params := map[string]any{"origin": origin, "storageType": args.StorageType, "limit": limit}
	if args.Cursor != "" {
		params["cursor"] = args.Cursor
	}
	return params, nil
}

func validateStorageItemArgs(rawOrigin, storageType, key string) (map[string]any, error) {
	origin, err := validateStorageOrigin(rawOrigin)
	if err != nil {
		return nil, err
	}
	if !performanceStringAllowed(storageTypes, storageType) {
		return nil, invalidStorage("storageType is unsupported")
	}
	if !utf8.ValidString(key) || len(key) > maxStorageKeyBytes {
		return nil, invalidStorage("key is invalid or exceeds 1024 UTF-8 bytes")
	}
	return map[string]any{"origin": origin, "storageType": storageType, "key": key}, nil
}

func validateStorageMetadataArgs(args storageMetadataArgs) (map[string]any, error) {
	origin, err := validateStorageOrigin(args.Origin)
	if err != nil {
		return nil, err
	}
	limit, err := validateStoragePage(args.Cursor, args.Limit)
	if err != nil {
		return nil, err
	}
	params := map[string]any{"origin": origin, "limit": limit}
	if args.Cursor != "" {
		params["cursor"] = args.Cursor
	}
	return params, nil
}

func validateStoragePage(cursor string, requestedLimit *int) (int, error) {
	limit := defaultStorageLimit
	assignInt(&limit, requestedLimit)
	if limit < 1 || limit > maxStorageLimit {
		return 0, invalidStorage("limit is outside the supported range")
	}
	if cursor != "" {
		value, err := strconv.ParseUint(cursor, 10, 53)
		if err != nil || value == 0 {
			return 0, invalidStorage("cursor must be a positive safe integer string")
		}
	}
	return limit, nil
}

func validateStorageOrigin(value string) (string, error) {
	parsed, err := validateCookieURL(value)
	if err != nil || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" {
		return "", invalidStorage("origin must be an exact HTTP(S) origin")
	}
	return cookieURLOrigin(parsed), nil
}

func decodeStorageResult(raw json.RawMessage, command string, target *protocol.Target) (storageWireResult, error) {
	var result storageWireResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&result)
	var trailing any
	if decodeErr == nil && decoder.Decode(&trailing) != io.EOF {
		decodeErr = invalidStorageResult()
	}
	expectedKind := storageResultKind(command)
	wantsValues := command == protocol.CommandStorageListSensitive || command == protocol.CommandStorageGetSensitive
	if len(raw) > maxStorageResultBytes || decodeErr != nil || result.Kind != expectedKind ||
		result.TabID < 0 || strings.TrimSpace(result.DocumentID) == "" || len(result.DocumentID) > maxCookieDocumentBytes ||
		!networkTargetMatches(target, result.TabID, result.DocumentID) || result.ValuesIncluded != wantsValues ||
		len(result.Warnings) > maxStorageWarnings || !validStorageResultOrigin(result.Origin) || !result.Supported {
		return result, invalidStorageResult()
	}
	for _, warning := range result.Warnings {
		if strings.TrimSpace(warning) == "" || len(warning) > 256 {
			return result, invalidStorageResult()
		}
	}
	if !validateStorageResultShape(&result, command, wantsValues) {
		return result, invalidStorageResult()
	}
	return result, nil
}

func validateStorageResultShape(result *storageWireResult, command string, wantsValues bool) bool {
	expectedKind := storageResultKind(command)
	if (expectedKind == "items" || expectedKind == "item" || expectedKind == "mutation") &&
		!performanceStringAllowed(storageTypes, result.StorageType) {
		return false
	}
	if expectedKind != "items" && expectedKind != "item" && expectedKind != "mutation" && result.StorageType != "" {
		return false
	}
	if len(result.Items) > maxStorageLimit || len(result.Caches) > maxStorageLimit || len(result.Databases) > maxStorageLimit ||
		result.TotalMatched < 0 || result.TotalMatched > maxStorageScanItems {
		return false
	}
	for _, item := range result.Items {
		if !validStorageItem(item, wantsValues) {
			return false
		}
	}
	for _, cache := range result.Caches {
		if !utf8.ValidString(cache.Name) || len(cache.Name) > maxStorageNameBytes {
			return false
		}
	}
	for _, database := range result.Databases {
		if !utf8.ValidString(database.Name) || len(database.Name) > maxStorageNameBytes ||
			!isFinite(database.Version) || database.Version < 1 || database.Version > 9_007_199_254_740_991 || database.Version != float64(uint64(database.Version)) {
			return false
		}
	}
	if result.NextCursor != "" {
		cursor, err := strconv.ParseUint(result.NextCursor, 10, 53)
		if err != nil || cursor == 0 || !performanceStringAllowed([]string{"items", "cacheMetadata", "indexedDBMetadata"}, expectedKind) {
			return false
		}
	}
	switch expectedKind {
	case "items", "item":
		maxItems := maxStorageLimit
		if expectedKind == "item" {
			maxItems = 1
		}
		return len(result.Items) <= maxItems && len(result.Caches) == 0 && len(result.Databases) == 0 &&
			result.TotalMatched >= len(result.Items) && result.Operation == "" && result.ClearedCounts == nil &&
			len(result.RequestedTypes) == 0 && len(result.ClearedTypes) == 0
	case "mutation":
		return len(result.Items) == 0 && len(result.Caches) == 0 && len(result.Databases) == 0 && result.TotalMatched == 0 &&
			performanceStringAllowed([]string{"set", "remove"}, result.Operation) && result.ClearedCounts == nil &&
			len(result.RequestedTypes) == 0 && len(result.ClearedTypes) == 0
	case "cacheMetadata":
		return len(result.Items) == 0 && len(result.Databases) == 0 && result.TotalMatched >= len(result.Caches) &&
			result.Operation == "" && result.ClearedCounts == nil && len(result.RequestedTypes) == 0 && len(result.ClearedTypes) == 0
	case "indexedDBMetadata":
		return len(result.Items) == 0 && len(result.Caches) == 0 && result.TotalMatched >= len(result.Databases) &&
			result.Operation == "" && result.ClearedCounts == nil && len(result.RequestedTypes) == 0 && len(result.ClearedTypes) == 0
	case "clear":
		return len(result.Items) == 0 && len(result.Caches) == 0 && len(result.Databases) == 0 && result.TotalMatched == 0 &&
			result.Operation == "clear" && uniqueAllowedStorageStrings(result.RequestedTypes, clearableStorageTypes) &&
			uniqueAllowedStorageStrings(result.ClearedTypes, result.RequestedTypes) && validClearedCounts(result.ClearedCounts, result.RequestedTypes)
	default:
		return false
	}
}

func validStorageItem(item storageWireItem, wantsValues bool) bool {
	if !utf8.ValidString(item.Key) || len(item.Key) > maxStorageKeyBytes || item.ValueLength < 0 || item.ValueLength > maxStorageObservedBytes {
		return false
	}
	if !wantsValues {
		return !item.ValueIncluded && item.Value == maskedStorageValue
	}
	if item.ValueIncluded {
		return utf8.ValidString(item.Value) && len(item.Value) == item.ValueLength && len(item.Value) <= maxStorageValueBytes
	}
	return item.Value == omittedStorageValue && item.ValueLength > maxStorageValueBytes
}

func validClearedCounts(counts map[string]int, requested []string) bool {
	if counts == nil || len(counts) != len(requested) {
		return false
	}
	allowed := make(map[string]bool, len(requested))
	for _, value := range requested {
		allowed[value] = true
	}
	for storageType, count := range counts {
		if !allowed[storageType] || count < 0 || count > maxStorageScanItems {
			return false
		}
	}
	return true
}

func uniqueAllowedStorageStrings(values, allowed []string) bool {
	if len(values) == 0 || len(values) > len(allowed) {
		return false
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] || !performanceStringAllowed(allowed, value) {
			return false
		}
		seen[value] = true
	}
	return true
}

func storageResultKind(command string) string {
	switch command {
	case protocol.CommandStorageList, protocol.CommandStorageListSensitive:
		return "items"
	case protocol.CommandStorageGet, protocol.CommandStorageGetSensitive:
		return "item"
	case protocol.CommandStorageSet, protocol.CommandStorageRemove:
		return "mutation"
	case protocol.CommandStorageCacheMetadata:
		return "cacheMetadata"
	case protocol.CommandStorageIndexedDBMetadata:
		return "indexedDBMetadata"
	case protocol.CommandStorageClear:
		return "clear"
	default:
		return ""
	}
}

func validStorageResultOrigin(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	normalized, err := validateStorageOrigin(value)
	return err == nil && normalized == cookieURLOrigin(parsed)
}

func isDedicatedStorageCommand(command string) bool {
	return performanceStringAllowed([]string{
		protocol.CommandStorageList, protocol.CommandStorageListSensitive,
		protocol.CommandStorageGet, protocol.CommandStorageGetSensitive,
		protocol.CommandStorageSet, protocol.CommandStorageRemove,
		protocol.CommandStorageCacheMetadata, protocol.CommandStorageIndexedDBMetadata,
		protocol.CommandStorageClear,
	}, command)
}

func (s *Service) auditStorage(operation, browserID string, target *protocol.Target, includeValues bool, outcome any, count int, duration time.Duration) {
	if s.auditLogger == nil {
		return
	}
	if !performanceStringAllowed([]string{
		"browser_list_storage_items", "browser_get_storage_item", "browser_set_storage_item",
		"browser_remove_storage_item", "browser_get_cache_metadata",
		"browser_get_indexeddb_metadata", "browser_clear_origin_storage",
	}, operation) {
		operation = "invalid"
	}
	tabID := -1
	if target != nil && target.TabID != nil {
		tabID = *target.TabID
	}
	s.auditLogger.Printf("operation=storage tool=%q browserId=%q tabId=%d valuesIncluded=%t count=%d outcome=%s duration=%s", operation, boundedRawCDPAudit(browserID), tabID, includeValues, count, fmt.Sprint(outcome), duration.Round(time.Microsecond))
}

func invalidStorage(message string) *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}

func invalidStorageResult() *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, "the browser returned an invalid storage result", false)
}
