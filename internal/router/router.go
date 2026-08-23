// Package router sends requests to exactly one browser connection and
// correlates responses without cross-browser delivery.
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
)

type pendingKey struct {
	browserID string
	requestID string
}

type pendingRequest struct {
	connectionID string
	target       *protocol.Target
	result       chan requestResult
}

type requestResult struct {
	message protocol.Message
	err     error
}

// Option configures a Router.
type Option func(*Router)

// WithDefaultTimeout changes the deadline used when the caller has none.
func WithDefaultTimeout(timeout time.Duration) Option {
	return func(router *Router) {
		if timeout > 0 {
			router.defaultTimeout = timeout
		}
	}
}

// WithIDGenerator overrides request ID generation, primarily for tests.
func WithIDGenerator(generator func() string) Option {
	return func(router *Router) {
		if generator != nil {
			router.newRequestID = generator
		}
	}
}

// WithLogger sets the router logger.
func WithLogger(logger *log.Logger) Option {
	return func(router *Router) {
		if logger != nil {
			router.logger = logger
		}
	}
}

// Router manages in-flight requests for browser connections.
type Router struct {
	registry       *registry.Registry
	defaultTimeout time.Duration
	newRequestID   func() string
	logger         *log.Logger

	mu      sync.Mutex
	pending map[pendingKey]*pendingRequest
	closed  bool
}

// New creates a targeted request router.
func New(browserRegistry *registry.Registry, options ...Option) *Router {
	router := &Router{
		registry:       browserRegistry,
		defaultTimeout: 15 * time.Second,
		newRequestID: func() string {
			return uuid.Must(uuid.NewV7()).String()
		},
		logger:  log.New(log.Writer(), "[Router] ", log.LstdFlags),
		pending: make(map[pendingKey]*pendingRequest),
	}
	for _, option := range options {
		option(router)
	}
	return router
}

// Send sends a command to one browser and waits for its correlated response.
func (r *Router) Send(
	ctx context.Context,
	browserID string,
	command string,
	target *protocol.Target,
	params any,
) (response json.RawMessage, responseErr error) {
	if ctx == nil {
		return nil, protocol.NewError(protocol.CodeInvalidMessage, "context is required", false)
	}
	started := time.Now()
	requestID := r.newRequestID()
	defer func() {
		code := "OK"
		if responseErr != nil {
			code = string(protocol.ErrorFrom(responseErr).Code)
		}
		r.logger.Printf(
			"request completed: requestId=%q browserId=%q tool=%q duration=%s code=%s",
			requestID,
			browserID,
			command,
			time.Since(started).Round(time.Microsecond),
			code,
		)
	}()
	if browserID == "" {
		return nil, protocol.NewError(protocol.CodeBrowserNotFound, "browserId is required", false)
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return nil, protocol.NewError(protocol.CodeCancelled, "router is closed", false)
	}

	connection, _, err := r.registry.Route(browserID, command)
	if err != nil {
		return nil, err
	}

	requestCtx, cancel := withDefaultTimeout(ctx, r.defaultTimeout)
	defer cancel()

	timeout := remainingTimeout(requestCtx)
	message, err := protocol.NewRequest(requestID, browserID, command, target, params, timeout)
	if err != nil {
		return nil, fmt.Errorf("create browser request: %w", err)
	}

	key := pendingKey{browserID: browserID, requestID: requestID}
	pending := &pendingRequest{
		connectionID: connection.ID(),
		target:       message.Target,
		result:       make(chan requestResult, 1),
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, protocol.NewError(protocol.CodeCancelled, "router is closed", false)
	}
	if _, exists := r.pending[key]; exists {
		r.mu.Unlock()
		return nil, protocol.NewError(protocol.CodeInternal, "request ID collision", true)
	}
	r.pending[key] = pending
	r.mu.Unlock()

	if err := connection.Send(requestCtx, message); err != nil {
		r.removePending(key, pending)
		return nil, routeSendError(err).WithContext(requestID, message.Target)
	}

	select {
	case result := <-pending.result:
		if result.err != nil {
			return nil, result.err
		}
		if result.message.Success == nil {
			return nil, protocol.NewError(
				protocol.CodeInvalidMessage,
				"browser response is missing success",
				false,
			).WithContext(requestID, pending.target)
		}
		if !*result.message.Success {
			if result.message.Error == nil {
				return nil, protocol.NewError(
					protocol.CodeInvalidMessage,
					"browser response is missing an error",
					false,
				).WithContext(requestID, pending.target)
			}
			responseTarget := result.message.Target
			if responseTarget == nil {
				responseTarget = pending.target
			}
			return nil, result.message.Error.WithContext(
				result.message.RequestID,
				responseTarget,
			)
		}
		return append(json.RawMessage(nil), result.message.Result...), nil
	case <-requestCtx.Done():
		r.removePending(key, pending)
		r.sendCancel(connection, requestID, browserID)
		return nil, protocol.ErrorFrom(requestCtx.Err()).WithContext(requestID, message.Target)
	}
}

// HandleResponse delivers a response only when browser, connection, and
// request IDs all match the pending request.
func (r *Router) HandleResponse(browserID, connectionID string, message protocol.Message) bool {
	if message.Type != protocol.TypeResponse ||
		message.BrowserID != browserID ||
		message.RequestID == "" {
		return false
	}

	key := pendingKey{browserID: browserID, requestID: message.RequestID}
	r.mu.Lock()
	pending, ok := r.pending[key]
	if !ok || pending.connectionID != connectionID {
		r.mu.Unlock()
		return false
	}
	delete(r.pending, key)
	r.mu.Unlock()

	pending.result <- requestResult{message: message}
	return true
}

// FailConnection fails only requests that were sent through connectionID.
func (r *Router) FailConnection(browserID, connectionID string) int {
	r.mu.Lock()
	failed := make([]struct {
		key     pendingKey
		pending *pendingRequest
	}, 0)
	for key, pending := range r.pending {
		if key.browserID == browserID && pending.connectionID == connectionID {
			delete(r.pending, key)
			failed = append(failed, struct {
				key     pendingKey
				pending *pendingRequest
			}{key: key, pending: pending})
		}
	}
	r.mu.Unlock()

	for _, failedRequest := range failed {
		failure := protocol.NewError(
			protocol.CodeBrowserDisconnected,
			"browser disconnected while the command was in progress",
			true,
		).WithContext(failedRequest.key.requestID, failedRequest.pending.target)
		failedRequest.pending.result <- requestResult{err: failure}
	}
	return len(failed)
}

// Close fails all in-flight requests.
func (r *Router) Close() int {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return 0
	}
	r.closed = true
	pendingRequests := make([]struct {
		key     pendingKey
		pending *pendingRequest
	}, 0, len(r.pending))
	for key, pending := range r.pending {
		delete(r.pending, key)
		pendingRequests = append(pendingRequests, struct {
			key     pendingKey
			pending *pendingRequest
		}{key: key, pending: pending})
	}
	r.mu.Unlock()

	for _, pendingRequest := range pendingRequests {
		failure := protocol.NewError(
			protocol.CodeCancelled,
			"server is shutting down",
			false,
		).WithContext(pendingRequest.key.requestID, pendingRequest.pending.target)
		pendingRequest.pending.result <- requestResult{err: failure}
	}
	return len(pendingRequests)
}

// PendingCount returns the number of in-flight requests.
func (r *Router) PendingCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

func (r *Router) removePending(key pendingKey, expected *pendingRequest) {
	r.mu.Lock()
	if current, ok := r.pending[key]; ok && current == expected {
		delete(r.pending, key)
	}
	r.mu.Unlock()
}

func (r *Router) sendCancel(connection registry.Connection, requestID, browserID string) {
	cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := connection.Send(cancelCtx, protocol.NewCancel(requestID, browserID)); err != nil {
		r.logger.Printf("failed to send cancellation for request %s to browser %s: %v", requestID, browserID, err)
	}
}

func withDefaultTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func remainingTimeout(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	timeout := time.Until(deadline)
	if timeout < 0 {
		return 0
	}
	return timeout
}

func routeSendError(err error) *protocol.Error {
	var protocolErr *protocol.Error
	if errors.As(err, &protocolErr) {
		if protocolErr.Code == protocol.CodeBackpressure ||
			protocolErr.Code == protocol.CodeBrowserDisconnected {
			return protocol.ErrorFrom(protocolErr)
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return protocol.ErrorFrom(err)
	}
	return protocol.NewError(
		protocol.CodeBrowserDisconnected,
		"failed to send command to the browser",
		true,
	)
}
