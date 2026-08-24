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

	"browser_page_info":              protocol.CommandPageInfo,
	"browser_get_html":               protocol.CommandPageGetHTML,
	"browser_get_html_by_selector":   protocol.CommandPageGetHTMLBySelector,
	"browser_get_text":               protocol.CommandPageGetText,
	"browser_query":                  protocol.CommandPageQuery,
	"browser_get_element":            protocol.CommandPageGetElement,
	"browser_snapshot":               protocol.CommandPageSnapshot,
	"browser_click_element":          protocol.CommandPageClick,
	"browser_double_click":           protocol.CommandPageClick,
	"browser_context_click":          protocol.CommandPageClick,
	"browser_input_data":             protocol.CommandPageFill,
	"browser_hover":                  protocol.CommandPageHover,
	"browser_focus":                  protocol.CommandPageFocus,
	"browser_blur":                   protocol.CommandPageBlur,
	"browser_type":                   protocol.CommandPageType,
	"browser_clear":                  protocol.CommandPageClear,
	"browser_press":                  protocol.CommandPagePress,
	"browser_select_option":          protocol.CommandPageSelect,
	"browser_set_checked":            protocol.CommandPageSetChecked,
	"browser_scroll":                 protocol.CommandPageScroll,
	"browser_drag_and_drop":          protocol.CommandPageDrag,
	"browser_dispatch_event":         protocol.CommandPageDispatch,
	"browser_submit":                 protocol.CommandPageSubmit,
	"browser_wait":                   protocol.CommandPageWait,
	"browser_screenshot":             protocol.CommandPageScreenshot,
	"browser_print_to_pdf":           protocol.CommandPagePrintToPDF,
	"browser_get_accessibility_tree": protocol.CommandAccessibilityGetTree,

	"browser_start_console_capture": protocol.CommandConsoleStart,
	"browser_stop_console_capture":  protocol.CommandConsoleStop,
	"browser_clear_console_log":     protocol.CommandConsoleClear,
	"browser_get_console_log":       protocol.CommandConsoleRead,
	"browser_get_network_log":       protocol.CommandNetworkRead,
	"browser_send_command":          dynamicCapability,
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
	"browser_start_console_capture":  {"bufferSize": 500, "captureConsole": true, "captureErrors": true},
	"browser_get_console_log":        {"levels": []string{"error", "warn"}, "limit": 50},
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
		return "the value of `command`; the extension still requires it in its advertised capability set"
	case protocol.CommandNetworkRead:
		return "`network.read` (reserved; not currently advertised by the extension)"
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
	case capability == protocol.CommandNetworkRead:
		return "Debug (`debugger`); the current extension backend is not implemented"
	case capability == protocol.CommandPagePrintToPDF:
		return "Debug (`debugger`) plus Observe (HTTP/HTTPS site access)"
	case capability == protocol.CommandAccessibilityGetTree:
		return "Debug (`debugger`) plus Observe (HTTP/HTTPS site access) and Core `scripting`/`webNavigation` for document-scoped references"
	case strings.HasPrefix(capability, "page.") || strings.HasPrefix(capability, "console."):
		return "Observe (HTTP/HTTPS site access) plus Core `scripting` and `webNavigation`"
	default:
		return "Core; `tabs` is required for tab metadata and operations"
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
	case "browser_wait":
		return "the satisfied condition, observation mode, elapsed time, and matching state in `data`"
	case "browser_start_console_capture", "browser_stop_console_capture", "browser_clear_console_log":
		return "capture state and document identity in `data`"
	case "browser_get_console_log":
		return "filtered redacted console/page-error entries and optional `nextCursor`"
	case "browser_get_network_log":
		return "network entries when a future extension advertises `network.read`"
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
	if strings.HasPrefix(capability, "page.") || strings.HasPrefix(capability, "console.") ||
		strings.HasPrefix(capability, "accessibility.") {
		errors = append(errors,
			"`TAB_NOT_FOUND`, `FRAME_NOT_FOUND`, or `STALE_TARGET`",
			"`PERMISSION_REQUIRED` or `RESTRICTED_URL`",
		)
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
	case "browser_get_accessibility_tree":
		errors = append(errors, "`PAYLOAD_TOO_LARGE` for a tree above the configured byte limit")
	case "browser_get_network_log":
		errors = append(errors, "currently always `CAPABILITY_UNAVAILABLE`")
	case "browser_send_command":
		errors = append(errors, "`INVALID_COMMAND` for an unknown command")
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
	case name == "browser_page_info" || name == "browser_get_html" ||
		name == "browser_get_html_by_selector" || name == "browser_get_text" ||
		name == "browser_query" || name == "browser_get_element" || name == "browser_snapshot" ||
		name == "browser_get_accessibility_tree":
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
