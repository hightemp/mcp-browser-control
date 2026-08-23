package registry

import (
	"context"
	"sync"
	"testing"

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
