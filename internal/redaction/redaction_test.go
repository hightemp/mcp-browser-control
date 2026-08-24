package redaction

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestJSONRedactsSensitiveBrowserData(t *testing.T) {
	t.Parallel()

	payload := []byte(`{
  "headers": {"Authorization":"Bearer auth-secret","Cookie":"sid=cookie-secret"},
  "cookies": [{"name":"sid","value":"cookie-value"}],
  "fields": [{"name":"password","value":"password-value"}],
  "formData": {"email":"ada@example.test","token":"form-token"},
  "url": "https://user:pass@example.test/?token=query-secret&safe=yes",
  "clipboardText": "clipboard-secret",
  "filePath": "/home/ada/private/report.txt",
  "message": "Authorization: Basic basic-secret; Cookie: sid=inline-cookie",
  "safe": "visible"
}`)
	result, report, err := JSON(payload, DefaultLimits(32<<10))
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	for _, secret := range []string{
		"auth-secret", "cookie-secret", "cookie-value", "password-value", "form-token",
		"query-secret", "clipboard-secret", "/home/ada/private/report.txt", "basic-secret",
		"inline-cookie", "user:pass",
	} {
		if strings.Contains(string(result), secret) {
			t.Errorf("redacted JSON contains %q: %s", secret, result)
		}
	}
	if !strings.Contains(string(result), `"safe":"visible"`) ||
		!strings.Contains(string(result), "safe=yes") {
		t.Fatalf("safe values were not preserved: %s", result)
	}
	wantRules := []string{
		"authorization", "clipboard", "cookies", "form-data", "local-paths",
		"password-fields", "query-tokens",
	}
	if !report.Applied || report.Truncated || !reflect.DeepEqual(report.Rules, wantRules) {
		t.Fatalf("report = %#v, want rules %#v", report, wantRules)
	}
}

func TestJSONRedactsFlatDOMAttributeLists(t *testing.T) {
	t.Parallel()

	result, report, err := JSON(
		[]byte(`{"node":{"attributes":["type","password","value","swordfish","data-token","raw-token","title","visible"]}}`),
		DefaultLimits(32<<10),
	)
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	for _, secret := range []string{"swordfish", "raw-token"} {
		if strings.Contains(string(result), secret) {
			t.Errorf("redacted flat attributes contain %q: %s", secret, result)
		}
	}
	if !strings.Contains(string(result), `"title","visible"`) ||
		!report.Applied || !reflect.DeepEqual(report.Rules, []string{"password-fields"}) {
		t.Fatalf("result = %s, report = %#v", result, report)
	}
}

func TestJSONAppliesStructuralAndOutputLimits(t *testing.T) {
	t.Parallel()

	limits := Limits{
		MaxInputBytes:  1_024,
		MaxOutputBytes: 1_024,
		MaxStringBytes: 4,
		MaxDepth:       2,
		MaxNodes:       6,
	}
	result, report, err := JSON(
		[]byte(`{"long":"abcdefgh","nested":{"one":{"two":"value"}},"list":[1,2,3,4]}`),
		limits,
	)
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	if !report.Truncated || !strings.Contains(string(result), "[TRUNCATED]") {
		t.Fatalf("bounded result = %s, report = %#v", result, report)
	}
	var decoded any
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("bounded result is invalid JSON: %v", err)
	}

	tooSmall := limits
	tooSmall.MaxOutputBytes = 8
	if _, _, err := JSON([]byte(`{"safe":"value"}`), tooSmall); !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("small output limit error = %v, want ErrOutputTooLarge", err)
	}
	tooSmall = limits
	tooSmall.MaxInputBytes = 4
	if _, _, err := JSON([]byte(`{"safe":"value"}`), tooSmall); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("small input limit error = %v, want ErrInputTooLarge", err)
	}
}

func TestStringRedactionPreservesUTF8Bounds(t *testing.T) {
	t.Parallel()

	result, report := String("prefix token=secret /tmp/private/\u0444\u0430\u0439\u043b.txt", 32)
	if !report.Applied || !report.Truncated || !strings.Contains(result, "[REDACTED]") {
		t.Fatalf("String() = (%q, %#v)", result, report)
	}
	if !json.Valid([]byte(`"` + result + `"`)) {
		t.Fatalf("String() returned invalid UTF-8 %q", result)
	}
}

func TestLimitsRejectInvalidConfigurationAndJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		limits Limits
		input  []byte
	}{
		{name: "zero", limits: Limits{}, input: []byte(`null`)},
		{name: "too deep", limits: Limits{1, 1, 1, 257, 1}, input: []byte(`null`)},
		{name: "invalid JSON", limits: DefaultLimits(1_024), input: []byte(`{"x":`)},
		{name: "multiple JSON values", limits: DefaultLimits(1_024), input: []byte(`{} {}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := JSON(test.input, test.limits); err == nil {
				t.Fatal("JSON() error = nil")
			}
		})
	}
}
