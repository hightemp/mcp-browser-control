package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestDownloadListRoutesToOneBrowserAndReturnsSafeMetadata(t *testing.T) {
	t.Parallel()

	service, connection, otherConnection := newTestService(t)
	limit := 10
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserListDownloadsHandler(
			context.Background(), mcp.CallToolRequest{},
			downloadListArgs{downloadBaseArgs: downloadBaseArgs{BrowserID: "browser-a"}, Limit: &limit, State: "complete"},
		)
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	select {
	case leaked := <-otherConnection.messages:
		t.Fatalf("download request leaked to another browser: %#v", leaked)
	default:
	}
	if request.Command != protocol.CommandDownloadsList || request.Target != nil {
		t.Fatalf("download request = %#v", request)
	}
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil || params["limit"] != float64(limit) ||
		params["state"] != "complete" || params["allowIncognito"] != false {
		t.Fatalf("download list params = %#v, error = %v", params, err)
	}
	respondToToolRequest(t, service, connection, request, downloadResult("list", "", false, downloadItem(7)))
	result := <-resultChannel
	text := toolText(t, result)
	if result.IsError || !strings.Contains(text, `"fileName":"report.zip"`) || strings.Contains(text, "/home/") {
		t.Fatalf("browserListDownloadsHandler() result = %s", text)
	}
}

func TestDownloadCreateAndEraseAreBoundedAndAuditedWithoutSecrets(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	var audit bytes.Buffer
	service.auditLogger = log.New(&audit, "", 0)
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserCreateDownloadHandler(
			context.Background(), mcp.CallToolRequest{},
			downloadCreateArgs{
				downloadBaseArgs: downloadBaseArgs{BrowserID: "browser-a"},
				URL:              "https://example.com/archive.zip?token=caller-secret",
			},
		)
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	if request.Command != protocol.CommandDownloadsCreate {
		t.Fatalf("create command = %q", request.Command)
	}
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil || !strings.Contains(params["url"].(string), "caller-secret") {
		t.Fatalf("create params = %#v, error = %v", params, err)
	}
	created := downloadResult("create", "create", true)
	created.DownloadID = 91
	created.Warnings = []string{"HTTP(S) downloads may include cookies already stored for the destination host"}
	respondToToolRequest(t, service, connection, request, created)
	result := <-resultChannel
	if result.IsError || strings.Contains(toolText(t, result), "caller-secret") || strings.Contains(audit.String(), "caller-secret") {
		t.Fatalf("download create leaked URL data: result=%s audit=%s", toolText(t, result), audit.String())
	}

	unconfirmed, err := service.browserEraseDownloadHistoryHandler(
		context.Background(), mcp.CallToolRequest{},
		downloadEraseArgs{downloadIDArgs: downloadIDArgs{downloadBaseArgs: downloadBaseArgs{BrowserID: "browser-a"}, DownloadID: 91}},
	)
	if err != nil || unconfirmed == nil || !unconfirmed.IsError ||
		decodeToolResponse(t, unconfirmed).Error.Code != protocol.CodeConfirmationRequired {
		t.Fatalf("unconfirmed erase = (%#v, %v)", unconfirmed, err)
	}

	resultChannel = make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserEraseDownloadHistoryHandler(
			context.Background(), mcp.CallToolRequest{},
			downloadEraseArgs{
				downloadIDArgs: downloadIDArgs{downloadBaseArgs: downloadBaseArgs{BrowserID: "browser-a"}, DownloadID: 91},
				Confirm:        true,
			},
		)
		resultChannel <- result
	}()
	request = receiveToolMessage(t, connection.messages)
	erased := downloadResult("erase", "erase", true)
	erased.DownloadID = 91
	erased.ErasedIDs = []int64{91}
	erased.Warnings = []string{"The downloaded file was not deleted"}
	respondToToolRequest(t, service, connection, request, erased)
	result = <-resultChannel
	if result.IsError || !strings.Contains(toolText(t, result), `"erasedIds":[91]`) {
		t.Fatalf("erase result = %s", toolText(t, result))
	}
}

func TestDownloadDecoderAndGenericBoundaryFailClosed(t *testing.T) {
	t.Parallel()

	valid := downloadResult("item", "", false, downloadItem(7))
	valid.DownloadID = 7
	if _, err := decodeDownloadResult(mustJSON(t, valid), protocol.CommandDownloadsGet, false); err != nil {
		t.Fatalf("decodeDownloadResult(valid) error = %v", err)
	}
	incognito := valid
	incognito.Downloads = append([]downloadWireItem(nil), valid.Downloads...)
	incognito.Downloads[0].Incognito = true
	if _, err := decodeDownloadResult(mustJSON(t, incognito), protocol.CommandDownloadsGet, false); err == nil {
		t.Fatal("incognito result bypassed disabled action policy")
	}
	if _, err := decodeDownloadResult(mustJSON(t, incognito), protocol.CommandDownloadsGet, true); err != nil {
		t.Fatalf("incognito-enabled decode error = %v", err)
	}
	invalidPath := valid
	invalidPath.Downloads = append([]downloadWireItem(nil), valid.Downloads...)
	invalidPath.Downloads[0].FileName = "/home/alice/report.zip"
	for _, raw := range []json.RawMessage{
		json.RawMessage(`null`),
		mustJSON(t, invalidPath),
		json.RawMessage(`{"kind":"item","downloads":[],"downloadId":7,"totalMatched":1,"nextCursor":"","operation":"","changed":false,"erasedIds":[],"warnings":[],"extra":true}`),
	} {
		if _, err := decodeDownloadResult(raw, protocol.CommandDownloadsGet, false); err == nil {
			t.Fatalf("decodeDownloadResult(%s) error = nil", raw)
		}
	}

	service, connection, _ := newTestService(t)
	for _, command := range []string{
		protocol.CommandDownloadsList, protocol.CommandDownloadsGet, protocol.CommandDownloadsCreate,
		protocol.CommandDownloadsPause, protocol.CommandDownloadsResume, protocol.CommandDownloadsCancel,
		protocol.CommandDownloadsErase,
	} {
		result, err := service.browserSendCommandHandler(
			context.Background(), mcp.CallToolRequest{}, sendCommandArgs{BrowserID: "browser-a", Command: command},
		)
		if err != nil || result == nil || !result.IsError || decodeToolResponse(t, result).Error.Code != protocol.CodeInvalidCommand {
			t.Fatalf("browserSendCommandHandler(%q) = (%#v, %v)", command, result, err)
		}
	}
	select {
	case message := <-connection.messages:
		t.Fatalf("generic download command reached browser: %#v", message)
	default:
	}
}

func downloadResult(kind, operation string, changed bool, items ...downloadWireItem) downloadWireResult {
	return downloadWireResult{
		Kind: kind, Downloads: items, TotalMatched: len(items), Operation: operation,
		Changed: changed, ErasedIDs: []int64{}, Warnings: []string{},
	}
}

func downloadItem(id int64) downloadWireItem {
	return downloadWireItem{
		ID: id, SourceURL: "https://example.com/report.zip", FinalURL: "https://cdn.example.com/report.zip",
		FileName: "report.zip", PathRedacted: true, State: "complete", Danger: "safe",
		BytesReceived: 100, TotalBytes: 100, FileSize: 100, Exists: true, MIME: "application/zip",
		StartTime: "2026-08-24T10:00:00Z", EndTime: "2026-08-24T10:00:01Z",
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
