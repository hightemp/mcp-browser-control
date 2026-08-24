package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/artifacts"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestPerformanceMetricsHandlerPreservesRootDocumentAndBounds(t *testing.T) {
	t.Parallel()

	service, connection, otherConnection := newTestService(t)
	var audit bytes.Buffer
	service.auditLogger = log.New(&audit, "", 0)
	tabID := 7
	documentID := "document-1"
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserGetPerformanceMetricsHandler(
			context.Background(),
			mcp.CallToolRequest{},
			performanceTargetArgs{
				BrowserID:  "browser-a",
				TabID:      &tabID,
				DocumentID: documentID,
			},
		)
		resultChannel <- result
	}()

	request := receiveToolMessage(t, connection.messages)
	select {
	case other := <-otherConnection.messages:
		t.Fatalf("performance request leaked to another browser: %#v", other)
	default:
	}
	if request.Command != protocol.CommandPerformanceMetrics || request.Target == nil ||
		request.Target.TabID == nil || *request.Target.TabID != tabID ||
		request.Target.FrameID == nil || *request.Target.FrameID != 0 ||
		request.Target.DocumentID != documentID {
		t.Fatalf("performance metrics request = %#v", request)
	}
	if string(request.Params) != "{}" {
		t.Fatalf("performance metrics params = %s", request.Params)
	}

	respondToToolRequest(t, service, connection, request, performanceMetricsWireResult{
		TabID:      tabID,
		DocumentID: documentID,
		Metrics: []performanceMetric{
			{Name: "Timestamp", Value: 42.5},
			{Name: "JSHeapUsedSize", Value: 1_024},
		},
		Warnings: []string{},
	})
	result := <-resultChannel
	if result.IsError {
		t.Fatalf("browserGetPerformanceMetricsHandler() returned error: %s", toolText(t, result))
	}
	text := toolText(t, result)
	for _, expected := range []string{`"documentId":"document-1"`, `"name":"Timestamp"`, `"value":42.5`} {
		if !strings.Contains(text, expected) {
			t.Errorf("metrics result does not contain %s: %s", expected, text)
		}
	}
	if !strings.Contains(audit.String(), "operation=performance kind=\"metrics\"") ||
		!strings.Contains(audit.String(), "outcome=OK") {
		t.Fatalf("performance audit = %q", audit.String())
	}
}

func TestPerformanceCaptureHandlerStoresArtifactWithoutInlineData(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	store, err := artifacts.New(t.TempDir(), time.Hour, artifacts.WithMaxBytes(3_000_000))
	if err != nil {
		t.Fatalf("artifacts.New() error = %v", err)
	}
	service.artifacts = store
	var audit bytes.Buffer
	service.auditLogger = log.New(&audit, "", 0)

	tabID := 9
	durationMS := 100
	maxBytes := minPerformanceCaptureBytes
	timeoutMS := 1_200
	artifactData := []byte(`{"traceEvents":[{"name":"sensitive-page-event"}]}`)
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserCapturePerformanceHandler(
			context.Background(),
			mcp.CallToolRequest{},
			performanceCaptureArgs{
				performanceTargetArgs: performanceTargetArgs{
					BrowserID:  "browser-a",
					TabID:      &tabID,
					DocumentID: "document-9",
					TimeoutMS:  &timeoutMS,
				},
				Kind:       "trace",
				DurationMS: &durationMS,
				MaxBytes:   &maxBytes,
			},
		)
		resultChannel <- result
	}()

	request := receiveToolMessage(t, connection.messages)
	if request.Command != protocol.CommandPerformanceCapture || request.Target == nil ||
		request.Target.FrameID == nil || *request.Target.FrameID != 0 ||
		request.Target.DocumentID != "document-9" || request.TimeoutMS < 1 ||
		request.TimeoutMS > int64(timeoutMS) {
		t.Fatalf("performance capture request = %#v", request)
	}
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatalf("decode performance params: %v", err)
	}
	if params["kind"] != "trace" || params["durationMs"] != float64(durationMS) ||
		params["maxBytes"] != float64(maxBytes) {
		t.Fatalf("performance capture params = %#v", params)
	}

	respondToToolRequest(t, service, connection, request, performanceCaptureWireResult{
		Kind:       "trace",
		MIMEType:   "application/json",
		DataBase64: base64.StdEncoding.EncodeToString(artifactData),
		ByteLength: len(artifactData),
		TabID:      tabID,
		DocumentID: "document-9",
		DurationMS: durationMS,
		Warnings:   []string{},
	})
	result := <-resultChannel
	if result.IsError {
		t.Fatalf("browserCapturePerformanceHandler() returned error: %s", toolText(t, result))
	}
	response := decodeToolResponse(t, result)
	if !strings.HasPrefix(response.ArtifactURI, "browser://artifacts/") {
		t.Fatalf("artifactUri = %q", response.ArtifactURI)
	}
	artifactID := strings.TrimPrefix(response.ArtifactURI, "browser://artifacts/")
	metadata, stored, err := store.Read(context.Background(), artifactID)
	if err != nil {
		t.Fatalf("artifact Read() error = %v", err)
	}
	if metadata.MIMEType != "application/json" || !bytes.Equal(stored, artifactData) {
		t.Fatalf("stored artifact = (%#v, %q)", metadata, stored)
	}
	text := toolText(t, result)
	if strings.Contains(text, "dataBase64") || strings.Contains(text, "sensitive-page-event") {
		t.Fatalf("capture result exposed inline artifact: %s", text)
	}
	if !strings.Contains(audit.String(), "operation=performance kind=\"trace\"") ||
		strings.Contains(audit.String(), "sensitive-page-event") {
		t.Fatalf("performance audit = %q", audit.String())
	}
}

func TestPerformanceValidationRejectsProhibitedAndMalformedValues(t *testing.T) {
	t.Parallel()

	validKinds := []string{"trace", "coverage", "cpuProfile", "audits"}
	for _, kind := range validKinds {
		validatedKind, durationMS, maxBytes, err := validatePerformanceCaptureArgs(
			performanceCaptureArgs{Kind: kind},
		)
		if err != nil || validatedKind != kind || durationMS != defaultPerformanceCaptureDurationMS ||
			maxBytes != defaultPerformanceCaptureBytes {
			t.Fatalf("validatePerformanceCaptureArgs(%q) = (%q, %d, %d, %v)", kind, validatedKind, durationMS, maxBytes, err)
		}
	}

	tooShort := minPerformanceCaptureDurationMS - 1
	tooLong := maxPerformanceCaptureDurationMS + 1
	tooSmall := minPerformanceCaptureBytes - 1
	tooLarge := defaultPerformanceCaptureBytes + 1
	for _, args := range []performanceCaptureArgs{
		{Kind: "heapSnapshot"},
		{Kind: "trace", DurationMS: &tooShort},
		{Kind: "trace", DurationMS: &tooLong},
		{Kind: "trace", MaxBytes: &tooSmall},
		{Kind: "trace", MaxBytes: &tooLarge},
	} {
		if _, _, _, err := validatePerformanceCaptureArgs(args); err == nil {
			t.Fatalf("validatePerformanceCaptureArgs(%#v) error = nil", args)
		}
	}

	invalidMetrics := []string{
		`null`,
		`{"tabId":1,"documentId":"doc","metrics":[{"name":"Timestamp","value":1},{"name":"Timestamp","value":2}],"warnings":[]}`,
		`{"tabId":1,"documentId":"","metrics":[],"warnings":[]}`,
		`{"tabId":1,"documentId":"doc","metrics":[],"warnings":[],"extra":true}`,
	}
	for _, raw := range invalidMetrics {
		if _, err := decodePerformanceMetrics(json.RawMessage(raw)); err == nil {
			t.Fatalf("decodePerformanceMetrics(%s) error = nil", raw)
		}
	}

	validData := []byte(`{"profile":{"nodes":[]}}`)
	validBase64 := base64.StdEncoding.EncodeToString(validData)
	for _, raw := range []string{
		`null`,
		`{"kind":"trace","mimeType":"text/plain","dataBase64":"` + validBase64 + `","byteLength":24,"tabId":1,"documentId":"doc","durationMs":100,"warnings":[]}`,
		`{"kind":"trace","mimeType":"application/json","dataBase64":"%%%","byteLength":3,"tabId":1,"documentId":"doc","durationMs":100,"warnings":[]}`,
		`{"kind":"trace","mimeType":"application/json","dataBase64":"W10=","byteLength":2,"tabId":1,"documentId":"doc","durationMs":100,"warnings":[]}`,
	} {
		if _, _, err := decodePerformanceCapture(json.RawMessage(raw), "trace", 100, minPerformanceCaptureBytes); err == nil {
			t.Fatalf("decodePerformanceCapture(%s) error = nil", raw)
		}
	}
}

func TestGenericCommandCannotBypassPerformanceTools(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	for _, command := range []string{
		protocol.CommandPerformanceMetrics,
		protocol.CommandPerformanceCapture,
	} {
		result, err := service.browserSendCommandHandler(
			context.Background(),
			mcp.CallToolRequest{},
			sendCommandArgs{BrowserID: "browser-a", Command: command},
		)
		if err != nil || result == nil || !result.IsError {
			t.Fatalf("browserSendCommandHandler(%q) = (%#v, %v), want tool error", command, result, err)
		}
		response := decodeToolResponse(t, result)
		if response.Error == nil || response.Error.Code != protocol.CodeInvalidCommand {
			t.Fatalf("browserSendCommandHandler(%q) error = %#v", command, response.Error)
		}
	}
	select {
	case message := <-connection.messages:
		t.Fatalf("generic performance command reached browser: %#v", message)
	default:
	}
}
