package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/artifacts"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestScreenshotHandlerStoresValidatedArtifact(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	store, err := artifacts.New(t.TempDir(), time.Hour, artifacts.WithMaxBytes(1<<20))
	if err != nil {
		t.Fatalf("artifacts.New() error = %v", err)
	}
	service.artifacts = store
	pngData := encodeTestImage(t, "png", 2, 3)
	tabID := 7
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserScreenshotHandler(
			context.Background(),
			mcp.CallToolRequest{},
			screenshotArgs{
				BrowserID: "browser-a",
				TabID:     &tabID,
				Capture:   "fullPage",
				Format:    "png",
			},
		)
		resultChannel <- result
	}()

	request := receiveToolMessage(t, connection.messages)
	if request.Command != protocol.CommandPageScreenshot || request.Target == nil ||
		request.Target.TabID == nil || *request.Target.TabID != tabID {
		t.Fatalf("screenshot request = %#v", request)
	}
	response, err := protocol.NewResponse(request.RequestID, "browser-a", map[string]any{
		"capture":    "fullPage",
		"format":     "png",
		"mimeType":   "image/png",
		"dataBase64": base64.StdEncoding.EncodeToString(pngData),
		"byteLength": len(pngData),
		"width":      2,
		"height":     3,
		"tabId":      tabID,
		"windowId":   4,
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
		t.Fatalf("browserScreenshotHandler() returned error: %s", toolText(t, result))
	}
	toolResponse := decodeToolResponse(t, result)
	if toolResponse.ArtifactURI == "" || !strings.HasPrefix(toolResponse.ArtifactURI, "browser://artifacts/") {
		t.Fatalf("artifactUri = %q", toolResponse.ArtifactURI)
	}
	artifactID := strings.TrimPrefix(toolResponse.ArtifactURI, "browser://artifacts/")
	metadata, stored, err := store.Read(context.Background(), artifactID)
	if err != nil {
		t.Fatalf("artifact Read() error = %v", err)
	}
	if metadata.MIMEType != "image/png" || !bytes.Equal(stored, pngData) {
		t.Fatalf("stored artifact = (%#v, %d bytes)", metadata, len(stored))
	}
	if strings.Contains(toolText(t, result), "dataBase64") {
		t.Fatal("tool result exposed inline screenshot data")
	}
}

func TestDecodeScreenshotResultSupportsPNGAndJPEG(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"png", "jpeg"} {
		format := format
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			data := encodeTestImage(t, format, 4, 5)
			raw := []byte(`{` +
				`"capture":"viewport",` +
				`"format":"` + format + `",` +
				`"mimeType":"` + screenshotMIMEType(format) + `",` +
				`"dataBase64":"` + base64.StdEncoding.EncodeToString(data) + `",` +
				`"byteLength":` + strconv.Itoa(len(data)) + `,` +
				`"width":4,"height":5,"tabId":1,"windowId":2,"warnings":[]}`)
			result, decoded, err := decodeScreenshotResult(raw, screenshotLimits{
				capture: "viewport", format: format, maxWidth: 10, maxHeight: 10, maxBytes: 100_000,
			})
			if err != nil {
				t.Fatalf("decodeScreenshotResult() error = %v", err)
			}
			if result.Width != 4 || result.Height != 5 || !bytes.Equal(decoded, data) {
				t.Fatalf("decoded result = (%#v, %d bytes)", result, len(decoded))
			}
		})
	}
}

func TestValidateScreenshotArgsRejectsInvalidBounds(t *testing.T) {
	t.Parallel()

	quality := 80
	tooLarge := defaultScreenshotMaxBytes + 1
	tests := []screenshotArgs{
		{Capture: "unknown"},
		{Capture: "element"},
		{Capture: "viewport", Locator: &protocol.Locator{CSS: "#button"}},
		{Format: "webp"},
		{Format: "png", Quality: &quality},
		{MaxBytes: &tooLarge},
	}
	for _, args := range tests {
		if _, _, err := validateScreenshotArgs(args, nil); err == nil {
			t.Fatalf("validateScreenshotArgs(%#v) error = nil", args)
		}
	}
}

func TestValidateScreenshotArgsAcceptsElementLocator(t *testing.T) {
	t.Parallel()

	documentID := "document-1"
	tabID := 7
	target := pageTarget(&tabID, nil, documentID)
	locator := &protocol.Locator{
		Element: &protocol.ElementReference{ElementID: "element-1", DocumentID: documentID},
	}
	params, limits, err := validateScreenshotArgs(screenshotArgs{
		Capture: "element", Locator: locator, DocumentID: documentID,
	}, target)
	if err != nil {
		t.Fatalf("validateScreenshotArgs() error = %v", err)
	}
	if limits.capture != "element" || params["locator"] != locator {
		t.Fatalf("validated screenshot = (%#v, %#v)", params, limits)
	}
}

func encodeTestImage(t *testing.T, format string, width, height int) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	canvas.Set(0, 0, color.RGBA{R: 0x22, G: 0x66, B: 0xaa, A: 0xff})
	var output bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&output, canvas, &jpeg.Options{Quality: 80})
	} else {
		err = png.Encode(&output, canvas)
	}
	if err != nil {
		t.Fatalf("encode %s: %v", format, err)
	}
	return output.Bytes()
}
