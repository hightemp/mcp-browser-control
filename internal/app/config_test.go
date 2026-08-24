package app

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestConfigPrecedence(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "server.json")
	payload := []byte(`{
  "commandTimeout": "20s",
  "mcpPort": "9000",
  "mcpRequestsPerSecond": 50,
  "originAllowlist": ["http://localhost:3000"],
  "toolProfile": "minimal"
}`)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	environment := map[string]string{
		environmentPrefix + "COMMAND_TIMEOUT":        "10s",
		environmentPrefix + "LOG_LEVEL":              "debug",
		environmentPrefix + "WS_MESSAGES_PER_SECOND": "750",
	}
	lookup := func(name string) (string, bool) {
		value, ok := environment[name]
		return value, ok
	}

	config, err := parseConfigWithEnvironment(
		[]string{
			"-config", path,
			"-command_timeout", "3s",
			"-mcp_request_burst", "125",
			"-tool_profile", "full",
		},
		io.Discard,
		lookup,
	)
	if err != nil {
		t.Fatalf("parseConfigWithEnvironment() error = %v", err)
	}
	if config.ConfigFile != path || config.CommandTimeout != 3*time.Second ||
		config.MCPPort != "9000" || config.ToolProfile != "full" || config.LogLevel != "debug" ||
		config.MCPRequestsPerSecond != 50 || config.MCPRequestBurst != 125 ||
		config.WebSocketMessagesPerSecond != 750 {
		t.Fatalf("config precedence result = %#v", config)
	}
	if !reflect.DeepEqual(config.OriginAllowlist, []string{"http://localhost:3000"}) {
		t.Errorf("OriginAllowlist = %#v", config.OriginAllowlist)
	}
}

func TestConfigFileFromEnvironment(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "server.json")
	if err := os.WriteFile(path, []byte(`{"redactLogs":false,"artifactTTL":"2h","artifactMaxBytes":1048576,"mcpMaxResultBytes":131072}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	lookup := func(name string) (string, bool) {
		if name == environmentPrefix+"CONFIG" {
			return path, true
		}
		return "", false
	}
	config, err := parseConfigWithEnvironment(nil, io.Discard, lookup)
	if err != nil {
		t.Fatalf("parseConfigWithEnvironment() error = %v", err)
	}
	if config.RedactLogs || config.ArtifactTTL != 2*time.Hour || config.ArtifactMaxBytes != 1048576 ||
		config.MCPMaxResultBytes != 131072 {
		t.Fatalf("config = %#v", config)
	}
}

func TestConfigRejectsInvalidSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fileContent string
		environment map[string]string
		wantError   string
	}{
		{name: "unknown file field", fileContent: `{"unknown":true}`, wantError: "unknown field"},
		{name: "invalid file duration", fileContent: `{"pairingTTL":"later"}`, wantError: "parse pairingTTL"},
		{name: "unsafe MCP host", environment: map[string]string{environmentPrefix + "MCP_HOST": "0.0.0.0"}, wantError: "loopback"},
		{name: "invalid environment integer", environment: map[string]string{environmentPrefix + "PAIRING_MAX_ATTEMPTS": "many"}, wantError: "PAIRING_MAX_ATTEMPTS"},
		{name: "invalid MCP rate", environment: map[string]string{environmentPrefix + "MCP_REQUESTS_PER_SECOND": "0"}, wantError: "mcp_requests_per_second"},
		{name: "invalid browser burst", environment: map[string]string{environmentPrefix + "WS_MESSAGE_BURST": "1000001"}, wantError: "ws_message_burst"},
		{name: "invalid artifact quota", environment: map[string]string{environmentPrefix + "ARTIFACT_MAX_BYTES": "0"}, wantError: "artifact_max_bytes"},
		{name: "invalid result limit", environment: map[string]string{environmentPrefix + "MCP_MAX_RESULT_BYTES": "0"}, wantError: "payload limits"},
		{name: "unsafe origin", environment: map[string]string{environmentPrefix + "ORIGIN_ALLOWLIST": "https://example.com"}, wantError: "allowed origin"},
		{name: "page origin with path", environment: map[string]string{environmentPrefix + "PAGE_ORIGIN_ALLOWLIST": "https://example.com/path"}, wantError: "page_origin_allowlist"},
		{name: "restricted page origin scheme", environment: map[string]string{environmentPrefix + "PAGE_ORIGIN_DENYLIST": "chrome://settings"}, wantError: "page_origin_denylist"},
		{name: "invalid incognito flag", environment: map[string]string{environmentPrefix + "ALLOW_INCOGNITO": "sometimes"}, wantError: "ALLOW_INCOGNITO"},
		{name: "ping not below read timeout", environment: map[string]string{environmentPrefix + "WS_READ_TIMEOUT": "10s", environmentPrefix + "WS_PING_INTERVAL": "10s"}, wantError: "shorter"},
		{name: "HTTP without token file", environment: map[string]string{environmentPrefix + "MCP_TOKEN_FILE": ""}, wantError: "mcp_token_file"},
		{name: "legacy SSE without opt in", environment: map[string]string{environmentPrefix + "TRANSPORT": "sse"}, wantError: "enable_legacy_sse"},
		{name: "invalid legacy SSE flag", environment: map[string]string{environmentPrefix + "ENABLE_LEGACY_SSE": "sometimes"}, wantError: "ENABLE_LEGACY_SSE"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			args := []string(nil)
			if test.fileContent != "" {
				path := filepath.Join(t.TempDir(), "server.json")
				if err := os.WriteFile(path, []byte(test.fileContent), 0o600); err != nil {
					t.Fatalf("WriteFile() error = %v", err)
				}
				args = []string{"-config", path}
			}
			lookup := func(name string) (string, bool) {
				value, ok := test.environment[name]
				return value, ok
			}
			_, err := parseConfigWithEnvironment(args, io.Discard, lookup)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestConfigActionPolicySources(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "server.json")
	payload := []byte(`{
  "pageOriginAllowlist": ["https://file.example"],
  "pageOriginDenylist": ["https://denied.example"],
  "allowIncognito": true
}`)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	environment := map[string]string{
		environmentPrefix + "PAGE_ORIGIN_DENYLIST": "https://environment.example",
		environmentPrefix + "ALLOW_INCOGNITO":      "false",
	}
	config, err := parseConfigWithEnvironment(
		[]string{
			"-config", path,
			"-page_origin_allowlist", "https://flag.example,https://flag.example",
			"-allow_incognito=true",
		},
		io.Discard,
		func(name string) (string, bool) {
			value, ok := environment[name]
			return value, ok
		},
	)
	if err != nil {
		t.Fatalf("parseConfigWithEnvironment() error = %v", err)
	}
	if !reflect.DeepEqual(config.PageOriginAllowlist, []string{"https://flag.example"}) ||
		!reflect.DeepEqual(config.PageOriginDenylist, []string{"https://environment.example"}) ||
		!config.AllowIncognito {
		t.Fatalf("action policy config = %#v", config)
	}
}

func TestConfigEnablesLegacySSEExplicitly(t *testing.T) {
	t.Parallel()

	config, err := parseConfigWithEnvironment(
		[]string{"-t", "sse", "-enable_legacy_sse", "-mcp_token_file", "/tmp/test-token"},
		io.Discard,
		emptyEnvironment,
	)
	if err != nil {
		t.Fatalf("parseConfigWithEnvironment() error = %v", err)
	}
	if !config.LegacySSEEnabled {
		t.Error("LegacySSEEnabled = false")
	}
}

func TestConfigOriginListFlag(t *testing.T) {
	t.Parallel()

	config, err := parseConfigWithEnvironment(
		[]string{"-origin_allowlist", "chrome-extension://abc, http://127.0.0.1:3000,chrome-extension://abc"},
		io.Discard,
		emptyEnvironment,
	)
	if err != nil {
		t.Fatalf("parseConfigWithEnvironment() error = %v", err)
	}
	want := []string{"chrome-extension://abc", "http://127.0.0.1:3000"}
	if !reflect.DeepEqual(config.OriginAllowlist, want) {
		t.Fatalf("OriginAllowlist = %#v, want %#v", config.OriginAllowlist, want)
	}
}
