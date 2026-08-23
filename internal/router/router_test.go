package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
)

type fakeConnection struct {
	id       string
	messages chan protocol.Message
	sendErr  error
}

func newFakeConnection(id string) *fakeConnection {
	return &fakeConnection{
		id:       id,
		messages: make(chan protocol.Message, 16),
	}
}

func (c *fakeConnection) ID() string {
	return c.id
}

func (c *fakeConnection) Send(ctx context.Context, message protocol.Message) error {
	if c.sendErr != nil {
		return c.sendErr
	}
	select {
	case c.messages <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *fakeConnection) Close() error {
	return nil
}

func TestRouterTargetsAndCorrelatesTwoBrowsers(t *testing.T) {
	t.Parallel()

	browserRegistry := registry.New()
	browserA := newFakeConnection("connection-a")
	browserB := newFakeConnection("connection-b")
	registerTestBrowser(t, browserRegistry, "browser-a", browserA)
	registerTestBrowser(t, browserRegistry, "browser-b", browserB)

	var counter atomic.Int64
	requestRouter := New(
		browserRegistry,
		WithDefaultTimeout(time.Second),
		WithIDGenerator(func() string {
			return fmt.Sprintf("request-%d", counter.Add(1))
		}),
		WithLogger(log.New(io.Discard, "", 0)),
	)

	type sendResult struct {
		browserID string
		result    json.RawMessage
		err       error
	}
	results := make(chan sendResult, 2)
	var wg sync.WaitGroup
	for _, browserID := range []string{"browser-a", "browser-b"} {
		browserID := browserID
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := requestRouter.Send(
				context.Background(),
				browserID,
				"tabs.list",
				nil,
				map[string]any{},
			)
			results <- sendResult{browserID: browserID, result: result, err: err}
		}()
	}

	messageA := receiveMessage(t, browserA.messages)
	messageB := receiveMessage(t, browserB.messages)
	if messageA.BrowserID != "browser-a" {
		t.Errorf("browser A received target %q", messageA.BrowserID)
	}
	if messageB.BrowserID != "browser-b" {
		t.Errorf("browser B received target %q", messageB.BrowserID)
	}

	responseA, err := protocol.NewResponse(messageA.RequestID, "browser-a", map[string]string{"owner": "A"}, nil)
	if err != nil {
		t.Fatalf("NewResponse(A) error = %v", err)
	}
	responseB, err := protocol.NewResponse(messageB.RequestID, "browser-b", map[string]string{"owner": "B"}, nil)
	if err != nil {
		t.Fatalf("NewResponse(B) error = %v", err)
	}

	if requestRouter.HandleResponse("browser-b", browserB.ID(), responseA) {
		t.Error("accepted browser A response from browser B connection")
	}
	if !requestRouter.HandleResponse("browser-a", browserA.ID(), responseA) {
		t.Error("did not accept browser A response")
	}
	if !requestRouter.HandleResponse("browser-b", browserB.ID(), responseB) {
		t.Error("did not accept browser B response")
	}

	wg.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatalf("Send(%s) error = %v", result.browserID, result.err)
		}
		var payload map[string]string
		if err := json.Unmarshal(result.result, &payload); err != nil {
			t.Fatalf("unmarshal result for %s: %v", result.browserID, err)
		}
		wantOwner := "A"
		if result.browserID == "browser-b" {
			wantOwner = "B"
		}
		if payload["owner"] != wantOwner {
			t.Errorf("result owner for %s = %q, want %q", result.browserID, payload["owner"], wantOwner)
		}
	}
}

func TestRouterFailsOnlyOldConnectionRequests(t *testing.T) {
	t.Parallel()

	browserRegistry := registry.New()
	oldConnection := newFakeConnection("connection-old")
	registerTestBrowser(t, browserRegistry, "browser-a", oldConnection)
	requestRouter := New(
		browserRegistry,
		WithDefaultTimeout(time.Second),
		WithIDGenerator(func() string { return "request-old" }),
		WithLogger(log.New(io.Discard, "", 0)),
	)

	result := make(chan error, 1)
	go func() {
		_, err := requestRouter.Send(context.Background(), "browser-a", "tabs.list", nil, nil)
		result <- err
	}()
	receiveMessage(t, oldConnection.messages)

	newConnection := newFakeConnection("connection-new")
	replaced, err := browserRegistry.Register(
		registry.Registration{BrowserID: "browser-a"},
		newConnection,
	)
	if err != nil {
		t.Fatalf("Register(replacement) error = %v", err)
	}
	if replaced != oldConnection {
		t.Fatalf("replaced = %v, want old connection", replaced)
	}

	if failed := requestRouter.FailConnection("browser-a", oldConnection.ID()); failed != 1 {
		t.Errorf("FailConnection() = %d, want 1", failed)
	}

	select {
	case err := <-result:
		assertProtocolErrorCode(t, err, protocol.CodeBrowserDisconnected)
	case <-time.After(time.Second):
		t.Fatal("Send() did not return after connection failure")
	}
}

func TestRouterCancellationRemovesPendingRequest(t *testing.T) {
	t.Parallel()

	browserRegistry := registry.New()
	connection := newFakeConnection("connection-a")
	registerTestBrowser(t, browserRegistry, "browser-a", connection)
	requestRouter := New(
		browserRegistry,
		WithDefaultTimeout(time.Second),
		WithIDGenerator(func() string { return "request-cancel" }),
		WithLogger(log.New(io.Discard, "", 0)),
	)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := requestRouter.Send(ctx, "browser-a", "tabs.list", nil, nil)
		result <- err
	}()

	request := receiveMessage(t, connection.messages)
	cancel()
	if err := <-result; err != context.Canceled {
		t.Fatalf("Send() error = %v, want context.Canceled", err)
	}
	cancelMessage := receiveMessage(t, connection.messages)
	if cancelMessage.Type != protocol.TypeCancel || cancelMessage.RequestID != request.RequestID {
		t.Errorf("cancel message = %#v, want request %q", cancelMessage, request.RequestID)
	}
	if got := requestRouter.PendingCount(); got != 0 {
		t.Errorf("PendingCount() = %d, want 0", got)
	}
}

func TestRouterRejectsInvalidAndDisconnectedTargets(t *testing.T) {
	t.Parallel()

	browserRegistry := registry.New()
	requestRouter := New(browserRegistry, WithLogger(log.New(io.Discard, "", 0)))
	for _, test := range []struct {
		name      string
		ctx       context.Context
		browserID string
		wantCode  protocol.ErrorCode
	}{
		{name: "nil context", ctx: nil, browserID: "browser-a", wantCode: protocol.CodeInvalidMessage},
		{name: "empty browser", ctx: context.Background(), wantCode: protocol.CodeBrowserNotFound},
		{name: "missing browser", ctx: context.Background(), browserID: "missing", wantCode: protocol.CodeBrowserNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := requestRouter.Send(test.ctx, test.browserID, "tabs.list", nil, nil)
			assertProtocolErrorCode(t, err, test.wantCode)
		})
	}

	broken := newFakeConnection("connection-broken")
	broken.sendErr = context.Canceled
	registerTestBrowser(t, browserRegistry, "browser-broken", broken)
	_, err := requestRouter.Send(context.Background(), "browser-broken", "tabs.list", nil, nil)
	assertProtocolErrorCode(t, err, protocol.CodeBrowserDisconnected)
}

func TestRouterCloseFailsPendingRequest(t *testing.T) {
	t.Parallel()

	browserRegistry := registry.New()
	connection := newFakeConnection("connection-a")
	registerTestBrowser(t, browserRegistry, "browser-a", connection)
	requestRouter := New(
		browserRegistry,
		WithIDGenerator(func() string { return "request-close" }),
		WithLogger(log.New(io.Discard, "", 0)),
	)
	result := make(chan error, 1)
	go func() {
		_, err := requestRouter.Send(context.Background(), "browser-a", "tabs.list", nil, nil)
		result <- err
	}()
	receiveMessage(t, connection.messages)
	if closed := requestRouter.Close(); closed != 1 {
		t.Fatalf("Close() = %d, want 1", closed)
	}
	select {
	case err := <-result:
		assertProtocolErrorCode(t, err, protocol.CodeCancelled)
	case <-time.After(time.Second):
		t.Fatal("Send() did not return after Close()")
	}
}

func TestRouterEnrichesBrowserErrorsWithRequestContext(t *testing.T) {
	t.Parallel()

	browserRegistry := registry.New()
	connection := newFakeConnection("connection-a")
	registerTestBrowser(t, browserRegistry, "browser-a", connection)
	requestRouter := New(
		browserRegistry,
		WithIDGenerator(func() string { return "request-error" }),
		WithLogger(log.New(io.Discard, "", 0)),
	)
	tabID := 42
	result := make(chan error, 1)
	go func() {
		_, err := requestRouter.Send(
			context.Background(),
			"browser-a",
			"page.click",
			&protocol.Target{TabID: &tabID},
			nil,
		)
		result <- err
	}()
	request := receiveMessage(t, connection.messages)
	response, err := protocol.NewResponse(
		request.RequestID,
		"browser-a",
		nil,
		protocol.NewError(protocol.CodeElementNotFound, "element was not found", false),
	)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	response.Target = request.Target
	if !requestRouter.HandleResponse("browser-a", connection.ID(), response) {
		t.Fatal("HandleResponse() = false")
	}

	select {
	case resultErr := <-result:
		var protocolErr *protocol.Error
		if !errors.As(resultErr, &protocolErr) {
			t.Fatalf("error type = %T", resultErr)
		}
		if protocolErr.RequestID != request.RequestID ||
			protocolErr.Target == nil ||
			protocolErr.Target.TabID == nil ||
			*protocolErr.Target.TabID != tabID {
			t.Fatalf("error context = %#v", protocolErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Send() did not return browser error")
	}
}

func registerTestBrowser(
	t *testing.T,
	browserRegistry *registry.Registry,
	browserID string,
	connection registry.Connection,
) {
	t.Helper()
	if _, err := browserRegistry.Register(
		registry.Registration{BrowserID: browserID, DisplayName: browserID},
		connection,
	); err != nil {
		t.Fatalf("Register(%s) error = %v", browserID, err)
	}
}

func receiveMessage(t *testing.T, messages <-chan protocol.Message) protocol.Message {
	t.Helper()
	select {
	case message := <-messages:
		return message
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
		return protocol.Message{}
	}
}

func assertProtocolErrorCode(t *testing.T, err error, want protocol.ErrorCode) {
	t.Helper()
	got := protocol.ErrorFrom(err)
	if got.Code != want {
		t.Errorf("error code = %q, want %q (error: %v)", got.Code, want, err)
	}
}
