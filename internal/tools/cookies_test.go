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

func TestCookieListRoutesToOneRootDocumentAndKeepsValuesMasked(t *testing.T) {
	t.Parallel()

	service, connection, otherConnection := newTestService(t)
	tabID := 7
	documentID := "document-7"
	limit := 10
	secure := true
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserListCookiesHandler(
			context.Background(),
			mcp.CallToolRequest{},
			cookieListArgs{
				cookieTargetArgs: cookieTargetArgs{BrowserID: "browser-a", TabID: &tabID, DocumentID: documentID},
				URL:              "https://example.com/account",
				Domain:           ".example.com",
				Secure:           &secure,
				Limit:            &limit,
				PartitionKey: &cookiePartitionKey{
					TopLevelSite: "https://example.com",
				},
			},
		)
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	select {
	case leaked := <-otherConnection.messages:
		t.Fatalf("cookie request leaked to another browser: %#v", leaked)
	default:
	}
	assertCookieRequestTarget(t, request, protocol.CommandCookiesList, tabID, documentID)
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil ||
		params["url"] != "https://example.com/account" || params["domain"] != ".example.com" ||
		params["secure"] != true || params["limit"] != float64(limit) {
		t.Fatalf("cookie list params = %#v, error = %v", params, err)
	}
	respondToToolRequest(t, service, connection, request, maskedCookieResult("list", tabID, documentID))
	result := <-resultChannel
	text := toolText(t, result)
	if result.IsError || !strings.Contains(text, maskedCookieValue) || strings.Contains(text, "browser-secret") {
		t.Fatalf("browserListCookiesHandler() result = %s", text)
	}
}

func TestSensitiveCookieReadUsesSeparateCapabilityAndSafeAudit(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	var audit bytes.Buffer
	service.auditLogger = log.New(&audit, "", 0)
	tabID := 8
	include := true
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserGetCookieHandler(
			context.Background(),
			mcp.CallToolRequest{},
			cookieGetArgs{
				cookieTargetArgs: cookieTargetArgs{BrowserID: "browser-a", TabID: &tabID, DocumentID: "document-8"},
				URL:              "https://example.com/",
				Name:             "session",
				IncludeValue:     &include,
			},
		)
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	assertCookieRequestTarget(t, request, protocol.CommandCookiesGetSensitive, tabID, "document-8")
	response := maskedCookieResult("get", tabID, "document-8")
	response.ValuesIncluded = true
	response.Cookies[0].Value = "browser-secret"
	response.Cookies[0].ValueIncluded = true
	response.Cookies[0].ValueLength = len("browser-secret")
	respondToToolRequest(t, service, connection, request, response)
	result := <-resultChannel
	if result.IsError || !strings.Contains(toolText(t, result), "browser-secret") {
		t.Fatalf("browserGetCookieHandler() result = %s", toolText(t, result))
	}
	if strings.Contains(audit.String(), "browser-secret") || !strings.Contains(audit.String(), "valuesIncluded=true") {
		t.Fatalf("cookie audit = %q", audit.String())
	}
}

func TestCookieSetAndRemoveUseTypedCommandsWithoutEchoingValue(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	var audit bytes.Buffer
	service.auditLogger = log.New(&audit, "", 0)
	tabID := 9
	secure := true
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserSetCookieHandler(
			context.Background(), mcp.CallToolRequest{},
			cookieSetArgs{
				cookieTargetArgs: cookieTargetArgs{BrowserID: "browser-a", TabID: &tabID, DocumentID: "document-9"},
				URL:              "https://example.com/", Name: "session", Value: "caller-secret",
				Path: "/", Secure: &secure, SameSite: "lax",
			},
		)
		resultChannel <- result
	}()
	request := receiveToolMessage(t, connection.messages)
	assertCookieRequestTarget(t, request, protocol.CommandCookiesSet, tabID, "document-9")
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil || params["value"] != "caller-secret" {
		t.Fatalf("cookie set params = %#v, error = %v", params, err)
	}
	respondToToolRequest(t, service, connection, request, maskedCookieResult("set", tabID, "document-9"))
	result := <-resultChannel
	if result.IsError || strings.Contains(toolText(t, result), "caller-secret") || strings.Contains(audit.String(), "caller-secret") {
		t.Fatalf("cookie set leaked value: result=%s audit=%s", toolText(t, result), audit.String())
	}

	resultChannel = make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserRemoveCookieHandler(
			context.Background(), mcp.CallToolRequest{},
			cookieRemoveArgs{
				cookieTargetArgs: cookieTargetArgs{BrowserID: "browser-a", TabID: &tabID, DocumentID: "document-9"},
				URL:              "https://example.com/", Name: "session",
			},
		)
		resultChannel <- result
	}()
	request = receiveToolMessage(t, connection.messages)
	assertCookieRequestTarget(t, request, protocol.CommandCookiesRemove, tabID, "document-9")
	respondToToolRequest(t, service, connection, request, cookieWireResult{
		Kind: "remove", TabID: tabID, DocumentID: "document-9", Origin: "https://example.com",
		ValuesIncluded: false, Cookies: []cookieWire{}, TotalMatched: 0, Removed: true, Warnings: []string{},
	})
	result = <-resultChannel
	if result.IsError || !strings.Contains(toolText(t, result), `"removed":true`) {
		t.Fatalf("browserRemoveCookieHandler() result = %s", toolText(t, result))
	}
}

func TestCookieArgumentResultAndGenericBoundariesFailClosed(t *testing.T) {
	t.Parallel()

	tooMany := maxCookieLimit + 1
	for _, args := range []cookieListArgs{
		{URL: "chrome://settings", Limit: &tooMany},
		{URL: "https://example.com/", Domain: "other.example"},
		{URL: "https://example.com/", Cursor: "0"},
		{URL: "https://example.com/", Path: "relative"},
		{URL: "https://example.com/", PartitionKey: &cookiePartitionKey{TopLevelSite: "https://other.example"}},
	} {
		if _, err := validateCookieListArgs(args); err == nil {
			t.Fatalf("validateCookieListArgs(%#v) error = nil", args)
		}
	}

	target := &protocol.Target{TabID: intPointer(1), DocumentID: "doc"}
	for _, raw := range []string{
		`null`,
		`{"kind":"list","tabId":1,"documentId":"doc","origin":"https://example.com","valuesIncluded":false,"cookies":[],"totalMatched":0,"nextCursor":"","removed":false,"warnings":[],"extra":true}`,
		`{"kind":"list","tabId":1,"documentId":"doc","origin":"https://example.com","valuesIncluded":false,"cookies":[{"name":"session","value":"leaked","valueIncluded":false,"valueLength":6,"domain":"example.com","hostOnly":true,"path":"/","secure":true,"httpOnly":true,"sameSite":"lax","session":true,"storeId":"0"}],"totalMatched":1,"nextCursor":"","removed":false,"warnings":[]}`,
	} {
		if _, err := decodeCookieResult(json.RawMessage(raw), protocol.CommandCookiesList, target); err == nil {
			t.Fatalf("decodeCookieResult(%s) error = nil", raw)
		}
	}

	service, connection, _ := newTestService(t)
	for _, command := range []string{
		protocol.CommandCookiesList, protocol.CommandCookiesListSensitive,
		protocol.CommandCookiesGet, protocol.CommandCookiesGetSensitive,
		protocol.CommandCookiesSet, protocol.CommandCookiesRemove,
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
		t.Fatalf("generic cookie command reached browser: %#v", message)
	default:
	}
}

func maskedCookieResult(kind string, tabID int, documentID string) cookieWireResult {
	return cookieWireResult{
		Kind:           kind,
		TabID:          tabID,
		DocumentID:     documentID,
		Origin:         "https://example.com",
		ValuesIncluded: false,
		Cookies: []cookieWire{{
			Name: "session", Value: maskedCookieValue, ValueIncluded: false, ValueLength: len("browser-secret"),
			Domain: "example.com", HostOnly: true, Path: "/", Secure: true, HTTPOnly: true,
			SameSite: "lax", Session: true, StoreID: "0",
		}},
		TotalMatched: 1,
		Warnings:     []string{},
	}
}

func assertCookieRequestTarget(t *testing.T, request protocol.Message, command string, tabID int, documentID string) {
	t.Helper()
	if request.Command != command || request.Target == nil || request.Target.TabID == nil ||
		*request.Target.TabID != tabID || request.Target.FrameID == nil || *request.Target.FrameID != 0 ||
		request.Target.DocumentID != documentID {
		t.Fatalf("cookie request = %#v", request)
	}
}

func intPointer(value int) *int { return &value }
