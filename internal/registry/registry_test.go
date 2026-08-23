package registry

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
)

type testConnection struct {
	id     string
	mu     sync.Mutex
	closed bool
}

func (c *testConnection) ID() string {
	return c.id
}

func (c *testConnection) Send(context.Context, protocol.Message) error {
	return nil
}

func (c *testConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func TestRegistryRegisterReplacesConnection(t *testing.T) {
	t.Parallel()

	registry := New()
	first := &testConnection{id: "connection-1"}
	second := &testConnection{id: "connection-2"}
	registration := Registration{
		BrowserID:    "browser-1",
		DisplayName:  "Work",
		Capabilities: []string{"tabs", "tabs", "page"},
	}

	replaced, err := registry.Register(registration, first)
	if err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if replaced != nil {
		t.Fatalf("first Register() replaced = %v, want nil", replaced)
	}

	replaced, err = registry.Register(registration, second)
	if err != nil {
		t.Fatalf("second Register() error = %v", err)
	}
	if replaced != first {
		t.Fatalf("second Register() replaced = %v, want first connection", replaced)
	}

	if registry.Unregister("browser-1", first.ID()) {
		t.Error("old connection removed the replacement")
	}
	browser, ok := registry.Get("browser-1")
	if !ok {
		t.Fatal("Get() did not find registered browser")
	}
	if browser.ConnectionID != second.ID() {
		t.Errorf("ConnectionID = %q, want %q", browser.ConnectionID, second.ID())
	}
	if len(browser.Capabilities) != 2 {
		t.Errorf("Capabilities = %v, want two unique values", browser.Capabilities)
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	t.Parallel()

	registry := New()
	const browserCount = 50
	var wg sync.WaitGroup

	for i := 0; i < browserCount; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := string(rune('A' + i))
			connection := &testConnection{id: "connection-" + id}
			if _, err := registry.Register(Registration{BrowserID: "browser-" + id}, connection); err != nil {
				t.Errorf("Register() error = %v", err)
				return
			}
			registry.Touch("browser-"+id, connection.ID())
			registry.List()
		}()
	}

	wg.Wait()
	if got := registry.Count(); got != browserCount {
		t.Errorf("Count() = %d, want %d", got, browserCount)
	}
}

func TestRegistryUpdatesCurrentConnection(t *testing.T) {
	t.Parallel()

	registry := New()
	connection := &testConnection{id: "connection-1"}
	if _, err := registry.Register(Registration{BrowserID: "browser-1"}, connection); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if registry.UpdateCapabilities("browser-1", "old-connection", nil, nil) {
		t.Fatal("UpdateCapabilities() accepted stale connection")
	}
	if !registry.UpdateCapabilities(
		"browser-1",
		connection.ID(),
		[]string{"page", "tabs", "page"},
		[]string{"tabs", "tabs"},
	) {
		t.Fatal("UpdateCapabilities() = false")
	}
	browser, ok := registry.Get("browser-1")
	if !ok {
		t.Fatal("Get() = false")
	}
	if got := len(browser.Capabilities); got != 2 {
		t.Errorf("capability count = %d, want 2", got)
	}
	if _, err := registry.Rename("browser-1", "  "); err == nil {
		t.Fatal("Rename(blank) error = nil")
	}
	if _, err := registry.Rename("missing", "Name"); err == nil {
		t.Fatal("Rename(missing) error = nil")
	}
}

func TestRegistryRetainsSafeDisconnectedSnapshot(t *testing.T) {
	t.Parallel()

	currentTime := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	registry := New()
	registry.now = func() time.Time { return currentTime }
	connection := &testConnection{id: "connection-1"}
	registration := Registration{
		BrowserID:    "browser-1",
		DisplayName:  "Work",
		Capabilities: []string{"tabs.list"},
		Permissions:  []string{"tabs"},
	}
	if _, err := registry.Register(registration, connection); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if !registry.RecordLatency("browser-1", connection.ID(), 1250*time.Microsecond) {
		t.Fatal("RecordLatency() = false")
	}

	currentTime = currentTime.Add(time.Minute)
	if !registry.Disconnect("browser-1", connection.ID(), "network lost") {
		t.Fatal("Disconnect() = false")
	}
	if registry.Count() != 0 || len(registry.List()) != 0 {
		t.Fatal("disconnected browser remained in the active registry view")
	}
	all := registry.ListAll()
	if len(all) != 1 {
		t.Fatalf("ListAll() count = %d, want 1", len(all))
	}
	snapshot := all[0]
	if snapshot.Connected || snapshot.DisconnectedAt == nil ||
		snapshot.DisconnectReason != "network lost" || snapshot.LatencyMS == nil ||
		*snapshot.LatencyMS != 1 {
		t.Fatalf("disconnected snapshot = %#v", snapshot)
	}
	if _, ok := registry.Connection("browser-1"); ok {
		t.Fatal("Connection() returned disconnected writer")
	}
	if registry.Touch("browser-1", connection.ID()) {
		t.Fatal("Touch() accepted disconnected connection")
	}

	snapshot.Capabilities[0] = "mutated"
	*snapshot.DisconnectedAt = time.Time{}
	*snapshot.LatencyMS = 999
	stored, ok := registry.Get("browser-1")
	if !ok {
		t.Fatal("Get() = false")
	}
	if stored.Capabilities[0] != "tabs.list" || stored.DisconnectedAt.IsZero() || *stored.LatencyMS != 1 {
		t.Fatalf("stored snapshot was mutated: %#v", stored)
	}

	if removed := registry.PruneDisconnected(currentTime.Add(-time.Second)); removed != 0 {
		t.Fatalf("PruneDisconnected(recent) = %d, want 0", removed)
	}
	if removed := registry.PruneDisconnected(currentTime.Add(time.Second)); removed != 1 {
		t.Fatalf("PruneDisconnected(expired) = %d, want 1", removed)
	}
	if _, ok := registry.Get("browser-1"); ok {
		t.Fatal("pruned browser still exists")
	}
}

func TestRegistryReconnectPreservesIdentityState(t *testing.T) {
	t.Parallel()

	currentTime := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	registry := New()
	registry.now = func() time.Time { return currentTime }
	first := &testConnection{id: "connection-1"}
	if _, err := registry.Register(
		Registration{BrowserID: "browser-1", DisplayName: "Work"},
		first,
	); err != nil {
		t.Fatalf("Register(first) error = %v", err)
	}
	firstSeen := currentTime
	if !registry.Disconnect("browser-1", first.ID(), "restart") {
		t.Fatal("Disconnect(first) = false")
	}

	currentTime = currentTime.Add(time.Minute)
	second := &testConnection{id: "connection-2"}
	replaced, err := registry.Register(Registration{BrowserID: "browser-1"}, second)
	if err != nil {
		t.Fatalf("Register(second) error = %v", err)
	}
	if replaced != nil {
		t.Fatalf("Register(second) replaced = %v, want nil disconnected connection", replaced)
	}
	browser, ok := registry.Get("browser-1")
	if !ok {
		t.Fatal("Get() = false")
	}
	if browser.DisplayName != "Work" || browser.FirstSeen != firstSeen ||
		browser.ConnectedAt != currentTime || browser.DisconnectedAt != nil ||
		browser.DisconnectReason != "" || !browser.Connected {
		t.Fatalf("reconnected browser = %#v", browser)
	}
	if registry.Disconnect("browser-1", first.ID(), "stale close") {
		t.Fatal("stale connection disconnected replacement")
	}
}
