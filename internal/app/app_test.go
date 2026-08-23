package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/mcpsession"
	"github.com/mark3labs/mcp-go/server"
)

func TestParseConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		args      []string
		want      Config
		wantError bool
	}{
		{
			name: "defaults",
			want: defaultConfig(),
		},
		{
			name: "stdio override",
			args: []string{"-t", "stdio", "-command_timeout", "3s"},
			want: func() Config {
				config := defaultConfig()
				config.Transport = "stdio"
				config.CommandTimeout = 3 * time.Second
				return config
			}(),
		},
		{
			name:      "bad transport",
			args:      []string{"-t", "gopher"},
			wantError: true,
		},
		{
			name:      "bad timeout",
			args:      []string{"-command_timeout", "0s"},
			wantError: true,
		},
		{
			name:      "bad pairing ttl",
			args:      []string{"-pairing_ttl", "0s"},
			wantError: true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseConfigWithEnvironment(test.args, io.Discard, emptyEnvironment)
			if test.wantError {
				if err == nil {
					t.Fatal("parseConfig() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseConfig() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("parseConfig() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestRunStopsCleanlyForEveryTransport(t *testing.T) {
	tests := []string{"streamable-http", "http", "stdio", "sse"}
	for _, transport := range tests {
		t.Run(transport, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			config := defaultConfig()
			config.Transport = transport
			config.MCPPort = "0"
			config.WebSocketPort = "0"
			config.CommandTimeout = time.Second
			config.CredentialFile = ""
			config.MCPTokenFile = filepath.Join(t.TempDir(), "mcp-token")
			config.ArtifactDirectory = filepath.Join(t.TempDir(), "artifacts")
			config.LegacySSEEnabled = transport == "sse"
			if err := run(
				ctx,
				config,
				bytes.NewReader(nil),
				io.Discard,
				log.New(io.Discard, "", 0),
			); err != nil {
				t.Fatalf("run() error = %v", err)
			}
		})
	}
}

func TestRunRejectsOccupiedWebSocketAddress(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}

	config := defaultConfig()
	config.Transport = "stdio"
	config.MCPHost = "127.0.0.1"
	config.MCPPort = "0"
	config.WebSocketHost = host
	config.WebSocketPort = port
	config.CommandTimeout = time.Second
	config.CredentialFile = ""
	config.ArtifactDirectory = filepath.Join(t.TempDir(), "artifacts")
	runErr := run(
		context.Background(),
		config,
		bytes.NewReader(nil),
		io.Discard,
		log.New(io.Discard, "", 0),
	)
	if runErr == nil || !strings.Contains(runErr.Error(), "listen for browser WebSocket") {
		t.Fatalf("run() error = %v, want WebSocket listen error", runErr)
	}
}

func TestHTTPServerHelpers(t *testing.T) {
	t.Parallel()

	config := Config{MCPHost: "127.0.0.1", MCPPort: "12345"}
	server := newHTTPServer(config, http.NotFoundHandler())
	if server.Addr != "127.0.0.1:12345" {
		t.Errorf("Addr = %q, want 127.0.0.1:12345", server.Addr)
	}
	if err := normalizeServerError(http.ErrServerClosed); err != nil {
		t.Fatalf("normalizeServerError(http.ErrServerClosed) = %v", err)
	}
	sentinel := context.DeadlineExceeded
	if err := normalizeServerError(sentinel); err != sentinel {
		t.Fatalf("normalizeServerError() = %v, want %v", err, sentinel)
	}
}

func TestGuardedMCPHandlerLimitsRequests(t *testing.T) {
	t.Parallel()

	config := defaultConfig()
	config.MCPMaxRequestBytes = 4
	next := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := io.ReadAll(request.Body); err != nil {
			http.Error(writer, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := guardedMCPHandler(config, "test-token", next)
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8896/mcp", strings.NewReader("12345"))
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestGuardedStreamableHTTPSessions(t *testing.T) {
	t.Parallel()

	sessionManager, err := mcpsession.NewManager()
	if err != nil {
		t.Fatalf("mcpsession.NewManager() error = %v", err)
	}
	mcpServer := server.NewMCPServer("test", "1.0.0")
	streamable := server.NewStreamableHTTPServer(
		mcpServer,
		server.WithSessionIdManager(sessionManager),
	)
	config := defaultConfig()
	mux := http.NewServeMux()
	mux.Handle("/mcp", guardedMCPHandler(config, "test-bearer-token", streamable))
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	initializePayload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"clientInfo": map[string]string{
				"name":    "integration-test",
				"version": "1.0.0",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal initialize request: %v", err)
	}

	unauthorizedRequest, err := http.NewRequest(
		http.MethodPost,
		httpServer.URL+"/mcp",
		bytes.NewReader(initializePayload),
	)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	unauthorizedRequest.Header.Set("Content-Type", "application/json")
	unauthorizedResponse, err := httpServer.Client().Do(unauthorizedRequest)
	if err != nil {
		t.Fatalf("unauthorized request error = %v", err)
	}
	_ = unauthorizedResponse.Body.Close()
	if unauthorizedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorizedResponse.StatusCode, http.StatusUnauthorized)
	}

	sessionIDs := make([]string, 0, 2)
	for range 2 {
		request, requestErr := http.NewRequest(
			http.MethodPost,
			httpServer.URL+"/mcp",
			bytes.NewReader(initializePayload),
		)
		if requestErr != nil {
			t.Fatalf("NewRequest() error = %v", requestErr)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer test-bearer-token")
		response, requestErr := httpServer.Client().Do(request)
		if requestErr != nil {
			t.Fatalf("initialize request error = %v", requestErr)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("initialize status = %d, want %d", response.StatusCode, http.StatusOK)
		}
		sessionID := response.Header.Get("Mcp-Session-Id")
		if sessionID == "" {
			t.Fatal("initialize response omitted Mcp-Session-Id")
		}
		sessionIDs = append(sessionIDs, sessionID)
	}
	if sessionIDs[0] == sessionIDs[1] {
		t.Fatalf("two clients received the same session ID %q", sessionIDs[0])
	}

	deleteRequest, err := http.NewRequest(http.MethodDelete, httpServer.URL+"/mcp", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	deleteRequest.Header.Set("Authorization", "Bearer test-bearer-token")
	deleteRequest.Header.Set("Mcp-Session-Id", sessionIDs[0])
	deleteResponse, err := httpServer.Client().Do(deleteRequest)
	if err != nil {
		t.Fatalf("delete session request error = %v", err)
	}
	_ = deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusOK {
		t.Fatalf("delete session status = %d, want %d", deleteResponse.StatusCode, http.StatusOK)
	}
	terminated, err := sessionManager.Validate(sessionIDs[0])
	if err != nil || !terminated {
		t.Fatalf("terminated session validation = (%v, %v), want (true, nil)", terminated, err)
	}
	terminated, err = sessionManager.Validate(sessionIDs[1])
	if err != nil || terminated {
		t.Fatalf("active session validation = (%v, %v), want (false, nil)", terminated, err)
	}
}

func TestRunReportsFlagErrors(t *testing.T) {
	t.Parallel()

	err := Run(
		context.Background(),
		[]string{"-t", "invalid"},
		bytes.NewReader(nil),
		io.Discard,
		io.Discard,
	)
	if err == nil {
		t.Fatal("Run() error = nil")
	}
}

func emptyEnvironment(string) (string, bool) {
	return "", false
}
