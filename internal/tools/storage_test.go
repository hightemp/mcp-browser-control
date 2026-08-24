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

func TestStorageListRoutesToOneDocumentAndKeepsValuesMasked(t *testing.T) {
	t.Parallel()

	service, connection, otherConnection := newTestService(t)
	tabID := 7
	limit := 10
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserListStorageItemsHandler(
			context.Background(), mcp.CallToolRequest{},
			storageListArgs{
				storageTargetArgs: storageTargetArgs{BrowserID: "browser-a", TabID: &tabID, DocumentID: "document-7"},
				Origin:            "https://example.com", StorageType: "localStorage", Limit: &limit,
			},
		)
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	select {
	case leaked := <-otherConnection.messages:
		t.Fatalf("storage request leaked to another browser: %#v", leaked)
	default:
	}
	assertStorageRequestTarget(t, request, protocol.CommandStorageList, tabID, "document-7")
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil ||
		params["origin"] != "https://example.com" || params["storageType"] != "localStorage" ||
		params["limit"] != float64(limit) {
		t.Fatalf("storage list params = %#v, error = %v", params, err)
	}
	respondToToolRequest(t, service, connection, request, storageItemResult("items", tabID, "document-7", false))
	result := <-resultChannel
	text := toolText(t, result)
	if result.IsError || !strings.Contains(text, maskedStorageValue) || strings.Contains(text, "browser-secret") {
		t.Fatalf("browserListStorageItemsHandler() result = %s", text)
	}
}

func TestSensitiveStorageGetPreservesBoundedValueAndSafeAudit(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	var audit bytes.Buffer
	service.auditLogger = log.New(&audit, "", 0)
	tabID := 8
	include := true
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserGetStorageItemHandler(
			context.Background(), mcp.CallToolRequest{},
			storageGetArgs{
				storageTargetArgs: storageTargetArgs{BrowserID: "browser-a", TabID: &tabID, DocumentID: "document-8"},
				Origin:            "https://example.com", StorageType: "sessionStorage", Key: "token", IncludeValue: &include,
			},
		)
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	assertStorageRequestTarget(t, request, protocol.CommandStorageGetSensitive, tabID, "document-8")
	response := storageItemResult("item", tabID, "document-8", true)
	response.StorageType = "sessionStorage"
	respondToToolRequest(t, service, connection, request, response)
	result := <-resultChannel
	if result.IsError || !strings.Contains(toolText(t, result), "browser-secret") {
		t.Fatalf("browserGetStorageItemHandler() result = %s", toolText(t, result))
	}
	if strings.Contains(audit.String(), "browser-secret") || strings.Contains(audit.String(), "token") ||
		!strings.Contains(audit.String(), "valuesIncluded=true") {
		t.Fatalf("storage audit = %q", audit.String())
	}
}

func TestStorageSetAndConfirmedClearDoNotEchoData(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	var audit bytes.Buffer
	service.auditLogger = log.New(&audit, "", 0)
	tabID := 9
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserSetStorageItemHandler(
			context.Background(), mcp.CallToolRequest{},
			storageSetArgs{
				storageTargetArgs: storageTargetArgs{BrowserID: "browser-a", TabID: &tabID, DocumentID: "document-9"},
				Origin:            "https://example.com", StorageType: "localStorage", Key: "theme", Value: "caller-secret",
			},
		)
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	assertStorageRequestTarget(t, request, protocol.CommandStorageSet, tabID, "document-9")
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil || params["value"] != "caller-secret" {
		t.Fatalf("storage set params = %#v, error = %v", params, err)
	}
	respondToToolRequest(t, service, connection, request, storageMutationResult(tabID, "document-9", "set", true))
	result := <-resultChannel
	if result.IsError || strings.Contains(toolText(t, result), "caller-secret") || strings.Contains(audit.String(), "caller-secret") {
		t.Fatalf("storage set leaked data: result=%s audit=%s", toolText(t, result), audit.String())
	}

	unconfirmed, err := service.browserClearOriginStorageHandler(
		context.Background(), mcp.CallToolRequest{},
		storageClearArgs{storageTargetArgs: storageTargetArgs{BrowserID: "browser-a"}, Origin: "https://example.com", Types: []string{"localStorage"}},
	)
	if err != nil || unconfirmed == nil || !unconfirmed.IsError ||
		decodeToolResponse(t, unconfirmed).Error.Code != protocol.CodeConfirmationRequired {
		t.Fatalf("unconfirmed clear = (%#v, %v)", unconfirmed, err)
	}

	resultChannel = make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserClearOriginStorageHandler(
			context.Background(), mcp.CallToolRequest{},
			storageClearArgs{
				storageTargetArgs: storageTargetArgs{BrowserID: "browser-a", TabID: &tabID, DocumentID: "document-9"},
				Origin:            "https://example.com", Types: []string{"localStorage", "indexedDB"}, Confirm: true,
			},
		)
		resultChannel <- result
	}()
	request = receiveToolMessage(t, connection.messages)
	assertStorageRequestTarget(t, request, protocol.CommandStorageClear, tabID, "document-9")
	respondToToolRequest(t, service, connection, request, storageWireResult{
		Kind: "clear", TabID: tabID, DocumentID: "document-9", Origin: "https://example.com",
		Supported: true, Items: []storageWireItem{}, Caches: []storageCacheWire{}, Databases: []storageDatabaseWire{},
		Operation: "clear", RequestedTypes: []string{"localStorage", "indexedDB"},
		ClearedTypes: []string{"localStorage", "indexedDB"}, ClearedCounts: map[string]int{"localStorage": 2, "indexedDB": 1}, Warnings: []string{},
	})
	result = <-resultChannel
	if result.IsError || !strings.Contains(toolText(t, result), `"indexedDB":1`) {
		t.Fatalf("browserClearOriginStorageHandler() result = %s", toolText(t, result))
	}
}

func TestStorageMetadataValidationAndGenericBoundaries(t *testing.T) {
	t.Parallel()

	tooMany := maxStorageLimit + 1
	for _, args := range []storageListArgs{
		{Origin: "https://example.com/path", StorageType: "localStorage"},
		{Origin: "https://example.com", StorageType: "unknown"},
		{Origin: "https://example.com", StorageType: "localStorage", Cursor: "0"},
		{Origin: "https://example.com", StorageType: "localStorage", Limit: &tooMany},
	} {
		if _, err := validateStorageListArgs(args); err == nil {
			t.Fatalf("validateStorageListArgs(%#v) error = nil", args)
		}
	}

	target := &protocol.Target{TabID: intPointer(1), DocumentID: "doc"}
	for _, raw := range []string{
		`null`,
		`{"kind":"items","tabId":1,"documentId":"doc","origin":"https://example.com","storageType":"localStorage","valuesIncluded":false,"items":[],"caches":[],"databases":[],"totalMatched":0,"nextCursor":"","operation":"","changed":false,"supported":true,"requestedTypes":[],"clearedTypes":[],"clearedCounts":null,"warnings":[],"extra":true}`,
		`{"kind":"items","tabId":1,"documentId":"doc","origin":"https://example.com","storageType":"localStorage","valuesIncluded":false,"items":[{"key":"token","value":"leaked","valueIncluded":false,"valueLength":6}],"caches":[],"databases":[],"totalMatched":1,"nextCursor":"","operation":"","changed":false,"supported":true,"requestedTypes":[],"clearedTypes":[],"clearedCounts":null,"warnings":[]}`,
	} {
		if _, err := decodeStorageResult(json.RawMessage(raw), protocol.CommandStorageList, target); err == nil {
			t.Fatalf("decodeStorageResult(%s) error = nil", raw)
		}
	}

	service, connection, _ := newTestService(t)
	for _, command := range []string{
		protocol.CommandStorageList, protocol.CommandStorageListSensitive,
		protocol.CommandStorageGet, protocol.CommandStorageGetSensitive,
		protocol.CommandStorageSet, protocol.CommandStorageRemove,
		protocol.CommandStorageCacheMetadata, protocol.CommandStorageIndexedDBMetadata,
		protocol.CommandStorageClear,
	} {
		result, err := service.browserSendCommandHandler(
			context.Background(), mcp.CallToolRequest{}, sendCommandArgs{BrowserID: "browser-a", Command: command},
		)
		if err != nil || result == nil || !result.IsError {
			t.Fatalf("browserSendCommandHandler(%q) = (%#v, %v), want tool error", command, result, err)
		}
	}
	select {
	case message := <-connection.messages:
		t.Fatalf("generic storage command reached browser: %#v", message)
	default:
	}
}

func storageItemResult(kind string, tabID int, documentID string, sensitive bool) storageWireResult {
	value := maskedStorageValue
	if sensitive {
		value = "browser-secret"
	}
	return storageWireResult{
		Kind: kind, TabID: tabID, DocumentID: documentID, Origin: "https://example.com",
		StorageType: "localStorage", ValuesIncluded: sensitive,
		Items:  []storageWireItem{{Key: "token", Value: value, ValueIncluded: sensitive, ValueLength: len("browser-secret")}},
		Caches: []storageCacheWire{}, Databases: []storageDatabaseWire{}, TotalMatched: 1,
		Supported: true, RequestedTypes: []string{}, ClearedTypes: []string{}, Warnings: []string{},
	}
}

func storageMutationResult(tabID int, documentID, operation string, changed bool) storageWireResult {
	return storageWireResult{
		Kind: "mutation", TabID: tabID, DocumentID: documentID, Origin: "https://example.com",
		StorageType: "localStorage", Items: []storageWireItem{}, Caches: []storageCacheWire{},
		Databases: []storageDatabaseWire{}, Operation: operation, Changed: changed, Supported: true,
		RequestedTypes: []string{}, ClearedTypes: []string{}, Warnings: []string{},
	}
}

func assertStorageRequestTarget(t *testing.T, request protocol.Message, command string, tabID int, documentID string) {
	t.Helper()
	if request.Command != command || request.Target == nil || request.Target.TabID == nil ||
		*request.Target.TabID != tabID || request.Target.FrameID == nil || *request.Target.FrameID != 0 ||
		request.Target.DocumentID != documentID {
		t.Fatalf("storage request = %#v", request)
	}
}
