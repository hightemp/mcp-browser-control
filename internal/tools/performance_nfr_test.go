package tools

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/registry"
	"github.com/mark3labs/mcp-go/mcp"
)

const browserListP95Budget = 100 * time.Millisecond

func TestBrowserListLatencyNFR(t *testing.T) {
	const sampleCount = 1_000

	service := newBrowserListPerformanceService(t)
	latencies := make([]time.Duration, 0, sampleCount)
	for range sampleCount {
		startedAt := time.Now()
		result, err := service.browserListHandler(
			context.Background(),
			mcp.CallToolRequest{},
			emptyArgs{},
		)
		latencies = append(latencies, time.Since(startedAt))
		if err != nil || result == nil || result.IsError {
			t.Fatalf("browserListHandler() = (%v, %v)", result, err)
		}
	}

	p95 := toolDurationPercentile(latencies, 95)
	t.Logf("browser_list latency: browsers=50 samples=%d p50=%s p95=%s budget=%s",
		sampleCount, toolDurationPercentile(latencies, 50), p95, browserListP95Budget)
	if p95 >= browserListP95Budget {
		t.Fatalf("browser_list latency p95 = %s, want less than %s", p95, browserListP95Budget)
	}
}

func BenchmarkBrowserList50(b *testing.B) {
	service := newBrowserListPerformanceService(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result, err := service.browserListHandler(
			context.Background(),
			mcp.CallToolRequest{},
			emptyArgs{},
		)
		if err != nil || result == nil || result.IsError {
			b.Fatalf("browserListHandler() = (%v, %v)", result, err)
		}
	}
}

func newBrowserListPerformanceService(tb testing.TB) *Service {
	tb.Helper()

	browserRegistry := registry.New()
	for index := range 50 {
		connection := newToolTestConnection(fmt.Sprintf("performance-connection-%02d", index))
		if _, err := browserRegistry.Register(
			registry.Registration{
				BrowserID:        fmt.Sprintf("performance-browser-%02d", index),
				DisplayName:      fmt.Sprintf("Performance Browser %02d", index),
				ExtensionVersion: "0.3.0-test",
				Capabilities:     []string{"tabs.list", "page.get_html"},
				Permissions:      []string{"tabs"},
			},
			connection,
		); err != nil {
			tb.Fatalf("Register(%d) error = %v", index, err)
		}
	}
	return NewService(browserRegistry, nil, nil)
}

func toolDurationPercentile(samples []time.Duration, percentile int) time.Duration {
	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := (len(ordered)*percentile + 99) / 100
	if index < 1 {
		index = 1
	}
	return ordered[index-1]
}
