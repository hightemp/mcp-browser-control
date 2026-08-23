// Package websocket implements the versioned browser-extension WebSocket
// transport.
package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	gorilla "github.com/gorilla/websocket"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/router"
)

const (
	// DefaultPath is the browser extension WebSocket endpoint.
	DefaultPath = "/ws"
)

// Option configures a Server.
type Option func(*Server)

// WithLogger sets the transport logger.
func WithLogger(logger *log.Logger) Option {
	return func(server *Server) {
		if logger != nil {
			server.logger = logger
		}
	}
}

// WithHandshakeTimeout changes the maximum time allowed for the hello message.
func WithHandshakeTimeout(timeout time.Duration) Option {
	return func(server *Server) {
		if timeout > 0 {
			server.handshakeTimeout = timeout
		}
	}
}

// WithWriteTimeout changes the deadline for a WebSocket write.
func WithWriteTimeout(timeout time.Duration) Option {
	return func(server *Server) {
		if timeout > 0 {
			server.writeTimeout = timeout
		}
	}
}

// Server accepts browser extension connections and connects them to the
// browser registry and request router.
type Server struct {
	registry         *registry.Registry
	router           *router.Router
	logger           *log.Logger
	handshakeTimeout time.Duration
	writeTimeout     time.Duration
	maxMessageBytes  int64
	upgrader         gorilla.Upgrader
}

// NewServer creates a browser WebSocket transport.
func NewServer(
	browserRegistry *registry.Registry,
	requestRouter *router.Router,
	options ...Option,
) *Server {
	server := &Server{
		registry:         browserRegistry,
		router:           requestRouter,
		logger:           log.New(log.Writer(), "[WebSocket] ", log.LstdFlags),
		handshakeTimeout: 5 * time.Second,
		writeTimeout:     5 * time.Second,
		maxMessageBytes:  4 << 20,
		upgrader: gorilla.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     allowedOrigin,
		},
	}
	for _, option := range options {
		option(server)
	}
	return server
}

// ServeHTTP upgrades an authenticated browser connection. Pairing credentials
// will be enforced by the pairing layer; this transport already restricts
// hosts and origins to local or extension contexts.
func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != DefaultPath {
		http.NotFound(writer, request)
		return
	}
	if !loopbackHost(request.Host) {
		http.Error(writer, "forbidden host", http.StatusForbidden)
		return
	}

	socket, err := s.upgrader.Upgrade(writer, request, nil)
	if err != nil {
		s.logger.Printf("failed to upgrade WebSocket connection: %v", err)
		return
	}

	connection := newConnection(socket, s.writeTimeout)
	connection.start()
	defer func() {
		if err := connection.Close(); err != nil {
			s.logger.Printf("failed to close WebSocket connection %s: %v", connection.ID(), err)
		}
	}()

	socket.SetReadLimit(s.maxMessageBytes)
	if err := socket.SetReadDeadline(time.Now().Add(s.handshakeTimeout)); err != nil {
		s.logger.Printf("failed to set handshake deadline: %v", err)
		return
	}

	var helloMessage protocol.Message
	if err := socket.ReadJSON(&helloMessage); err != nil {
		s.logger.Printf("failed to read browser hello: %v", err)
		return
	}
	if err := helloMessage.Validate(); err != nil || helloMessage.Type != protocol.TypeHello {
		s.logger.Printf("invalid browser hello: %v", err)
		return
	}
	if _, err := uuid.Parse(helloMessage.BrowserID); err != nil {
		s.logger.Printf("invalid browserId %q: %v", helloMessage.BrowserID, err)
		return
	}

	var hello protocol.HelloParams
	if err := helloMessage.DecodeParams(&hello); err != nil {
		s.logger.Printf("invalid browser hello params: %v", err)
		return
	}
	if hello.ExtensionVersion == "" {
		s.logger.Printf("invalid browser hello: extensionVersion is required")
		return
	}

	if err := socket.SetReadDeadline(time.Time{}); err != nil {
		s.logger.Printf("failed to clear handshake deadline: %v", err)
		return
	}

	browserID := helloMessage.BrowserID
	replaced, err := s.registry.Register(
		registry.Registration{
			BrowserID:        browserID,
			DisplayName:      hello.DisplayName,
			ExtensionVersion: hello.ExtensionVersion,
			Browser:          hello.Browser,
			Capabilities:     hello.Capabilities,
			Permissions:      hello.Permissions,
			Incognito:        hello.Incognito,
			RemoteAddress:    request.RemoteAddr,
		},
		connection,
	)
	if err != nil {
		s.logger.Printf("failed to register browser %s: %v", browserID, err)
		return
	}
	if replaced != nil {
		if err := replaced.Close(); err != nil {
			s.logger.Printf("failed to close replaced browser connection: %v", err)
		}
	}

	defer func() {
		s.registry.Unregister(browserID, connection.ID())
		s.router.FailConnection(browserID, connection.ID())
	}()

	welcome := protocol.NewMessage(protocol.TypeWelcome)
	welcome.BrowserID = browserID
	welcome.ConnectionID = connection.ID()
	welcomeResult := protocol.WelcomeResult{
		BrowserID:    browserID,
		ConnectionID: connection.ID(),
		ServerTime:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	welcome.Result, err = json.Marshal(welcomeResult)
	if err != nil {
		s.logger.Printf("failed to marshal welcome for browser %s: %v", browserID, err)
		return
	}
	if err := connection.Send(request.Context(), welcome); err != nil {
		s.logger.Printf("failed to welcome browser %s: %v", browserID, err)
		return
	}

	s.logger.Printf("browser connected: browserId=%s connectionId=%s", browserID, connection.ID())
	for {
		var message protocol.Message
		if err := socket.ReadJSON(&message); err != nil {
			if !gorilla.IsCloseError(err, gorilla.CloseNormalClosure, gorilla.CloseGoingAway) &&
				!errors.Is(err, net.ErrClosed) {
				s.logger.Printf(
					"failed to read browser message: browserId=%s connectionId=%s error=%v",
					browserID,
					connection.ID(),
					err,
				)
			}
			return
		}
		if err := message.Validate(); err != nil {
			s.logger.Printf("ignored invalid browser message: browserId=%s error=%v", browserID, err)
			continue
		}
		if message.BrowserID != browserID {
			s.logger.Printf(
				"ignored message with mismatched browserId: connectionBrowserId=%s messageBrowserId=%s",
				browserID,
				message.BrowserID,
			)
			continue
		}
		s.registry.Touch(browserID, connection.ID())

		switch message.Type {
		case protocol.TypeResponse:
			if !s.router.HandleResponse(browserID, connection.ID(), message) {
				s.logger.Printf(
					"ignored unmatched response: browserId=%s requestId=%s",
					browserID,
					message.RequestID,
				)
			}
		case protocol.TypePing:
			pong := protocol.NewMessage(protocol.TypePong)
			pong.BrowserID = browserID
			pong.ConnectionID = connection.ID()
			if err := connection.Send(request.Context(), pong); err != nil {
				s.logger.Printf("failed to send pong to browser %s: %v", browserID, err)
				return
			}
		case protocol.TypeCapabilitiesChanged:
			var changed protocol.CapabilitiesChangedParams
			if err := message.DecodeParams(&changed); err != nil {
				s.logger.Printf("ignored invalid capability update from browser %s: %v", browserID, err)
				continue
			}
			s.registry.UpdateCapabilities(
				browserID,
				connection.ID(),
				changed.Capabilities,
				changed.Permissions,
			)
		case protocol.TypePong, protocol.TypeEvent:
			// Touch above is sufficient for the first protocol increment.
		default:
			s.logger.Printf("ignored unsupported browser message type %q", message.Type)
		}
	}
}

type outboundMessage struct {
	message protocol.Message
	result  chan error
}

type connection struct {
	id           string
	socket       *gorilla.Conn
	writeTimeout time.Duration
	outbound     chan outboundMessage
	done         chan struct{}
	closeOnce    sync.Once
}

func newConnection(socket *gorilla.Conn, writeTimeout time.Duration) *connection {
	return &connection{
		id:           uuid.NewString(),
		socket:       socket,
		writeTimeout: writeTimeout,
		outbound:     make(chan outboundMessage, 64),
		done:         make(chan struct{}),
	}
}

func (c *connection) ID() string {
	return c.id
}

func (c *connection) start() {
	go c.writePump()
}

func (c *connection) Send(ctx context.Context, message protocol.Message) error {
	result := make(chan error, 1)
	outbound := outboundMessage{message: message, result: result}

	select {
	case c.outbound <- outbound:
	case <-c.done:
		return protocol.NewError(protocol.CodeBrowserDisconnected, "browser connection is closed", true)
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-result:
		return err
	case <-c.done:
		return protocol.NewError(protocol.CodeBrowserDisconnected, "browser connection is closed", true)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *connection) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		close(c.done)
		closeErr = c.socket.Close()
	})
	return closeErr
}

func (c *connection) writePump() {
	for {
		select {
		case outbound := <-c.outbound:
			if err := c.socket.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err != nil {
				writeErr := fmt.Errorf("set WebSocket write deadline: %w", err)
				if closeErr := c.Close(); closeErr != nil {
					writeErr = errors.Join(writeErr, fmt.Errorf("close WebSocket: %w", closeErr))
				}
				outbound.result <- writeErr
				return
			}
			if err := c.socket.WriteJSON(outbound.message); err != nil {
				writeErr := fmt.Errorf("write WebSocket message: %w", err)
				if closeErr := c.Close(); closeErr != nil {
					writeErr = errors.Join(writeErr, fmt.Errorf("close WebSocket: %w", closeErr))
				}
				outbound.result <- writeErr
				return
			}
			outbound.result <- nil
		case <-c.done:
			return
		}
	}
}

func allowedOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch parsed.Scheme {
	case "chrome-extension", "moz-extension":
		return parsed.Host != ""
	case "http", "https":
		return loopbackHost(parsed.Host)
	default:
		return false
	}
}

func loopbackHost(hostPort string) bool {
	host := hostPort
	if parsedHost, _, err := net.SplitHostPort(hostPort); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
