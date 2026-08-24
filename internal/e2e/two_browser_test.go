//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/artifacts"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/mcpsession"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/netguard"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/router"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/security/pairing"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/selection"
	browsertools "github.com/hightemp/go_mcp_browser_ext_tool/internal/tools"
	websockettransport "github.com/hightemp/go_mcp_browser_ext_tool/internal/transport/websocket"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	profileAName = "E2E Chromium A"
	profileBName = "E2E Chromium B"
)

func TestTwoChromiumProfilesRouteAndReconnect(t *testing.T) {
	chromeBinary := chromeExecutable(t)
	extensionDirectory := extensionPath(t)
	testSite := newTestSite(t)
	browserRegistry, pairingManager, webSocketURL, mcpURL := newE2EServers(t)

	chromeA := launchChrome(t, chromeBinary, extensionDirectory)
	chromeB := launchChrome(t, chromeBinary, extensionDirectory)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	workerA, extensionIDA, err := chromeA.waitForServiceWorker(ctx, "")
	if err != nil {
		t.Fatalf("find profile A service worker: %v\nChrome output:\n%s", err, chromeA.logs.String())
	}
	_, extensionIDB, err := chromeB.waitForServiceWorker(ctx, "")
	if err != nil {
		t.Fatalf("find profile B service worker: %v\nChrome output:\n%s", err, chromeB.logs.String())
	}
	if extensionIDA != extensionIDB {
		t.Fatalf("extension IDs differ between profiles: %s != %s", extensionIDA, extensionIDB)
	}

	configureAndPair(
		t,
		ctx,
		chromeA,
		extensionIDA,
		webSocketURL,
		profileAName,
		pairingManager,
	)
	waitForBrowserCount(t, browserRegistry, 1, 10*time.Second)
	configureAndPair(
		t,
		ctx,
		chromeB,
		extensionIDA,
		webSocketURL,
		profileBName,
		pairingManager,
	)
	waitForBrowserCount(t, browserRegistry, 2, 10*time.Second)

	clientA := newE2EMCPClient(t, mcpURL, "e2e-client-a")
	clientB := newE2EMCPClient(t, mcpURL, "e2e-client-b")
	browsers := successfulToolCall(t, clientA, "browser_list", nil)
	browserAID := browserIDByName(t, browsers, profileAName)
	browserBID := browserIDByName(t, browsers, profileBName)
	if browserAID == browserBID {
		t.Fatal("isolated Chromium profiles reported the same browser ID")
	}
	assertToolErrorCode(
		t,
		clientA,
		"browser_get_tabs",
		nil,
		protocol.CodeAmbiguousBrowser,
	)

	successfulToolCall(t, clientA, "browser_select", map[string]any{"browserId": browserAID})
	successfulToolCall(t, clientB, "browser_select", map[string]any{"browserId": browserBID})
	createdAURL := testSite.URL + "/a?stage=created"
	createdBURL := testSite.URL + "/b?stage=created"
	createdA := successfulToolCall(t, clientA, "browser_create_tab", map[string]any{
		"url": createdAURL, "active": false,
	})
	createdB := successfulToolCall(t, clientB, "browser_create_tab", map[string]any{
		"url": createdBURL, "active": false,
	})
	tabAID := tabIDFromToolPayload(t, createdA)
	tabBID := tabIDFromToolPayload(t, createdB)
	if listed := waitForTab(t, clientA, createdAURL); listed != tabAID {
		t.Fatalf("profile A created tab ID = %d, listed ID = %d", tabAID, listed)
	}
	if listed := waitForTab(t, clientB, createdBURL); listed != tabBID {
		t.Fatalf("profile B created tab ID = %d, listed ID = %d", tabBID, listed)
	}
	successfulToolCall(t, clientA, "browser_activate_tab", map[string]any{"tabId": tabAID})
	successfulToolCall(t, clientB, "browser_activate_tab", map[string]any{"tabId": tabBID})

	pageAURL := testSite.URL + "/a?stage=navigated"
	pageBURL := testSite.URL + "/b?stage=navigated"
	successfulToolCall(t, clientA, "browser_navigate_tab", map[string]any{
		"tabId": tabAID, "url": pageAURL,
	})
	successfulToolCall(t, clientB, "browser_navigate_tab", map[string]any{
		"tabId": tabBID, "url": pageBURL,
	})
	if navigated := waitForTab(t, clientA, pageAURL); navigated != tabAID {
		t.Fatalf("profile A navigated tab ID = %d, listed ID = %d", tabAID, navigated)
	}
	if navigated := waitForTab(t, clientB, pageBURL); navigated != tabBID {
		t.Fatalf("profile B navigated tab ID = %d, listed ID = %d", tabBID, navigated)
	}
	successfulToolCall(t, clientA, "browser_select_tab", map[string]any{"tabId": tabAID})
	successfulToolCall(t, clientB, "browser_select_tab", map[string]any{"tabId": tabBID})

	assertPageText(t, clientA, "PROFILE_A_ONLY", "PROFILE_B_ONLY")
	assertPageText(t, clientB, "PROFILE_B_ONLY", "PROFILE_A_ONLY")
	assertPageInspection(t, clientA, "PROFILE_A_ONLY", "PROFILE_B_ONLY")
	assertPageInspection(t, clientB, "PROFILE_B_ONLY", "PROFILE_A_ONLY")
	assertPageWait(t, clientA, "PROFILE_A_ONLY")
	assertPageWait(t, clientB, "PROFILE_B_ONLY")
	assertFullPageScreenshot(t, clientA, tabAID)
	assertFullPageScreenshot(t, clientB, tabBID)
	fillAndApply(t, clientA, "Alice")
	assertPageText(t, clientA, "A:Alice", "B:Alice")
	assertPageText(t, clientB, "B:ready", "Alice")
	fillAndApply(t, clientB, "Bob")
	assertPageText(t, clientB, "B:Bob", "A:Bob")
	assertPageText(t, clientA, "A:Alice", "Bob")

	assertParallelIsolation(t, clientA, clientB)

	beforeReconnect, ok := browserRegistry.Get(browserAID)
	if !ok || !beforeReconnect.Connected {
		t.Fatalf("profile A before reconnect = %#v", beforeReconnect)
	}
	if err := chromeA.closeTarget(ctx, workerA.TargetID); err != nil {
		t.Fatalf("stop profile A service worker: %v", err)
	}
	waitForBrowserCount(t, browserRegistry, 1, 10*time.Second)
	if _, err := chromeA.createTarget(
		ctx,
		"chrome-extension://"+extensionIDA+"/src/options.html",
	); err != nil {
		t.Fatalf("wake profile A service worker: %v", err)
	}
	if _, _, err := chromeA.waitForServiceWorker(ctx, workerA.TargetID); err != nil {
		t.Fatalf("restart profile A service worker: %v", err)
	}
	waitForBrowserCount(t, browserRegistry, 2, 10*time.Second)
	afterReconnect, ok := browserRegistry.Get(browserAID)
	if !ok || !afterReconnect.Connected {
		t.Fatalf("profile A after reconnect = %#v", afterReconnect)
	}
	if afterReconnect.ConnectionID == beforeReconnect.ConnectionID {
		t.Fatal("service worker restart did not create a new connection ID")
	}
	assertPageText(t, clientA, "A:Alice", "B:Bob")

	successfulToolCall(t, clientA, "browser_close_tab", map[string]any{"tabId": tabAID})
	waitForTabAbsent(t, clientA, tabAID)
	assertPageText(t, clientB, "B:Bob", "A:Alice")
	successfulToolCall(t, clientB, "browser_close_tab", map[string]any{"tabId": tabBID})
	waitForTabAbsent(t, clientB, tabBID)
}

func newE2EServers(
	t *testing.T,
) (*registry.Registry, *pairing.Manager, string, string) {
	t.Helper()

	browserRegistry := registry.New()
	requestRouter := router.New(
		browserRegistry,
		router.WithDefaultTimeout(10*time.Second),
		router.WithLogger(log.New(io.Discard, "", 0)),
	)
	t.Cleanup(func() { requestRouter.Close() })
	selections := selection.NewStore()
	artifactStore, err := artifacts.New(
		t.TempDir(),
		time.Hour,
		artifacts.WithMaxBytes(16<<20),
	)
	if err != nil {
		t.Fatalf("artifacts.New() error = %v", err)
	}
	pairingManager, err := pairing.NewManager()
	if err != nil {
		t.Fatalf("pairing.NewManager() error = %v", err)
	}

	webSocketHandler := websockettransport.NewServer(
		browserRegistry,
		requestRouter,
		websockettransport.WithLogger(log.New(io.Discard, "", 0)),
		websockettransport.WithAuthenticator(pairingManager),
	)
	webSocketServer := httptest.NewServer(webSocketHandler)
	t.Cleanup(webSocketServer.Close)

	hooks := &server.Hooks{}
	hooks.AddOnUnregisterSession(func(_ context.Context, session server.ClientSession) {
		selections.Delete(session.SessionID())
	})
	mcpServer := server.NewMCPServer(
		"real-browser-e2e",
		"0.1.0-test",
		server.WithHooks(hooks),
		server.WithRecovery(),
		server.WithToolCapabilities(true),
	)
	browsertools.NewService(
		browserRegistry,
		requestRouter,
		selections,
		browsertools.WithArtifactStore(artifactStore),
	).Register(mcpServer)
	sessionManager, err := mcpsession.NewManager()
	if err != nil {
		t.Fatalf("mcpsession.NewManager() error = %v", err)
	}
	streamable := server.NewStreamableHTTPServer(
		mcpServer,
		server.WithSessionIdManager(sessionManager),
	)
	mcpHTTP := httptest.NewServer(netguard.LocalOnly(streamable))
	t.Cleanup(mcpHTTP.Close)

	return browserRegistry,
		pairingManager,
		"ws" + strings.TrimPrefix(webSocketServer.URL, "http") + websockettransport.DefaultPath,
		mcpHTTP.URL + "/mcp"
}

func newTestSite(t *testing.T) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		profile := "A"
		marker := "PROFILE_A_ONLY"
		if request.URL.Path == "/b" {
			profile = "B"
			marker = "PROFILE_B_ONLY"
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(writer, `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Profile %s</title></head>
<body>
  <h1>%s</h1>
  <label>Name <input id="name" autocomplete="off"></label>
  <button id="apply" type="button">Apply</button>
  <p id="state">%s:ready</p>
  <script>
    document.querySelector("#apply").addEventListener("click", () => {
      document.querySelector("#state").textContent = "%s:" + document.querySelector("#name").value;
    });
  </script>
</body>
</html>`, profile, marker, profile, profile)
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func configureAndPair(
	t *testing.T,
	ctx context.Context,
	chrome *chromeInstance,
	extensionID string,
	webSocketURL string,
	displayName string,
	pairingManager *pairing.Manager,
) {
	t.Helper()
	optionsTarget, err := chrome.createTarget(
		ctx,
		"chrome-extension://"+extensionID+"/src/options.html",
	)
	if err != nil {
		t.Fatalf("open %s options page: %v", displayName, err)
	}
	optionsSession, err := chrome.attach(ctx, optionsTarget)
	if err != nil {
		t.Fatalf("attach %s options page: %v", displayName, err)
	}
	if err := chrome.waitForExtensionRuntime(ctx, optionsSession); err != nil {
		t.Fatalf("wait for %s options page: %v", displayName, err)
	}
	response, err := chrome.runtimeMessage(ctx, optionsSession, map[string]any{
		"type": "SAVE_SETTINGS",
		"settings": map[string]any{
			"endpoint":    webSocketURL,
			"displayName": displayName,
			"autoConnect": false,
			"featureFlags": map[string]any{
				"pageAutomation": true,
			},
		},
	})
	assertRuntimeSuccess(t, displayName+" settings", response, err)
	pairingCode, _, err := pairingManager.CurrentCode()
	if err != nil {
		t.Fatalf("get %s pairing code: %v", displayName, err)
	}
	response, err = chrome.runtimeMessage(ctx, optionsSession, map[string]any{
		"type":        "PAIR",
		"pairingCode": pairingCode,
	})
	assertRuntimeSuccess(t, displayName+" pairing", response, err)
	if err := chrome.closeTarget(ctx, optionsTarget); err != nil {
		t.Fatalf("close %s options page: %v", displayName, err)
	}
}

func assertRuntimeSuccess(
	t *testing.T,
	operation string,
	response map[string]any,
	err error,
) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", operation, err)
	}
	if success, _ := response["success"].(bool); !success {
		t.Fatalf("%s response = %#v", operation, response)
	}
}

func newE2EMCPClient(t *testing.T, endpoint, name string) *client.Client {
	t.Helper()
	mcpClient, err := client.NewStreamableHttpClient(endpoint)
	if err != nil {
		t.Fatalf("NewStreamableHttpClient(%s) error = %v", name, err)
	}
	if err := mcpClient.Start(context.Background()); err != nil {
		t.Fatalf("Start(%s) error = %v", name, err)
	}
	initialize := mcp.InitializeRequest{}
	initialize.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initialize.Params.ClientInfo = mcp.Implementation{Name: name, Version: "0.1.0-test"}
	if _, err := mcpClient.Initialize(context.Background(), initialize); err != nil {
		t.Fatalf("Initialize(%s) error = %v", name, err)
	}
	return mcpClient
}

func successfulToolCall(
	t *testing.T,
	mcpClient *client.Client,
	name string,
	arguments map[string]any,
) map[string]any {
	t.Helper()
	payload, err := toolCall(mcpClient, name, arguments)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return payload
}

func toolCall(
	mcpClient *client.Client,
	name string,
	arguments map[string]any,
) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	request := mcp.CallToolRequest{}
	request.Params.Name = name
	request.Params.Arguments = arguments
	result, err := mcpClient.CallTool(ctx, request)
	if err != nil {
		return nil, err
	}
	if len(result.Content) != 1 {
		return nil, fmt.Errorf("content length = %d, want 1", len(result.Content))
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		return nil, fmt.Errorf("content type = %T, want text", result.Content[0])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text.Text), &payload); err != nil {
		return nil, fmt.Errorf("decode tool payload: %w", err)
	}
	if result.IsError {
		return nil, fmt.Errorf("tool returned an error: %s", text.Text)
	}
	return payload, nil
}

func browserIDByName(t *testing.T, payload map[string]any, displayName string) string {
	t.Helper()
	data := objectField(t, payload, "data")
	connectedCount, _ := data["connectedCount"].(float64)
	if connectedCount != 2 {
		t.Fatalf("browser_list connectedCount = %v, want 2", data["connectedCount"])
	}
	browsers, ok := data["browsers"].([]any)
	if !ok {
		t.Fatalf("browser_list browsers = %T", data["browsers"])
	}
	for _, value := range browsers {
		browser, ok := value.(map[string]any)
		if ok && browser["displayName"] == displayName {
			browserID, _ := browser["browserId"].(string)
			if browserID != "" {
				return browserID
			}
		}
	}
	t.Fatalf("browser_list does not contain %q: %#v", displayName, browsers)
	return ""
}

func tabIDFromToolPayload(t *testing.T, payload map[string]any) int {
	t.Helper()
	data := objectField(t, payload, "data")
	tab := objectField(t, data, "tab")
	tabID, ok := tab["id"].(float64)
	if !ok || tabID < 0 {
		t.Fatalf("tab ID = %#v", tab["id"])
	}
	return int(tabID)
}

func assertToolErrorCode(
	t *testing.T,
	mcpClient *client.Client,
	name string,
	arguments map[string]any,
	want protocol.ErrorCode,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	request := mcp.CallToolRequest{}
	request.Params.Name = name
	request.Params.Arguments = arguments
	result, err := mcpClient.CallTool(ctx, request)
	if err != nil {
		t.Fatalf("%s transport error: %v", name, err)
	}
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("%s result = %#v, want one structured tool error", name, result)
	}
	content, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("%s error content type = %T, want text", name, result.Content[0])
	}
	var payload struct {
		Error *protocol.Error `json:"error"`
	}
	if err := json.Unmarshal([]byte(content.Text), &payload); err != nil {
		t.Fatalf("decode %s error: %v", name, err)
	}
	if payload.Error == nil || payload.Error.Code != want {
		t.Fatalf("%s error = %#v, want %s", name, payload.Error, want)
	}
}

func waitForTab(t *testing.T, mcpClient *client.Client, targetURL string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var lastPayload map[string]any
	var lastErr error
	for time.Now().Before(deadline) {
		lastPayload, lastErr = toolCall(mcpClient, "browser_get_tabs", nil)
		if lastErr == nil {
			data, _ := lastPayload["data"].(map[string]any)
			tabs, _ := data["tabs"].([]any)
			for _, value := range tabs {
				tab, _ := value.(map[string]any)
				if tab["url"] == targetURL && tab["status"] == "complete" {
					if tabID, ok := tab["id"].(float64); ok {
						return int(tabID)
					}
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("tab %s was not ready: payload=%#v error=%v", targetURL, lastPayload, lastErr)
	return 0
}

func fillAndApply(t *testing.T, mcpClient *client.Client, value string) {
	t.Helper()
	successfulToolCall(t, mcpClient, "browser_input_data", map[string]any{
		"selector": "#name",
		"value":    value,
	})
	successfulToolCall(t, mcpClient, "browser_click_element", map[string]any{
		"selector": "#apply",
	})
}

func assertPageInspection(
	t *testing.T,
	mcpClient *client.Client,
	want string,
	forbidden string,
) {
	t.Helper()
	htmlPayload := successfulToolCall(t, mcpClient, "browser_get_html", nil)
	html, _ := objectField(t, htmlPayload, "data")["html"].(string)
	if !strings.Contains(html, want) ||
		(forbidden != "" && strings.Contains(html, forbidden)) {
		t.Fatalf("page HTML failed isolation: required=%q forbidden=%q html=%q", want, forbidden, html)
	}

	snapshotPayload := successfulToolCall(t, mcpClient, "browser_snapshot", map[string]any{
		"maxDepth": 20,
		"maxNodes": 100,
	})
	snapshot := objectField(t, snapshotPayload, "data")
	nodeCount, _ := snapshot["nodeCount"].(float64)
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal semantic snapshot: %v", err)
	}
	if nodeCount <= 0 || !strings.Contains(string(snapshotJSON), want) ||
		(forbidden != "" && strings.Contains(string(snapshotJSON), forbidden)) {
		t.Fatalf(
			"semantic snapshot failed isolation: required=%q forbidden=%q snapshot=%s",
			want,
			forbidden,
			snapshotJSON,
		)
	}
}

func assertPageWait(t *testing.T, mcpClient *client.Client, expected string) {
	t.Helper()
	payload := successfulToolCall(t, mcpClient, "browser_wait", map[string]any{
		"condition":     "text",
		"expected":      expected,
		"matchOperator": "contains",
		"mode":          "auto",
		"timeoutMs":     5_000,
	})
	data := objectField(t, payload, "data")
	if matched, _ := data["matched"].(bool); !matched {
		t.Fatalf("browser_wait data = %#v, want matched", data)
	}
}

func assertFullPageScreenshot(t *testing.T, mcpClient *client.Client, tabID int) {
	t.Helper()
	payload := successfulToolCall(t, mcpClient, "browser_screenshot", map[string]any{
		"tabId":   tabID,
		"capture": "fullPage",
		"format":  "png",
	})
	data := objectField(t, payload, "data")
	width, _ := data["width"].(float64)
	height, _ := data["height"].(float64)
	size, _ := data["size"].(float64)
	artifactURI, _ := payload["artifactUri"].(string)
	if data["capture"] != "fullPage" || data["mimeType"] != "image/png" ||
		width <= 0 || height <= 0 || size <= 0 ||
		!strings.HasPrefix(artifactURI, "browser://artifacts/") {
		t.Fatalf("browser_screenshot payload = %#v", payload)
	}
}

func assertPageText(
	t *testing.T,
	mcpClient *client.Client,
	want string,
	forbidden string,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastPayload map[string]any
	var lastErr error
	var text string
	for time.Now().Before(deadline) {
		lastPayload, lastErr = toolCall(mcpClient, "browser_get_text", nil)
		if lastErr == nil {
			data, _ := lastPayload["data"].(map[string]any)
			text, _ = data["text"].(string)
			if strings.Contains(text, want) &&
				(forbidden == "" || !strings.Contains(text, forbidden)) {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}

	pageInfo, pageInfoErr := toolCall(mcpClient, "browser_page_info", nil)
	pageHTML, pageHTMLErr := toolCall(mcpClient, "browser_get_html", nil)
	t.Fatalf(
		"page text %q did not converge to required=%q forbidden=%q; lastPayload=%#v lastError=%v pageInfo=%#v pageInfoError=%v pageHTML=%#v pageHTMLError=%v",
		text,
		want,
		forbidden,
		lastPayload,
		lastErr,
		pageInfo,
		pageInfoErr,
		pageHTML,
		pageHTMLErr,
	)
}

func waitForTabAbsent(t *testing.T, mcpClient *client.Client, tabID int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastPayload map[string]any
	var lastErr error
	for time.Now().Before(deadline) {
		lastPayload, lastErr = toolCall(mcpClient, "browser_get_tabs", nil)
		if lastErr == nil {
			data, _ := lastPayload["data"].(map[string]any)
			tabs, _ := data["tabs"].([]any)
			found := false
			for _, value := range tabs {
				tab, _ := value.(map[string]any)
				if listedID, ok := tab["id"].(float64); ok && int(listedID) == tabID {
					found = true
					break
				}
			}
			if !found {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("tab %d remained after close: payload=%#v error=%v", tabID, lastPayload, lastErr)
}

func assertParallelIsolation(t *testing.T, clientA, clientB *client.Client) {
	t.Helper()
	type outcome struct {
		owner   string
		payload map[string]any
		err     error
	}
	results := make(chan outcome, 2)
	var group sync.WaitGroup
	for owner, mcpClient := range map[string]*client.Client{"A": clientA, "B": clientB} {
		owner := owner
		mcpClient := mcpClient
		group.Add(1)
		go func() {
			defer group.Done()
			payload, err := toolCall(mcpClient, "browser_get_text", nil)
			results <- outcome{owner: owner, payload: payload, err: err}
		}()
	}
	group.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatalf("parallel profile %s: %v", result.owner, result.err)
		}
		data, ok := result.payload["data"].(map[string]any)
		if !ok {
			t.Fatalf("parallel profile %s data = %T", result.owner, result.payload["data"])
		}
		text, _ := data["text"].(string)
		if !strings.Contains(text, result.owner+":") {
			t.Fatalf("parallel profile %s text = %q", result.owner, text)
		}
		other := "A:"
		if result.owner == "A" {
			other = "B:"
		}
		if strings.Contains(text, other) {
			t.Fatalf("parallel profile %s received cross-routed text %q", result.owner, text)
		}
	}
}

func objectField(t *testing.T, payload map[string]any, name string) map[string]any {
	t.Helper()
	value, ok := payload[name].(map[string]any)
	if !ok {
		t.Fatalf("%s field = %T, want object", name, payload[name])
	}
	return value
}

func waitForBrowserCount(
	t *testing.T,
	browserRegistry *registry.Registry,
	want int,
	timeout time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for browserRegistry.Count() != want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := browserRegistry.Count(); got != want {
		t.Fatalf("registry count = %d, want %d; browsers=%#v", got, want, browserRegistry.ListAll())
	}
}

func chromeExecutable(t *testing.T) string {
	t.Helper()
	if configured := strings.TrimSpace(os.Getenv("CHROME_BIN")); configured != "" {
		path, err := exec.LookPath(configured)
		if err != nil {
			t.Fatalf("CHROME_BIN %q is unavailable: %v", configured, err)
		}
		return path
	}
	for _, candidate := range []string{"chromium", "chromium-browser"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	t.Fatal("Chrome for Testing or Chromium 116+ is required; set CHROME_BIN to its executable")
	return ""
}

func extensionPath(t *testing.T) string {
	t.Helper()
	configured := strings.TrimSpace(os.Getenv("MCP_BROWSER_EXTENSION_DIR"))
	if configured == "" {
		configured = filepath.Join("..", "..", "chrome-extension", "dist", "e2e-extension")
	}
	absolute, err := filepath.Abs(configured)
	if err != nil {
		t.Fatalf("resolve extension directory: %v", err)
	}
	manifest := filepath.Join(absolute, "manifest.json")
	if metadata, err := os.Stat(manifest); err != nil || !metadata.Mode().IsRegular() {
		t.Fatalf("extension manifest %s is unavailable: %v", manifest, err)
	}
	return absolute
}
