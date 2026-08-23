package websocket

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	gorilla "github.com/gorilla/websocket"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/router"
)

func TestServerRegistersAndRoutesBrowser(t *testing.T) {
	t.Parallel()

	browserRegistry := registry.New()
	requestRouter := router.New(
		browserRegistry,
		router.WithDefaultTimeout(time.Second),
		router.WithIDGenerator(func() string { return "request-1" }),
		router.WithLogger(log.New(io.Discard, "", 0)),
	)
	transport := NewServer(
		browserRegistry,
		requestRouter,
		WithLogger(log.New(io.Discard, "", 0)),
	)

	mux := http.NewServeMux()
	mux.Handle(DefaultPath, transport)
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	headers := http.Header{"Origin": []string{"chrome-extension://test-extension"}}
	socket, response, err := gorilla.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(httpServer.URL, "http")+DefaultPath,
		headers,
	)
	if err != nil {
		if response != nil {
			t.Fatalf("Dial() error = %v, status = %s", err, response.Status)
		}
		t.Fatalf("Dial() error = %v", err)
	}
	defer socket.Close()

	browserID := uuid.NewString()
	hello := protocol.NewMessage(protocol.TypeHello)
	hello.BrowserID = browserID
	hello.Params, err = json.Marshal(protocol.HelloParams{
		DisplayName:      "Work Chrome",
		ExtensionVersion: "0.1.0",
		Browser: protocol.BrowserMetadata{
			Name:    "Chrome",
			Version: "116",
		},
		Capabilities: []string{"tabs.list"},
	})
	if err != nil {
		t.Fatalf("marshal hello: %v", err)
	}
	if err := socket.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	var welcome protocol.Message
	if err := socket.ReadJSON(&welcome); err != nil {
		t.Fatalf("read welcome: %v", err)
	}
	if welcome.Type != protocol.TypeWelcome {
		t.Fatalf("welcome type = %q, want %q", welcome.Type, protocol.TypeWelcome)
	}
	if welcome.BrowserID != browserID || welcome.ConnectionID == "" {
		t.Fatalf("welcome = %#v", welcome)
	}
	if got := browserRegistry.Count(); got != 1 {
		t.Fatalf("registry count = %d, want 1", got)
	}

	resultChannel := make(chan json.RawMessage, 1)
	errorChannel := make(chan error, 1)
	go func() {
		result, err := requestRouter.Send(context.Background(), browserID, "tabs.list", nil, nil)
		if err != nil {
			errorChannel <- err
			return
		}
		resultChannel <- result
	}()

	var requestMessage protocol.Message
	if err := socket.ReadJSON(&requestMessage); err != nil {
		t.Fatalf("read routed request: %v", err)
	}
	if requestMessage.BrowserID != browserID || requestMessage.Command != "tabs.list" {
		t.Fatalf("routed request = %#v", requestMessage)
	}

	responseMessage, err := protocol.NewResponse(
		requestMessage.RequestID,
		browserID,
		map[string]any{"tabs": []any{}},
		nil,
	)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	if err := socket.WriteJSON(responseMessage); err != nil {
		t.Fatalf("write response: %v", err)
	}

	select {
	case err := <-errorChannel:
		t.Fatalf("router Send() error = %v", err)
	case result := <-resultChannel:
		var payload struct {
			Tabs []any `json:"tabs"`
		}
		if err := json.Unmarshal(result, &payload); err != nil {
			t.Fatalf("unmarshal routed result: %v", err)
		}
		if payload.Tabs == nil {
			t.Error("tabs result is nil, want empty array")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for routed result")
	}
}

func TestServerRejectsNonLoopbackHost(t *testing.T) {
	t.Parallel()

	transport := NewServer(
		registry.New(),
		router.New(registry.New(), router.WithLogger(log.New(io.Discard, "", 0))),
		WithLogger(log.New(io.Discard, "", 0)),
	)
	request := httptest.NewRequest(http.MethodGet, "http://example.com/ws", nil)
	request.Host = "example.com"
	response := httptest.NewRecorder()

	transport.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestServerOptionsAndRequestGuards(t *testing.T) {
	t.Parallel()

	browserRegistry := registry.New()
	transport := NewServer(
		browserRegistry,
		router.New(browserRegistry, router.WithLogger(log.New(io.Discard, "", 0))),
		WithLogger(nil),
		WithHandshakeTimeout(250*time.Millisecond),
		WithWriteTimeout(300*time.Millisecond),
	)
	if transport.handshakeTimeout != 250*time.Millisecond {
		t.Errorf("handshakeTimeout = %v", transport.handshakeTimeout)
	}
	if transport.writeTimeout != 300*time.Millisecond {
		t.Errorf("writeTimeout = %v", transport.writeTimeout)
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/not-ws", nil)
	response := httptest.NewRecorder()
	transport.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", response.Code, http.StatusNotFound)
	}

	origins := []struct {
		origin string
		want   bool
	}{
		{origin: "", want: true},
		{origin: "chrome-extension://extension-id", want: true},
		{origin: "moz-extension://extension-id", want: true},
		{origin: "http://127.0.0.1:3000", want: true},
		{origin: "https://localhost", want: true},
		{origin: "https://example.com", want: false},
		{origin: "://invalid", want: false},
		{origin: "file:///tmp/page.html", want: false},
	}
	for _, test := range origins {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/ws", nil)
		request.Header.Set("Origin", test.origin)
		if got := allowedOrigin(request); got != test.want {
			t.Errorf("allowedOrigin(%q) = %v, want %v", test.origin, got, test.want)
		}
	}
}
