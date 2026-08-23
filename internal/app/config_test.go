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
  "originAllowlist": ["http://localhost:3000"],
  "toolProfile": "minimal"
}`)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	environment := map[string]string{
		environmentPrefix + "COMMAND_TIMEOUT": "10s",
		environmentPrefix + "LOG_LEVEL":       "debug",
	}
	lookup := func(name string) (string, bool) {
		value, ok := environment[name]
		return value, ok
	}

	config, err := parseConfigWithEnvironment(
		[]string{"-config", path, "-command_timeout", "3s", "-tool_profile", "full"},
		io.Discard,
		lookup,
	)
	if err != nil {
		t.Fatalf("parseConfigWithEnvironment() error = %v", err)
	}
	if config.ConfigFile != path || config.CommandTimeout != 3*time.Second ||
		config.MCPPort != "9000" || config.ToolProfile != "full" || config.LogLevel != "debug" {
		t.Fatalf("config precedence result = %#v", config)
	}
	if !reflect.DeepEqual(config.OriginAllowlist, []string{"http://localhost:3000"}) {
		t.Errorf("OriginAllowlist = %#v", config.OriginAllowlist)
	}
}

func TestConfigFileFromEnvironment(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "server.json")
	if err := os.WriteFile(path, []byte(`{"redactLogs":false,"artifactTTL":"2h"}`), 0o600); err != nil {
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
	if config.RedactLogs || config.ArtifactTTL != 2*time.Hour {
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
		{name: "unsafe origin", environment: map[string]string{environmentPrefix + "ORIGIN_ALLOWLIST": "https://example.com"}, wantError: "allowed origin"},
		{name: "ping not below read timeout", environment: map[string]string{environmentPrefix + "WS_READ_TIMEOUT": "10s", environmentPrefix + "WS_PING_INTERVAL": "10s"}, wantError: "shorter"},
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
