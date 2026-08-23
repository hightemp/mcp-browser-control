package mcpsession

import (
	"sync"
	"testing"
)

func TestManagerLifecycle(t *testing.T) {
	t.Parallel()

	var observerMu sync.Mutex
	var terminatedSessions []string
	manager, err := NewManager(WithTerminationObserver(func(sessionID string) {
		observerMu.Lock()
		terminatedSessions = append(terminatedSessions, sessionID)
		observerMu.Unlock()
	}))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	first := manager.Generate()
	second := manager.Generate()
	if first == "" || second == "" || first == second {
		t.Fatalf("generated IDs = %q and %q", first, second)
	}

	terminated, err := manager.Validate(first)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if terminated {
		t.Error("new session is terminated")
	}

	notAllowed, err := manager.Terminate(first)
	if err != nil {
		t.Fatalf("Terminate() error = %v", err)
	}
	if notAllowed {
		t.Error("Terminate() unexpectedly not allowed")
	}

	terminated, err = manager.Validate(first)
	if err != nil {
		t.Fatalf("Validate(terminated) error = %v", err)
	}
	if !terminated {
		t.Error("terminated session is active")
	}
	if _, err := manager.Terminate(first); err != nil {
		t.Fatalf("second Terminate() error = %v", err)
	}
	observerMu.Lock()
	if len(terminatedSessions) != 1 || terminatedSessions[0] != first {
		t.Errorf("terminated sessions = %#v", terminatedSessions)
	}
	observerMu.Unlock()

	terminated, err = manager.Validate(second)
	if err != nil || terminated {
		t.Errorf("second session Validate() = (%v, %v), want (false, nil)", terminated, err)
	}

	if _, err := manager.Validate("untrusted-session"); err == nil {
		t.Error("Validate(untrusted) error = nil")
	}
	if _, err := manager.Terminate("untrusted-session"); err == nil {
		t.Error("Terminate(untrusted) error = nil")
	}
}
