package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"image/png"
	"strings"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/artifacts"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultScreenshotMaxBytes = 2_000_000
	maxScreenshotDimension    = 16_384
	minScreenshotMaxBytes     = 1_024
)

type screenshotArgs struct {
	BrowserID string `json:"browserId,omitempty"`
	TabID     *int   `json:"tabId,omitempty"`
	Capture   string `json:"capture,omitempty"`
	Format    string `json:"format,omitempty"`
	Quality   *int   `json:"quality,omitempty"`
	MaxWidth  *int   `json:"maxWidth,omitempty"`
	MaxHeight *int   `json:"maxHeight,omitempty"`
	MaxBytes  *int   `json:"maxBytes,omitempty"`
	TimeoutMS *int   `json:"timeoutMs,omitempty"`
}

type screenshotWireResult struct {
	Capture    string   `json:"capture"`
	Format     string   `json:"format"`
	MIMEType   string   `json:"mimeType"`
	DataBase64 string   `json:"dataBase64"`
	ByteLength int      `json:"byteLength"`
	Width      int      `json:"width"`
	Height     int      `json:"height"`
	TabID      int      `json:"tabId"`
	WindowID   int      `json:"windowId"`
	Warnings   []string `json:"warnings"`
}

func (s *Service) registerScreenshotTool(mcpServer *server.MCPServer) {
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_screenshot",
			mcp.WithDescription("Capture the selected tab viewport and store it as a temporary artifact"),
			optionalBrowserID(),
			optionalTabID(),
			mcp.WithString("capture", mcp.Description("Capture area"), mcp.Enum("viewport")),
			mcp.WithString("format", mcp.Description("Image format"), mcp.Enum("png", "jpeg")),
			mcp.WithNumber("quality", mcp.Description("JPEG quality from 0 to 100"), mcp.Min(0), mcp.Max(100)),
			mcp.WithNumber("maxWidth", mcp.Description("Reject wider images"), mcp.Min(1), mcp.Max(maxScreenshotDimension)),
			mcp.WithNumber("maxHeight", mcp.Description("Reject taller images"), mcp.Min(1), mcp.Max(maxScreenshotDimension)),
			mcp.WithNumber("maxBytes", mcp.Description("Reject larger encoded images"), mcp.Min(minScreenshotMaxBytes), mcp.Max(defaultScreenshotMaxBytes)),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserScreenshotHandler),
	)
}

func (s *Service) browserScreenshotHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args screenshotArgs,
) (*mcp.CallToolResult, error) {
	params, limits, err := validateScreenshotArgs(args)
	if err != nil {
		return errorResult(err)
	}
	if s.artifacts == nil {
		return errorResult(protocol.NewError(
			protocol.CodeCapabilityUnavailable,
			"Screenshot artifact storage is unavailable",
			false,
		))
	}
	operationCtx, cancel, err := toolContext(ctx, args.TimeoutMS)
	if err != nil {
		return errorResult(err)
	}
	defer cancel()

	browserID, target, raw, duration, err := s.sendRaw(
		operationCtx,
		args.BrowserID,
		protocol.CommandPageScreenshot,
		targetWithTab(args.TabID),
		params,
		nil,
	)
	if err != nil {
		return errorResultWithDuration(err, duration)
	}
	wireResult, imageData, err := decodeScreenshotResult(raw, limits)
	if err != nil {
		return errorResultWithDuration(err, duration)
	}
	if target != nil && target.TabID != nil && wireResult.TabID != *target.TabID {
		return errorResultWithDuration(invalidScreenshotResult(), duration)
	}
	if target == nil || target.TabID == nil {
		tabID := wireResult.TabID
		target = &protocol.Target{BrowserID: browserID, TabID: &tabID}
	}
	storeStarted := time.Now()
	metadata, err := s.artifacts.Put(
		operationCtx,
		wireResult.MIMEType,
		imageData,
		artifacts.RedactionMetadata{},
	)
	duration += time.Since(storeStarted)
	if err != nil {
		return errorResultWithDuration(fmt.Errorf("store screenshot artifact: %w", err), duration)
	}

	result := map[string]any{
		"artifactUri":         metadata.URI,
		"artifactMetadataUri": metadata.URI + "/metadata",
		"capture":             wireResult.Capture,
		"format":              wireResult.Format,
		"mimeType":            metadata.MIMEType,
		"size":                metadata.Size,
		"width":               wireResult.Width,
		"height":              wireResult.Height,
		"tabId":               wireResult.TabID,
		"windowId":            wireResult.WindowID,
		"expiresAt":           metadata.ExpiresAt,
		"redactionApplied":    metadata.Redaction.Applied,
		"warnings": append(
			append([]string(nil), wireResult.Warnings...),
			"Screenshots may contain sensitive page content; no pixel redaction was applied",
		),
	}
	return successResultWithTarget(browserID, target, result, duration)
}

type screenshotLimits struct {
	format    string
	maxWidth  int
	maxHeight int
	maxBytes  int
}

func validateScreenshotArgs(args screenshotArgs) (map[string]any, screenshotLimits, error) {
	capture := strings.TrimSpace(args.Capture)
	if capture == "" {
		capture = "viewport"
	}
	if capture != "viewport" {
		return nil, screenshotLimits{}, invalidScreenshot("capture must be viewport")
	}
	format := strings.ToLower(strings.TrimSpace(args.Format))
	if format == "" {
		format = "png"
	}
	if format != "png" && format != "jpeg" {
		return nil, screenshotLimits{}, invalidScreenshot("format must be png or jpeg")
	}
	if args.Quality != nil && (format != "jpeg" || *args.Quality < 0 || *args.Quality > 100) {
		return nil, screenshotLimits{}, invalidScreenshot("quality is only valid for JPEG and must be between 0 and 100")
	}

	limits := screenshotLimits{
		format:    format,
		maxWidth:  maxScreenshotDimension,
		maxHeight: maxScreenshotDimension,
		maxBytes:  defaultScreenshotMaxBytes,
	}
	if args.MaxWidth != nil {
		limits.maxWidth = *args.MaxWidth
	}
	if args.MaxHeight != nil {
		limits.maxHeight = *args.MaxHeight
	}
	if args.MaxBytes != nil {
		limits.maxBytes = *args.MaxBytes
	}
	if limits.maxWidth < 1 || limits.maxWidth > maxScreenshotDimension {
		return nil, screenshotLimits{}, invalidScreenshot("maxWidth is out of range")
	}
	if limits.maxHeight < 1 || limits.maxHeight > maxScreenshotDimension {
		return nil, screenshotLimits{}, invalidScreenshot("maxHeight is out of range")
	}
	if limits.maxBytes < minScreenshotMaxBytes || limits.maxBytes > defaultScreenshotMaxBytes {
		return nil, screenshotLimits{}, invalidScreenshot("maxBytes is out of range")
	}

	params := map[string]any{
		"capture":   capture,
		"format":    format,
		"maxWidth":  limits.maxWidth,
		"maxHeight": limits.maxHeight,
		"maxBytes":  limits.maxBytes,
	}
	putOptional(params, "quality", args.Quality)
	return params, limits, nil
}

func decodeScreenshotResult(
	raw json.RawMessage,
	limits screenshotLimits,
) (screenshotWireResult, []byte, error) {
	var result screenshotWireResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, nil, invalidScreenshotResult()
	}
	if result.Capture != "viewport" || result.Format != limits.format ||
		result.MIMEType != screenshotMIMEType(limits.format) || result.DataBase64 == "" {
		return result, nil, invalidScreenshotResult()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(result.DataBase64)
	if err != nil || len(data) == 0 || len(data) != result.ByteLength || len(data) > limits.maxBytes {
		return result, nil, invalidScreenshotResult()
	}
	width, height, err := screenshotDimensions(limits.format, data)
	if err != nil || width != result.Width || height != result.Height ||
		width < 1 || height < 1 || width > limits.maxWidth || height > limits.maxHeight {
		return result, nil, invalidScreenshotResult()
	}
	if result.TabID < 0 || result.WindowID < 0 || len(result.Warnings) > 10 {
		return result, nil, invalidScreenshotResult()
	}
	for _, warning := range result.Warnings {
		if strings.TrimSpace(warning) == "" || len(warning) > 1_000 {
			return result, nil, invalidScreenshotResult()
		}
	}
	return result, data, nil
}

func screenshotDimensions(format string, data []byte) (int, int, error) {
	var (
		width  int
		height int
	)
	switch format {
	case "png":
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return 0, 0, err
		}
		width, height = config.Width, config.Height
	case "jpeg":
		config, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return 0, 0, err
		}
		width, height = config.Width, config.Height
	default:
		return 0, 0, fmt.Errorf("unsupported screenshot format %q", format)
	}
	return width, height, nil
}

func screenshotMIMEType(format string) string {
	if format == "jpeg" {
		return "image/jpeg"
	}
	return "image/png"
}

func invalidScreenshot(message string) *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}

func invalidScreenshotResult() *protocol.Error {
	return protocol.NewError(
		protocol.CodeInvalidMessage,
		"The browser returned an invalid screenshot payload",
		false,
	)
}
