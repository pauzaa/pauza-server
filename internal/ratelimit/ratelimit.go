package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Result holds the outcome of a rate-limit check.
type Result struct {
	// Allowed is true when the request is within the rate budget.
	Allowed bool
	// Remaining is how many requests are still available in the current
	// window (zero when the request is denied).
	Remaining int
	// ResetAt is the UTC time when the current window expires and counters
	// reset.
	ResetAt time.Time
}

// Limiter is the interface that all rate-limiter backends must implement.
// Implementations must be safe for concurrent use.
type Limiter interface {
	// Allow records a request for key and reports whether the request is
	// allowed. Implementations that depend on external services (e.g. Redis)
	// use the context for cancellation and timeouts. An error indicates a
	// backend failure; callers should decide how to handle it (e.g. fail
	// open or closed).
	Allow(ctx context.Context, key string) (Result, error)

	// Stop releases background resources (goroutines, connections) held by
	// the limiter. It is safe to call multiple times.
	Stop()
}

// entry tracks the request count and window start for a single key.
type entry struct {
	count    int
	windowAt time.Time
}

// MemoryLimiter is a concurrency-safe, in-memory, fixed-window rate limiter
// keyed by arbitrary strings. It is independent of net/http and suitable for
// use in middleware or service-layer call sites.
//
// Stale entries are evicted by a background goroutine. Call Stop to release
// that goroutine when the MemoryLimiter is no longer needed.
type MemoryLimiter struct {
	rate   int
	window time.Duration

	mu      sync.Mutex
	entries map[string]entry
	stop    chan struct{}
	stopped chan struct{}

	// now is an injectable clock for testing. Defaults to time.Now.
	now func() time.Time
}

// New creates a MemoryLimiter that allows rate requests per window for each
// key. A background goroutine evicts stale entries once per window.
func New(rate int, window time.Duration) *MemoryLimiter {
	l := &MemoryLimiter{
		rate:    rate,
		window:  window,
		entries: make(map[string]entry),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
		now:     time.Now,
	}
	go l.evictLoop()
	return l
}

// Allow records a request for key and reports whether the request is allowed.
// The context is accepted for interface compliance but is unused by the
// in-memory implementation.
func (l *MemoryLimiter) Allow(_ context.Context, key string) (Result, error) {
	now := l.now().UTC()

	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.entries[key]
	if !ok || now.Sub(e.windowAt) >= l.window {
		// First request or the window has expired; start a new window.
		e = entry{count: 1, windowAt: now}
		l.entries[key] = e
		return Result{
			Allowed:   true,
			Remaining: l.rate - 1,
			ResetAt:   now.Add(l.window),
		}, nil
	}

	resetAt := e.windowAt.Add(l.window)

	if e.count >= l.rate {
		return Result{
			Allowed:   false,
			Remaining: 0,
			ResetAt:   resetAt,
		}, nil
	}

	e.count++
	l.entries[key] = e
	return Result{
		Allowed:   true,
		Remaining: l.rate - e.count,
		ResetAt:   resetAt,
	}, nil
}

// Stop shuts down the background eviction goroutine. It is safe to call
// multiple times; only the first call has an effect.
func (l *MemoryLimiter) Stop() {
	l.mu.Lock()
	select {
	case <-l.stop:
		// Already stopped.
		l.mu.Unlock()
		return
	default:
		close(l.stop)
	}
	l.mu.Unlock()
	<-l.stopped
}

// evictLoop removes entries whose window has expired. It runs once per window
// duration and exits when Stop is called.
func (l *MemoryLimiter) evictLoop() {
	defer close(l.stopped)
	ticker := time.NewTicker(l.window)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			l.evict()
		}
	}
}

// evict removes all entries whose window has fully elapsed.
func (l *MemoryLimiter) evict() {
	now := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, e := range l.entries {
		if now.Sub(e.windowAt) >= l.window {
			delete(l.entries, key)
		}
	}
}
