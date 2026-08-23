package selection

import (
	"errors"
	"testing"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
)

func TestStoreResolve(t *testing.T) {
	t.Parallel()

	browserA := registry.Browser{BrowserID: "browser-a", DisplayName: "A", Connected: true}
	browserB := registry.Browser{BrowserID: "browser-b", DisplayName: "B", Connected: true}

	tests := []struct {
		name      string
		selected  string
		explicit  string
		browsers  []registry.Browser
		want      string
		wantError protocol.ErrorCode
	}{
		{
			name:     "explicit browser",
			explicit: browserB.BrowserID,
			browsers: []registry.Browser{browserA, browserB},
			want:     browserB.BrowserID,
		},
		{
			name:     "single browser fallback",
			browsers: []registry.Browser{browserA},
			want:     browserA.BrowserID,
		},
		{
			name:     "session selection",
			selected: browserB.BrowserID,
			browsers: []registry.Browser{browserA, browserB},
			want:     browserB.BrowserID,
		},
		{
			name:      "no browser",
			wantError: protocol.CodeNoBrowserConnected,
		},
		{
			name:      "ambiguous",
			browsers:  []registry.Browser{browserA, browserB},
			wantError: protocol.CodeAmbiguousBrowser,
		},
		{
			name:      "stale selection",
			selected:  "browser-gone",
			browsers:  []registry.Browser{browserA},
			wantError: protocol.CodeBrowserDisconnected,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := NewStore()
			if test.selected != "" {
				if err := store.Set("session-1", test.selected); err != nil {
					t.Fatalf("Set() error = %v", err)
				}
			}

			got, err := store.Resolve("session-1", test.explicit, test.browsers)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("Resolve() error = %v", err)
				}
				if got != test.want {
					t.Errorf("Resolve() = %q, want %q", got, test.want)
				}
				return
			}

			var protocolErr *protocol.Error
			if !errors.As(err, &protocolErr) {
				t.Fatalf("Resolve() error type = %T, want *protocol.Error", err)
			}
			if protocolErr.Code != test.wantError {
				t.Errorf("Resolve() code = %q, want %q", protocolErr.Code, test.wantError)
			}
		})
	}
}
