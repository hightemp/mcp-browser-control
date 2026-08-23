package tools

import (
	"testing"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
)

func TestValidateWaitArgsAcceptsEveryCondition(t *testing.T) {
	t.Parallel()

	locator := &protocol.Locator{CSS: "#status"}
	tests := []struct {
		name string
		args waitArgs
	}{
		{name: "delay", args: waitArgs{Condition: "delay", DelayMS: waitPointer(0)}},
		{name: "load state", args: waitArgs{Condition: "loadState", ReadyState: "complete"}},
		{name: "exact URL", args: waitArgs{Condition: "url", URL: waitPointer("https://example.com/")}},
		{name: "URL pattern", args: waitArgs{Condition: "url", URLPattern: waitPointer("https://example.com/*")}},
		{name: "element", args: waitArgs{Condition: "element", Locator: locator, ElementState: "visible"}},
		{name: "page text", args: waitArgs{Condition: "text", Expected: waitPointer("ready")}},
		{name: "value", args: waitArgs{Condition: "value", Locator: locator, Expected: waitPointer("")}},
		{name: "count", args: waitArgs{Condition: "count", Locator: locator, Count: waitPointer(2), CountOperator: "atLeast"}},
		{name: "navigation", args: waitArgs{Condition: "navigation"}},
		{name: "network idle", args: waitArgs{Condition: "networkIdle", IdleMS: waitPointer(500)}},
		{
			name: "safe attribute",
			args: waitArgs{
				Condition: "attribute", Locator: locator, Attribute: "aria-busy",
				AttributeState: "equals", Expected: waitPointer("false"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			params, _, err := validateWaitArgs(test.args)
			if err != nil {
				t.Fatalf("validateWaitArgs() error = %v", err)
			}
			if params["condition"] != test.args.Condition {
				t.Fatalf("condition = %#v, want %q", params["condition"], test.args.Condition)
			}
		})
	}
}

func waitPointer[T any](value T) *T {
	return &value
}
