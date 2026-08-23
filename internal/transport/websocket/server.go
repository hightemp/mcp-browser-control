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
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/ratelimit"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/router"
)

const (
	// DefaultPath is the browser extension WebSocket endpoint.
	DefaultPath          = "/ws"
	defaultReadTimeout   = 60 * time.Second
	defaultPingInterval  = 20 * time.Second
	defaultSendQueueSize = 64
	defaultMessageRate   = 1_000
	defaultMessageBurst  = 2_000
)

// Option configures a Server.
type Option func(*Server)

// Authenticator validates browser handshakes and revokes browser
// credentials.
type Authenticator interface {
	Authorize(browserID, credential, pairingCode string) (issuedCredential string, err error)
	Revoke(browserID string) (bool, error)
}

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

// WithMaxMessageBytes changes the browser message size limit.
func WithMaxMessageBytes(maxBytes int64) Option {
	return func(server *Server) {
		if maxBytes > 0 {
			server.maxMessageBytes = maxBytes
		}
	}
}

// WithOriginAllowlist restricts accepted safe origins to exact values.
func WithOriginAllowlist(origins []string) Option {
	return func(server *Server) {
		server.originAllowlist = append([]string(nil), origins...)
	}
}

// WithReadTimeout changes the maximum interval without browser activity.
func WithReadTimeout(timeout time.Duration) Option {
	return func(server *Server) {
		if timeout > 0 {
			server.readTimeout = timeout
		}
	}
}

// WithPingInterval changes the WebSocket control-ping interval.
func WithPingInterval(interval time.Duration) Option {
	return func(server *Server) {
		if interval > 0 {
			server.pingInterval = interval
		}
	}
}

// WithSendQueueSize changes the bounded per-connection send queue capacity.
func WithSendQueueSize(size int) Option {
	return func(server *Server) {
		if size > 0 {
			server.sendQueueSize = size
		}
	}
}

// WithMessageRateLimit changes the sustained and burst inbound message limits
// applied independently to each browser connection.
func WithMessageRateLimit(messagesPerSecond, burst int) Option {
	return func(server *Server) {
		if messagesPerSecond > 0 && burst > 0 {
			server.messageRate = messagesPerSecond
			server.messageBurst = burst
		}
	}
}

// WithAuthenticator configures the browser pairing authenticator.
func WithAuthenticator(authenticator Authenticator) Option {
	return func(server *Server) {
		if authenticator != nil {
			server.authenticator = authenticator
		}
	}
}

// Server accepts browser extension connections and connects them to the
// browser registry and request router.
type Server struct {
	registry         *registry.Registry
	router           *router.Router
	authenticator    Authenticator
	logger           *log.Logger
	handshakeTimeout time.Duration
	writeTimeout     time.Duration
	readTimeout      time.Duration
	pingInterval     time.Duration
	sendQueueSize    int
	maxMessageBytes  int64
	messageRate      int
	messageBurst     int
	originAllowlist  []string
	upgrader         gorilla.Upgrader

	connectionsMu sync.Mutex
	connections   map[string]activeConnection
	shuttingDown  bool
	handlers      sync.WaitGroup
}

type activeConnection struct {
	browserID string
	writer    *connection
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
		authenticator:    rejectingAuthenticator{},
		logger:           log.New(log.Writer(), "[WebSocket] ", log.LstdFlags),
		handshakeTimeout: 5 * time.Second,
		writeTimeout:     5 * time.Second,
		readTimeout:      defaultReadTimeout,
		pingInterval:     defaultPingInterval,
		sendQueueSize:    defaultSendQueueSize,
		maxMessageBytes:  4 << 20,
		messageRate:      defaultMessageRate,
		messageBurst:     defaultMessageBurst,
		connections:      make(map[string]activeConnection),
		upgrader: gorilla.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
		},
	}
	for _, option := range options {
		option(server)
	}
	server.upgrader.CheckOrigin = server.checkOrigin
	return server
}

// ServeHTTP upgrades, authenticates, and registers a browser connection.
func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != DefaultPath {
		http.NotFound(writer, request)
		return
	}
	if !loopbackHost(request.Host) {
		http.Error(writer, "forbidden host", http.StatusForbidden)
		return
	}
	if s.isShuttingDown() {
		http.Error(writer, "server is shutting down", http.StatusServiceUnavailable)
		return
	}

	socket, err := s.upgrader.Upgrade(writer, request, nil)
	if err != nil {
		s.logger.Printf("failed to upgrade WebSocket connection: %v", err)
		return
	}

	connection := newConnection(socket, s.writeTimeout, s.pingInterval, s.sendQueueSize)
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
	issuedCredential, err := s.authenticator.Authorize(
		helloMessage.BrowserID,
		hello.Credential,
		hello.PairingCode,
	)
	if err != nil {
		authenticationError := protocol.ErrorFrom(err)
		s.logger.Printf(
			"browser authentication failed: browserId=%s code=%s",
			helloMessage.BrowserID,
			authenticationError.Code,
		)
		message := protocol.NewMessage(protocol.TypeAuthError)
		message.BrowserID = helloMessage.BrowserID
		message.Error = authenticationError
		if sendErr := connection.Send(request.Context(), message); sendErr != nil {
			s.logger.Printf("failed to send browser authentication error: %v", sendErr)
		}
		return
	}

	if err := socket.SetReadDeadline(time.Now().Add(s.readTimeout)); err != nil {
		s.logger.Printf("failed to set browser read deadline: %v", err)
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
		var closeErr error
		if replaceable, ok := replaced.(interface {
			CloseWithCode(code int, reason string) error
		}); ok {
			closeErr = replaceable.CloseWithCode(gorilla.CloseServiceRestart, "connection replaced")
		} else {
			closeErr = replaced.Close()
		}
		if closeErr != nil {
			s.logger.Printf("failed to close replaced browser connection: %v", closeErr)
		}
	}
	if !s.trackConnection(browserID, connection) {
		s.registry.Disconnect(browserID, connection.ID(), "server is shutting down")
		return
	}

	disconnectReason := "connection closed"
	socket.SetPongHandler(func(string) error {
		s.registry.Touch(browserID, connection.ID())
		return socket.SetReadDeadline(time.Now().Add(s.readTimeout))
	})
	defer func() {
		s.untrackConnection(connection.ID())
		s.registry.Disconnect(browserID, connection.ID(), disconnectReason)
		s.router.FailConnection(browserID, connection.ID())
	}()

	welcome := protocol.NewMessage(protocol.TypeWelcome)
	welcome.BrowserID = browserID
	welcome.ConnectionID = connection.ID()
	welcomeResult := protocol.WelcomeResult{
		BrowserID:    browserID,
		ConnectionID: connection.ID(),
		ServerTime:   time.Now().UTC().Format(time.RFC3339Nano),
		Credential:   issuedCredential,
		Paired:       true,
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
	messageLimiter, err := ratelimit.New(s.messageRate, s.messageBurst)
	if err != nil {
		disconnectReason = "invalid message rate limit"
		s.logger.Printf("failed to initialize browser message rate limit: %v", err)
		return
	}
	for {
		var message protocol.Message
		if err := socket.ReadJSON(&message); err != nil {
			if gorilla.IsCloseError(err, gorilla.CloseNormalClosure, gorilla.CloseGoingAway) {
				disconnectReason = "browser closed connection"
			} else if !errors.Is(err, net.ErrClosed) {
				disconnectReason = "connection read failure"
				s.logger.Printf(
					"failed to read browser message: browserId=%s connectionId=%s error=%v",
					browserID,
					connection.ID(),
					err,
				)
			}
			return
		}
		if !messageLimiter.Allow() {
			disconnectReason = "browser message rate limit exceeded"
			s.logger.Printf(
				"browser message rate limit exceeded: browserId=%s connectionId=%s",
				browserID,
				connection.ID(),
			)
			_ = connection.CloseWithCode(gorilla.ClosePolicyViolation, disconnectReason)
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
		if err := socket.SetReadDeadline(time.Now().Add(s.readTimeout)); err != nil {
			disconnectReason = "failed to refresh read deadline"
			s.logger.Printf("failed to refresh browser read deadline: %v", err)
			return
		}

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
				disconnectReason = "connection write failure"
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
		case protocol.TypeRevoke:
			revoked, revokeErr := s.authenticator.Revoke(browserID)
			acknowledgement := protocol.NewMessage(protocol.TypeRevoke)
			acknowledgement.BrowserID = browserID
			acknowledgement.ConnectionID = connection.ID()
			succeeded := revokeErr == nil
			acknowledgement.Success = &succeeded
			if revokeErr != nil {
				acknowledgement.Error = protocol.ErrorFrom(revokeErr)
				s.logger.Printf(
					"failed to revoke browser credential: browserId=%s code=%s",
					browserID,
					acknowledgement.Error.Code,
				)
			} else {
				acknowledgement.Result, err = json.Marshal(map[string]bool{"revoked": revoked})
				if err != nil {
					s.logger.Printf("failed to marshal browser revoke acknowledgement: %v", err)
					return
				}
			}
			if err := connection.Send(request.Context(), acknowledgement); err != nil {
				disconnectReason = "connection write failure"
				s.logger.Printf("failed to send browser revoke acknowledgement: %v", err)
				return
			}
			if revokeErr == nil {
				disconnectReason = "credential revoked"
				return
			}
		case protocol.TypePong, protocol.TypeEvent:
			// Touch above is sufficient for the first protocol increment.
		default:
			s.logger.Printf("ignored unsupported browser message type %q", message.Type)
		}
	}
}

// Shutdown notifies and gracefully closes every active browser connection.
func (s *Server) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return protocol.NewError(protocol.CodeInvalidMessage, "context is required", false)
	}
	s.connectionsMu.Lock()
	s.shuttingDown = true
	connections := make([]activeConnection, 0, len(s.connections))
	for _, active := range s.connections {
		connections = append(connections, active)
	}
	s.connectionsMu.Unlock()

	var closeGroup sync.WaitGroup
	for _, active := range connections {
		active := active
		closeGroup.Add(1)
		go func() {
			defer closeGroup.Done()
			message := protocol.NewMessage(protocol.TypeEvent)
			message.BrowserID = active.browserID
			message.ConnectionID = active.writer.ID()
			message.Params, _ = json.Marshal(map[string]string{
				"name":   "server.shutdown",
				"reason": "server is shutting down",
			})
			_ = active.writer.Send(ctx, message)
			_ = active.writer.CloseWithCode(gorilla.CloseGoingAway, "server shutting down")
		}()
	}

	done := make(chan struct{})
	go func() {
		closeGroup.Wait()
		s.handlers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		for _, active := range connections {
			active.writer.abort()
		}
		return ctx.Err()
	}
}

func (s *Server) isShuttingDown() bool {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	return s.shuttingDown
}

func (s *Server) trackConnection(browserID string, connection *connection) bool {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	if s.shuttingDown {
		return false
	}
	s.handlers.Add(1)
	s.connections[connection.ID()] = activeConnection{browserID: browserID, writer: connection}
	return true
}

func (s *Server) untrackConnection(connectionID string) {
	s.connectionsMu.Lock()
	delete(s.connections, connectionID)
	s.connectionsMu.Unlock()
	s.handlers.Done()
}

type rejectingAuthenticator struct{}

func (rejectingAuthenticator) Authorize(_, _, _ string) (string, error) {
	return "", protocol.NewError(protocol.CodePairingRequired, "browser pairing is required", false)
}

func (rejectingAuthenticator) Revoke(string) (bool, error) {
	return false, nil
}

type outboundMessage struct {
	message protocol.Message
	result  chan error
}

type connection struct {
	id           string
	socket       *gorilla.Conn
	writeTimeout time.Duration
	pingInterval time.Duration
	outbound     chan outboundMessage
	done         chan struct{}
	pumpDone     chan struct{}
	doneOnce     sync.Once
	errMu        sync.Mutex
	closeErr     error
	closeCode    int
	closeReason  string
}

func newConnection(
	socket *gorilla.Conn,
	writeTimeout time.Duration,
	pingInterval time.Duration,
	sendQueueSize int,
) *connection {
	return &connection{
		id:           uuid.NewString(),
		socket:       socket,
		writeTimeout: writeTimeout,
		pingInterval: pingInterval,
		outbound:     make(chan outboundMessage, sendQueueSize),
		done:         make(chan struct{}),
		pumpDone:     make(chan struct{}),
		closeCode:    gorilla.CloseNormalClosure,
		closeReason:  "connection closed",
	}
}

func (c *connection) ID() string {
	return c.id
}

func (c *connection) start() {
	go c.writePump()
}

func (c *connection) Send(ctx context.Context, message protocol.Message) error {
	if ctx == nil {
		return protocol.NewError(protocol.CodeInvalidMessage, "context is required", false)
	}
	result := make(chan error, 1)
	outbound := outboundMessage{message: message, result: result}

	select {
	case <-c.done:
		return protocol.NewError(protocol.CodeBrowserDisconnected, "browser connection is closed", true)
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	select {
	case c.outbound <- outbound:
	default:
		return protocol.NewError(
			protocol.CodeBackpressure,
			"browser connection send queue is full",
			true,
		)
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
	return c.CloseWithCode(gorilla.CloseNormalClosure, "connection closed")
}

// CloseWithCode gracefully closes the WebSocket with the first requested code.
func (c *connection) CloseWithCode(code int, reason string) error {
	c.signalDone(code, reason)
	<-c.pumpDone
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.closeErr
}

func (c *connection) writePump() {
	ticker := time.NewTicker(c.pingInterval)
	defer ticker.Stop()
	defer func() {
		if err := c.socket.Close(); err != nil {
			c.recordCloseError(fmt.Errorf("close WebSocket: %w", err))
		}
		c.signalDone(gorilla.CloseAbnormalClosure, "writer stopped")
		close(c.pumpDone)
	}()

	for {
		select {
		case <-c.done:
			c.writeCloseFrame()
			return
		default:
		}
		select {
		case outbound := <-c.outbound:
			if err := c.writeJSON(outbound.message); err != nil {
				c.recordCloseError(err)
				outbound.result <- err
				return
			}
			outbound.result <- nil
		case <-ticker.C:
			if err := c.writePing(); err != nil {
				c.recordCloseError(err)
				return
			}
		case <-c.done:
			c.writeCloseFrame()
			return
		}
	}
}

func (c *connection) writeJSON(message protocol.Message) error {
	if err := c.socket.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err != nil {
		return fmt.Errorf("set WebSocket write deadline: %w", err)
	}
	if err := c.socket.WriteJSON(message); err != nil {
		return fmt.Errorf("write WebSocket message: %w", err)
	}
	return nil
}

func (c *connection) writePing() error {
	deadline := time.Now().Add(c.writeTimeout)
	if err := c.socket.WriteControl(gorilla.PingMessage, nil, deadline); err != nil {
		return fmt.Errorf("write WebSocket ping: %w", err)
	}
	return nil
}

func (c *connection) writeCloseFrame() {
	c.errMu.Lock()
	code := c.closeCode
	reason := c.closeReason
	c.errMu.Unlock()
	deadline := time.Now().Add(c.writeTimeout)
	payload := gorilla.FormatCloseMessage(code, reason)
	if err := c.socket.WriteControl(gorilla.CloseMessage, payload, deadline); err != nil {
		c.recordCloseError(fmt.Errorf("write WebSocket close frame: %w", err))
	}
}

func (c *connection) signalDone(code int, reason string) {
	c.doneOnce.Do(func() {
		c.errMu.Lock()
		c.closeCode = code
		c.closeReason = reason
		c.errMu.Unlock()
		close(c.done)
	})
}

func (c *connection) abort() {
	c.signalDone(gorilla.CloseGoingAway, "shutdown deadline exceeded")
	_ = c.socket.Close()
}

func (c *connection) recordCloseError(err error) {
	if err == nil {
		return
	}
	c.errMu.Lock()
	c.closeErr = errors.Join(c.closeErr, err)
	c.errMu.Unlock()
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

func (s *Server) checkOrigin(request *http.Request) bool {
	if !allowedOrigin(request) {
		return false
	}
	origin := request.Header.Get("Origin")
	if origin == "" || len(s.originAllowlist) == 0 {
		return true
	}
	for _, allowed := range s.originAllowlist {
		if origin == allowed {
			return true
		}
	}
	return false
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
