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
	// Remaining is how many requests are still available before the next
	// request would be denied (zero when the request is denied).
	Remaining int
	// ResetAt is the UTC time when another request slot will become available.
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

// entry tracks request timestamps for a single key.
type entry struct {
	timestamps []time.Time
}

// MemoryLimiter is a concurrency-safe, in-memory, sliding-window rate limiter
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
// key using a sliding-window algorithm. A background goroutine evicts stale
// entries once per window.
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

	e := l.pruneEntry(l.entries[key], now)
	if len(e.timestamps) >= l.rate {
		resetAt := e.timestamps[0].Add(l.window)
		l.entries[key] = e
		return Result{
			Allowed:   false,
			Remaining: 0,
			ResetAt:   resetAt,
		}, nil
	}

	e.timestamps = append(e.timestamps, now)
	l.entries[key] = e
	resetAt := e.timestamps[0].Add(l.window)
	return Result{
		Allowed:   true,
		Remaining: l.rate - len(e.timestamps),
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

// evict removes all entries whose tracked timestamps have fully elapsed.
func (l *MemoryLimiter) evict() {
	now := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, e := range l.entries {
		e = l.pruneEntry(e, now)
		if len(e.timestamps) == 0 {
			delete(l.entries, key)
			continue
		}
		l.entries[key] = e
	}
}

func (l *MemoryLimiter) pruneEntry(e entry, now time.Time) entry {
	cutoff := now.Add(-l.window)
	keep := e.timestamps[:0]
	for _, ts := range e.timestamps {
		if ts.After(cutoff) {
			keep = append(keep, ts)
		}
	}
	e.timestamps = keep
	return e
}
