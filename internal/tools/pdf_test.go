package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/artifacts"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestPrintToPDFHandlerStoresValidatedArtifact(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	store, err := artifacts.New(t.TempDir(), time.Hour, artifacts.WithMaxBytes(1<<20))
	if err != nil {
		t.Fatalf("artifacts.New() error = %v", err)
	}
	service.artifacts = store
	pdfData := testPDF()
	tabID := 7
	landscape := true
	background := true
	scale := 0.9
	width := 11.0
	height := 8.5
	margin := 0.25
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserPrintToPDFHandler(
			context.Background(),
			mcp.CallToolRequest{},
			printToPDFArgs{
				BrowserID:       "browser-a",
				TabID:           &tabID,
				Landscape:       &landscape,
				PrintBackground: &background,
				Scale:           &scale,
				PaperWidth:      &width,
				PaperHeight:     &height,
				MarginTop:       &margin,
				MarginBottom:    &margin,
				MarginLeft:      &margin,
				MarginRight:     &margin,
				PageRanges:      "1-3, 5",
			},
		)
		resultChannel <- result
	}()

	request := receiveToolMessage(t, connection.messages)
	if request.Command != protocol.CommandPagePrintToPDF || request.Target == nil ||
		request.Target.TabID == nil || *request.Target.TabID != tabID {
		t.Fatalf("PDF request = %#v", request)
	}
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatalf("decode PDF params: %v", err)
	}
	if params["pageRanges"] != "1-3,5" || params["paperWidth"] != 11.0 ||
		params["maxBytes"] != float64(defaultPDFMaxBytes) {
		t.Fatalf("PDF params = %#v", params)
	}

	response, err := protocol.NewResponse(request.RequestID, "browser-a", map[string]any{
		"mimeType":   "application/pdf",
		"dataBase64": base64.StdEncoding.EncodeToString(pdfData),
		"byteLength": len(pdfData),
		"tabId":      tabID,
		"warnings":   []string{},
	}, nil)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	if !service.router.HandleResponse("browser-a", connection.ID(), response) {
		t.Fatal("HandleResponse() = false")
	}

	result := <-resultChannel
	if result.IsError {
		t.Fatalf("browserPrintToPDFHandler() returned error: %s", toolText(t, result))
	}
	toolResponse := decodeToolResponse(t, result)
	if toolResponse.ArtifactURI == "" ||
		!strings.HasPrefix(toolResponse.ArtifactURI, "browser://artifacts/") {
		t.Fatalf("artifactUri = %q", toolResponse.ArtifactURI)
	}
	artifactID := strings.TrimPrefix(toolResponse.ArtifactURI, "browser://artifacts/")
	metadata, stored, err := store.Read(context.Background(), artifactID)
	if err != nil {
		t.Fatalf("artifact Read() error = %v", err)
	}
	if metadata.MIMEType != "application/pdf" || !bytes.Equal(stored, pdfData) {
		t.Fatalf("stored artifact = (%#v, %d bytes)", metadata, len(stored))
	}
	if strings.Contains(toolText(t, result), "dataBase64") {
		t.Fatal("tool result exposed inline PDF data")
	}
}

func TestValidatePrintToPDFArgsNormalizesDefaultsAndRanges(t *testing.T) {
	t.Parallel()

	params, settings, err := validatePrintToPDFArgs(printToPDFArgs{PageRanges: " 1 - 3, 5 "})
	if err != nil {
		t.Fatalf("validatePrintToPDFArgs() error = %v", err)
	}
	if settings.PageRanges != "1-3,5" || settings.Scale != 1 || settings.PaperWidth != 8.5 ||
		settings.PaperHeight != 11 || settings.MarginTop != 0.4 ||
		params["maxBytes"] != defaultPDFMaxBytes {
		t.Fatalf("settings = %#v, params = %#v", settings, params)
	}
}

func TestValidatePrintToPDFArgsRejectsUnsafeBounds(t *testing.T) {
	t.Parallel()

	nan := math.NaN()
	tooSmallPaper := 1.0
	largeMargin := 0.5
	tooLarge := defaultPDFMaxBytes + 1
	for _, args := range []printToPDFArgs{
		{PageRanges: "0"},
		{PageRanges: "5-2"},
		{PageRanges: "1-2-3"},
		{PageRanges: "100001"},
		{Scale: &nan},
		{PaperWidth: &tooSmallPaper, MarginLeft: &largeMargin, MarginRight: &largeMargin},
		{MaxBytes: &tooLarge},
	} {
		if _, _, err := validatePrintToPDFArgs(args); err == nil {
			t.Fatalf("validatePrintToPDFArgs(%#v) error = nil", args)
		}
	}
}

func TestDecodePDFWireResultRejectsMalformedPayloads(t *testing.T) {
	t.Parallel()

	validData := testPDF()
	validBase64 := base64.StdEncoding.EncodeToString(validData)
	for _, raw := range []string{
		`null`,
		`{"mimeType":"text/plain","dataBase64":"` + validBase64 + `","byteLength":` +
			strconv.Itoa(len(validData)) + `,"tabId":1,"warnings":[]}`,
		`{"mimeType":"application/pdf","dataBase64":"%%%","byteLength":3,"tabId":1,"warnings":[]}`,
		`{"mimeType":"application/pdf","dataBase64":"` +
			base64.StdEncoding.EncodeToString([]byte("%PDF-1.7\nmissing eof")) +
			`","byteLength":20,"tabId":1,"warnings":[]}`,
	} {
		if _, _, err := decodePDFWireResult(json.RawMessage(raw), defaultPDFMaxBytes); err == nil {
			t.Fatalf("decodePDFWireResult(%s) error = nil", raw)
		}
	}
}

func testPDF() []byte {
	return []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n")
}
