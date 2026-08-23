package app

import (
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"net/http"
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
			want: Config{
				Transport:      "streamable-http",
				MCPHost:        "127.0.0.1",
				MCPPort:        "8896",
				WebSocketHost:  "127.0.0.1",
				WebSocketPort:  "8090",
				CommandTimeout: 15 * time.Second,
				CredentialFile: defaultCredentialFile(),
				PairingTTL:     10 * time.Minute,
			},
		},
		{
			name: "stdio override",
			args: []string{"-t", "stdio", "-command_timeout", "3s"},
			want: Config{
				Transport:      "stdio",
				MCPHost:        "127.0.0.1",
				MCPPort:        "8896",
				WebSocketHost:  "127.0.0.1",
				WebSocketPort:  "8090",
				CommandTimeout: 3 * time.Second,
				CredentialFile: defaultCredentialFile(),
				PairingTTL:     10 * time.Minute,
			},
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
			got, err := parseConfig(test.args, io.Discard)
			if test.wantError {
				if err == nil {
					t.Fatal("parseConfig() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseConfig() error = %v", err)
			}
			if got != test.want {
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
			config := Config{
				Transport:      transport,
				MCPHost:        "127.0.0.1",
				MCPPort:        "0",
				WebSocketHost:  "127.0.0.1",
				WebSocketPort:  "0",
				CommandTimeout: time.Second,
			}
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

	runErr := run(
		context.Background(),
		Config{
			Transport:      "stdio",
			MCPHost:        "127.0.0.1",
			MCPPort:        "0",
			WebSocketHost:  host,
			WebSocketPort:  port,
			CommandTimeout: time.Second,
		},
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
