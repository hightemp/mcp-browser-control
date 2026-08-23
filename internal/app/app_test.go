package app

import (
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
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
	defer listener.Close()
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
	handler := guardedMCPHandler(config, next)
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8896/mcp", strings.NewReader("12345"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
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
