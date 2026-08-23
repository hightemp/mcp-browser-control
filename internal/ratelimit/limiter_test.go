package ratelimit

import (
	"testing"
	"time"
)

func TestLimiterRefillsWithoutExceedingBurst(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	limiter, err := New(2, 3, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for index := range 3 {
		if !limiter.Allow() {
			t.Fatalf("Allow() at burst index %d = false", index)
		}
	}
	if limiter.Allow() {
		t.Fatal("Allow() above burst = true")
	}
	now = now.Add(500 * time.Millisecond)
	if !limiter.Allow() || limiter.Allow() {
		t.Fatal("500ms refill did not provide exactly one token")
	}
	now = now.Add(10 * time.Second)
	for index := range 3 {
		if !limiter.Allow() {
			t.Fatalf("Allow() after refill at burst index %d = false", index)
		}
	}
	if limiter.Allow() {
		t.Fatal("refill exceeded configured burst")
	}
}

func TestKeyedLimiterIsolatesAndBoundsCallers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	limiter, err := NewKeyed(1, 1, 2, time.Minute, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewKeyed() error = %v", err)
	}
	if !limiter.Allow("session-a") || limiter.Allow("session-a") {
		t.Fatal("session-a did not receive an isolated one-token burst")
	}
	if !limiter.Allow("session-b") {
		t.Fatal("session-b was affected by session-a")
	}

	now = now.Add(time.Second)
	if !limiter.Allow("session-a") {
		t.Fatal("session-a did not refill")
	}
	if !limiter.Allow("session-c") {
		t.Fatal("session-c was not admitted after bounded eviction")
	}
	if !limiter.Allow("session-b") {
		t.Fatal("oldest session was not evicted when the key limit was reached")
	}

	limiter.Delete("session-a")
	if !limiter.Allow("session-a") {
		t.Fatal("deleted session did not receive a fresh bucket")
	}
	now = now.Add(2 * time.Minute)
	if !limiter.Allow("session-d") {
		t.Fatal("idle buckets were not cleaned before admitting a new key")
	}
}

func TestLimiterRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := New(0, 1); err == nil {
		t.Fatal("New(0, 1) error = nil")
	}
	if _, err := New(1, 0); err == nil {
		t.Fatal("New(1, 0) error = nil")
	}
	if _, err := NewKeyed(1, 1, 0, time.Minute); err == nil {
		t.Fatal("NewKeyed() with zero max keys error = nil")
	}
	if _, err := NewKeyed(1, 1, 1, 0); err == nil {
		t.Fatal("NewKeyed() with zero idle TTL error = nil")
	}
}
