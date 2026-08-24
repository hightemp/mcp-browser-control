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

func TestNetworkStartAndReadPreserveRootDocumentAndAuditMetadata(t *testing.T) {
	t.Parallel()

	service, connection, otherConnection := newTestService(t)
	var audit bytes.Buffer
	service.auditLogger = log.New(&audit, "", 0)
	tabID := 7
	documentID := "document-7"
	maxEntries := 200
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserStartNetworkHandler(
			context.Background(),
			mcp.CallToolRequest{},
			networkStartArgs{
				networkTargetArgs: networkTargetArgs{BrowserID: "browser-a", TabID: &tabID, DocumentID: documentID},
				MaxEntries:        &maxEntries,
			},
		)
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	select {
	case leaked := <-otherConnection.messages:
		t.Fatalf("network request leaked to another browser: %#v", leaked)
	default:
	}
	assertNetworkRequestTarget(t, request, protocol.CommandNetworkStart, tabID, documentID)
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil || params["maxEntries"] != float64(maxEntries) {
		t.Fatalf("network start params = %#v, error = %v", params, err)
	}
	respondToToolRequest(t, service, connection, request, map[string]any{
		"tabId": tabID, "documentId": documentID, "active": true,
		"maxEntries": maxEntries, "retainedEntries": 0, "warnings": []string{},
	})
	if result := <-resultChannel; result.IsError {
		t.Fatalf("browserStartNetworkHandler() returned error: %s", toolText(t, result))
	}

	limit := 10
	maxBytes := minNetworkReadMaxBytes
	failedOnly := true
	statusMin := 400
	resultChannel = make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserGetNetworkHandler(
			context.Background(),
			mcp.CallToolRequest{},
			networkReadArgs{
				networkTargetArgs: networkTargetArgs{BrowserID: "browser-a", TabID: &tabID, DocumentID: documentID},
				Cursor:            "4",
				Limit:             &limit,
				ResourceTypes:     []string{"Fetch"},
				FailedOnly:        &failedOnly,
				StatusMin:         &statusMin,
				Since:             "2026-08-24T00:00:00Z",
				MaxBytes:          &maxBytes,
			},
		)
		resultChannel <- result
	}()
	request = receiveToolMessage(t, connection.messages)
	assertNetworkRequestTarget(t, request, protocol.CommandNetworkRead, tabID, documentID)
	if err := json.Unmarshal(request.Params, &params); err != nil || params["cursor"] != "4" ||
		params["limit"] != float64(limit) || params["maxBytes"] != float64(maxBytes) ||
		params["failedOnly"] != true || params["statusMin"] != float64(statusMin) {
		t.Fatalf("network read params = %#v, error = %v", params, err)
	}
	respondToToolRequest(t, service, connection, request, map[string]any{
		"tabId": tabID, "documentId": documentID, "active": true,
		"entries":    []map[string]any{{"entryId": "5", "url": "https://example.com/fail", "failed": true}},
		"nextCursor": "5", "warnings": []string{},
	})
	result := <-resultChannel
	if result.IsError || !strings.Contains(toolText(t, result), `"entryId":"5"`) {
		t.Fatalf("browserGetNetworkHandler() result = %s", toolText(t, result))
	}
	if strings.Contains(audit.String(), "https://example.com") ||
		!strings.Contains(audit.String(), "operation=network") ||
		!strings.Contains(audit.String(), `tool="browser_get_network_log"`) {
		t.Fatalf("network audit = %q", audit.String())
	}
}

func TestNetworkInlineAddsResolvedDocumentToResultTarget(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	tabID := 17
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserStartNetworkHandler(
			context.Background(),
			mcp.CallToolRequest{},
			networkStartArgs{networkTargetArgs: networkTargetArgs{BrowserID: "browser-a", TabID: &tabID}},
		)
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	assertNetworkRequestTarget(t, request, protocol.CommandNetworkStart, tabID, "")
	respondToToolRequest(t, service, connection, request, map[string]any{
		"tabId": tabID, "documentId": "resolved-document", "active": true,
		"maxEntries": defaultNetworkMaxEntries, "retainedEntries": 0, "warnings": []string{},
	})
	result := <-resultChannel
	response := decodeToolResponse(t, result)
	if result.IsError || response.Target == nil || response.Target.DocumentID != "resolved-document" {
		t.Fatalf("browserStartNetworkHandler() target = %#v, result = %s", response.Target, toolText(t, result))
	}
}

func TestNetworkBodyHandlerIndependentlyRedactsAndStoresArtifact(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	store, err := artifacts.New(t.TempDir(), time.Hour, artifacts.WithMaxBytes(2_000_000))
	if err != nil {
		t.Fatalf("artifacts.New() error = %v", err)
	}
	service.artifacts = store
	var audit bytes.Buffer
	service.auditLogger = log.New(&audit, "", 0)
	tabID := 8
	maxBytes := minNetworkReadMaxBytes
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserGetNetworkBodyHandler(
			context.Background(),
			mcp.CallToolRequest{},
			networkBodyArgs{
				networkTargetArgs: networkTargetArgs{BrowserID: "browser-a", TabID: &tabID, DocumentID: "document-8"},
				EntryID:           "12",
				Direction:         "response",
				MaxBytes:          &maxBytes,
			},
		)
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	assertNetworkRequestTarget(t, request, protocol.CommandNetworkGetBody, tabID, "document-8")
	body := []byte(`{"ok":true,"password":"extension-leaked-secret"}`)
	respondToToolRequest(t, service, connection, request, networkArtifactWireResult{
		Kind: "responseBody", MIMEType: "application/json", DataBase64: base64.StdEncoding.EncodeToString(body),
		ByteLength: len(body), TabID: tabID, DocumentID: "document-8", EntryID: "12",
		RedactionApplied: false, RedactionRules: []string{}, Warnings: []string{},
	})
	result := <-resultChannel
	if result.IsError {
		t.Fatalf("browserGetNetworkBodyHandler() returned error: %s", toolText(t, result))
	}
	response := decodeToolResponse(t, result)
	artifactID := strings.TrimPrefix(response.ArtifactURI, "browser://artifacts/")
	metadata, stored, err := store.Read(context.Background(), artifactID)
	if err != nil {
		t.Fatalf("artifact Read() error = %v", err)
	}
	if metadata.MIMEType != "application/json" || !metadata.Redaction.Applied ||
		strings.Contains(string(stored), "extension-leaked-secret") ||
		!strings.Contains(string(stored), `"password":"[REDACTED]"`) {
		t.Fatalf("stored body = (%#v, %s)", metadata, stored)
	}
	if strings.Contains(toolText(t, result), "dataBase64") || strings.Contains(audit.String(), "extension-leaked-secret") {
		t.Fatalf("body leaked to result or audit: result=%s audit=%s", toolText(t, result), audit.String())
	}
}

func TestNetworkHARHandlerStoresOnlyValidatedJSON(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	store, err := artifacts.New(t.TempDir(), time.Hour, artifacts.WithMaxBytes(2_000_000))
	if err != nil {
		t.Fatalf("artifacts.New() error = %v", err)
	}
	service.artifacts = store
	tabID := 9
	maxBytes := minNetworkHARMaxBytes
	har := []byte(`{"log":{"version":"1.2","entries":[],"authorization":"Bearer leaked"}}`)
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserExportNetworkHARHandler(
			context.Background(),
			mcp.CallToolRequest{},
			networkHARArgs{
				networkTargetArgs: networkTargetArgs{BrowserID: "browser-a", TabID: &tabID, DocumentID: "document-9"},
				MaxBytes:          &maxBytes,
			},
		)
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	respondToToolRequest(t, service, connection, request, networkArtifactWireResult{
		Kind: "har", MIMEType: "application/json", DataBase64: base64.StdEncoding.EncodeToString(har),
		ByteLength: len(har), TabID: tabID, DocumentID: "document-9", EntryCount: 0,
		RedactionApplied: true, RedactionRules: []string{"authorization"}, Warnings: []string{},
	})
	result := <-resultChannel
	if result.IsError {
		t.Fatalf("browserExportNetworkHARHandler() returned error: %s", toolText(t, result))
	}
	response := decodeToolResponse(t, result)
	_, stored, err := store.Read(context.Background(), strings.TrimPrefix(response.ArtifactURI, "browser://artifacts/"))
	if err != nil || strings.Contains(string(stored), "Bearer leaked") || !json.Valid(stored) {
		t.Fatalf("stored HAR = %s, error = %v", stored, err)
	}
}

func TestNetworkValidationAndGenericBypassFailClosed(t *testing.T) {
	t.Parallel()

	tooMany := maxNetworkReadLimit + 1
	tooSmall := minNetworkReadMaxBytes - 1
	badStatusMin := 500
	badStatusMax := 400
	for _, args := range []networkReadArgs{
		{Cursor: "0"},
		{Cursor: "not-a-cursor"},
		{Limit: &tooMany},
		{MaxBytes: &tooSmall},
		{ResourceTypes: []string{"Fetch", "Fetch"}},
		{StatusMin: &badStatusMin, StatusMax: &badStatusMax},
		{Since: "yesterday"},
	} {
		if _, err := validateNetworkReadArgs(args); err == nil {
			t.Fatalf("validateNetworkReadArgs(%#v) error = nil", args)
		}
	}

	validBody := []byte(`{"ok":true}`)
	validBase64 := base64.StdEncoding.EncodeToString(validBody)
	for _, raw := range []string{
		`null`,
		`{"kind":"responseBody","mimeType":"application/octet-stream","dataBase64":"` + validBase64 + `","byteLength":11,"tabId":1,"documentId":"doc","entryId":"1","entryCount":0,"truncated":false,"redactionApplied":false,"redactionRules":[],"warnings":[]}`,
		`{"kind":"responseBody","mimeType":"application/json","dataBase64":"%%%","byteLength":3,"tabId":1,"documentId":"doc","entryId":"1","entryCount":0,"truncated":false,"redactionApplied":false,"redactionRules":[],"warnings":[]}`,
	} {
		if _, _, err := decodeNetworkArtifact(json.RawMessage(raw), "responseBody", minNetworkReadMaxBytes, nil); err == nil {
			t.Fatalf("decodeNetworkArtifact(%s) error = nil", raw)
		}
	}

	service, connection, _ := newTestService(t)
	for _, command := range []string{
		protocol.CommandNetworkStart, protocol.CommandNetworkStop, protocol.CommandNetworkClear,
		protocol.CommandNetworkRead, protocol.CommandNetworkGetBody, protocol.CommandNetworkExportHAR,
	} {
		result, err := service.browserSendCommandHandler(
			context.Background(), mcp.CallToolRequest{},
			sendCommandArgs{BrowserID: "browser-a", Command: command},
		)
		if err != nil || result == nil || !result.IsError {
			t.Fatalf("browserSendCommandHandler(%q) = (%#v, %v), want tool error", command, result, err)
		}
	}
	select {
	case message := <-connection.messages:
		t.Fatalf("generic network command reached browser: %#v", message)
	default:
	}
}

func assertNetworkRequestTarget(t *testing.T, request protocol.Message, command string, tabID int, documentID string) {
	t.Helper()
	if request.Command != command || request.Target == nil || request.Target.TabID == nil ||
		*request.Target.TabID != tabID || request.Target.FrameID == nil || *request.Target.FrameID != 0 ||
		request.Target.DocumentID != documentID {
		t.Fatalf("network request = %#v", request)
	}
}
