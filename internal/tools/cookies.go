package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultCookieLimit     = 50
	maxCookieLimit         = 200
	maxCookieResultBytes   = 1_000_000
	maxCookieNameBytes     = 256
	maxCookieValueBytes    = 4_096
	maxCookieDomainBytes   = 253
	maxCookiePathBytes     = 2_048
	maxCookieStoreIDBytes  = 256
	maxCookieDocumentBytes = 256
	maxCookieWarnings      = 4
	maskedCookieValue      = "[MASKED]"
	omittedCookieValue     = "[OMITTED]"
)

var cookieNamePattern = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

type cookieTargetArgs struct {
	BrowserID  string `json:"browserId,omitempty"`
	TabID      *int   `json:"tabId,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
	TimeoutMS  *int   `json:"timeoutMs,omitempty"`
}

type cookiePartitionKey struct {
	TopLevelSite         string `json:"topLevelSite"`
	HasCrossSiteAncestor *bool  `json:"hasCrossSiteAncestor,omitempty"`
}

type cookieListArgs struct {
	cookieTargetArgs
	URL           string              `json:"url"`
	Domain        string              `json:"domain,omitempty"`
	Name          string              `json:"name,omitempty"`
	Path          string              `json:"path,omitempty"`
	Secure        *bool               `json:"secure,omitempty"`
	Session       *bool               `json:"session,omitempty"`
	StoreID       string              `json:"storeId,omitempty"`
	PartitionKey  *cookiePartitionKey `json:"partitionKey,omitempty"`
	Cursor        string              `json:"cursor,omitempty"`
	Limit         *int                `json:"limit,omitempty"`
	IncludeValues *bool               `json:"includeValues,omitempty"`
}

type cookieGetArgs struct {
	cookieTargetArgs
	URL          string              `json:"url"`
	Name         string              `json:"name"`
	StoreID      string              `json:"storeId,omitempty"`
	PartitionKey *cookiePartitionKey `json:"partitionKey,omitempty"`
	IncludeValue *bool               `json:"includeValue,omitempty"`
}

type cookieSetArgs struct {
	cookieTargetArgs
	URL            string              `json:"url"`
	Name           string              `json:"name"`
	Value          string              `json:"value"`
	Domain         string              `json:"domain,omitempty"`
	Path           string              `json:"path,omitempty"`
	Secure         *bool               `json:"secure,omitempty"`
	HTTPOnly       *bool               `json:"httpOnly,omitempty"`
	SameSite       string              `json:"sameSite,omitempty"`
	ExpirationDate *float64            `json:"expirationDate,omitempty"`
	StoreID        string              `json:"storeId,omitempty"`
	PartitionKey   *cookiePartitionKey `json:"partitionKey,omitempty"`
}

type cookieRemoveArgs struct {
	cookieTargetArgs
	URL          string              `json:"url"`
	Name         string              `json:"name"`
	StoreID      string              `json:"storeId,omitempty"`
	PartitionKey *cookiePartitionKey `json:"partitionKey,omitempty"`
}

type cookieWire struct {
	Name           string              `json:"name"`
	Value          string              `json:"value"`
	ValueIncluded  bool                `json:"valueIncluded"`
	ValueLength    int                 `json:"valueLength"`
	Domain         string              `json:"domain"`
	HostOnly       bool                `json:"hostOnly"`
	Path           string              `json:"path"`
	Secure         bool                `json:"secure"`
	HTTPOnly       bool                `json:"httpOnly"`
	SameSite       string              `json:"sameSite"`
	Session        bool                `json:"session"`
	ExpirationDate *float64            `json:"expirationDate,omitempty"`
	StoreID        string              `json:"storeId"`
	PartitionKey   *cookiePartitionKey `json:"partitionKey,omitempty"`
}

type cookieWireResult struct {
	Kind           string       `json:"kind"`
	TabID          int          `json:"tabId"`
	DocumentID     string       `json:"documentId"`
	Origin         string       `json:"origin"`
	ValuesIncluded bool         `json:"valuesIncluded"`
	Cookies        []cookieWire `json:"cookies"`
	TotalMatched   int          `json:"totalMatched"`
	NextCursor     string       `json:"nextCursor"`
	Removed        bool         `json:"removed"`
	Warnings       []string     `json:"warnings"`
}

func (s *Service) registerCookieTools(mcpServer *server.MCPServer) {
	partitionKey := mcp.WithObject(
		"partitionKey",
		mcp.Description("CHIPS partition key, restricted to the selected root-document origin"),
		mcp.Properties(map[string]any{
			"topLevelSite":         map[string]any{"type": "string", "maxLength": 8192},
			"hasCrossSiteAncestor": map[string]any{"type": "boolean"},
		}),
		requiredObjectProperties("topLevelSite"),
		mcp.AdditionalProperties(false),
	)
	base := func(description string, extra ...mcp.ToolOption) []mcp.ToolOption {
		options := []mcp.ToolOption{mcp.WithDescription(description), optionalBrowserID(), optionalTabID(), optionalDocumentID()}
		options = append(options, extra...)
		options = append(options, optionalTimeout())
		return options
	}
	mcpServer.AddTool(
		mcp.NewTool("browser_list_cookies", base(
			"List bounded exact-origin cookie metadata with values masked by default",
			mcp.WithString("url", mcp.Required(), mcp.Description("HTTP(S) URL on the selected tab origin"), mcp.MaxLength(8192)),
			mcp.WithString("domain", mcp.Description("Cookie domain filter"), mcp.MaxLength(maxCookieDomainBytes)),
			mcp.WithString("name", mcp.Description("Cookie name filter"), mcp.MaxLength(maxCookieNameBytes)),
			mcp.WithString("path", mcp.Description("Cookie path filter"), mcp.MaxLength(maxCookiePathBytes)),
			mcp.WithBoolean("secure", mcp.Description("Filter by Secure attribute")),
			mcp.WithBoolean("session", mcp.Description("Filter session cookies")),
			mcp.WithString("storeId", mcp.Description("Cookie store containing the selected tab"), mcp.MaxLength(maxCookieStoreIDBytes)),
			partitionKey,
			mcp.WithString("cursor", mcp.Description("Positive offset cursor from a previous result")),
			mcp.WithNumber("limit", mcp.Description("Maximum cookies to return"), mcp.Min(1), mcp.Max(maxCookieLimit), mcp.DefaultNumber(defaultCookieLimit)),
			mcp.WithBoolean("includeValues", mcp.Description("Return values only when Sensitive data is enabled"), mcp.DefaultBool(false)),
		)...),
		mcp.NewTypedToolHandler(s.browserListCookiesHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool("browser_get_cookie", base(
			"Get one exact-origin cookie with its value masked by default",
			mcp.WithString("url", mcp.Required(), mcp.MaxLength(8192)),
			mcp.WithString("name", mcp.Required(), mcp.MaxLength(maxCookieNameBytes)),
			mcp.WithString("storeId", mcp.MaxLength(maxCookieStoreIDBytes)), partitionKey,
			mcp.WithBoolean("includeValue", mcp.Description("Return the value only when Sensitive data is enabled"), mcp.DefaultBool(false)),
		)...),
		mcp.NewTypedToolHandler(s.browserGetCookieHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool("browser_set_cookie", base(
			"Set one exact-origin cookie without echoing its supplied value",
			mcp.WithString("url", mcp.Required(), mcp.MaxLength(8192)),
			mcp.WithString("name", mcp.Required(), mcp.MaxLength(maxCookieNameBytes)),
			mcp.WithString("value", mcp.Required(), mcp.MaxLength(maxCookieValueBytes)),
			mcp.WithString("domain", mcp.MaxLength(maxCookieDomainBytes)),
			mcp.WithString("path", mcp.MaxLength(maxCookiePathBytes)),
			mcp.WithBoolean("secure"), mcp.WithBoolean("httpOnly"),
			mcp.WithString("sameSite", mcp.Enum("no_restriction", "lax", "strict", "unspecified")),
			mcp.WithNumber("expirationDate", mcp.Description("Unix timestamp in seconds; omit for a session cookie"), mcp.Min(1)),
			mcp.WithString("storeId", mcp.MaxLength(maxCookieStoreIDBytes)), partitionKey,
		)...),
		mcp.NewTypedToolHandler(s.browserSetCookieHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool("browser_remove_cookie", base(
			"Remove one exact-origin cookie",
			mcp.WithString("url", mcp.Required(), mcp.MaxLength(8192)),
			mcp.WithString("name", mcp.Required(), mcp.MaxLength(maxCookieNameBytes)),
			mcp.WithString("storeId", mcp.MaxLength(maxCookieStoreIDBytes)), partitionKey,
		)...),
		mcp.NewTypedToolHandler(s.browserRemoveCookieHandler),
	)
}

func (s *Service) browserListCookiesHandler(ctx context.Context, _ mcp.CallToolRequest, args cookieListArgs) (*mcp.CallToolResult, error) {
	params, err := validateCookieListArgs(args)
	if err != nil {
		return errorResult(err)
	}
	command := protocol.CommandCookiesList
	includeValues := args.IncludeValues != nil && *args.IncludeValues
	if includeValues {
		command = protocol.CommandCookiesListSensitive
	}
	return s.sendCookieCommand(ctx, "browser_list_cookies", command, args.cookieTargetArgs, params, includeValues)
}

func (s *Service) browserGetCookieHandler(ctx context.Context, _ mcp.CallToolRequest, args cookieGetArgs) (*mcp.CallToolResult, error) {
	params, err := validateCookieIdentity(args.URL, args.Name, args.StoreID, args.PartitionKey)
	if err != nil {
		return errorResult(err)
	}
	command := protocol.CommandCookiesGet
	includeValue := args.IncludeValue != nil && *args.IncludeValue
	if includeValue {
		command = protocol.CommandCookiesGetSensitive
	}
	return s.sendCookieCommand(ctx, "browser_get_cookie", command, args.cookieTargetArgs, params, includeValue)
}

func (s *Service) browserSetCookieHandler(ctx context.Context, _ mcp.CallToolRequest, args cookieSetArgs) (*mcp.CallToolResult, error) {
	params, err := validateCookieSetArgs(args)
	if err != nil {
		return errorResult(err)
	}
	return s.sendCookieCommand(ctx, "browser_set_cookie", protocol.CommandCookiesSet, args.cookieTargetArgs, params, false)
}

func (s *Service) browserRemoveCookieHandler(ctx context.Context, _ mcp.CallToolRequest, args cookieRemoveArgs) (*mcp.CallToolResult, error) {
	params, err := validateCookieIdentity(args.URL, args.Name, args.StoreID, args.PartitionKey)
	if err != nil {
		return errorResult(err)
	}
	return s.sendCookieCommand(ctx, "browser_remove_cookie", protocol.CommandCookiesRemove, args.cookieTargetArgs, params, false)
}

func (s *Service) sendCookieCommand(ctx context.Context, operation, command string, args cookieTargetArgs, params map[string]any, includeValues bool) (*mcp.CallToolResult, error) {
	startedAt := time.Now()
	rootFrameID := 0
	browserID, target, raw, duration, err := s.sendRaw(ctx, args.BrowserID, command, pageTarget(args.TabID, &rootFrameID, args.DocumentID), params, args.TimeoutMS)
	if err != nil {
		s.auditCookie(operation, browserID, target, includeValues, protocol.ErrorFrom(err).Code, 0, duration)
		return errorResultWithDuration(err, duration)
	}
	result, err := decodeCookieResult(raw, command, target)
	if err != nil {
		s.auditCookie(operation, browserID, target, includeValues, protocol.ErrorFrom(err).Code, 0, duration)
		return errorResultWithDuration(err, duration)
	}
	if target == nil {
		frameID := 0
		target = &protocol.Target{BrowserID: browserID, TabID: &result.TabID, FrameID: &frameID, DocumentID: result.DocumentID}
	} else if target.DocumentID == "" {
		target.DocumentID = result.DocumentID
	}
	s.auditCookie(operation, browserID, target, includeValues, "OK", len(result.Cookies), time.Since(startedAt))
	return successResultWithTargetWarningsLimited(browserID, target, result, duration, nil, s.resultLimits.MaxOutputBytes)
}

func validateCookieListArgs(args cookieListArgs) (map[string]any, error) {
	parsed, err := validateCookieURL(args.URL)
	if err != nil {
		return nil, err
	}
	limit := defaultCookieLimit
	assignInt(&limit, args.Limit)
	if limit < 1 || limit > maxCookieLimit {
		return nil, invalidCookie("limit is outside the supported range")
	}
	if args.Cursor != "" {
		value, parseErr := strconv.ParseUint(args.Cursor, 10, 53)
		if parseErr != nil || value == 0 {
			return nil, invalidCookie("cursor must be a positive safe integer string")
		}
	}
	domain, err := validateCookieDomain(args.Domain, parsed.Hostname(), true)
	if err != nil {
		return nil, err
	}
	if args.Name != "" && !validCookieName(args.Name) {
		return nil, invalidCookie("name is invalid")
	}
	if args.Path != "" && !validCookiePath(args.Path) {
		return nil, invalidCookie("path is invalid")
	}
	if !validCookieStoreID(args.StoreID) {
		return nil, invalidCookie("storeId is invalid")
	}
	partition, err := normalizeCookiePartition(args.PartitionKey, parsed)
	if err != nil {
		return nil, err
	}
	params := map[string]any{"url": parsed.String(), "limit": limit}
	if domain != "" {
		params["domain"] = domain
	}
	if args.Name != "" {
		params["name"] = args.Name
	}
	if args.Path != "" {
		params["path"] = args.Path
	}
	if args.StoreID != "" {
		params["storeId"] = args.StoreID
	}
	if args.Cursor != "" {
		params["cursor"] = args.Cursor
	}
	putOptional(params, "secure", args.Secure)
	putOptional(params, "session", args.Session)
	if partition != nil {
		params["partitionKey"] = partition
	}
	return params, nil
}

func validateCookieIdentity(rawURL, name, storeID string, partition *cookiePartitionKey) (map[string]any, error) {
	parsed, err := validateCookieURL(rawURL)
	if err != nil {
		return nil, err
	}
	if !validCookieName(name) {
		return nil, invalidCookie("name is invalid")
	}
	if !validCookieStoreID(storeID) {
		return nil, invalidCookie("storeId is invalid")
	}
	normalizedPartition, err := normalizeCookiePartition(partition, parsed)
	if err != nil {
		return nil, err
	}
	params := map[string]any{"url": parsed.String(), "name": name}
	if storeID != "" {
		params["storeId"] = storeID
	}
	if normalizedPartition != nil {
		params["partitionKey"] = normalizedPartition
	}
	return params, nil
}

func validateCookieSetArgs(args cookieSetArgs) (map[string]any, error) {
	params, err := validateCookieIdentity(args.URL, args.Name, args.StoreID, args.PartitionKey)
	if err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(params["url"].(string))
	domain, err := validateCookieDomain(args.Domain, parsed.Hostname(), true)
	if err != nil {
		return nil, err
	}
	if !utf8.ValidString(args.Value) || len(args.Value) > maxCookieValueBytes || containsCookieControl(args.Value) || strings.Contains(args.Value, ";") {
		return nil, invalidCookie("value is invalid or exceeds 4096 bytes")
	}
	if args.Path != "" && !validCookiePath(args.Path) {
		return nil, invalidCookie("path is invalid")
	}
	if args.SameSite != "" && !performanceStringAllowed([]string{"no_restriction", "lax", "strict", "unspecified"}, args.SameSite) {
		return nil, invalidCookie("sameSite is unsupported")
	}
	if args.SameSite == "no_restriction" && (args.Secure == nil || !*args.Secure) {
		return nil, invalidCookie("SameSite=None requires secure=true")
	}
	if args.ExpirationDate != nil && (!isFinite(*args.ExpirationDate) || *args.ExpirationDate <= 0) {
		return nil, invalidCookie("expirationDate must be a positive finite Unix timestamp")
	}
	params["value"] = args.Value
	if domain != "" {
		params["domain"] = domain
	}
	if args.Path != "" {
		params["path"] = args.Path
	}
	putOptional(params, "secure", args.Secure)
	putOptional(params, "httpOnly", args.HTTPOnly)
	if args.SameSite != "" {
		params["sameSite"] = args.SameSite
	}
	putOptional(params, "expirationDate", args.ExpirationDate)
	return params, nil
}

func validateCookieURL(value string) (*url.URL, error) {
	if len(value) == 0 || len(value) > 8192 || strings.TrimSpace(value) != value {
		return nil, invalidCookie("url is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, invalidCookie("url must be an HTTP(S) URL without credentials or a fragment")
	}
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed, nil
}

func validateCookieDomain(value, urlHost string, allowEmpty bool) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" && allowEmpty {
		return "", nil
	}
	host := strings.TrimPrefix(value, ".")
	if len(value) > maxCookieDomainBytes || !validASCIIHostname(host) ||
		urlHost != host && !strings.HasSuffix(urlHost, "."+host) {
		return "", invalidCookie("domain must contain the URL host or one of its parent domains")
	}
	return value, nil
}

func normalizeCookiePartition(value *cookiePartitionKey, targetURL *url.URL) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	parsed, err := validateCookieURL(value.TopLevelSite)
	if err != nil || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || cookieURLOrigin(parsed) != cookieURLOrigin(targetURL) {
		return nil, invalidCookie("partitionKey.topLevelSite must exactly match the selected URL origin")
	}
	result := map[string]any{"topLevelSite": cookieURLOrigin(parsed)}
	if value.HasCrossSiteAncestor != nil {
		result["hasCrossSiteAncestor"] = *value.HasCrossSiteAncestor
	}
	return result, nil
}

func decodeCookieResult(raw json.RawMessage, command string, target *protocol.Target) (cookieWireResult, error) {
	var result cookieWireResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&result)
	var trailing any
	if decodeErr == nil && decoder.Decode(&trailing) != io.EOF {
		decodeErr = invalidCookieResult()
	}
	expectedKind := map[string]string{
		protocol.CommandCookiesList: "list", protocol.CommandCookiesListSensitive: "list",
		protocol.CommandCookiesGet: "get", protocol.CommandCookiesGetSensitive: "get",
		protocol.CommandCookiesSet: "set", protocol.CommandCookiesRemove: "remove",
	}[command]
	wantsValues := command == protocol.CommandCookiesListSensitive || command == protocol.CommandCookiesGetSensitive
	if len(raw) > maxCookieResultBytes || decodeErr != nil || expectedKind == "" || result.Kind != expectedKind ||
		result.TabID < 0 || strings.TrimSpace(result.DocumentID) == "" || len(result.DocumentID) > maxCookieDocumentBytes ||
		!networkTargetMatches(target, result.TabID, result.DocumentID) || result.ValuesIncluded != wantsValues ||
		len(result.Cookies) > maxCookieLimit || result.TotalMatched < len(result.Cookies) || result.TotalMatched > 100_000 ||
		len(result.Warnings) > maxCookieWarnings || !validCookieOrigin(result.Origin) {
		return result, invalidCookieResult()
	}
	if result.NextCursor != "" {
		cursor, err := strconv.ParseUint(result.NextCursor, 10, 53)
		if err != nil || cursor == 0 || expectedKind != "list" {
			return result, invalidCookieResult()
		}
	}
	if expectedKind != "list" && result.NextCursor != "" || expectedKind == "remove" && (len(result.Cookies) != 0 || result.TotalMatched != 0) || expectedKind != "remove" && result.Removed {
		return result, invalidCookieResult()
	}
	for _, warning := range result.Warnings {
		if strings.TrimSpace(warning) == "" || len(warning) > 256 {
			return result, invalidCookieResult()
		}
	}
	for _, cookie := range result.Cookies {
		if !validCookieWire(cookie, wantsValues, result.Origin) {
			return result, invalidCookieResult()
		}
	}
	return result, nil
}

func validCookieWire(cookie cookieWire, wantsValues bool, origin string) bool {
	parsed, _ := url.Parse(origin)
	if _, err := validateCookieDomain(cookie.Domain, parsed.Hostname(), false); err != nil {
		return false
	}
	if !validCookieName(cookie.Name) || cookie.ValueLength < 0 || cookie.ValueLength > maxCookieResultBytes ||
		!validCookiePath(cookie.Path) || !validCookieStoreID(cookie.StoreID) ||
		!performanceStringAllowed([]string{"no_restriction", "lax", "strict", "unspecified"}, cookie.SameSite) ||
		cookie.Session != (cookie.ExpirationDate == nil) || cookie.ExpirationDate != nil && (!isFinite(*cookie.ExpirationDate) || *cookie.ExpirationDate <= 0) {
		return false
	}
	if cookie.PartitionKey != nil {
		targetURL, _ := url.Parse(origin)
		if _, err := normalizeCookiePartition(cookie.PartitionKey, targetURL); err != nil {
			return false
		}
	}
	if !wantsValues {
		return !cookie.ValueIncluded && cookie.Value == maskedCookieValue
	}
	if cookie.ValueIncluded {
		return utf8.ValidString(cookie.Value) && len(cookie.Value) == cookie.ValueLength && len(cookie.Value) <= maxCookieValueBytes && !containsCookieControl(cookie.Value)
	}
	return cookie.Value == omittedCookieValue
}

func validCookieName(value string) bool {
	return len(value) > 0 && len(value) <= maxCookieNameBytes && cookieNamePattern.MatchString(value)
}

func validCookiePath(value string) bool {
	return len(value) > 0 && len(value) <= maxCookiePathBytes && strings.HasPrefix(value, "/") && utf8.ValidString(value) && !containsCookieControl(value) && !strings.Contains(value, ";")
}

func validCookieStoreID(value string) bool {
	return len(value) <= maxCookieStoreIDBytes && utf8.ValidString(value) && !containsCookieControl(value)
}

func validASCIIHostname(value string) bool {
	if value == "" || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func validCookieOrigin(value string) bool {
	parsed, err := validateCookieURL(value)
	return err == nil && parsed.String() == cookieURLOrigin(parsed)
}

func cookieURLOrigin(value *url.URL) string {
	return value.Scheme + "://" + value.Host
}

func containsCookieControl(value string) bool {
	return strings.ContainsFunc(value, func(character rune) bool { return character < 32 || character == 127 })
}

func isFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func isDedicatedCookieCommand(command string) bool {
	return performanceStringAllowed([]string{
		protocol.CommandCookiesList, protocol.CommandCookiesListSensitive,
		protocol.CommandCookiesGet, protocol.CommandCookiesGetSensitive,
		protocol.CommandCookiesSet, protocol.CommandCookiesRemove,
	}, command)
}

func (s *Service) auditCookie(operation, browserID string, target *protocol.Target, includeValues bool, outcome any, count int, duration time.Duration) {
	if s.auditLogger == nil {
		return
	}
	if !performanceStringAllowed([]string{"browser_list_cookies", "browser_get_cookie", "browser_set_cookie", "browser_remove_cookie"}, operation) {
		operation = "invalid"
	}
	tabID := -1
	if target != nil && target.TabID != nil {
		tabID = *target.TabID
	}
	s.auditLogger.Printf("operation=cookies tool=%q browserId=%q tabId=%d valuesIncluded=%t count=%d outcome=%s duration=%s", operation, boundedRawCDPAudit(browserID), tabID, includeValues, count, fmt.Sprint(outcome), duration.Round(time.Microsecond))
}

func invalidCookie(message string) *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}

func invalidCookieResult() *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, "the browser returned an invalid cookie result", false)
}
