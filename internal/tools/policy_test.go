package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/router"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/selection"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestToolProfilesAreExplicitAndNested(t *testing.T) {
	t.Parallel()

	available := make([]mcp.Tool, 0, len(browserToolLevels)+1)
	for name := range browserToolLevels {
		available = append(available, mcp.NewTool(name))
	}
	available = append(available, mcp.NewTool("browser_future_unreviewed"))

	filtered := make(map[string]map[string]bool)
	for _, profile := range []string{"minimal", "standard", "full"} {
		filtered[profile] = make(map[string]bool)
		for _, tool := range ToolProfileFilter(profile)(context.Background(), available) {
			filtered[profile][tool.Name] = true
		}
		if filtered[profile]["browser_future_unreviewed"] {
			t.Fatalf("%s profile allowed an unclassified tool", profile)
		}
	}
	for name := range filtered["minimal"] {
		if !filtered["standard"][name] || !filtered["full"][name] {
			t.Errorf("minimal tool %q is missing from a broader profile", name)
		}
	}
	for name := range filtered["standard"] {
		if !filtered["full"][name] {
			t.Errorf("standard tool %q is missing from full profile", name)
		}
	}
	if !filtered["minimal"]["browser_list"] || filtered["minimal"]["browser_click_element"] {
		t.Fatalf("minimal profile = %#v", filtered["minimal"])
	}
	if !filtered["standard"]["browser_click_element"] || filtered["standard"]["browser_send_command"] {
		t.Fatalf("standard profile = %#v", filtered["standard"])
	}
	if filtered["standard"]["browser_print_to_pdf"] || !filtered["full"]["browser_print_to_pdf"] {
		t.Fatal("PDF printing must remain full-profile only")
	}
	if filtered["standard"]["browser_get_accessibility_tree"] ||
		!filtered["full"]["browser_get_accessibility_tree"] {
		t.Fatal("CDP accessibility must remain full-profile only")
	}
	for _, name := range []string{
		"browser_set_emulation", "browser_get_emulation_state", "browser_reset_emulation",
		"browser_evaluate_javascript", "browser_send_cdp_command",
		"browser_get_performance_metrics", "browser_capture_performance",
	} {
		if filtered["standard"][name] || !filtered["full"][name] {
			t.Fatalf("sensitive CDP tool %q must remain full-profile only", name)
		}
	}
	if !filtered["full"]["browser_send_command"] {
		t.Fatal("full profile does not allow browser_send_command")
	}
}

func TestToolProfileName(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		profile string
		ok      bool
	}{
		{name: "browser_list", profile: "minimal", ok: true},
		{name: "browser_click_element", profile: "standard", ok: true},
		{name: "browser_send_command", profile: "full", ok: true},
		{name: "browser_send_cdp_command", profile: "full", ok: true},
		{name: "browser_get_performance_metrics", profile: "full", ok: true},
		{name: "browser_capture_performance", profile: "full", ok: true},
		{name: "browser_future_unreviewed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			profile, ok := ToolProfileName(test.name)
			if profile != test.profile || ok != test.ok {
				t.Fatalf("ToolProfileName(%q) = (%q, %v), want (%q, %v)", test.name, profile, ok, test.profile, test.ok)
			}
		})
	}
}

func TestEveryRegisteredBrowserToolHasProfileClassification(t *testing.T) {
	t.Parallel()

	browserRegistry := registry.New()
	mcpServer := server.NewMCPServer("test", "1.0.0")
	NewService(
		browserRegistry,
		router.New(browserRegistry, router.WithLogger(log.New(io.Discard, "", 0))),
		selection.NewStore(),
	).Register(mcpServer)

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	response := mcpServer.HandleMessage(context.Background(), payload)
	jsonRPC, ok := response.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("tools/list response = %#v", response)
	}
	result, ok := jsonRPC.Result.(mcp.ListToolsResult)
	if !ok {
		t.Fatalf("tools/list result = %#v", jsonRPC.Result)
	}
	registered := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		registered[tool.Name] = true
		if _, classified := browserToolLevels[tool.Name]; !classified {
			t.Errorf("registered tool %q has no profile classification", tool.Name)
		}
	}
	for name := range browserToolLevels {
		if !registered[name] {
			t.Errorf("profile classifies unregistered tool %q", name)
		}
	}
}

func TestToolProfileMiddlewareDeniesDirectCallsAndAuditsOnlyMetadata(t *testing.T) {
	t.Parallel()

	var audit bytes.Buffer
	mcpServer := server.NewMCPServer(
		"test",
		"1.0.0",
		server.WithToolHandlerMiddleware(ToolProfileMiddleware(
			"minimal",
			log.New(&audit, "", 0),
		)),
	)
	mcpServer.AddTool(
		mcp.NewTool("browser_click_element"),
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			t.Fatal("denied handler was invoked")
			return nil, nil
		},
	)
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "browser_click_element",
			"arguments": map[string]any{"value": "must-not-be-logged"},
		},
	})
	if err != nil {
		t.Fatalf("marshal call: %v", err)
	}
	response := mcpServer.HandleMessage(context.Background(), payload)
	jsonRPC, ok := response.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("tools/call response = %#v", response)
	}
	result, ok := jsonRPC.Result.(mcp.CallToolResult)
	if !ok || !result.IsError {
		t.Fatalf("tools/call result = %#v", jsonRPC.Result)
	}
	logText := audit.String()
	if !strings.Contains(logText, "tool=browser_click_element") ||
		strings.Contains(logText, "must-not-be-logged") {
		t.Fatalf("audit log = %q", logText)
	}
}
