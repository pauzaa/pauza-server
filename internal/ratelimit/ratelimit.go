package ratelimit

import (
	"sync"
	"time"
)

// entry tracks the request count and window start for a single key.
type entry struct {
	count    int
	windowAt time.Time
}

// Limiter is a concurrency-safe, in-memory, fixed-window rate limiter keyed
// by arbitrary strings. It is independent of net/http and suitable for use in
// middleware or service-layer call sites.
//
// Stale entries are evicted by a background goroutine. Call Stop to release
// that goroutine when the Limiter is no longer needed.
type Limiter struct {
	rate   int
	window time.Duration

	mu      sync.Mutex
	entries map[string]entry
	stop    chan struct{}
	stopped chan struct{}

	// now is an injectable clock for testing. Defaults to time.Now.
	now func() time.Time
}

// New creates a Limiter that allows rate requests per window for each key.
// A background goroutine evicts stale entries once per window.
func New(rate int, window time.Duration) *Limiter {
	l := &Limiter{
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
// It returns:
//   - allowed: true when the request is within the rate budget.
//   - remaining: how many requests are still available in the current window
//     (zero when the request is denied).
//   - resetAt: the UTC time when the current window expires and counters reset.
func (l *Limiter) Allow(key string) (allowed bool, remaining int, resetAt time.Time) {
	now := l.now().UTC()

	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.entries[key]
	if !ok || now.Sub(e.windowAt) >= l.window {
		// First request or the window has expired; start a new window.
		e = entry{count: 1, windowAt: now}
		l.entries[key] = e
		return true, l.rate - 1, now.Add(l.window)
	}

	resetAt = e.windowAt.Add(l.window)

	if e.count >= l.rate {
		return false, 0, resetAt
	}

	e.count++
	l.entries[key] = e
	return true, l.rate - e.count, resetAt
}

// Stop shuts down the background eviction goroutine. It is safe to call
// multiple times; only the first call has an effect.
func (l *Limiter) Stop() {
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
func (l *Limiter) evictLoop() {
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
func (l *Limiter) evict() {
	now := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, e := range l.entries {
		if now.Sub(e.windowAt) >= l.window {
			delete(l.entries, key)
		}
	}
}
