//go:build soak

package soak_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	gorilla "github.com/gorilla/websocket"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/router"
	websockettransport "github.com/hightemp/go_mcp_browser_ext_tool/internal/transport/websocket"
)

const (
	defaultSoakDuration          = 8 * time.Hour
	defaultReconnectInterval     = 250 * time.Millisecond
	defaultEventsPerCycle        = 32
	maxRetainedHeapGrowthBytes   = 32 << 20
	maxRetainedGoroutineIncrease = 16
	minimumReconnectSuccessRate  = 0.995
	soakBrowserID                = "91ed1c7e-38eb-4a1a-a5fb-d137f6d9fdf3"
	soakCredential               = "soak-test-credential"
)

type soakAuthenticator struct{}

func (soakAuthenticator) Authorize(_, credential, pairingCode string) (string, error) {
	if pairingCode == "soak-test-pairing-code" || credential == soakCredential {
		return soakCredential, nil
	}
	return "", protocol.NewError(protocol.CodePairingRequired, "soak authentication failed", false)
}

func (soakAuthenticator) Revoke(string) (bool, error) {
	return true, nil
}

type soakReport struct {
	Component                 string  `json:"component"`
	DurationMS                int64   `json:"durationMs"`
	ReconnectAttempts         int     `json:"reconnectAttempts"`
	ReconnectSuccesses        int     `json:"reconnectSuccesses"`
	ReconnectSuccessRate      float64 `json:"reconnectSuccessRate"`
	ReconnectP95MS            float64 `json:"reconnectP95Ms"`
	PongP95MS                 float64 `json:"pongP95Ms"`
	EventsAttempted           int64   `json:"eventsAttempted"`
	EventsAccepted            int64   `json:"eventsAccepted"`
	DroppedEvents             int64   `json:"droppedEvents"`
	InitialHeapBytes          uint64  `json:"initialHeapBytes"`
	PeakHeapBytes             uint64  `json:"peakHeapBytes"`
	FinalHeapBytes            uint64  `json:"finalHeapBytes"`
	RetainedHeapGrowthBytes   uint64  `json:"retainedHeapGrowthBytes"`
	InitialGoroutines         int     `json:"initialGoroutines"`
	PeakGoroutines            int     `json:"peakGoroutines"`
	FinalGoroutines           int     `json:"finalGoroutines"`
	RetainedGoroutineIncrease int     `json:"retainedGoroutineIncrease"`
}

func TestReconnectAndEventSoak(t *testing.T) {
	duration := environmentDuration(t, "MCP_BROWSER_SOAK_DURATION", defaultSoakDuration)
	reconnectInterval := environmentDuration(
		t,
		"MCP_BROWSER_SOAK_RECONNECT_INTERVAL",
		defaultReconnectInterval,
	)
	eventsPerCycle := environmentPositiveInteger(
		t,
		"MCP_BROWSER_SOAK_EVENTS_PER_CYCLE",
		defaultEventsPerCycle,
	)

	browserRegistry := registry.New()
	requestRouter := router.New(
		browserRegistry,
		router.WithDefaultTimeout(5*time.Second),
		router.WithLogger(log.New(io.Discard, "", 0)),
	)
	webSocketHandler := websockettransport.NewServer(
		browserRegistry,
		requestRouter,
		websockettransport.WithAuthenticator(soakAuthenticator{}),
		websockettransport.WithLogger(log.New(io.Discard, "", 0)),
		websockettransport.WithMessageRateLimit(100_000, 100_000),
		websockettransport.WithReadTimeout(time.Hour),
		websockettransport.WithPingInterval(time.Hour),
	)
	httpServer := httptest.NewServer(webSocketHandler)

	var activeSocket *gorilla.Conn
	cleanup := func() {
		if activeSocket != nil {
			_ = activeSocket.Close()
			activeSocket = nil
		}
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = webSocketHandler.Shutdown(shutdownContext)
		cancel()
		httpServer.Close()
		requestRouter.Close()
	}
	cleanedUp := false
	t.Cleanup(func() {
		if !cleanedUp {
			cleanup()
		}
	})

	initialSocket, initialWelcome, err := openSoakSocket(
		httpServer.URL,
		protocol.HelloParams{PairingCode: "soak-test-pairing-code"},
	)
	if err != nil {
		t.Fatalf("initial browser connection: %v", err)
	}
	activeSocket = initialSocket
	previousConnectionID := initialWelcome.ConnectionID

	runtime.GC()
	initialMemory := readMemory()
	initialGoroutines := runtime.NumGoroutine()
	peakHeapBytes := initialMemory.HeapAlloc
	peakGoroutines := initialGoroutines
	startedAt := time.Now()
	deadline := startedAt.Add(duration)
	nextProgressAt := startedAt.Add(15 * time.Minute)
	var reconnectLatencies []time.Duration
	var pongLatencies []time.Duration
	var reconnectAttempts int
	var reconnectSuccesses int
	var eventsAttempted int64
	var eventsAccepted int64
	var operationErr error
	var sequence int64

	for time.Now().Before(deadline) {
		acceptedThisCycle := int64(0)
		for range eventsPerCycle {
			sequence++
			event := protocol.NewMessage(protocol.TypeEvent)
			event.BrowserID = soakBrowserID
			event.Params, _ = json.Marshal(map[string]any{
				"name":     "console.entry",
				"sequence": sequence,
			})
			eventsAttempted++
			if err := activeSocket.WriteJSON(event); err != nil {
				operationErr = fmt.Errorf("write event %d: %w", sequence, err)
				break
			}
			acceptedThisCycle++
		}
		if operationErr != nil {
			break
		}

		pingStartedAt := time.Now()
		ping := protocol.NewMessage(protocol.TypePing)
		ping.BrowserID = soakBrowserID
		if err := activeSocket.WriteJSON(ping); err != nil {
			operationErr = fmt.Errorf("write ping: %w", err)
			break
		}
		if err := activeSocket.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			operationErr = fmt.Errorf("set pong deadline: %w", err)
			break
		}
		var pong protocol.Message
		if err := activeSocket.ReadJSON(&pong); err != nil {
			operationErr = fmt.Errorf("read pong: %w", err)
			break
		}
		if pong.Type != protocol.TypePong || pong.BrowserID != soakBrowserID {
			operationErr = fmt.Errorf("invalid pong: %#v", pong)
			break
		}
		pongLatencies = append(pongLatencies, time.Since(pingStartedAt))
		eventsAccepted += acceptedThisCycle

		_ = activeSocket.WriteControl(
			gorilla.CloseMessage,
			gorilla.FormatCloseMessage(gorilla.CloseNormalClosure, "soak reconnect"),
			time.Now().Add(time.Second),
		)
		_ = activeSocket.Close()
		activeSocket = nil
		if err := waitForRegistryCount(browserRegistry, 0, 5*time.Second); err != nil {
			operationErr = err
			break
		}

		sleepUntil(time.Now().Add(reconnectInterval), deadline)
		if !time.Now().Before(deadline) {
			break
		}
		for activeSocket == nil && time.Now().Before(deadline) {
			reconnectAttempts++
			reconnectStartedAt := time.Now()
			var reconnectWelcome protocol.WelcomeResult
			activeSocket, reconnectWelcome, err = openSoakSocket(
				httpServer.URL,
				protocol.HelloParams{Credential: soakCredential},
			)
			if err != nil {
				activeSocket = nil
				sleepUntil(time.Now().Add(reconnectInterval), deadline)
				continue
			}
			if reconnectWelcome.ConnectionID == previousConnectionID {
				operationErr = fmt.Errorf(
					"reconnect reused connection ID %s",
					reconnectWelcome.ConnectionID,
				)
				break
			}
			previousConnectionID = reconnectWelcome.ConnectionID
			reconnectSuccesses++
			reconnectLatencies = append(reconnectLatencies, time.Since(reconnectStartedAt))
		}
		if operationErr != nil {
			break
		}
		if activeSocket == nil {
			break
		}

		memory := readMemory()
		peakHeapBytes = max(peakHeapBytes, memory.HeapAlloc)
		peakGoroutines = max(peakGoroutines, runtime.NumGoroutine())
		if time.Now().After(nextProgressAt) {
			t.Logf("soak progress: elapsed=%s reconnects=%d events=%d",
				time.Since(startedAt).Round(time.Second), reconnectSuccesses, eventsAccepted)
			nextProgressAt = nextProgressAt.Add(15 * time.Minute)
		}
	}

	cleanup()
	cleanedUp = true
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	finalMemory := readMemory()
	finalGoroutines := runtime.NumGoroutine()
	reconnectSuccessRate := 1.0
	if reconnectAttempts > 0 {
		reconnectSuccessRate = float64(reconnectSuccesses) / float64(reconnectAttempts)
	}
	report := soakReport{
		Component:                 "go_websocket_transport",
		DurationMS:                time.Since(startedAt).Milliseconds(),
		ReconnectAttempts:         reconnectAttempts,
		ReconnectSuccesses:        reconnectSuccesses,
		ReconnectSuccessRate:      reconnectSuccessRate,
		ReconnectP95MS:            durationMilliseconds(percentile(reconnectLatencies, 95)),
		PongP95MS:                 durationMilliseconds(percentile(pongLatencies, 95)),
		EventsAttempted:           eventsAttempted,
		EventsAccepted:            eventsAccepted,
		DroppedEvents:             eventsAttempted - eventsAccepted,
		InitialHeapBytes:          initialMemory.HeapAlloc,
		PeakHeapBytes:             peakHeapBytes,
		FinalHeapBytes:            finalMemory.HeapAlloc,
		RetainedHeapGrowthBytes:   positiveDifference(finalMemory.HeapAlloc, initialMemory.HeapAlloc),
		InitialGoroutines:         initialGoroutines,
		PeakGoroutines:            peakGoroutines,
		FinalGoroutines:           finalGoroutines,
		RetainedGoroutineIncrease: max(0, finalGoroutines-initialGoroutines),
	}
	reportJSON, _ := json.Marshal(report)
	t.Logf("SOAK_REPORT %s", reportJSON)

	if operationErr != nil {
		t.Errorf("soak operation failed: %v", operationErr)
	}
	if reconnectAttempts == 0 {
		t.Error("soak completed without a reconnect attempt")
	}
	if reconnectSuccessRate < minimumReconnectSuccessRate {
		t.Errorf("reconnect success rate = %.4f, want at least %.4f",
			reconnectSuccessRate, minimumReconnectSuccessRate)
	}
	if report.DroppedEvents != 0 {
		t.Errorf("transport dropped events = %d, want 0", report.DroppedEvents)
	}
	if report.RetainedHeapGrowthBytes > maxRetainedHeapGrowthBytes {
		t.Errorf("retained heap growth = %d bytes, want at most %d",
			report.RetainedHeapGrowthBytes, maxRetainedHeapGrowthBytes)
	}
	if report.RetainedGoroutineIncrease > maxRetainedGoroutineIncrease {
		t.Errorf("retained goroutine increase = %d, want at most %d",
			report.RetainedGoroutineIncrease, maxRetainedGoroutineIncrease)
	}
}

func openSoakSocket(
	serverURL string,
	helloParams protocol.HelloParams,
) (*gorilla.Conn, protocol.WelcomeResult, error) {
	headers := http.Header{"Origin": []string{"chrome-extension://soak-test"}}
	socket, response, err := gorilla.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(serverURL, "http")+websockettransport.DefaultPath,
		headers,
	)
	if err != nil {
		if response != nil {
			return nil, protocol.WelcomeResult{}, fmt.Errorf("dial: %w (%s)", err, response.Status)
		}
		return nil, protocol.WelcomeResult{}, fmt.Errorf("dial: %w", err)
	}
	fail := func(cause error) (*gorilla.Conn, protocol.WelcomeResult, error) {
		_ = socket.Close()
		return nil, protocol.WelcomeResult{}, cause
	}

	helloParams.DisplayName = "Soak Browser"
	helloParams.ExtensionVersion = "0.3.0-soak"
	helloParams.Browser = protocol.BrowserMetadata{Name: "Soak Chromium", Version: "125"}
	helloParams.Capabilities = []string{protocol.CommandBrowserPing}
	hello := protocol.NewMessage(protocol.TypeHello)
	hello.BrowserID = soakBrowserID
	hello.Params, err = json.Marshal(helloParams)
	if err != nil {
		return fail(fmt.Errorf("marshal hello: %w", err))
	}
	if err := socket.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fail(fmt.Errorf("set hello write deadline: %w", err))
	}
	if err := socket.WriteJSON(hello); err != nil {
		return fail(fmt.Errorf("write hello: %w", err))
	}
	if err := socket.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fail(fmt.Errorf("set welcome deadline: %w", err))
	}
	var welcome protocol.Message
	if err := socket.ReadJSON(&welcome); err != nil {
		return fail(fmt.Errorf("read welcome: %w", err))
	}
	if welcome.Type != protocol.TypeWelcome || welcome.BrowserID != soakBrowserID {
		return fail(fmt.Errorf("invalid welcome: %#v", welcome))
	}
	var welcomeResult protocol.WelcomeResult
	if err := json.Unmarshal(welcome.Result, &welcomeResult); err != nil {
		return fail(fmt.Errorf("decode welcome: %w", err))
	}
	if welcomeResult.BrowserID != soakBrowserID || welcomeResult.ConnectionID == "" {
		return fail(fmt.Errorf("invalid welcome result: %#v", welcomeResult))
	}
	if _, err := uuid.Parse(welcomeResult.ConnectionID); err != nil {
		return fail(fmt.Errorf("invalid connection ID: %w", err))
	}
	if err := socket.SetReadDeadline(time.Time{}); err != nil {
		return fail(fmt.Errorf("clear welcome deadline: %w", err))
	}
	if err := socket.SetWriteDeadline(time.Time{}); err != nil {
		return fail(fmt.Errorf("clear hello deadline: %w", err))
	}
	return socket, welcomeResult, nil
}

func environmentDuration(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		t.Fatalf("%s must be a positive Go duration, got %q", name, value)
	}
	return duration
}

func environmentPositiveInteger(t *testing.T, name string, fallback int) int {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		t.Fatalf("%s must be a positive integer, got %q", name, value)
	}
	return parsed
}

func waitForRegistryCount(
	browserRegistry *registry.Registry,
	want int,
	timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if browserRegistry.Count() == want {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return fmt.Errorf("registry count = %d, want %d", browserRegistry.Count(), want)
}

func sleepUntil(wakeAt, deadline time.Time) {
	if wakeAt.After(deadline) {
		wakeAt = deadline
	}
	if delay := time.Until(wakeAt); delay > 0 {
		time.Sleep(delay)
	}
}

func percentile(samples []time.Duration, requested int) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := (len(ordered)*requested + 99) / 100
	return ordered[max(1, index)-1]
}

func durationMilliseconds(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1_000
}

func readMemory() runtime.MemStats {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return memory
}

func positiveDifference(after, before uint64) uint64 {
	if after <= before {
		return 0
	}
	return after - before
}
