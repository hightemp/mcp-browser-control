package tools

import (
	"context"
	"log"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type toolProfileLevel uint8

const (
	toolProfileMinimal toolProfileLevel = iota + 1
	toolProfileStandard
	toolProfileFull
)

// Every browser tool is classified explicitly so newly added tools fail
// closed until their profile is reviewed.
var browserToolLevels = map[string]toolProfileLevel{
	"browser_list":             toolProfileMinimal,
	"browser_get":              toolProfileMinimal,
	"browser_select":           toolProfileMinimal,
	"browser_get_selected":     toolProfileMinimal,
	"browser_select_tab":       toolProfileMinimal,
	"browser_rename":           toolProfileMinimal,
	"browser_get_capabilities": toolProfileMinimal,
	"browser_ping":             toolProfileMinimal,
	"browser_get_windows":      toolProfileMinimal,
	"browser_get_window":       toolProfileMinimal,
	"browser_get_tabs":         toolProfileMinimal,
	"browser_get_tab":          toolProfileMinimal,
	"browser_get_tab_zoom":     toolProfileMinimal,

	"browser_create_window":         toolProfileStandard,
	"browser_update_window":         toolProfileStandard,
	"browser_focus_window":          toolProfileStandard,
	"browser_close_window":          toolProfileStandard,
	"browser_create_tab":            toolProfileStandard,
	"browser_activate_tab":          toolProfileStandard,
	"browser_navigate_tab":          toolProfileStandard,
	"browser_reload_tab":            toolProfileStandard,
	"browser_stop_tab":              toolProfileStandard,
	"browser_go_back":               toolProfileStandard,
	"browser_go_forward":            toolProfileStandard,
	"browser_move_tab":              toolProfileStandard,
	"browser_duplicate_tab":         toolProfileStandard,
	"browser_close_tab":             toolProfileStandard,
	"browser_pin_tab":               toolProfileStandard,
	"browser_mute_tab":              toolProfileStandard,
	"browser_set_tab_zoom":          toolProfileStandard,
	"browser_page_info":             toolProfileStandard,
	"browser_get_html":              toolProfileStandard,
	"browser_get_html_by_selector":  toolProfileStandard,
	"browser_get_text":              toolProfileStandard,
	"browser_query":                 toolProfileStandard,
	"browser_get_element":           toolProfileStandard,
	"browser_snapshot":              toolProfileStandard,
	"browser_click_element":         toolProfileStandard,
	"browser_double_click":          toolProfileStandard,
	"browser_context_click":         toolProfileStandard,
	"browser_input_data":            toolProfileStandard,
	"browser_hover":                 toolProfileStandard,
	"browser_focus":                 toolProfileStandard,
	"browser_blur":                  toolProfileStandard,
	"browser_type":                  toolProfileStandard,
	"browser_clear":                 toolProfileStandard,
	"browser_press":                 toolProfileStandard,
	"browser_select_option":         toolProfileStandard,
	"browser_set_checked":           toolProfileStandard,
	"browser_scroll":                toolProfileStandard,
	"browser_drag_and_drop":         toolProfileStandard,
	"browser_dispatch_event":        toolProfileStandard,
	"browser_submit":                toolProfileStandard,
	"browser_wait":                  toolProfileStandard,
	"browser_screenshot":            toolProfileStandard,
	"browser_start_console_capture": toolProfileStandard,
	"browser_stop_console_capture":  toolProfileStandard,
	"browser_clear_console_log":     toolProfileStandard,
	"browser_get_console_log":       toolProfileStandard,
	"browser_batch":                 toolProfileStandard,

	"browser_group_tabs":              toolProfileFull,
	"browser_ungroup_tabs":            toolProfileFull,
	"browser_update_tab_group":        toolProfileFull,
	"browser_get_recently_closed":     toolProfileFull,
	"browser_restore_session":         toolProfileFull,
	"browser_start_network_capture":   toolProfileFull,
	"browser_stop_network_capture":    toolProfileFull,
	"browser_clear_network_log":       toolProfileFull,
	"browser_get_network_log":         toolProfileFull,
	"browser_get_network_body":        toolProfileFull,
	"browser_export_network_har":      toolProfileFull,
	"browser_print_to_pdf":            toolProfileFull,
	"browser_get_accessibility_tree":  toolProfileFull,
	"browser_set_emulation":           toolProfileFull,
	"browser_get_emulation_state":     toolProfileFull,
	"browser_reset_emulation":         toolProfileFull,
	"browser_evaluate_javascript":     toolProfileFull,
	"browser_send_cdp_command":        toolProfileFull,
	"browser_get_performance_metrics": toolProfileFull,
	"browser_capture_performance":     toolProfileFull,
	"browser_list_cookies":            toolProfileFull,
	"browser_get_cookie":              toolProfileFull,
	"browser_set_cookie":              toolProfileFull,
	"browser_remove_cookie":           toolProfileFull,
	"browser_list_storage_items":      toolProfileFull,
	"browser_get_storage_item":        toolProfileFull,
	"browser_set_storage_item":        toolProfileFull,
	"browser_remove_storage_item":     toolProfileFull,
	"browser_get_cache_metadata":      toolProfileFull,
	"browser_get_indexeddb_metadata":  toolProfileFull,
	"browser_clear_origin_storage":    toolProfileFull,
	"browser_send_command":            toolProfileFull,
}

// ToolProfileName returns the least-privileged MCP tool profile that exposes a
// registered browser tool. Documentation generators use the same fail-closed
// classification as the runtime tool filter.
func ToolProfileName(name string) (string, bool) {
	level, ok := browserToolLevels[name]
	if !ok {
		return "", false
	}
	switch level {
	case toolProfileMinimal:
		return "minimal", true
	case toolProfileStandard:
		return "standard", true
	case toolProfileFull:
		return "full", true
	default:
		return "", false
	}
}

// ToolProfileFilter hides browser tools outside the configured profile.
func ToolProfileFilter(profile string) server.ToolFilterFunc {
	return func(_ context.Context, available []mcp.Tool) []mcp.Tool {
		filtered := make([]mcp.Tool, 0, len(available))
		for _, tool := range available {
			if toolAllowed(profile, tool.Name) {
				filtered = append(filtered, tool)
			}
		}
		return filtered
	}
}

// ToolProfileMiddleware prevents direct calls from bypassing tools/list
// filtering and audits denied names without recording arguments.
func ToolProfileMiddleware(profile string, logger *log.Logger) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if toolAllowed(profile, request.Params.Name) {
				return next(ctx, request)
			}
			if logger != nil {
				logger.Printf("denied tool=%s profile=%s reason=tool_not_allowlisted", request.Params.Name, profile)
			}
			return errorResult(protocol.NewError(
				protocol.CodePermissionRequired,
				"the tool is disabled by the configured tool profile",
				false,
			))
		}
	}
}

func toolAllowed(profile, name string) bool {
	level, classified := browserToolLevels[name]
	if !classified {
		return false
	}
	switch profile {
	case "minimal":
		return level <= toolProfileMinimal
	case "standard":
		return level <= toolProfileStandard
	case "full":
		return level <= toolProfileFull
	default:
		return false
	}
}
