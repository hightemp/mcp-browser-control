// Command tool-reference generates the MCP tool reference from the schemas
// registered by the production tool service.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/router"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/selection"
	browsertools "github.com/hightemp/go_mcp_browser_ext_tool/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultOutputPath = "docs/tool-reference.md"
	serverLocal       = "server-local"
	batchCapability   = "batch"
	dynamicCapability = "dynamic"
)

var toolCapabilities = map[string]string{
	"browser_list":             serverLocal,
	"browser_get":              serverLocal,
	"browser_select":           serverLocal,
	"browser_get_selected":     serverLocal,
	"browser_select_tab":       serverLocal,
	"browser_rename":           serverLocal,
	"browser_get_capabilities": serverLocal,
	"browser_ping":             protocol.CommandBrowserPing,
	"browser_batch":            batchCapability,

	"browser_get_windows":   protocol.CommandWindowsList,
	"browser_get_window":    protocol.CommandWindowsGet,
	"browser_create_window": protocol.CommandWindowsCreate,
	"browser_update_window": protocol.CommandWindowsUpdate,
	"browser_focus_window":  protocol.CommandWindowsFocus,
	"browser_close_window":  protocol.CommandWindowsClose,

	"browser_get_tabs":      protocol.CommandTabsList,
	"browser_get_tab":       protocol.CommandTabsGet,
	"browser_create_tab":    protocol.CommandTabsCreate,
	"browser_activate_tab":  protocol.CommandTabsActivate,
	"browser_navigate_tab":  protocol.CommandTabsNavigate,
	"browser_reload_tab":    protocol.CommandTabsReload,
	"browser_stop_tab":      protocol.CommandTabsStop,
	"browser_go_back":       protocol.CommandTabsBack,
	"browser_go_forward":    protocol.CommandTabsForward,
	"browser_move_tab":      protocol.CommandTabsMove,
	"browser_duplicate_tab": protocol.CommandTabsDuplicate,
	"browser_close_tab":     protocol.CommandTabsClose,
	"browser_pin_tab":       protocol.CommandTabsPin,
	"browser_mute_tab":      protocol.CommandTabsMute,
	"browser_get_tab_zoom":  protocol.CommandTabsGetZoom,
	"browser_set_tab_zoom":  protocol.CommandTabsSetZoom,

	"browser_group_tabs":          protocol.CommandTabsGroup,
	"browser_ungroup_tabs":        protocol.CommandTabsUngroup,
	"browser_update_tab_group":    protocol.CommandTabGroupsUpdate,
	"browser_get_recently_closed": protocol.CommandSessionsRecentlyClosed,
	"browser_restore_session":     protocol.CommandSessionsRestore,

	"browser_page_info":               protocol.CommandPageInfo,
	"browser_get_html":                protocol.CommandPageGetHTML,
	"browser_get_html_by_selector":    protocol.CommandPageGetHTMLBySelector,
	"browser_get_text":                protocol.CommandPageGetText,
	"browser_query":                   protocol.CommandPageQuery,
	"browser_get_element":             protocol.CommandPageGetElement,
	"browser_snapshot":                protocol.CommandPageSnapshot,
	"browser_click_element":           protocol.CommandPageClick,
	"browser_double_click":            protocol.CommandPageClick,
	"browser_context_click":           protocol.CommandPageClick,
	"browser_input_data":              protocol.CommandPageFill,
	"browser_hover":                   protocol.CommandPageHover,
	"browser_focus":                   protocol.CommandPageFocus,
	"browser_blur":                    protocol.CommandPageBlur,
	"browser_type":                    protocol.CommandPageType,
	"browser_clear":                   protocol.CommandPageClear,
	"browser_press":                   protocol.CommandPagePress,
	"browser_select_option":           protocol.CommandPageSelect,
	"browser_set_checked":             protocol.CommandPageSetChecked,
	"browser_scroll":                  protocol.CommandPageScroll,
	"browser_drag_and_drop":           protocol.CommandPageDrag,
	"browser_dispatch_event":          protocol.CommandPageDispatch,
	"browser_submit":                  protocol.CommandPageSubmit,
	"browser_wait":                    protocol.CommandPageWait,
	"browser_screenshot":              protocol.CommandPageScreenshot,
	"browser_print_to_pdf":            protocol.CommandPagePrintToPDF,
	"browser_get_accessibility_tree":  protocol.CommandAccessibilityGetTree,
	"browser_set_emulation":           protocol.CommandEmulationSet,
	"browser_get_emulation_state":     protocol.CommandEmulationGet,
	"browser_reset_emulation":         protocol.CommandEmulationReset,
	"browser_evaluate_javascript":     protocol.CommandRuntimeEvaluateIsolated,
	"browser_send_cdp_command":        protocol.CommandCDPSendReadOnly,
	"browser_get_performance_metrics": protocol.CommandPerformanceMetrics,
	"browser_capture_performance":     protocol.CommandPerformanceCapture,

	"browser_start_console_capture":  protocol.CommandConsoleStart,
	"browser_stop_console_capture":   protocol.CommandConsoleStop,
	"browser_clear_console_log":      protocol.CommandConsoleClear,
	"browser_get_console_log":        protocol.CommandConsoleRead,
	"browser_start_network_capture":  protocol.CommandNetworkStart,
	"browser_stop_network_capture":   protocol.CommandNetworkStop,
	"browser_clear_network_log":      protocol.CommandNetworkClear,
	"browser_get_network_log":        protocol.CommandNetworkRead,
	"browser_get_network_body":       protocol.CommandNetworkGetBody,
	"browser_export_network_har":     protocol.CommandNetworkExportHAR,
	"browser_list_cookies":           protocol.CommandCookiesList,
	"browser_get_cookie":             protocol.CommandCookiesGet,
	"browser_set_cookie":             protocol.CommandCookiesSet,
	"browser_remove_cookie":          protocol.CommandCookiesRemove,
	"browser_list_storage_items":     protocol.CommandStorageList,
	"browser_get_storage_item":       protocol.CommandStorageGet,
	"browser_set_storage_item":       protocol.CommandStorageSet,
	"browser_remove_storage_item":    protocol.CommandStorageRemove,
	"browser_get_cache_metadata":     protocol.CommandStorageCacheMetadata,
	"browser_get_indexeddb_metadata": protocol.CommandStorageIndexedDBMetadata,
	"browser_clear_origin_storage":   protocol.CommandStorageClear,
	"browser_list_downloads":         protocol.CommandDownloadsList,
	"browser_get_download":           protocol.CommandDownloadsGet,
	"browser_create_download":        protocol.CommandDownloadsCreate,
	"browser_pause_download":         protocol.CommandDownloadsPause,
	"browser_resume_download":        protocol.CommandDownloadsResume,
	"browser_cancel_download":        protocol.CommandDownloadsCancel,
	"browser_erase_download_history": protocol.CommandDownloadsErase,
	"browser_send_command":           dynamicCapability,
}

var exampleOverrides = map[string]map[string]any{
	"browser_select":               {"browserId": "browser-id"},
	"browser_select_tab":           {"tabId": 42},
	"browser_rename":               {"browserId": "browser-id", "displayName": "Work Chrome"},
	"browser_create_window":        {"urls": []string{"https://example.com/"}, "focused": true},
	"browser_update_window":        {"windowId": 1, "focused": true},
	"browser_close_window":         {"windowId": 1, "confirm": true},
	"browser_create_tab":           {"url": "https://example.com/", "active": true},
	"browser_navigate_tab":         {"url": "https://example.com/"},
	"browser_move_tab":             {"index": -1},
	"browser_pin_tab":              {"pinned": true},
	"browser_mute_tab":             {"muted": true},
	"browser_set_tab_zoom":         {"factor": 1.25},
	"browser_group_tabs":           {"tabIds": []int{41, 42}},
	"browser_ungroup_tabs":         {"tabIds": []int{41, 42}},
	"browser_update_tab_group":     {"groupId": 3, "title": "Research"},
	"browser_get_recently_closed":  {"maxResults": 10},
	"browser_restore_session":      {"sessionId": "session-id"},
	"browser_get_html_by_selector": {"selector": "main"},
	"browser_query":                {"locator": map[string]any{"role": "button", "name": "Save"}},
	"browser_get_element":          {"locator": map[string]any{"css": "#status"}},
	"browser_click_element":        {"locator": map[string]any{"role": "button", "name": "Save"}},
	"browser_double_click":         {"locator": map[string]any{"text": "Open"}},
	"browser_context_click":        {"locator": map[string]any{"css": ".file"}},
	"browser_input_data":           {"locator": map[string]any{"label": "Email"}, "value": "user@example.com"},
	"browser_hover":                {"locator": map[string]any{"css": ".menu"}},
	"browser_focus":                {"locator": map[string]any{"label": "Search"}},
	"browser_blur":                 {"locator": map[string]any{"label": "Search"}},
	"browser_type":                 {"locator": map[string]any{"label": "Message"}, "text": "Hello"},
	"browser_clear":                {"locator": map[string]any{"label": "Search"}},
	"browser_press":                {"locator": map[string]any{"label": "Search"}, "key": "Enter"},
	"browser_select_option":        {"locator": map[string]any{"label": "Country"}, "values": []string{"Canada"}},
	"browser_set_checked":          {"locator": map[string]any{"label": "Remember me"}, "checked": true},
	"browser_scroll":               {"deltaY": 600},
	"browser_drag_and_drop": {
		"source":        map[string]any{"text": "Draft"},
		"targetLocator": map[string]any{"text": "Done"},
	},
	"browser_dispatch_event": {
		"locator":   map[string]any{"css": "#editor"},
		"eventType": "change",
	},
	"browser_submit":                 {"locator": map[string]any{"css": "form"}},
	"browser_wait":                   {"condition": "delay", "delayMs": 250},
	"browser_screenshot":             {"format": "png", "capture": "viewport"},
	"browser_print_to_pdf":           {"pageRanges": "1-3", "printBackground": true},
	"browser_get_accessibility_tree": {"mode": "full", "roles": []string{"button", "link"}},
	"browser_set_emulation": {
		"viewport": map[string]any{"width": 390, "height": 844, "deviceScaleFactor": 3, "mobile": true},
		"media":    map[string]any{"colorScheme": "dark"},
	},
	"browser_evaluate_javascript": {
		"expression": "({ title: document.title, links: document.links.length })",
		"maxDepth":   4,
		"maxNodes":   100,
	},
	"browser_send_cdp_command": {
		"method": "Performance.getMetrics",
		"params": map[string]any{},
	},
	"browser_capture_performance":    {"kind": "trace", "durationMs": 1_000, "maxBytes": 1_000_000},
	"browser_start_console_capture":  {"bufferSize": 500, "captureConsole": true, "captureErrors": true},
	"browser_get_console_log":        {"levels": []string{"error", "warn"}, "limit": 50},
	"browser_start_network_capture":  {"maxEntries": 1_000},
	"browser_get_network_log":        {"limit": 50, "maxBytes": 524_288},
	"browser_get_network_body":       {"entryId": "1", "direction": "response", "maxBytes": 262_144},
	"browser_export_network_har":     {"maxBytes": 1_000_000},
	"browser_list_cookies":           {"url": "https://example.com/", "limit": 50},
	"browser_get_cookie":             {"url": "https://example.com/", "name": "session"},
	"browser_set_cookie":             {"url": "https://example.com/", "name": "preference", "value": "compact", "sameSite": "lax"},
	"browser_remove_cookie":          {"url": "https://example.com/", "name": "preference"},
	"browser_list_storage_items":     {"origin": "https://example.com", "storageType": "localStorage", "limit": 50},
	"browser_get_storage_item":       {"origin": "https://example.com", "storageType": "localStorage", "key": "theme"},
	"browser_set_storage_item":       {"origin": "https://example.com", "storageType": "localStorage", "key": "theme", "value": "dark"},
	"browser_remove_storage_item":    {"origin": "https://example.com", "storageType": "localStorage", "key": "theme"},
	"browser_get_cache_metadata":     {"origin": "https://example.com", "limit": 50},
	"browser_get_indexeddb_metadata": {"origin": "https://example.com", "limit": 50},
	"browser_clear_origin_storage":   {"origin": "https://example.com", "types": []string{"localStorage", "cacheStorage"}, "confirm": true},
	"browser_list_downloads":         {"state": "complete", "limit": 50},
	"browser_get_download":           {"downloadId": 7},
	"browser_create_download":        {"url": "https://example.com/archive.zip"},
	"browser_pause_download":         {"downloadId": 7},
	"browser_resume_download":        {"downloadId": 7},
	"browser_cancel_download":        {"downloadId": 7},
	"browser_erase_download_history": {"downloadId": 7, "confirm": true},
	"browser_send_command":           {"command": protocol.CommandBrowserPing, "data": map[string]any{}},
	"browser_batch": {
		"steps":       []map[string]any{{"tool": "browser_get_tabs", "arguments": map[string]any{}}},
		"stopOnError": true,
	},
}

func main() {
	output := flag.String("output", defaultOutputPath, "generated Markdown output path")
	check := flag.Bool("check", false, "fail when the output file is not current")
	flag.Parse()

	document, err := renderReference()
	if err != nil {
		log.Fatal(err)
	}
	if *check {
		current, readErr := os.ReadFile(*output)
		if readErr != nil {
			log.Fatalf("read generated tool reference: %v", readErr)
		}
		if !bytes.Equal(current, document) {
			log.Fatalf("%s is stale; run make tool-reference", *output)
		}
		return
	}
	if err := os.WriteFile(*output, document, 0o644); err != nil {
		log.Fatalf("write generated tool reference: %v", err)
	}
}

func renderReference() ([]byte, error) {
	registered, err := registeredTools()
	if err != nil {
		return nil, err
	}
	if err := validateCatalog(registered); err != nil {
		return nil, err
	}
	sort.Slice(registered, func(left, right int) bool {
		leftCategory := categoryOrder(categoryFor(registered[left].Name))
		rightCategory := categoryOrder(categoryFor(registered[right].Name))
		if leftCategory != rightCategory {
			return leftCategory < rightCategory
		}
		return registered[left].Name < registered[right].Name
	})

	var output strings.Builder
	output.WriteString("# MCP Tool Reference\n\n")
	output.WriteString("<!-- Generated by `make tool-reference`; do not edit manually. -->\n\n")
	output.WriteString("This reference is generated from the exact MCP schemas registered by the Go server. ")
	output.WriteString("It documents every currently exposed tool, including tools whose extension backend is not complete yet.\n\n")
	output.WriteString("## Calling Conventions\n\n")
	output.WriteString("`browserId` is optional on routed tools when the MCP session has a selected browser or exactly one browser is connected. ")
	output.WriteString("Likewise, `tabId` may resolve from the session's selected tab or the active tab. Explicit IDs are safest in concurrent workflows. ")
	output.WriteString("`timeoutMs` must be between 1 and 120000 when supplied.\n\n")
	output.WriteString("The server returns JSON as MCP text content. Successful responses use `success`, `browserId`, optional `target`, `data`, `warnings`, optional pagination or artifact fields, optional `durationMs`, and `timestamp`. ")
	output.WriteString("Failures set MCP `isError` and return the same envelope with `error.code`, `error.message`, `error.retryable`, and optional safe details.\n\n")
	output.WriteString("The MCP tool profiles are nested: `minimal` is included in `standard`, and `standard` is included in `full`. ")
	output.WriteString("Extension permission profiles are separate: Core is installed by default; Observe, Debug, and Personal data require an explicit user grant.\n\n")

	currentCategory := ""
	for _, tool := range registered {
		category := categoryFor(tool.Name)
		if category != currentCategory {
			fmt.Fprintf(&output, "## %s\n\n", category)
			currentCategory = category
		}
		if err := renderTool(&output, tool); err != nil {
			return nil, err
		}
	}
	return []byte(strings.TrimSpace(output.String()) + "\n"), nil
}

func renderTool(output *strings.Builder, tool mcp.Tool) error {
	profile, ok := browsertools.ToolProfileName(tool.Name)
	if !ok {
		return fmt.Errorf("tool %q has no profile", tool.Name)
	}
	capability := toolCapabilities[tool.Name]
	schema, err := json.MarshalIndent(tool.InputSchema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s input schema: %w", tool.Name, err)
	}
	example, err := exampleFor(tool)
	if err != nil {
		return err
	}
	exampleJSON, err := json.MarshalIndent(map[string]any{
		"name":      tool.Name,
		"arguments": example,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s example: %w", tool.Name, err)
	}

	fmt.Fprintf(output, "### `%s`\n\n", tool.Name)
	fmt.Fprintf(output, "%s.\n\n", strings.TrimSuffix(tool.Description, "."))
	fmt.Fprintf(output, "- MCP profile: `%s`\n", profile)
	fmt.Fprintf(output, "- Extension capability: %s\n", capabilityDescription(capability))
	fmt.Fprintf(output, "- Permissions: %s\n", permissionDescription(capability))
	fmt.Fprintf(output, "- Result: %s\n", resultDescription(tool.Name))
	fmt.Fprintf(output, "- Errors: %s\n\n", errorDescription(tool.Name, capability))
	output.WriteString("Input schema:\n\n```json\n")
	output.Write(schema)
	output.WriteString("\n```\n\nExample MCP tool payload:\n\n```json\n")
	output.Write(exampleJSON)
	output.WriteString("\n```\n\n")
	return nil
}

func registeredTools() ([]mcp.Tool, error) {
	browserRegistry := registry.New()
	mcpServer := server.NewMCPServer("tool-reference", "generated")
	browsertools.NewService(
		browserRegistry,
		router.New(browserRegistry, router.WithLogger(log.New(io.Discard, "", 0))),
		selection.NewStore(),
	).Register(mcpServer)
	response := mcpServer.HandleMessage(
		context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`),
	)
	jsonRPC, ok := response.(mcp.JSONRPCResponse)
	if !ok {
		return nil, fmt.Errorf("tools/list returned %T", response)
	}
	result, ok := jsonRPC.Result.(mcp.ListToolsResult)
	if !ok {
		return nil, fmt.Errorf("tools/list result is %T", jsonRPC.Result)
	}
	return result.Tools, nil
}

func validateCatalog(registered []mcp.Tool) error {
	seen := make(map[string]bool, len(registered))
	for _, tool := range registered {
		if tool.Description == "" {
			return fmt.Errorf("tool %q has no description", tool.Name)
		}
		if _, ok := toolCapabilities[tool.Name]; !ok {
			return fmt.Errorf("tool %q has no documented capability", tool.Name)
		}
		if _, ok := browsertools.ToolProfileName(tool.Name); !ok {
			return fmt.Errorf("tool %q has no documented profile", tool.Name)
		}
		seen[tool.Name] = true
	}
	for name := range toolCapabilities {
		if !seen[name] {
			return fmt.Errorf("capability catalog contains unregistered tool %q", name)
		}
	}
	return nil
}

func exampleFor(tool mcp.Tool) (map[string]any, error) {
	if example, ok := exampleOverrides[tool.Name]; ok {
		return example, nil
	}
	schemaJSON, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("marshal %s schema for example: %w", tool.Name, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return nil, fmt.Errorf("decode %s schema for example: %w", tool.Name, err)
	}
	properties, _ := schema["properties"].(map[string]any)
	required, _ := schema["required"].([]any)
	example := make(map[string]any, len(required))
	for _, rawName := range required {
		name, ok := rawName.(string)
		if !ok {
			return nil, errors.New("input schema contains a non-string required property")
		}
		property, _ := properties[name].(map[string]any)
		example[name] = exampleValue(name, property)
	}
	return example, nil
}

func exampleValue(name string, schema map[string]any) any {
	switch name {
	case "browserId":
		return "browser-id"
	case "tabId":
		return 42
	case "windowId", "groupId":
		return 1
	case "url":
		return "https://example.com/"
	case "selector":
		return "main"
	case "displayName":
		return "Work Chrome"
	case "locator", "source", "targetLocator":
		return map[string]any{"css": "button"}
	case "tabIds":
		return []int{41, 42}
	case "values":
		return []string{"value"}
	case "confirm", "pinned", "muted", "checked":
		return true
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	switch schema["type"] {
	case "boolean":
		return true
	case "integer", "number":
		if minimum, ok := schema["minimum"].(float64); ok {
			return minimum
		}
		return 1
	case "array":
		return []any{"value"}
	case "object":
		return map[string]any{}
	default:
		return "value"
	}
}

func capabilityDescription(capability string) string {
	switch capability {
	case serverLocal:
		return "server-local; no extension command"
	case batchCapability:
		return "the capability required by each nested typed command"
	case dynamicCapability:
		return "the value of `command`; the extension still requires it in its advertised capability set and dedicated-only capabilities are rejected"
	default:
		return "`" + capability + "`"
	}
}

func permissionDescription(capability string) string {
	switch {
	case capability == serverLocal:
		return "authenticated MCP access only; no browser permission"
	case capability == batchCapability:
		return "depends on every nested command; each command is checked independently"
	case capability == dynamicCapability:
		return "depends on `command`; the tool itself also requires the `full` MCP profile"
	case capability == protocol.CommandTabGroupsUpdate:
		return "Personal data (`tabGroups`)"
	case strings.HasPrefix(capability, "sessions."):
		return "Personal data (`sessions`)"
	case strings.HasPrefix(capability, "network."):
		return "Debug (`debugger`) plus Observe (HTTP/HTTPS site access), Core `webNavigation`, and MCP `full`"
	case capability == protocol.CommandCookiesList || capability == protocol.CommandCookiesGet:
		return "Personal data (`cookies`) plus Observe (HTTP/HTTPS site access), Core `tabs`/`webNavigation`, and MCP `full`; unmasked reads also require Sensitive data mode"
	case capability == protocol.CommandCookiesSet || capability == protocol.CommandCookiesRemove:
		return "Personal data (`cookies`) plus Observe (HTTP/HTTPS site access), Core `tabs`/`webNavigation`, and MCP `full`"
	case capability == protocol.CommandStorageList || capability == protocol.CommandStorageGet:
		return "Personal data (`browsingData` profile marker) plus Observe (HTTP/HTTPS site access), Core `tabs`/`scripting`/`webNavigation`, and MCP `full`; unmasked Web Storage reads also require Sensitive data mode"
	case strings.HasPrefix(capability, "storage."):
		return "Personal data (`browsingData` profile marker) plus Observe (HTTP/HTTPS site access), Core `tabs`/`scripting`/`webNavigation`, and MCP `full`"
	case strings.HasPrefix(capability, "downloads."):
		return "Personal data (`downloads`) and MCP `full`; file contents and absolute local paths are never exposed"
	case capability == protocol.CommandPagePrintToPDF:
		return "Debug (`debugger`) plus Observe (HTTP/HTTPS site access)"
	case capability == protocol.CommandAccessibilityGetTree:
		return "Debug (`debugger`) plus Observe (HTTP/HTTPS site access) and Core `scripting`/`webNavigation` for document-scoped references"
	case capability == protocol.CommandEmulationReset:
		return "Debug (`debugger`); cleanup remains available without target-origin access"
	case strings.HasPrefix(capability, "emulation."):
		return "Debug (`debugger`) plus Observe (HTTP/HTTPS site access) and Core `webNavigation` for root-document identity"
	case capability == protocol.CommandRuntimeEvaluateIsolated:
		return "Debug (`debugger`) plus Observe (HTTP/HTTPS site access), Core `webNavigation`, MCP `full`, and the disabled-by-default JavaScript evaluation feature flag"
	case capability == protocol.CommandCDPSendReadOnly:
		return "Debug (`debugger`) plus Observe (HTTP/HTTPS site access), Core `webNavigation`, MCP `full`, and the disabled-by-default raw CDP feature flag"
	case capability == protocol.CommandPerformanceMetrics || capability == protocol.CommandPerformanceCapture:
		return "Debug (`debugger`) plus Observe (HTTP/HTTPS site access), Core `webNavigation`, and MCP `full`"
	case capability == protocol.CommandPageScreenshot:
		return "Observe (HTTP/HTTPS site access) plus Core `scripting` and `webNavigation`; `fullPage` and `element` modes also require Debug (`debugger`)"
	case trustedInputCapability(capability):
		return "Observe (HTTP/HTTPS site access) plus Core `scripting` and `webNavigation`; explicit root-document `cdp` input also requires Debug (`debugger`)"
	case strings.HasPrefix(capability, "page.") || strings.HasPrefix(capability, "console."):
		return "Observe (HTTP/HTTPS site access) plus Core `scripting` and `webNavigation`"
	default:
		return "Core; `tabs` is required for tab metadata and operations"
	}
}

func trustedInputCapability(capability string) bool {
	switch capability {
	case protocol.CommandPageClick, protocol.CommandPageFill, protocol.CommandPageHover,
		protocol.CommandPageType, protocol.CommandPageClear, protocol.CommandPagePress,
		protocol.CommandPageSetChecked, protocol.CommandPageScroll:
		return true
	default:
		return false
	}
}

func resultDescription(name string) string {
	switch name {
	case "browser_list":
		return "connected and recently disconnected browser summaries plus `connectedCount` in `data`"
	case "browser_get", "browser_get_capabilities":
		return "browser identity, connection, capability, and permission metadata in `data`"
	case "browser_select", "browser_select_tab", "browser_rename":
		return "the updated session selection or browser metadata in `data`"
	case "browser_get_selected":
		return "the current browser/tab selection and its connection state in `data`"
	case "browser_ping":
		return "the extension pong payload and measured `durationMs`"
	case "browser_batch":
		return "ordered nested result envelopes, completion state, and deadline state; execution is sequential with no rollback"
	case "browser_get_windows":
		return "an array of normalized windows in `data.windows`"
	case "browser_get_window", "browser_create_window", "browser_update_window", "browser_focus_window":
		return "the normalized affected window in `data`"
	case "browser_close_window":
		return "confirmation that the addressed window was closed"
	case "browser_get_tabs":
		return "an array of normalized tabs in `data.tabs`"
	case "browser_get_tab", "browser_create_tab", "browser_activate_tab", "browser_navigate_tab",
		"browser_reload_tab", "browser_stop_tab", "browser_go_back", "browser_go_forward",
		"browser_move_tab", "browser_duplicate_tab", "browser_pin_tab", "browser_mute_tab":
		return "the normalized affected tab or operation acknowledgement in `data`"
	case "browser_close_tab":
		return "confirmation that the addressed tab was closed"
	case "browser_get_tab_zoom", "browser_set_tab_zoom":
		return "the tab ID and zoom factor in `data`"
	case "browser_group_tabs", "browser_ungroup_tabs", "browser_update_tab_group":
		return "the affected group ID, tabs, or normalized group metadata in `data`"
	case "browser_get_recently_closed":
		return "bounded recently closed tab/window entries in `data.sessions`"
	case "browser_restore_session":
		return "the restored tab or window in `data`"
	case "browser_page_info":
		return "page URL/title, document/frame, viewport, and scroll metadata in `data`"
	case "browser_get_html", "browser_get_html_by_selector":
		return "bounded redacted HTML and truncation metadata in `data`"
	case "browser_get_text":
		return "bounded redacted visible text; pagination may set `nextCursor`"
	case "browser_query":
		return "a bounded page of normalized locator matches and optional `nextCursor`"
	case "browser_get_element":
		return "one strict match with document-scoped element reference and normalized attributes"
	case "browser_snapshot":
		return "a bounded semantic tree with document-scoped element references"
	case "browser_screenshot":
		return "image metadata plus `artifactUri` and artifact metadata URI; binary data stays in the artifact store"
	case "browser_print_to_pdf":
		return "PDF metadata and normalized print settings plus `artifactUri`; binary data stays in the artifact store"
	case "browser_get_accessibility_tree":
		return "a bounded normalized full or partial AX tree with frame associations and optional locator/reference links"
	case "browser_set_emulation", "browser_get_emulation_state", "browser_reset_emulation":
		return "the tab-scoped managed emulation state, applied setting groups, and reset-on-detach guarantee"
	case "browser_evaluate_javascript":
		return "a bounded JSON-safe value, unsupported/unserializable marker, or bounded exception from an ephemeral root-frame isolated world"
	case "browser_send_cdp_command":
		return "the independently validated, bounded, redacted result of one reviewed read-only CDP method plus truncation metadata"
	case "browser_get_performance_metrics":
		return "bounded numeric runtime metrics inline with the resolved root-document identity"
	case "browser_capture_performance":
		return "capture metadata plus an owner-only JSON `artifactUri`; trace, coverage, CPU profile, or audit content is never returned inline"
	case "browser_wait":
		return "the satisfied condition, observation mode, elapsed time, and matching state in `data`"
	case "browser_start_console_capture", "browser_stop_console_capture", "browser_clear_console_log":
		return "capture state and document identity in `data`"
	case "browser_get_console_log":
		return "filtered redacted console/page-error entries and optional `nextCursor`"
	case "browser_start_network_capture", "browser_stop_network_capture", "browser_clear_network_log":
		return "the document-scoped capture state, retained/evicted counts, byte usage, and TTL metadata"
	case "browser_get_network_log":
		return "bounded redacted request/response metadata with public entry IDs and an optional pagination cursor"
	case "browser_get_network_body":
		return "metadata plus an owner-only artifact URI for one redacted same-origin textual request or response body"
	case "browser_export_network_har":
		return "metadata plus an owner-only bounded HAR 1.2-like artifact URI; bodies are excluded"
	case "browser_list_cookies":
		return "bounded paginated exact-origin cookie metadata; values are masked unless `includeValues` is explicitly requested and Sensitive data mode is enabled"
	case "browser_get_cookie":
		return "zero or one exact-origin cookie; its value is masked unless `includeValue` is explicitly requested and Sensitive data mode is enabled"
	case "browser_set_cookie":
		return "the normalized cookie metadata with its value masked; the supplied value is never echoed"
	case "browser_remove_cookie":
		return "whether one exact-origin cookie was removed, without cookie content"
	case "browser_list_storage_items":
		return "bounded paginated exact-origin Web Storage items; values are masked unless explicitly requested with Sensitive data mode enabled"
	case "browser_get_storage_item":
		return "zero or one exact-origin Web Storage item; its value is masked unless explicitly requested with Sensitive data mode enabled"
	case "browser_set_storage_item", "browser_remove_storage_item":
		return "whether the exact-origin Web Storage item changed; supplied and previous values are never returned"
	case "browser_get_cache_metadata":
		return "bounded paginated exact-origin Cache Storage names without requests, responses, or bodies"
	case "browser_get_indexeddb_metadata":
		return "bounded paginated exact-origin IndexedDB names and versions without stores, records, or blobs"
	case "browser_clear_origin_storage":
		return "requested storage types, completed types, bounded deletion counts, and warnings; no stored content is returned"
	case "browser_list_downloads":
		return "bounded paginated lifecycle metadata with URL secrets removed and only a basename instead of the absolute local path"
	case "browser_get_download":
		return "one bounded download status record with URL secrets removed and only a basename instead of the absolute local path"
	case "browser_create_download":
		return "the new persistent download ID without a local path or file content"
	case "browser_pause_download", "browser_resume_download", "browser_cancel_download":
		return "the updated bounded download status and lifecycle operation"
	case "browser_erase_download_history":
		return "the erased download ID and a warning that the downloaded file was not deleted"
	case "browser_send_command":
		return "the selected extension command's bounded, redacted payload in `data`"
	default:
		return "the addressed action acknowledgement and resulting element/page state in `data`"
	}
}

func errorDescription(name, capability string) string {
	errors := []string{"`INVALID_MESSAGE` for invalid arguments"}
	if capability != serverLocal {
		errors = append(errors,
			"browser selection/connection errors",
			"`CAPABILITY_UNAVAILABLE`",
			"`TIMEOUT` or `CANCELLED`",
		)
	}
	if (strings.HasPrefix(capability, "page.") || strings.HasPrefix(capability, "console.") ||
		strings.HasPrefix(capability, "accessibility.") || strings.HasPrefix(capability, "emulation.") ||
		strings.HasPrefix(capability, "performance.") || strings.HasPrefix(capability, "network.") ||
		strings.HasPrefix(capability, "cookies.") ||
		strings.HasPrefix(capability, "storage.") ||
		capability == protocol.CommandRuntimeEvaluateIsolated ||
		capability == protocol.CommandCDPSendReadOnly) &&
		capability != protocol.CommandEmulationReset {
		errors = append(errors,
			"`TAB_NOT_FOUND`, `FRAME_NOT_FOUND`, or `STALE_TARGET`",
			"`PERMISSION_REQUIRED` or `RESTRICTED_URL`",
		)
	}
	if capability == protocol.CommandEmulationReset {
		errors = append(errors, "`TAB_NOT_FOUND`", "`PERMISSION_REQUIRED` when Debug was revoked")
	}
	if strings.Contains(name, "element") || isInteractionTool(name) {
		errors = append(errors, "`ELEMENT_NOT_FOUND` or `STRICT_MODE_VIOLATION`")
	}
	switch name {
	case "browser_list", "browser_get_selected":
		errors = []string{"`INTERNAL_ERROR` only for an unexpected server failure"}
	case "browser_get", "browser_select", "browser_select_tab", "browser_rename", "browser_get_capabilities":
		errors = append(errors, "`BROWSER_NOT_FOUND` or `BROWSER_DISCONNECTED`")
	case "browser_close_window":
		errors = append(errors, "`CONFIRMATION_REQUIRED` unless `confirm` is true")
	case "browser_create_window", "browser_create_tab", "browser_navigate_tab":
		errors = append(errors, "`RESTRICTED_URL` for a disallowed URL or browser store")
	case "browser_screenshot", "browser_print_to_pdf":
		errors = append(errors, "`PAYLOAD_TOO_LARGE` or artifact storage failure")
		if name == "browser_screenshot" {
			errors = append(errors, "`ELEMENT_NOT_FOUND` or `STRICT_MODE_VIOLATION` for element capture")
		}
	case "browser_get_accessibility_tree":
		errors = append(errors, "`PAYLOAD_TOO_LARGE` for a tree above the configured byte limit")
	case "browser_evaluate_javascript":
		errors = append(errors, "`PAYLOAD_TOO_LARGE` for a result above the configured byte limit")
	case "browser_send_cdp_command":
		errors = append(errors,
			"`INVALID_COMMAND` for a prohibited or unreviewed CDP method",
			"`PAYLOAD_TOO_LARGE` for a result above the configured byte limit",
		)
	case "browser_capture_performance":
		errors = append(errors,
			"`INVALID_COMMAND` for a prohibited capture kind such as a heap snapshot",
			"`PAYLOAD_TOO_LARGE` or artifact storage failure",
		)
	case "browser_get_network_body":
		errors = append(errors,
			"`CAPABILITY_UNAVAILABLE` when capture is stopped or the body/MIME is unavailable",
			"`RESTRICTED_URL` for a cross-origin body",
			"`PAYLOAD_TOO_LARGE` or artifact storage failure",
		)
	case "browser_export_network_har":
		errors = append(errors, "`PAYLOAD_TOO_LARGE` or artifact storage failure")
	case "browser_list_cookies", "browser_get_cookie":
		errors = append(errors, "`CAPABILITY_UNAVAILABLE` when an unmasked value is requested while Sensitive data mode is disabled")
	case "browser_list_storage_items", "browser_get_storage_item":
		errors = append(errors,
			"`CAPABILITY_UNAVAILABLE` when an unmasked value is requested while Sensitive data mode is disabled",
			"`PAYLOAD_TOO_LARGE` when an area, key, value, or result exceeds a fixed bound",
		)
	case "browser_set_storage_item", "browser_remove_storage_item", "browser_get_cache_metadata", "browser_get_indexeddb_metadata":
		errors = append(errors, "`PAYLOAD_TOO_LARGE` when an area, key, value, metadata set, or result exceeds a fixed bound")
	case "browser_clear_origin_storage":
		errors = append(errors,
			"`CONFIRMATION_REQUIRED` unless `confirm` is true",
			"`PAYLOAD_TOO_LARGE` when an origin inventory exceeds a fixed bound",
		)
	case "browser_list_downloads":
		errors = append(errors, "`PAYLOAD_TOO_LARGE` when bounded history or metadata limits are exceeded")
	case "browser_get_download", "browser_pause_download", "browser_resume_download", "browser_cancel_download":
		errors = append(errors, "`DOWNLOAD_NOT_FOUND`", "`RESTRICTED_URL` for a disallowed incognito item")
	case "browser_create_download":
		errors = append(errors, "`RESTRICTED_URL` for a disallowed source URL or incognito context")
	case "browser_erase_download_history":
		errors = append(errors,
			"`DOWNLOAD_NOT_FOUND`",
			"`CONFIRMATION_REQUIRED` unless `confirm` is true",
			"`RESTRICTED_URL` for a disallowed incognito item",
		)
	case "browser_send_command":
		errors = append(errors, "`INVALID_COMMAND` for an unknown or dedicated-only command")
	case "browser_batch":
		errors = append(errors,
			"`INVALID_COMMAND` for a non-batchable nested tool",
			"`PAYLOAD_TOO_LARGE` for the combined result",
			"individual step errors retained in ordered results",
		)
	}
	return strings.Join(uniqueStrings(errors), "; ")
}

func isInteractionTool(name string) bool {
	for _, candidate := range []string{
		"browser_click_element", "browser_double_click", "browser_context_click",
		"browser_input_data", "browser_hover", "browser_focus", "browser_blur",
		"browser_type", "browser_clear", "browser_press", "browser_select_option",
		"browser_set_checked", "browser_drag_and_drop", "browser_dispatch_event",
		"browser_submit",
	} {
		if name == candidate {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func categoryFor(name string) string {
	switch {
	case name == "browser_list" || name == "browser_get" || name == "browser_select" ||
		name == "browser_get_selected" || name == "browser_select_tab" ||
		name == "browser_rename" || name == "browser_get_capabilities" || name == "browser_ping":
		return "Browser Discovery and Selection"
	case strings.Contains(name, "window"):
		return "Windows"
	case strings.Contains(name, "tab") || strings.Contains(name, "recently_closed") || strings.Contains(name, "restore_session"):
		return "Tabs, Groups, and Sessions"
	case strings.Contains(name, "cookie") || strings.Contains(name, "storage") ||
		name == "browser_get_cache_metadata" || name == "browser_get_indexeddb_metadata":
		return "Cookies and Personal Data"
	case strings.Contains(name, "download"):
		return "Downloads"
	case name == "browser_page_info" || name == "browser_get_html" ||
		name == "browser_get_html_by_selector" || name == "browser_get_text" ||
		name == "browser_query" || name == "browser_get_element" || name == "browser_snapshot" ||
		name == "browser_get_accessibility_tree" || name == "browser_evaluate_javascript" ||
		name == "browser_send_cdp_command":
		return "Page Inspection"
	case isInteractionTool(name) || name == "browser_scroll":
		return "Page Interaction"
	case name == "browser_batch":
		return "Batch"
	default:
		return "Waits, Artifacts, and Diagnostics"
	}
}

func categoryOrder(category string) int {
	for index, candidate := range []string{
		"Browser Discovery and Selection",
		"Windows",
		"Tabs, Groups, and Sessions",
		"Cookies and Personal Data",
		"Downloads",
		"Page Inspection",
		"Page Interaction",
		"Batch",
		"Waits, Artifacts, and Diagnostics",
	} {
		if category == candidate {
			return index
		}
	}
	return 100
}
