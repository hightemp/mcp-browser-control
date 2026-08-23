// Package ratelimit provides bounded, concurrency-safe token buckets.
package ratelimit

import (
	"errors"
	"sync"
	"time"
)

// ErrExceeded is returned when a caller has exhausted its rate-limit bucket.
var ErrExceeded = errors.New("rate limit exceeded")

// Option configures a limiter.
type Option func(*options)

type options struct {
	now func() time.Time
}

// WithClock replaces the clock used for rate-limit decisions.
func WithClock(now func() time.Time) Option {
	return func(settings *options) {
		if now != nil {
			settings.now = now
		}
	}
}

type bucket struct {
	tokens   float64
	updated  time.Time
	lastSeen time.Time
}

func (b *bucket) allow(now time.Time, rate, burst float64) bool {
	if b.updated.IsZero() {
		b.tokens = burst
		b.updated = now
	}
	if elapsed := now.Sub(b.updated); elapsed > 0 {
		b.tokens += elapsed.Seconds() * rate
		if b.tokens > burst {
			b.tokens = burst
		}
		b.updated = now
	}
	b.lastSeen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Limiter applies one token bucket to a single caller.
type Limiter struct {
	mu    sync.Mutex
	rate  float64
	burst float64
	now   func() time.Time
	state bucket
}

// New creates a single-caller token bucket.
func New(requestsPerSecond, burst int, limiterOptions ...Option) (*Limiter, error) {
	settings, err := validate(requestsPerSecond, burst, limiterOptions)
	if err != nil {
		return nil, err
	}
	return &Limiter{
		rate:  float64(requestsPerSecond),
		burst: float64(burst),
		now:   settings.now,
	}, nil
}

// Allow consumes one token if the caller remains within its configured rate.
func (l *Limiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state.allow(l.now(), l.rate, l.burst)
}

// KeyedLimiter applies an independent bucket to each key while bounding the
// number and lifetime of tracked keys.
type KeyedLimiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	maxKeys int
	idleTTL time.Duration
	now     func() time.Time
	states  map[string]*bucket
}

// NewKeyed creates a bounded set of per-key token buckets.
func NewKeyed(
	requestsPerSecond, burst, maxKeys int,
	idleTTL time.Duration,
	limiterOptions ...Option,
) (*KeyedLimiter, error) {
	settings, err := validate(requestsPerSecond, burst, limiterOptions)
	if err != nil {
		return nil, err
	}
	if maxKeys <= 0 {
		return nil, errors.New("max keys must be positive")
	}
	if idleTTL <= 0 {
		return nil, errors.New("idle TTL must be positive")
	}
	return &KeyedLimiter{
		rate:    float64(requestsPerSecond),
		burst:   float64(burst),
		maxKeys: maxKeys,
		idleTTL: idleTTL,
		now:     settings.now,
		states:  make(map[string]*bucket),
	}, nil
}

// Allow consumes one token from key's bucket. An empty key shares one bounded
// anonymous bucket.
func (l *KeyedLimiter) Allow(key string) bool {
	if key == "" {
		key = "anonymous"
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.states[key]
	if !ok {
		l.makeRoom(now)
		state = &bucket{}
		l.states[key] = state
	}
	return state.allow(now, l.rate, l.burst)
}

// Delete releases the bucket for a completed session.
func (l *KeyedLimiter) Delete(key string) {
	if key == "" {
		key = "anonymous"
	}
	l.mu.Lock()
	delete(l.states, key)
	l.mu.Unlock()
}

func (l *KeyedLimiter) makeRoom(now time.Time) {
	for key, state := range l.states {
		if now.Sub(state.lastSeen) >= l.idleTTL {
			delete(l.states, key)
		}
	}
	if len(l.states) < l.maxKeys {
		return
	}
	var oldestKey string
	var oldestTime time.Time
	for key, state := range l.states {
		if oldestKey == "" || state.lastSeen.Before(oldestTime) {
			oldestKey = key
			oldestTime = state.lastSeen
		}
	}
	delete(l.states, oldestKey)
}

func validate(requestsPerSecond, burst int, limiterOptions []Option) (options, error) {
	if requestsPerSecond <= 0 {
		return options{}, errors.New("requests per second must be positive")
	}
	if burst <= 0 {
		return options{}, errors.New("burst must be positive")
	}
	settings := options{now: time.Now}
	for _, option := range limiterOptions {
		option(&settings)
	}
	return settings, nil
}
