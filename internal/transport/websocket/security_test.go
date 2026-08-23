package websocket

import (
	"bytes"
	"encoding/json"
	"errors"
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
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/security/pairing"
)

func TestServerRejectsForbiddenOriginDuringUpgrade(t *testing.T) {
	t.Parallel()

	browserRegistry := registry.New()
	transport := NewServer(
		browserRegistry,
		router.New(browserRegistry, router.WithLogger(log.New(io.Discard, "", 0))),
		WithLogger(log.New(io.Discard, "", 0)),
		WithOriginAllowlist([]string{"chrome-extension://allowed"}),
	)
	httpServer := httptest.NewServer(transport)
	t.Cleanup(httpServer.Close)

	for _, origin := range []string{
		"https://attacker.example",
		"chrome-extension://not-allowlisted",
	} {
		_, response, err := gorilla.DefaultDialer.Dial(
			"ws"+strings.TrimPrefix(httpServer.URL, "http")+DefaultPath,
			http.Header{"Origin": []string{origin}},
		)
		if err == nil {
			t.Fatalf("Dial() with origin %q error = nil", origin)
		}
		if response == nil || response.StatusCode != http.StatusForbidden {
			t.Fatalf("Dial() with origin %q response = %#v, want 403", origin, response)
		}
		_ = response.Body.Close()
	}
	if browserRegistry.Count() != 0 {
		t.Fatal("forbidden origin registered a browser")
	}
}

func TestServerClosesConnectionForOversizedBrowserMessage(t *testing.T) {
	t.Parallel()

	socket, browserRegistry, _ := pairedSecuritySocket(t, WithMaxMessageBytes(512))
	if err := socket.WriteMessage(gorilla.TextMessage, bytes.Repeat([]byte("x"), 1024)); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	if err := socket.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	_, _, err := socket.ReadMessage()
	var closeError *gorilla.CloseError
	if !errors.As(err, &closeError) || closeError.Code != gorilla.CloseMessageTooBig {
		t.Fatalf("ReadMessage() error = %v, want close code %d", err, gorilla.CloseMessageTooBig)
	}
	waitForRegistryCount(t, browserRegistry, 0)
}

func TestServerDiscardsEventFloodWithoutPendingState(t *testing.T) {
	t.Parallel()

	socket, browserRegistry, requestRouter := pairedSecuritySocket(t)
	browser := browserRegistry.List()[0]
	event := protocol.NewMessage(protocol.TypeEvent)
	event.BrowserID = browser.BrowserID
	event.Params = json.RawMessage(`{"name":"console.entry","sequence":1}`)

	for range 5_000 {
		if err := socket.WriteJSON(event); err != nil {
			t.Fatalf("WriteJSON(event) error = %v", err)
		}
	}
	ping := protocol.NewMessage(protocol.TypePing)
	ping.BrowserID = browser.BrowserID
	if err := socket.WriteJSON(ping); err != nil {
		t.Fatalf("WriteJSON(ping) error = %v", err)
	}
	if err := socket.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	var pong protocol.Message
	if err := socket.ReadJSON(&pong); err != nil {
		t.Fatalf("ReadJSON(pong) error = %v", err)
	}
	if pong.Type != protocol.TypePong || pong.BrowserID != browser.BrowserID {
		t.Fatalf("pong = %#v", pong)
	}
	if got := requestRouter.PendingCount(); got != 0 {
		t.Fatalf("pending requests after event flood = %d, want 0", got)
	}
	if got := browserRegistry.Count(); got != 1 {
		t.Fatalf("registry count after event flood = %d, want 1", got)
	}
}

func pairedSecuritySocket(
	t *testing.T,
	options ...Option,
) (*gorilla.Conn, *registry.Registry, *router.Router) {
	t.Helper()

	browserRegistry := registry.New()
	requestRouter := router.New(
		browserRegistry,
		router.WithLogger(log.New(io.Discard, "", 0)),
	)
	pairingManager, err := pairing.NewManager()
	if err != nil {
		t.Fatalf("pairing.NewManager() error = %v", err)
	}
	serverOptions := []Option{
		WithLogger(log.New(io.Discard, "", 0)),
		WithAuthenticator(pairingManager),
		WithPingInterval(time.Hour),
	}
	serverOptions = append(serverOptions, options...)
	transport := NewServer(browserRegistry, requestRouter, serverOptions...)
	httpServer := httptest.NewServer(transport)
	t.Cleanup(httpServer.Close)

	socket, response, err := gorilla.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(httpServer.URL, "http")+DefaultPath,
		http.Header{"Origin": []string{"chrome-extension://security-test"}},
	)
	if err != nil {
		if response != nil {
			t.Fatalf("Dial() error = %v, status = %s", err, response.Status)
		}
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = socket.Close() })

	pairingCode, _, err := pairingManager.CurrentCode()
	if err != nil {
		t.Fatalf("CurrentCode() error = %v", err)
	}
	browserID := uuid.NewString()
	hello := protocol.NewMessage(protocol.TypeHello)
	hello.BrowserID = browserID
	hello.Params, err = json.Marshal(protocol.HelloParams{
		ExtensionVersion: "0.1.0-test",
		PairingCode:      pairingCode,
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
	if welcome.Type != protocol.TypeWelcome || welcome.BrowserID != browserID {
		t.Fatalf("welcome = %#v", welcome)
	}
	waitForRegistryCount(t, browserRegistry, 1)
	return socket, browserRegistry, requestRouter
}
