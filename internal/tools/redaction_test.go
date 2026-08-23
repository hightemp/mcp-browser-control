package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/redaction"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestServiceRedactsBrowserResultBeforeMCP(t *testing.T) {
	t.Parallel()

	service, connectionA, _ := newTestService(t)
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserSendCommandHandler(
			context.Background(),
			mcp.CallToolRequest{},
			sendCommandArgs{BrowserID: "browser-a", Command: "page.custom"},
		)
		resultChannel <- result
	}()

	request := receiveToolMessage(t, connectionA.messages)
	response, err := protocol.NewResponse(request.RequestID, "browser-a", map[string]any{
		"headers": map[string]string{
			"Authorization": "Bearer browser-auth-secret",
			"Cookie":        "sid=browser-cookie-secret",
		},
		"formData":      map[string]string{"token": "browser-form-secret"},
		"clipboardText": "browser-clipboard-secret",
		"filePath":      "/home/ada/browser-private.txt",
		"url":           "https://example.test/?token=browser-query-secret&safe=yes",
		"safe":          "visible",
	}, nil)
	if err != nil {
		t.Fatalf("protocol.NewResponse() error = %v", err)
	}
	if !service.router.HandleResponse("browser-a", connectionA.ID(), response) {
		t.Fatal("HandleResponse() = false")
	}

	result := <-resultChannel
	if result.IsError {
		t.Fatalf("browserSendCommandHandler() returned error: %s", toolText(t, result))
	}
	text := toolText(t, result)
	for _, secret := range []string{
		"browser-auth-secret", "browser-cookie-secret", "browser-form-secret",
		"browser-clipboard-secret", "/home/ada/browser-private.txt", "browser-query-secret",
	} {
		if strings.Contains(text, secret) {
			t.Errorf("MCP result contains %q: %s", secret, text)
		}
	}
	decoded := decodeToolResponse(t, result)
	if !strings.Contains(strings.Join(decoded.Warnings, " "), "redacted by the server") {
		t.Fatalf("warnings = %#v", decoded.Warnings)
	}
	data, ok := decoded.Data.(map[string]any)
	if !ok || data["safe"] != "visible" || !strings.Contains(data["url"].(string), "safe=yes") {
		t.Fatalf("safe data = %#v", decoded.Data)
	}
}

func TestServiceRejectsOversizedFinalToolEnvelope(t *testing.T) {
	t.Parallel()

	service, connectionA, _ := newTestService(t)
	service.resultLimits = redaction.DefaultLimits(128)
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserSendCommandHandler(
			context.Background(),
			mcp.CallToolRequest{},
			sendCommandArgs{BrowserID: "browser-a", Command: "page.custom"},
		)
		resultChannel <- result
	}()

	request := receiveToolMessage(t, connectionA.messages)
	response, err := protocol.NewResponse(
		request.RequestID,
		"browser-a",
		map[string]any{"safe": "ok"},
		nil,
	)
	if err != nil {
		t.Fatalf("protocol.NewResponse() error = %v", err)
	}
	if !service.router.HandleResponse("browser-a", connectionA.ID(), response) {
		t.Fatal("HandleResponse() = false")
	}

	result := <-resultChannel
	decoded := decodeToolResponse(t, result)
	if !result.IsError || decoded.Error == nil || decoded.Error.Code != protocol.CodePayloadTooLarge {
		t.Fatalf("oversized response = %#v", decoded)
	}
}

func TestToolErrorRedactsMessageAndDetails(t *testing.T) {
	t.Parallel()

	protocolErr := protocol.NewError(
		protocol.CodeInvalidMessage,
		"Authorization: Bearer error-message-secret at /home/ada/private.txt",
		false,
	)
	protocolErr.Details = map[string]any{
		"Cookie": "sid=error-cookie-secret",
		"field":  map[string]any{"name": "password", "value": "error-password-secret"},
	}
	result, err := errorResult(protocolErr)
	if err != nil {
		t.Fatalf("errorResult() error = %v", err)
	}
	text := toolText(t, result)
	for _, secret := range []string{
		"error-message-secret", "/home/ada/private.txt", "error-cookie-secret", "error-password-secret",
	} {
		if strings.Contains(text, secret) {
			t.Errorf("tool error contains %q: %s", secret, text)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("tool error is invalid JSON: %v", err)
	}
}

func TestBrowserResourceRejectsOversizedSanitizedOutput(t *testing.T) {
	t.Parallel()

	service, _, _ := newTestService(t)
	service.resultLimits = redaction.DefaultLimits(64)
	_, err := service.jsonResource(
		"browser://instances",
		map[string]any{"safe": strings.Repeat("x", 128)},
	)
	resultErr := protocol.ErrorFrom(err)
	if resultErr == nil || resultErr.Code != protocol.CodePayloadTooLarge {
		t.Fatalf("jsonResource() error = %v, want %s", err, protocol.CodePayloadTooLarge)
	}
}
