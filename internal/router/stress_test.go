package router

import (
	"context"
	"encoding/json"
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

func TestRouterRoutesTenThousandCommandsWithoutCrossDelivery(t *testing.T) {
	const (
		browserCount       = 4
		commandsPerBrowser = 2_500
		totalCommands      = browserCount * commandsPerBrowser
		workerCount        = 128
	)

	browserRegistry := registry.New()
	connections := make(map[string]*fakeConnection, browserCount)
	for index := range browserCount {
		browserID := fmt.Sprintf("browser-%d", index)
		connection := newFakeConnection(fmt.Sprintf("connection-%d", index))
		registerTestBrowser(t, browserRegistry, browserID, connection)
		connections[browserID] = connection
	}

	var requestCounter atomic.Int64
	requestRouter := New(
		browserRegistry,
		WithDefaultTimeout(30*time.Second),
		WithIDGenerator(func() string {
			return fmt.Sprintf("stress-request-%d", requestCounter.Add(1))
		}),
		WithLogger(log.New(io.Discard, "", 0)),
	)

	responseErrors := make(chan error, totalCommands*2)
	var responderGroup sync.WaitGroup
	for browserID, connection := range connections {
		browserID := browserID
		connection := connection
		responderGroup.Add(1)
		go func() {
			defer responderGroup.Done()
			for range commandsPerBrowser {
				request := <-connection.messages
				if request.Type != protocol.TypeRequest || request.BrowserID != browserID {
					responseErrors <- fmt.Errorf("%s received cross-routed request %#v", browserID, request)
				}
				response, err := protocol.NewResponse(
					request.RequestID,
					browserID,
					map[string]string{"owner": browserID},
					nil,
				)
				if err != nil {
					responseErrors <- fmt.Errorf("create %s response: %w", browserID, err)
					continue
				}
				if !requestRouter.HandleResponse(browserID, connection.ID(), response) {
					responseErrors <- fmt.Errorf("response for %s request %s was rejected", browserID, request.RequestID)
				}
			}
		}()
	}

	testContext, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	jobs := make(chan string)
	requestErrors := make(chan error, totalCommands)
	var workerGroup sync.WaitGroup
	for range workerCount {
		workerGroup.Add(1)
		go func() {
			defer workerGroup.Done()
			for browserID := range jobs {
				result, err := requestRouter.Send(testContext, browserID, "tabs.list", nil, nil)
				if err != nil {
					requestErrors <- fmt.Errorf("Send(%s): %w", browserID, err)
					continue
				}
				var payload struct {
					Owner string `json:"owner"`
				}
				if err := json.Unmarshal(result, &payload); err != nil {
					requestErrors <- fmt.Errorf("decode %s response: %w", browserID, err)
					continue
				}
				if payload.Owner != browserID {
					requestErrors <- fmt.Errorf("%s received response owned by %s", browserID, payload.Owner)
				}
			}
		}()
	}
	for index := range totalCommands {
		jobs <- fmt.Sprintf("browser-%d", index%browserCount)
	}
	close(jobs)
	workerGroup.Wait()
	responderGroup.Wait()
	close(requestErrors)
	close(responseErrors)

	for err := range requestErrors {
		t.Error(err)
	}
	for err := range responseErrors {
		t.Error(err)
	}
	if got := requestRouter.PendingCount(); got != 0 {
		t.Fatalf("PendingCount() = %d, want 0", got)
	}
}

func TestRouterCloseUnderConcurrentLoad(t *testing.T) {
	const requestCount = 256

	browserRegistry := registry.New()
	connection := newFakeConnection("connection-shutdown")
	registerTestBrowser(t, browserRegistry, "browser-shutdown", connection)
	var requestCounter atomic.Int64
	requestRouter := New(
		browserRegistry,
		WithDefaultTimeout(30*time.Second),
		WithIDGenerator(func() string {
			return fmt.Sprintf("shutdown-request-%d", requestCounter.Add(1))
		}),
		WithLogger(log.New(io.Discard, "", 0)),
	)

	results := make(chan error, requestCount)
	for range requestCount {
		go func() {
			_, err := requestRouter.Send(
				context.Background(),
				"browser-shutdown",
				"tabs.list",
				nil,
				nil,
			)
			results <- err
		}()
	}
	for range requestCount {
		<-connection.messages
	}
	if got := requestRouter.PendingCount(); got != requestCount {
		t.Fatalf("PendingCount() = %d, want %d", got, requestCount)
	}
	if closed := requestRouter.Close(); closed != requestCount {
		t.Fatalf("Close() = %d, want %d", closed, requestCount)
	}
	for range requestCount {
		assertProtocolErrorCode(t, <-results, protocol.CodeCancelled)
	}
	if got := requestRouter.PendingCount(); got != 0 {
		t.Fatalf("PendingCount() after Close = %d, want 0", got)
	}
}
