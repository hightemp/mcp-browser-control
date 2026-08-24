package policy

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
)

func TestNormalizeOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		want      string
		wantError bool
	}{
		{name: "canonical", input: "HTTPS://Example.COM/", want: "https://example.com"},
		{name: "default port", input: "https://example.com:443", want: "https://example.com"},
		{name: "port", input: "http://localhost:8080", want: "http://localhost:8080"},
		{name: "path", input: "https://example.com/private", wantError: true},
		{name: "query", input: "https://example.com?token=secret", wantError: true},
		{name: "scheme", input: "file:///tmp/example", wantError: true},
		{name: "missing host", input: "https://", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeOrigin(test.input)
			if test.wantError {
				if err == nil {
					t.Fatalf("NormalizeOrigin(%q) error = nil", test.input)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("NormalizeOrigin(%q) = %q, %v; want %q", test.input, got, err, test.want)
			}
		})
	}
}

func TestActionCheckURL(t *testing.T) {
	t.Parallel()

	policy, err := NewAction(
		[]string{"https://allowed.example", "http://localhost:8080"},
		[]string{"https://denied.example", "http://localhost:8080"},
		false,
		nil,
	)
	if err != nil {
		t.Fatalf("NewAction() error = %v", err)
	}
	tests := []struct {
		name     string
		url      string
		wantCode protocol.ErrorCode
	}{
		{name: "allowlisted", url: "https://allowed.example/path?token=hidden"},
		{name: "deny wins", url: "http://localhost:8080/page", wantCode: protocol.CodeRestrictedURL},
		{name: "not allowlisted", url: "https://other.example", wantCode: protocol.CodeRestrictedURL},
		{name: "restricted scheme", url: "chrome://settings", wantCode: protocol.CodeRestrictedURL},
		{name: "Chrome Web Store", url: "https://chromewebstore.google.com/detail/example", wantCode: protocol.CodeRestrictedURL},
		{name: "Edge Add-ons", url: "https://microsoftedge.microsoft.com/addons/detail/example", wantCode: protocol.CodeRestrictedURL},
		{name: "credentials", url: "https://user:secret@allowed.example", wantCode: protocol.CodeRestrictedURL},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policyError := policy.CheckURL("page.info", "browser-a", test.url)
			if test.wantCode == "" {
				if policyError != nil {
					t.Fatalf("CheckURL() error = %v", policyError)
				}
				return
			}
			if policyError == nil || policyError.Code != test.wantCode {
				t.Fatalf("CheckURL() error = %#v, want code %s", policyError, test.wantCode)
			}
		})
	}
}

func TestActionIncognitoAndAuditRedaction(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	policy, err := NewAction(
		nil,
		[]string{"https://denied.example"},
		false,
		log.New(&output, "", 0),
	)
	if err != nil {
		t.Fatalf("NewAction() error = %v", err)
	}
	if policy.CheckIncognito("windows.create", "browser-a", false) != nil {
		t.Fatal("normal context was denied")
	}
	policyError := policy.CheckIncognito("windows.create", "browser-a", true)
	if policyError == nil || policyError.Code != protocol.CodeRestrictedURL {
		t.Fatalf("CheckIncognito() error = %#v", policyError)
	}
	if policy.CheckURL(
		"page.info",
		"browser-a",
		"https://denied.example/path?token=must-not-appear#secret",
	) == nil {
		t.Fatal("denylisted URL was allowed")
	}
	logText := output.String()
	if !strings.Contains(logText, "reason=incognito_disabled") ||
		!strings.Contains(logText, "origin=https://denied.example") {
		t.Fatalf("audit log = %q", logText)
	}
	for _, secret := range []string{"must-not-appear", "path", "#secret"} {
		if strings.Contains(logText, secret) {
			t.Fatalf("audit log contains %q: %s", secret, logText)
		}
	}
}
