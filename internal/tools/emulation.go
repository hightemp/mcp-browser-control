package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	maxEmulationViewportPixels = 10_000
	maxEmulationLatencyMS      = 300_000
	maxEmulationKbps           = 10_000_000
	maxEmulationString         = 1_000
	maxEmulationWarnings       = 10
)

var (
	emulationLocalePattern   = regexp.MustCompile(`^[A-Za-z]{2,8}(?:[_-][A-Za-z0-9]{1,8})*$`)
	emulationTimezonePattern = regexp.MustCompile(`^[A-Za-z0-9_+./-]+$`)
	emulationControlPattern  = regexp.MustCompile(`[\x00-\x1f\x7f]`)
)

type emulationSetArgs struct {
	BrowserID   string                `json:"browserId,omitempty"`
	TabID       *int                  `json:"tabId,omitempty"`
	DocumentID  string                `json:"documentId,omitempty"`
	Viewport    *emulationViewport    `json:"viewport,omitempty"`
	Touch       *emulationTouch       `json:"touch,omitempty"`
	Network     *emulationNetwork     `json:"network,omitempty"`
	UserAgent   *emulationUserAgent   `json:"userAgent,omitempty"`
	Locale      *string               `json:"locale,omitempty"`
	TimezoneID  *string               `json:"timezoneId,omitempty"`
	Geolocation *emulationGeolocation `json:"geolocation,omitempty"`
	Media       *emulationMedia       `json:"media,omitempty"`
	TimeoutMS   *int                  `json:"timeoutMs,omitempty"`
}

type emulationTargetArgs struct {
	BrowserID  string `json:"browserId,omitempty"`
	TabID      *int   `json:"tabId,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
	TimeoutMS  *int   `json:"timeoutMs,omitempty"`
}

type emulationConfig struct {
	Viewport    *emulationViewport    `json:"viewport,omitempty"`
	Touch       *emulationTouch       `json:"touch,omitempty"`
	Network     *emulationNetwork     `json:"network,omitempty"`
	UserAgent   *emulationUserAgent   `json:"userAgent,omitempty"`
	Locale      *string               `json:"locale,omitempty"`
	TimezoneID  *string               `json:"timezoneId,omitempty"`
	Geolocation *emulationGeolocation `json:"geolocation,omitempty"`
	Media       *emulationMedia       `json:"media,omitempty"`
}

type emulationViewport struct {
	Width             int     `json:"width"`
	Height            int     `json:"height"`
	DeviceScaleFactor float64 `json:"deviceScaleFactor"`
	Mobile            bool    `json:"mobile"`
	Orientation       string  `json:"orientation,omitempty"`
}

type emulationTouch struct {
	Enabled        bool `json:"enabled"`
	MaxTouchPoints *int `json:"maxTouchPoints,omitempty"`
}

type emulationNetwork struct {
	Offline        bool     `json:"offline,omitempty"`
	LatencyMS      *float64 `json:"latencyMs,omitempty"`
	DownloadKbps   *float64 `json:"downloadKbps,omitempty"`
	UploadKbps     *float64 `json:"uploadKbps,omitempty"`
	ConnectionType string   `json:"connectionType,omitempty"`
}

type emulationUserAgent struct {
	Value          string `json:"value"`
	AcceptLanguage string `json:"acceptLanguage,omitempty"`
	Platform       string `json:"platform,omitempty"`
}

type emulationGeolocation struct {
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	Accuracy  float64  `json:"accuracy"`
	Altitude  *float64 `json:"altitude,omitempty"`
	Heading   *float64 `json:"heading,omitempty"`
	Speed     *float64 `json:"speed,omitempty"`
}

type emulationMedia struct {
	Type          string `json:"type,omitempty"`
	ColorScheme   string `json:"colorScheme,omitempty"`
	ReducedMotion string `json:"reducedMotion,omitempty"`
	ForcedColors  string `json:"forcedColors,omitempty"`
	Contrast      string `json:"contrast,omitempty"`
}

type emulationWireResult struct {
	Active        bool             `json:"active"`
	TabID         int              `json:"tabId"`
	DocumentID    string           `json:"documentId"`
	Settings      *emulationConfig `json:"settings,omitempty"`
	Applied       []string         `json:"applied"`
	ResetOnDetach bool             `json:"resetOnDetach"`
	Warnings      []string         `json:"warnings"`
}

func (s *Service) registerEmulationTools(mcpServer *server.MCPServer) {
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_set_emulation",
			mcp.WithDescription("Replace all managed emulation overrides for one browser tab"),
			optionalBrowserID(), optionalTabID(), optionalDocumentID(),
			emulationViewportOption(), emulationTouchOption(), emulationNetworkOption(),
			emulationUserAgentOption(),
			mcp.WithString("locale", mcp.Description("ICU-style locale such as en_US"), mcp.MinLength(2), mcp.MaxLength(100)),
			mcp.WithString("timezoneId", mcp.Description("IANA timezone such as America/New_York"), mcp.MinLength(1), mcp.MaxLength(100)),
			emulationGeolocationOption(), emulationMediaOption(), optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserSetEmulationHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_get_emulation_state",
			mcp.WithDescription("Get the managed emulation state for one browser tab"),
			optionalBrowserID(), optionalTabID(), optionalDocumentID(), optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserGetEmulationHandler),
	)
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_reset_emulation",
			mcp.WithDescription("Clear every managed emulation override for one browser tab"),
			optionalBrowserID(), optionalTabID(), optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserResetEmulationHandler),
	)
}

func (s *Service) browserSetEmulationHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args emulationSetArgs,
) (*mcp.CallToolResult, error) {
	config, err := normalizeEmulationConfig(emulationConfig{
		Viewport: args.Viewport, Touch: args.Touch, Network: args.Network,
		UserAgent: args.UserAgent, Locale: args.Locale, TimezoneID: args.TimezoneID,
		Geolocation: args.Geolocation, Media: args.Media,
	})
	if err != nil {
		return errorResult(err)
	}
	return s.sendEmulation(ctx, args.BrowserID, args.TabID, args.DocumentID, args.TimeoutMS, protocol.CommandEmulationSet, config)
}

func (s *Service) browserGetEmulationHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args emulationTargetArgs,
) (*mcp.CallToolResult, error) {
	return s.sendEmulation(ctx, args.BrowserID, args.TabID, args.DocumentID, args.TimeoutMS, protocol.CommandEmulationGet, nil)
}

func (s *Service) browserResetEmulationHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args emulationTargetArgs,
) (*mcp.CallToolResult, error) {
	return s.sendEmulation(ctx, args.BrowserID, args.TabID, "", args.TimeoutMS, protocol.CommandEmulationReset, nil)
}

func (s *Service) sendEmulation(
	ctx context.Context,
	browserID string,
	tabID *int,
	documentID string,
	timeoutMS *int,
	command string,
	expected *emulationConfig,
) (*mcp.CallToolResult, error) {
	rootFrameID := 0
	params := any(map[string]any{})
	if expected != nil {
		params = expected
	}
	resolvedBrowserID, target, raw, duration, err := s.sendRaw(
		ctx, browserID, command, pageTarget(tabID, &rootFrameID, documentID), params, timeoutMS,
	)
	if err != nil {
		return errorResultWithDuration(err, duration)
	}
	result, err := decodeEmulationResult(raw, command, expected)
	if err != nil {
		return errorResultWithDuration(err, duration)
	}
	if target == nil || target.TabID == nil || *target.TabID != result.TabID ||
		(target.DocumentID != "" && target.DocumentID != result.DocumentID) {
		return errorResultWithDuration(invalidEmulationResult(), duration)
	}
	if target.DocumentID == "" {
		target.DocumentID = result.DocumentID
	}
	sanitized, report, err := s.sanitizeBrowserResult(raw)
	if err != nil {
		return errorResultWithDuration(err, duration)
	}
	return successResultWithTargetWarningsLimited(
		resolvedBrowserID, target, sanitized, duration, report.Warnings(), s.resultLimits.MaxOutputBytes,
	)
}

func normalizeEmulationConfig(input emulationConfig) (*emulationConfig, error) {
	config := input
	if config.Viewport == nil && config.Touch == nil && config.Network == nil &&
		config.UserAgent == nil && config.Locale == nil && config.TimezoneID == nil &&
		config.Geolocation == nil && config.Media == nil {
		return nil, invalidEmulation("at least one emulation setting is required")
	}
	if viewport := config.Viewport; viewport != nil {
		viewport.Orientation = strings.TrimSpace(viewport.Orientation)
		if viewport.Width < 1 || viewport.Width > maxEmulationViewportPixels ||
			viewport.Height < 1 || viewport.Height > maxEmulationViewportPixels ||
			!finiteRange(viewport.DeviceScaleFactor, 0.1, 10) ||
			!allowedString(viewport.Orientation, "", "portraitPrimary", "portraitSecondary", "landscapePrimary", "landscapeSecondary") {
			return nil, invalidEmulation("viewport contains an invalid dimension, scale, or orientation")
		}
	}
	if touch := config.Touch; touch != nil {
		if touch.MaxTouchPoints != nil && (!touch.Enabled || *touch.MaxTouchPoints < 1 || *touch.MaxTouchPoints > 10) {
			return nil, invalidEmulation("maxTouchPoints requires enabled touch and must be between 1 and 10")
		}
	}
	if network := config.Network; network != nil {
		network.ConnectionType = strings.TrimSpace(network.ConnectionType)
		if !optionalFiniteRange(network.LatencyMS, 0, maxEmulationLatencyMS) ||
			!optionalFiniteRange(network.DownloadKbps, 0, maxEmulationKbps) ||
			!optionalFiniteRange(network.UploadKbps, 0, maxEmulationKbps) ||
			!allowedString(network.ConnectionType, "", "none", "cellular2g", "cellular3g", "cellular4g", "bluetooth", "ethernet", "wifi", "wimax", "other") {
			return nil, invalidEmulation("network contains an invalid bound or connectionType")
		}
	}
	if userAgent := config.UserAgent; userAgent != nil {
		userAgent.Value = strings.TrimSpace(userAgent.Value)
		userAgent.AcceptLanguage = strings.TrimSpace(userAgent.AcceptLanguage)
		userAgent.Platform = strings.TrimSpace(userAgent.Platform)
		if !safeEmulationString(userAgent.Value, 1, maxEmulationString) ||
			!safeEmulationString(userAgent.AcceptLanguage, 0, 200) ||
			!safeEmulationString(userAgent.Platform, 0, 100) {
			return nil, invalidEmulation("userAgent contains an invalid or unsafe string")
		}
	}
	if config.Locale != nil {
		value := strings.TrimSpace(*config.Locale)
		if len(value) > 100 || !emulationLocalePattern.MatchString(value) {
			return nil, invalidEmulation("locale must be a bounded ICU-style locale")
		}
		config.Locale = &value
	}
	if config.TimezoneID != nil {
		value := strings.TrimSpace(*config.TimezoneID)
		if len(value) > 100 || !emulationTimezonePattern.MatchString(value) {
			return nil, invalidEmulation("timezoneId must be a bounded IANA timezone")
		}
		config.TimezoneID = &value
	}
	if location := config.Geolocation; location != nil {
		if !finiteRange(location.Latitude, -90, 90) || !finiteRange(location.Longitude, -180, 180) ||
			!finiteRange(location.Accuracy, 0, 1_000_000) ||
			!optionalFiniteRange(location.Altitude, -10_000, 100_000) ||
			!optionalFiniteRange(location.Heading, 0, 360) ||
			!optionalFiniteRange(location.Speed, 0, 1_000_000) {
			return nil, invalidEmulation("geolocation contains an invalid coordinate or bound")
		}
	}
	if media := config.Media; media != nil {
		media.Type = strings.ToLower(strings.TrimSpace(media.Type))
		media.ColorScheme = strings.ToLower(strings.TrimSpace(media.ColorScheme))
		media.ReducedMotion = strings.ToLower(strings.TrimSpace(media.ReducedMotion))
		media.ForcedColors = strings.ToLower(strings.TrimSpace(media.ForcedColors))
		media.Contrast = strings.ToLower(strings.TrimSpace(media.Contrast))
		if media.Type == "" && media.ColorScheme == "" && media.ReducedMotion == "" && media.ForcedColors == "" && media.Contrast == "" {
			return nil, invalidEmulation("media requires at least one override")
		}
		if !allowedString(media.Type, "", "screen", "print") ||
			!allowedString(media.ColorScheme, "", "light", "dark", "no-preference") ||
			!allowedString(media.ReducedMotion, "", "reduce", "no-preference") ||
			!allowedString(media.ForcedColors, "", "active", "none") ||
			!allowedString(media.Contrast, "", "more", "less", "custom", "no-preference") {
			return nil, invalidEmulation("media contains an unsupported type or feature value")
		}
	}
	return &config, nil
}

func decodeEmulationResult(raw json.RawMessage, command string, expected *emulationConfig) (emulationWireResult, error) {
	var result emulationWireResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&result)
	var trailing any
	if decodeErr == nil && decoder.Decode(&trailing) != io.EOF {
		decodeErr = invalidEmulationResult()
	}
	if len(raw) > 64*1024 || decodeErr != nil || result.TabID < 0 ||
		len(result.DocumentID) > 256 ||
		len(result.Warnings) > maxEmulationWarnings {
		return result, invalidEmulationResult()
	}
	if command != protocol.CommandEmulationReset && strings.TrimSpace(result.DocumentID) == "" {
		return result, invalidEmulationResult()
	}
	for _, warning := range result.Warnings {
		if strings.TrimSpace(warning) == "" || len(warning) > maxEmulationString {
			return result, invalidEmulationResult()
		}
	}
	if command == protocol.CommandEmulationReset {
		if result.Active || result.Settings != nil || len(result.Applied) != 0 || !result.ResetOnDetach {
			return result, invalidEmulationResult()
		}
		return result, nil
	}
	if !result.Active {
		if command == protocol.CommandEmulationSet || result.Settings != nil || len(result.Applied) != 0 {
			return result, invalidEmulationResult()
		}
		return result, nil
	}
	if !result.ResetOnDetach || result.Settings == nil {
		return result, invalidEmulationResult()
	}
	originalSettings, err := json.Marshal(result.Settings)
	if err != nil {
		return result, invalidEmulationResult()
	}
	normalized, err := normalizeEmulationConfig(*result.Settings)
	normalizedSettings, marshalErr := json.Marshal(normalized)
	if err != nil || marshalErr != nil || !bytes.Equal(originalSettings, normalizedSettings) ||
		!reflect.DeepEqual(result.Applied, emulationApplied(*normalized)) {
		return result, invalidEmulationResult()
	}
	if command == protocol.CommandEmulationSet && !reflect.DeepEqual(normalized, expected) {
		return result, invalidEmulationResult()
	}
	return result, nil
}

func emulationApplied(config emulationConfig) []string {
	applied := make([]string, 0, 8)
	for _, setting := range []struct {
		name    string
		present bool
	}{
		{"viewport", config.Viewport != nil}, {"touch", config.Touch != nil},
		{"network", config.Network != nil}, {"userAgent", config.UserAgent != nil},
		{"locale", config.Locale != nil}, {"timezoneId", config.TimezoneID != nil},
		{"geolocation", config.Geolocation != nil}, {"media", config.Media != nil},
	} {
		if setting.present {
			applied = append(applied, setting.name)
		}
	}
	sort.Strings(applied)
	return applied
}

func finiteRange(value, minimum, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= minimum && value <= maximum
}

func optionalFiniteRange(value *float64, minimum, maximum float64) bool {
	return value == nil || finiteRange(*value, minimum, maximum)
}

func allowedString(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func safeEmulationString(value string, minimum, maximum int) bool {
	return len(value) >= minimum && len(value) <= maximum && !emulationControlPattern.MatchString(value)
}

func invalidEmulation(message string) *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}

func invalidEmulationResult() *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, "the browser returned an invalid emulation result", false)
}

func emulationViewportOption() mcp.ToolOption {
	return mcp.WithObject("viewport", mcp.Description("Viewport and mobile device metrics"),
		mcp.Properties(map[string]any{
			"width":             map[string]any{"type": "integer", "minimum": 1, "maximum": maxEmulationViewportPixels},
			"height":            map[string]any{"type": "integer", "minimum": 1, "maximum": maxEmulationViewportPixels},
			"deviceScaleFactor": map[string]any{"type": "number", "minimum": 0.1, "maximum": 10},
			"mobile":            map[string]any{"type": "boolean"},
			"orientation":       map[string]any{"type": "string", "enum": []string{"portraitPrimary", "portraitSecondary", "landscapePrimary", "landscapeSecondary"}},
		}), requiredObjectProperties("width", "height", "deviceScaleFactor", "mobile"), mcp.AdditionalProperties(false))
}

func emulationTouchOption() mcp.ToolOption {
	return mcp.WithObject("touch", mcp.Description("Touch-input emulation"),
		mcp.Properties(map[string]any{
			"enabled":        map[string]any{"type": "boolean"},
			"maxTouchPoints": map[string]any{"type": "integer", "minimum": 1, "maximum": 10},
		}), requiredObjectProperties("enabled"), mcp.AdditionalProperties(false))
}

func emulationNetworkOption() mcp.ToolOption {
	return mcp.WithObject("network", mcp.Description("Offline and network-throttling emulation"),
		mcp.Properties(map[string]any{
			"offline":        map[string]any{"type": "boolean"},
			"latencyMs":      map[string]any{"type": "number", "minimum": 0, "maximum": maxEmulationLatencyMS},
			"downloadKbps":   map[string]any{"type": "number", "minimum": 0, "maximum": maxEmulationKbps},
			"uploadKbps":     map[string]any{"type": "number", "minimum": 0, "maximum": maxEmulationKbps},
			"connectionType": map[string]any{"type": "string", "enum": []string{"none", "cellular2g", "cellular3g", "cellular4g", "bluetooth", "ethernet", "wifi", "wimax", "other"}},
		}), mcp.MinProperties(1), mcp.AdditionalProperties(false))
}

func emulationUserAgentOption() mcp.ToolOption {
	return mcp.WithObject("userAgent", mcp.Description("User-Agent and navigator platform override"),
		mcp.Properties(map[string]any{
			"value":          map[string]any{"type": "string", "minLength": 1, "maxLength": maxEmulationString},
			"acceptLanguage": map[string]any{"type": "string", "maxLength": 200},
			"platform":       map[string]any{"type": "string", "maxLength": 100},
		}), requiredObjectProperties("value"), mcp.AdditionalProperties(false))
}

func emulationGeolocationOption() mcp.ToolOption {
	return mcp.WithObject("geolocation", mcp.Description("Geolocation override"),
		mcp.Properties(map[string]any{
			"latitude":  map[string]any{"type": "number", "minimum": -90, "maximum": 90},
			"longitude": map[string]any{"type": "number", "minimum": -180, "maximum": 180},
			"accuracy":  map[string]any{"type": "number", "minimum": 0, "maximum": 1_000_000},
			"altitude":  map[string]any{"type": "number", "minimum": -10_000, "maximum": 100_000},
			"heading":   map[string]any{"type": "number", "minimum": 0, "maximum": 360},
			"speed":     map[string]any{"type": "number", "minimum": 0, "maximum": 1_000_000},
		}), requiredObjectProperties("latitude", "longitude", "accuracy"), mcp.AdditionalProperties(false))
}

func emulationMediaOption() mcp.ToolOption {
	return mcp.WithObject("media", mcp.Description("CSS media and preference overrides"),
		mcp.Properties(map[string]any{
			"type":          map[string]any{"type": "string", "enum": []string{"screen", "print"}},
			"colorScheme":   map[string]any{"type": "string", "enum": []string{"light", "dark", "no-preference"}},
			"reducedMotion": map[string]any{"type": "string", "enum": []string{"reduce", "no-preference"}},
			"forcedColors":  map[string]any{"type": "string", "enum": []string{"active", "none"}},
			"contrast":      map[string]any{"type": "string", "enum": []string{"more", "less", "custom", "no-preference"}},
		}), mcp.MinProperties(1), mcp.AdditionalProperties(false))
}
