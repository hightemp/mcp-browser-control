package router

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
)

const routingP95Budget = 50 * time.Millisecond

type immediateConnection struct {
	id     string
	router *Router
}

func (c *immediateConnection) ID() string {
	return c.id
}

func (c *immediateConnection) Send(_ context.Context, message protocol.Message) error {
	response, err := protocol.NewResponse(
		message.RequestID,
		message.BrowserID,
		map[string]string{"owner": message.BrowserID},
		nil,
	)
	if err != nil {
		return err
	}
	if !c.router.HandleResponse(message.BrowserID, c.id, response) {
		return fmt.Errorf("router rejected response %s", message.RequestID)
	}
	return nil
}

func (c *immediateConnection) Close() error {
	return nil
}

func TestRoutingLatencyNFR(t *testing.T) {
	const sampleCount = 10_000

	requestRouter, browserID := newImmediateRouter(t)
	latencies := make([]time.Duration, 0, sampleCount)
	for range sampleCount {
		startedAt := time.Now()
		response, err := requestRouter.Send(
			context.Background(),
			browserID,
			protocol.CommandTabsList,
			nil,
			nil,
		)
		latencies = append(latencies, time.Since(startedAt))
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		var result struct {
			Owner string `json:"owner"`
		}
		if err := json.Unmarshal(response, &result); err != nil || result.Owner != browserID {
			t.Fatalf("response = %s, error = %v", response, err)
		}
	}

	p95 := durationPercentile(latencies, 95)
	t.Logf("routing latency: samples=%d p50=%s p95=%s budget=%s", sampleCount,
		durationPercentile(latencies, 50), p95, routingP95Budget)
	if p95 >= routingP95Budget {
		t.Fatalf("routing latency p95 = %s, want less than %s", p95, routingP95Budget)
	}
	if pending := requestRouter.PendingCount(); pending != 0 {
		t.Fatalf("PendingCount() = %d, want 0", pending)
	}
}

func BenchmarkRouterRoundTrip(b *testing.B) {
	requestRouter, browserID := newImmediateRouter(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := requestRouter.Send(
			context.Background(),
			browserID,
			protocol.CommandTabsList,
			nil,
			nil,
		); err != nil {
			b.Fatal(err)
		}
	}
}

func newImmediateRouter(tb testing.TB) (*Router, string) {
	tb.Helper()

	const browserID = "performance-browser"
	browserRegistry := registry.New()
	connection := &immediateConnection{id: "performance-connection"}
	var requestCounter atomic.Uint64
	requestRouter := New(
		browserRegistry,
		WithIDGenerator(func() string {
			return fmt.Sprintf("performance-request-%d", requestCounter.Add(1))
		}),
		WithLogger(log.New(io.Discard, "", 0)),
	)
	connection.router = requestRouter
	if _, err := browserRegistry.Register(
		registry.Registration{
			BrowserID:    browserID,
			DisplayName:  "Performance Browser",
			Capabilities: []string{protocol.CommandTabsList},
		},
		connection,
	); err != nil {
		tb.Fatalf("Register() error = %v", err)
	}
	return requestRouter, browserID
}

func durationPercentile(samples []time.Duration, percentile int) time.Duration {
	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := (len(ordered)*percentile + 99) / 100
	if index < 1 {
		index = 1
	}
	return ordered[index-1]
}
