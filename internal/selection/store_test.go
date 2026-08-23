package selection

import (
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

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

func TestStoreKeepsBrowserScopedTabSelections(t *testing.T) {
	t.Parallel()

	currentTime := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	store := NewStore()
	store.now = func() time.Time { return currentTime }
	if err := store.Set("session-a", "browser-a"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := store.SetTab("session-a", "browser-a", 11); err != nil {
		t.Fatalf("SetTab(browser-a) error = %v", err)
	}
	if err := store.SetTab("session-a", "browser-b", 22); err != nil {
		t.Fatalf("SetTab(browser-b) error = %v", err)
	}

	currentTime = currentTime.Add(time.Minute)
	tabA, err := store.ResolveTab("session-a", "browser-a", nil)
	if err != nil || tabA == nil || *tabA != 11 {
		t.Fatalf("ResolveTab(browser-a) = (%v, %v)", tabA, err)
	}
	tabB, err := store.ResolveTab("session-a", "browser-b", nil)
	if err != nil || tabB == nil || *tabB != 22 {
		t.Fatalf("ResolveTab(browser-b) = (%v, %v)", tabB, err)
	}
	explicit := 99
	resolved, err := store.ResolveTab("session-a", "browser-a", &explicit)
	if err != nil || resolved == nil || *resolved != explicit {
		t.Fatalf("ResolveTab(explicit) = (%v, %v)", resolved, err)
	}
	storedTab, ok := store.GetTab("session-a", "browser-a")
	if !ok || storedTab.TabID != 11 || storedTab.LastUsedAt != currentTime {
		t.Fatalf("stored tab = %#v", storedTab)
	}

	selection, ok := store.Get("session-a")
	if !ok || selection.BrowserID != "browser-a" || selection.LastUsedAt != currentTime {
		t.Fatalf("selection = %#v", selection)
	}
	delete(selection.Tabs, "browser-a")
	if _, ok := store.GetTab("session-a", "browser-a"); !ok {
		t.Fatal("Get() exposed mutable tab map")
	}
	store.Delete("session-a")
	if _, ok := store.Get("session-a"); ok {
		t.Fatal("Delete() retained session state")
	}
}

func TestStoreIsolatesConcurrentSessions(t *testing.T) {
	t.Parallel()

	store := NewStore()
	const sessionCount = 128
	var waitGroup sync.WaitGroup
	for index := range sessionCount {
		index := index
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			sessionID := "session-" + strconv.Itoa(index)
			browserID := "browser-a"
			if index%2 == 1 {
				browserID = "browser-b"
			}
			if err := store.Set(sessionID, browserID); err != nil {
				t.Errorf("Set(%d) error = %v", index, err)
				return
			}
			if err := store.SetTab(sessionID, browserID, index); err != nil {
				t.Errorf("SetTab(%d) error = %v", index, err)
			}
		}()
	}
	waitGroup.Wait()
	for index := range sessionCount {
		sessionID := "session-" + strconv.Itoa(index)
		selection, ok := store.Get(sessionID)
		if !ok {
			t.Fatalf("Get(%d) = false", index)
		}
		tab, ok := store.GetTab(sessionID, selection.BrowserID)
		if !ok || tab.TabID != index {
			t.Fatalf("session %d tab = %#v", index, tab)
		}
	}
}

func TestStoreRejectsInvalidTabSelection(t *testing.T) {
	t.Parallel()

	store := NewStore()
	for _, test := range []struct {
		sessionID string
		browserID string
		tabID     int
	}{
		{browserID: "browser-a", tabID: 1},
		{sessionID: "session-a", tabID: 1},
		{sessionID: "session-a", browserID: "browser-a", tabID: -1},
	} {
		if err := store.SetTab(test.sessionID, test.browserID, test.tabID); err == nil {
			t.Fatalf("SetTab(%#v) error = nil", test)
		}
	}
}
