package mcpsession

import "testing"

func TestManagerLifecycle(t *testing.T) {
	t.Parallel()

	manager, err := NewManager()
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

	if _, err := manager.Validate("untrusted-session"); err == nil {
		t.Error("Validate(untrusted) error = nil")
	}
}
