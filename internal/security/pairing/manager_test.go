package pairing

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
)

func TestPairAuthenticateReplayRevokeAndPersistence(t *testing.T) {
	t.Parallel()

	storePath := filepath.Join(t.TempDir(), "auth", "credentials.json")
	manager, err := NewManager(WithStorePath(storePath))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	code, _, err := manager.CurrentCode()
	if err != nil {
		t.Fatalf("CurrentCode() error = %v", err)
	}
	credential, err := manager.Pair("browser-a", code)
	if err != nil {
		t.Fatalf("Pair() error = %v", err)
	}
	if credential == "" {
		t.Fatal("Pair() credential is empty")
	}
	if err := manager.Authenticate("browser-a", credential); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if _, err := manager.Pair("browser-b", code); err == nil {
		t.Fatal("Pair() replay error = nil")
	}

	reloaded, err := NewManager(WithStorePath(storePath))
	if err != nil {
		t.Fatalf("reload NewManager() error = %v", err)
	}
	if err := reloaded.Authenticate("browser-a", credential); err != nil {
		t.Fatalf("reloaded Authenticate() error = %v", err)
	}
	revoked, err := reloaded.Revoke("browser-a")
	if err != nil || !revoked {
		t.Fatalf("Revoke() = (%v, %v), want (true, nil)", revoked, err)
	}
	if err := reloaded.Authenticate("browser-a", credential); err == nil {
		t.Fatal("Authenticate() after revoke error = nil")
	}
	if revoked, err := reloaded.Revoke("browser-a"); err != nil || revoked {
		t.Fatalf("second Revoke() = (%v, %v), want (false, nil)", revoked, err)
	}

	info, err := os.Stat(storePath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != credentialFileMode {
		t.Errorf("credential file mode = %o, want %o", got, credentialFileMode)
	}
}

func TestPairingExpirationAndRateLimit(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	manager, err := NewManager(
		WithClock(func() time.Time { return now }),
		WithCodeTTL(time.Minute),
		WithAttemptLimit(2, 30*time.Second),
	)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	expiredCode, _, err := manager.CurrentCode()
	if err != nil {
		t.Fatalf("CurrentCode() error = %v", err)
	}
	now = now.Add(time.Minute + time.Second)
	if _, err := manager.Pair("browser-a", expiredCode); err == nil {
		t.Fatal("Pair(expired code) error = nil")
	}
	currentCode, _, err := manager.CurrentCode()
	if err != nil {
		t.Fatalf("CurrentCode() after expiration error = %v", err)
	}

	if _, err := manager.Pair("browser-a", "not-a-code"); err == nil {
		t.Fatal("first invalid Pair() error = nil")
	}
	_, err = manager.Pair("browser-a", "not-a-code")
	assertPairingError(t, err, true)
	_, err = manager.Pair("browser-a", currentCode)
	assertPairingError(t, err, true)

	now = now.Add(31 * time.Second)
	credential, err := manager.Pair("browser-a", currentCode)
	if err != nil || credential == "" {
		t.Fatalf("Pair() after rate limit returned empty credential = %v, error = %v", credential == "", err)
	}
}

func TestAuthorizeRequiresValidAuthenticationMaterial(t *testing.T) {
	t.Parallel()

	manager, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	for _, test := range []struct {
		name       string
		browserID  string
		credential string
		code       string
		wantCode   protocol.ErrorCode
	}{
		{name: "missing browser", wantCode: protocol.CodeInvalidMessage},
		{name: "missing material", browserID: "browser-a", wantCode: protocol.CodePairingRequired},
		{name: "bad credential", browserID: "browser-a", credential: "invalid", wantCode: protocol.CodePairingRequired},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := manager.Authorize(test.browserID, test.credential, test.code)
			if got := protocol.ErrorFrom(err).Code; got != test.wantCode {
				t.Errorf("Authorize() error code = %q, want %q", got, test.wantCode)
			}
		})
	}
}

func assertPairingError(t *testing.T, err error, retryable bool) {
	t.Helper()
	var protocolErr *protocol.Error
	if !errors.As(err, &protocolErr) {
		t.Fatalf("error type = %T, want *protocol.Error", err)
	}
	if protocolErr.Code != protocol.CodePairingRequired || protocolErr.Retryable != retryable {
		t.Errorf("error = %#v", protocolErr)
	}
}
