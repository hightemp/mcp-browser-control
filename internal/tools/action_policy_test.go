package tools

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/policy"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestActionPolicyRejectsDestinationBeforeBrowserDispatch(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	var audit bytes.Buffer
	service.actionPolicy = mustActionPolicy(
		t,
		[]string{"https://allowed.example"},
		nil,
		false,
		log.New(&audit, "", 0),
	)
	tests := []struct {
		name string
		call func() (*mcp.CallToolResult, error)
	}{
		{
			name: "navigate outside allowlist",
			call: func() (*mcp.CallToolResult, error) {
				return service.browserNavigateTabHandler(
					context.Background(),
					mcp.CallToolRequest{},
					tabNavigateArgs{
						BrowserID: "browser-a",
						URL:       "https://blocked.example/path?token=must-not-be-logged",
					},
				)
			},
		},
		{
			name: "restricted scheme",
			call: func() (*mcp.CallToolResult, error) {
				return service.browserCreateTabHandler(
					context.Background(),
					mcp.CallToolRequest{},
					tabCreateArgs{BrowserID: "browser-a", URL: "chrome://settings"},
				)
			},
		},
		{
			name: "cookie URL outside allowlist",
			call: func() (*mcp.CallToolResult, error) {
				return service.browserListCookiesHandler(
					context.Background(),
					mcp.CallToolRequest{},
					cookieListArgs{
						cookieTargetArgs: cookieTargetArgs{BrowserID: "browser-a"},
						URL:              "https://blocked.example/path?token=must-not-be-logged",
					},
				)
			},
		},
		{
			name: "storage origin outside allowlist",
			call: func() (*mcp.CallToolResult, error) {
				return service.browserListStorageItemsHandler(
					context.Background(), mcp.CallToolRequest{},
					storageListArgs{
						storageTargetArgs: storageTargetArgs{BrowserID: "browser-a"},
						Origin:            "https://blocked.example", StorageType: "localStorage",
					},
				)
			},
		},
		{
			name: "download URL outside allowlist",
			call: func() (*mcp.CallToolResult, error) {
				return service.browserCreateDownloadHandler(
					context.Background(), mcp.CallToolRequest{},
					downloadCreateArgs{
						downloadBaseArgs: downloadBaseArgs{BrowserID: "browser-a"},
						URL:              "https://blocked.example/path?token=must-not-be-logged",
					},
				)
			},
		},
		{
			name: "incognito window",
			call: func() (*mcp.CallToolResult, error) {
				incognito := true
				return service.browserCreateWindowHandler(
					context.Background(),
					mcp.CallToolRequest{},
					windowCreateArgs{BrowserID: "browser-a", Incognito: &incognito},
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.call()
			if err != nil || result == nil || !result.IsError {
				t.Fatalf("handler result = (%#v, %v), want policy error", result, err)
			}
			response := decodeToolResponse(t, result)
			if response.Error == nil || response.Error.Code != protocol.CodeRestrictedURL {
				t.Fatalf("error = %#v, want %s", response.Error, protocol.CodeRestrictedURL)
			}
			select {
			case request := <-connection.messages:
				t.Fatalf("browser received denied request: %#v", request)
			default:
			}
		})
	}
	if strings.Contains(audit.String(), "must-not-be-logged") || strings.Contains(audit.String(), "/path") {
		t.Fatalf("audit log leaked URL data: %s", audit.String())
	}
}

func TestActionPolicyPreflightsCurrentTabOrigin(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	service.actionPolicy = mustActionPolicy(
		t,
		[]string{"https://allowed.example"},
		nil,
		false,
		nil,
	)
	tabID := 42

	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserPageInfoHandler(
			context.Background(),
			mcp.CallToolRequest{},
			targetedArgs{BrowserID: "browser-a", TabID: &tabID},
		)
		resultChannel <- result
	}()

	preflight := receiveToolMessage(t, connection.messages)
	if preflight.Command != protocol.CommandTabsGet {
		t.Fatalf("preflight command = %q, want %q", preflight.Command, protocol.CommandTabsGet)
	}
	respondToToolRequest(t, service, connection, preflight, map[string]any{
		"tab": map[string]any{
			"id":        tabID,
			"url":       "https://blocked.example/private?token=hidden",
			"incognito": false,
		},
	})

	select {
	case result := <-resultChannel:
		response := decodeToolResponse(t, result)
		if !result.IsError || response.Error == nil || response.Error.Code != protocol.CodeRestrictedURL {
			t.Fatalf("handler result = %#v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for policy result")
	}
	select {
	case request := <-connection.messages:
		t.Fatalf("browser received command after denied preflight: %#v", request)
	default:
	}
}

func TestActionPolicyRejectsTabCreationInIncognitoWindow(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	service.actionPolicy = mustActionPolicy(t, nil, nil, false, nil)
	windowID := 9
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserCreateTabHandler(
			context.Background(),
			mcp.CallToolRequest{},
			tabCreateArgs{BrowserID: "browser-a", WindowID: &windowID},
		)
		resultChannel <- result
	}()

	preflight := receiveToolMessage(t, connection.messages)
	if preflight.Command != protocol.CommandWindowsGet {
		t.Fatalf("preflight command = %q, want %q", preflight.Command, protocol.CommandWindowsGet)
	}
	respondToToolRequest(t, service, connection, preflight, map[string]any{
		"window": map[string]any{"id": windowID, "incognito": true},
	})
	select {
	case result := <-resultChannel:
		response := decodeToolResponse(t, result)
		if !result.IsError || response.Error == nil || response.Error.Code != protocol.CodeRestrictedURL {
			t.Fatalf("handler result = %#v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for incognito policy result")
	}
	select {
	case request := <-connection.messages:
		t.Fatalf("browser received tab creation after denied preflight: %#v", request)
	default:
	}
}

func TestActionPolicyAllowsCommandAfterSuccessfulPreflight(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	service.actionPolicy = mustActionPolicy(
		t,
		[]string{"https://allowed.example"},
		nil,
		false,
		nil,
	)
	tabID := 43
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserPageInfoHandler(
			context.Background(),
			mcp.CallToolRequest{},
			targetedArgs{BrowserID: "browser-a", TabID: &tabID},
		)
		resultChannel <- result
	}()

	preflight := receiveToolMessage(t, connection.messages)
	respondToToolRequest(t, service, connection, preflight, map[string]any{
		"tab": map[string]any{
			"id":        tabID,
			"url":       "https://allowed.example/page",
			"incognito": false,
		},
	})
	request := receiveToolMessage(t, connection.messages)
	if request.Command != protocol.CommandPageInfo {
		t.Fatalf("command = %q, want %q", request.Command, protocol.CommandPageInfo)
	}
	respondToToolRequest(t, service, connection, request, map[string]any{"title": "Allowed"})
	select {
	case result := <-resultChannel:
		if result == nil || result.IsError {
			t.Fatalf("handler result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for allowed result")
	}
}

func TestActionPolicyNeverBlocksEmulationCleanup(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	service.actionPolicy = mustActionPolicy(
		t,
		[]string{"https://allowed.example"},
		nil,
		false,
		nil,
	)
	tabID := 44
	resultChannel := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := service.browserResetEmulationHandler(
			context.Background(),
			mcp.CallToolRequest{},
			emulationTargetArgs{BrowserID: "browser-a", TabID: &tabID},
		)
		resultChannel <- result
	}()

	request := receiveToolMessage(t, connection.messages)
	if request.Command != protocol.CommandEmulationReset {
		t.Fatalf("command = %q, want cleanup without a tab policy preflight", request.Command)
	}
	respondToToolRequest(t, service, connection, request, emulationWireResult{
		Active: false, TabID: tabID, Applied: []string{}, ResetOnDetach: true, Warnings: []string{},
	})
	select {
	case result := <-resultChannel:
		if result == nil || result.IsError {
			t.Fatalf("handler result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cleanup result")
	}
}

func TestCloseWindowRequiresConfirmationAndAuditsDenial(t *testing.T) {
	t.Parallel()

	service, connection, _ := newTestService(t)
	var audit bytes.Buffer
	service.actionPolicy = mustActionPolicy(t, nil, nil, false, log.New(&audit, "", 0))
	result, err := service.browserCloseWindowHandler(
		context.Background(),
		mcp.CallToolRequest{},
		windowCloseArgs{BrowserID: "browser-a", WindowID: 7},
	)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("handler result = (%#v, %v)", result, err)
	}
	response := decodeToolResponse(t, result)
	if response.Error == nil || response.Error.Code != protocol.CodeConfirmationRequired {
		t.Fatalf("error = %#v", response.Error)
	}
	if !strings.Contains(audit.String(), "reason=confirmation_required") {
		t.Fatalf("audit log = %q", audit.String())
	}
	select {
	case request := <-connection.messages:
		t.Fatalf("browser received unconfirmed close: %#v", request)
	default:
	}
}

func mustActionPolicy(
	t *testing.T,
	allowOrigins []string,
	denyOrigins []string,
	allowIncognito bool,
	logger *log.Logger,
) *policy.Action {
	t.Helper()
	actionPolicy, err := policy.NewAction(allowOrigins, denyOrigins, allowIncognito, logger)
	if err != nil {
		t.Fatalf("policy.NewAction() error = %v", err)
	}
	return actionPolicy
}

func respondToToolRequest(
	t *testing.T,
	service *Service,
	connection *toolTestConnection,
	request protocol.Message,
	result any,
) {
	t.Helper()
	response, err := protocol.NewResponse(request.RequestID, "browser-a", result, nil)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	if !service.router.HandleResponse("browser-a", connection.ID(), response) {
		t.Fatal("HandleResponse() = false")
	}
}
