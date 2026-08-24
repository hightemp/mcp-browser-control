package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/artifacts"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultPDFMaxBytes = 2_000_000
	minPDFMaxBytes     = 1_024
	maxPDFPageNumber   = 100_000
	maxPDFRanges       = 50
)

type printToPDFArgs struct {
	BrowserID         string   `json:"browserId,omitempty"`
	TabID             *int     `json:"tabId,omitempty"`
	Landscape         *bool    `json:"landscape,omitempty"`
	PrintBackground   *bool    `json:"printBackground,omitempty"`
	Scale             *float64 `json:"scale,omitempty"`
	PaperWidth        *float64 `json:"paperWidth,omitempty"`
	PaperHeight       *float64 `json:"paperHeight,omitempty"`
	MarginTop         *float64 `json:"marginTop,omitempty"`
	MarginBottom      *float64 `json:"marginBottom,omitempty"`
	MarginLeft        *float64 `json:"marginLeft,omitempty"`
	MarginRight       *float64 `json:"marginRight,omitempty"`
	PageRanges        string   `json:"pageRanges,omitempty"`
	PreferCSSPageSize *bool    `json:"preferCSSPageSize,omitempty"`
	MaxBytes          *int     `json:"maxBytes,omitempty"`
	TimeoutMS         *int     `json:"timeoutMs,omitempty"`
}

type pdfSettings struct {
	Landscape         bool
	PrintBackground   bool
	Scale             float64
	PaperWidth        float64
	PaperHeight       float64
	MarginTop         float64
	MarginBottom      float64
	MarginLeft        float64
	MarginRight       float64
	PageRanges        string
	PreferCSSPageSize bool
	MaxBytes          int
}

type pdfWireResult struct {
	MIMEType   string   `json:"mimeType"`
	DataBase64 string   `json:"dataBase64"`
	ByteLength int      `json:"byteLength"`
	TabID      int      `json:"tabId"`
	Warnings   []string `json:"warnings"`
}

func (s *Service) registerPrintToPDFTool(mcpServer *server.MCPServer) {
	mcpServer.AddTool(
		mcp.NewTool(
			"browser_print_to_pdf",
			mcp.WithDescription("Print the selected tab to a validated temporary PDF artifact"),
			optionalBrowserID(),
			optionalTabID(),
			mcp.WithBoolean("landscape", mcp.Description("Use landscape paper orientation")),
			mcp.WithBoolean("printBackground", mcp.Description("Include CSS background graphics")),
			mcp.WithNumber("scale", mcp.Description("Page rendering scale"), mcp.Min(0.1), mcp.Max(2)),
			mcp.WithNumber("paperWidth", mcp.Description("Paper width in inches"), mcp.Min(1), mcp.Max(200)),
			mcp.WithNumber("paperHeight", mcp.Description("Paper height in inches"), mcp.Min(1), mcp.Max(200)),
			mcp.WithNumber("marginTop", mcp.Description("Top margin in inches"), mcp.Min(0), mcp.Max(10)),
			mcp.WithNumber("marginBottom", mcp.Description("Bottom margin in inches"), mcp.Min(0), mcp.Max(10)),
			mcp.WithNumber("marginLeft", mcp.Description("Left margin in inches"), mcp.Min(0), mcp.Max(10)),
			mcp.WithNumber("marginRight", mcp.Description("Right margin in inches"), mcp.Min(0), mcp.Max(10)),
			mcp.WithString("pageRanges", mcp.Description("One-based page ranges such as 1-5, 8, 11-13"), mcp.MaxLength(256)),
			mcp.WithBoolean("preferCSSPageSize", mcp.Description("Prefer the page size declared by CSS @page")),
			mcp.WithNumber("maxBytes", mcp.Description("Reject larger encoded PDFs"), mcp.Min(minPDFMaxBytes), mcp.Max(defaultPDFMaxBytes)),
			optionalTimeout(),
		),
		mcp.NewTypedToolHandler(s.browserPrintToPDFHandler),
	)
}

func (s *Service) browserPrintToPDFHandler(
	ctx context.Context,
	_ mcp.CallToolRequest,
	args printToPDFArgs,
) (*mcp.CallToolResult, error) {
	params, settings, err := validatePrintToPDFArgs(args)
	if err != nil {
		return errorResult(err)
	}
	if s.artifacts == nil {
		return errorResult(protocol.NewError(
			protocol.CodeCapabilityUnavailable,
			"PDF artifact storage is unavailable",
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
		protocol.CommandPagePrintToPDF,
		targetWithTab(args.TabID),
		params,
		nil,
	)
	if err != nil {
		return errorResultWithDuration(err, duration)
	}
	wireResult, pdfData, err := decodePDFWireResult(raw, settings.MaxBytes)
	if err != nil {
		return errorResultWithDuration(err, duration)
	}
	if target != nil && target.TabID != nil && wireResult.TabID != *target.TabID {
		return errorResultWithDuration(invalidPDFResult(), duration)
	}
	if target == nil || target.TabID == nil {
		tabID := wireResult.TabID
		target = &protocol.Target{BrowserID: browserID, TabID: &tabID}
	}

	storeStarted := time.Now()
	metadata, err := s.artifacts.Put(
		operationCtx,
		wireResult.MIMEType,
		pdfData,
		artifacts.RedactionMetadata{},
	)
	duration += time.Since(storeStarted)
	if err != nil {
		return errorResultWithDuration(fmt.Errorf("store PDF artifact: %w", err), duration)
	}

	result := map[string]any{
		"artifactUri":         metadata.URI,
		"artifactMetadataUri": metadata.URI + "/metadata",
		"mimeType":            metadata.MIMEType,
		"size":                metadata.Size,
		"tabId":               wireResult.TabID,
		"expiresAt":           metadata.ExpiresAt,
		"redactionApplied":    metadata.Redaction.Applied,
		"settings": map[string]any{
			"landscape":         settings.Landscape,
			"printBackground":   settings.PrintBackground,
			"scale":             settings.Scale,
			"paperWidth":        settings.PaperWidth,
			"paperHeight":       settings.PaperHeight,
			"marginTop":         settings.MarginTop,
			"marginBottom":      settings.MarginBottom,
			"marginLeft":        settings.MarginLeft,
			"marginRight":       settings.MarginRight,
			"pageRanges":        settings.PageRanges,
			"preferCSSPageSize": settings.PreferCSSPageSize,
		},
		"warnings": append(
			append([]string(nil), wireResult.Warnings...),
			"PDF artifacts may contain sensitive page content; no content redaction was applied",
		),
	}
	return successResultWithTarget(browserID, target, result, duration)
}

func validatePrintToPDFArgs(args printToPDFArgs) (map[string]any, pdfSettings, error) {
	settings := pdfSettings{
		Scale:        1,
		PaperWidth:   8.5,
		PaperHeight:  11,
		MarginTop:    0.4,
		MarginBottom: 0.4,
		MarginLeft:   0.4,
		MarginRight:  0.4,
		MaxBytes:     defaultPDFMaxBytes,
	}
	assignBool(&settings.Landscape, args.Landscape)
	assignBool(&settings.PrintBackground, args.PrintBackground)
	assignBool(&settings.PreferCSSPageSize, args.PreferCSSPageSize)
	assignFloat(&settings.Scale, args.Scale)
	assignFloat(&settings.PaperWidth, args.PaperWidth)
	assignFloat(&settings.PaperHeight, args.PaperHeight)
	assignFloat(&settings.MarginTop, args.MarginTop)
	assignFloat(&settings.MarginBottom, args.MarginBottom)
	assignFloat(&settings.MarginLeft, args.MarginLeft)
	assignFloat(&settings.MarginRight, args.MarginRight)
	if args.MaxBytes != nil {
		settings.MaxBytes = *args.MaxBytes
	}

	var err error
	settings.PageRanges, err = normalizePDFPageRanges(args.PageRanges)
	if err != nil {
		return nil, pdfSettings{}, err
	}
	for _, value := range []struct {
		name    string
		value   float64
		minimum float64
		maximum float64
	}{
		{name: "scale", value: settings.Scale, minimum: 0.1, maximum: 2},
		{name: "paperWidth", value: settings.PaperWidth, minimum: 1, maximum: 200},
		{name: "paperHeight", value: settings.PaperHeight, minimum: 1, maximum: 200},
		{name: "marginTop", value: settings.MarginTop, minimum: 0, maximum: 10},
		{name: "marginBottom", value: settings.MarginBottom, minimum: 0, maximum: 10},
		{name: "marginLeft", value: settings.MarginLeft, minimum: 0, maximum: 10},
		{name: "marginRight", value: settings.MarginRight, minimum: 0, maximum: 10},
	} {
		if !finiteBetween(value.value, value.minimum, value.maximum) {
			return nil, pdfSettings{}, invalidPDF(value.name + " is out of range")
		}
	}
	if settings.MarginLeft+settings.MarginRight >= settings.PaperWidth {
		return nil, pdfSettings{}, invalidPDF("horizontal margins must be smaller than paper width")
	}
	if settings.MarginTop+settings.MarginBottom >= settings.PaperHeight {
		return nil, pdfSettings{}, invalidPDF("vertical margins must be smaller than paper height")
	}
	if settings.MaxBytes < minPDFMaxBytes || settings.MaxBytes > defaultPDFMaxBytes {
		return nil, pdfSettings{}, invalidPDF("maxBytes is out of range")
	}

	return map[string]any{
		"landscape":         settings.Landscape,
		"printBackground":   settings.PrintBackground,
		"scale":             settings.Scale,
		"paperWidth":        settings.PaperWidth,
		"paperHeight":       settings.PaperHeight,
		"marginTop":         settings.MarginTop,
		"marginBottom":      settings.MarginBottom,
		"marginLeft":        settings.MarginLeft,
		"marginRight":       settings.MarginRight,
		"pageRanges":        settings.PageRanges,
		"preferCSSPageSize": settings.PreferCSSPageSize,
		"maxBytes":          settings.MaxBytes,
	}, settings, nil
}

func normalizePDFPageRanges(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > 256 {
		return "", invalidPDF("pageRanges is too long")
	}
	tokens := strings.Split(value, ",")
	if len(tokens) > maxPDFRanges {
		return "", invalidPDF("pageRanges contains too many ranges")
	}
	normalized := make([]string, 0, len(tokens))
	for _, token := range tokens {
		parts := strings.Split(strings.TrimSpace(token), "-")
		if len(parts) < 1 || len(parts) > 2 {
			return "", invalidPDF("pageRanges is invalid")
		}
		start, err := parsePDFPageNumber(parts[0])
		if err != nil {
			return "", err
		}
		end := start
		if len(parts) == 2 {
			end, err = parsePDFPageNumber(parts[1])
			if err != nil || end < start {
				return "", invalidPDF("pageRanges is invalid")
			}
		}
		if start == end {
			normalized = append(normalized, strconv.Itoa(start))
		} else {
			normalized = append(normalized, strconv.Itoa(start)+"-"+strconv.Itoa(end))
		}
	}
	return strings.Join(normalized, ","), nil
}

func parsePDFPageNumber(value string) (int, error) {
	value = strings.TrimSpace(value)
	page, err := strconv.Atoi(value)
	if err != nil || page < 1 || page > maxPDFPageNumber {
		return 0, invalidPDF("pageRanges is invalid")
	}
	return page, nil
}

func decodePDFWireResult(raw json.RawMessage, maxBytes int) (pdfWireResult, []byte, error) {
	var result pdfWireResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, nil, invalidPDFResult()
	}
	if result.MIMEType != "application/pdf" || result.DataBase64 == "" || result.TabID < 0 ||
		result.ByteLength < 1 || len(result.Warnings) > 10 {
		return result, nil, invalidPDFResult()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(result.DataBase64)
	if err != nil || len(data) != result.ByteLength || len(data) > maxBytes || !validPDF(data) {
		return result, nil, invalidPDFResult()
	}
	for _, warning := range result.Warnings {
		if strings.TrimSpace(warning) == "" || len(warning) > 1_000 {
			return result, nil, invalidPDFResult()
		}
	}
	return result, data, nil
}

func validPDF(data []byte) bool {
	if len(data) < 8 || !bytes.HasPrefix(data, []byte("%PDF-")) {
		return false
	}
	eof := bytes.LastIndex(data, []byte("%%EOF"))
	if eof < 0 || eof < len(data)-1_024 {
		return false
	}
	for _, value := range data[eof+5:] {
		switch value {
		case '\t', '\n', '\f', '\r', ' ':
		default:
			return false
		}
	}
	return true
}

func assignBool(destination *bool, value *bool) {
	if value != nil {
		*destination = *value
	}
}

func assignFloat(destination *float64, value *float64) {
	if value != nil {
		*destination = *value
	}
}

func finiteBetween(value, minimum, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= minimum && value <= maximum
}

func invalidPDF(message string) *protocol.Error {
	return protocol.NewError(protocol.CodeInvalidMessage, message, false)
}

func invalidPDFResult() *protocol.Error {
	return protocol.NewError(
		protocol.CodeInvalidMessage,
		"the browser returned an invalid PDF payload",
		false,
	)
}
