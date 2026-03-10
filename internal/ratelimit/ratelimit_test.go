package ratelimit_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/ratelimit"
)

func TestAllow_UnderLimit(t *testing.T) {
	lim := ratelimit.New(5, time.Minute)
	defer lim.Stop()

	ctx := context.Background()

	for i := range 5 {
		res, err := lim.Allow(ctx, "user1")
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i+1, err)
		}
		if !res.Allowed {
			t.Fatalf("request %d: expected allowed, got denied", i+1)
		}
		wantRemaining := 5 - (i + 1)
		if res.Remaining != wantRemaining {
			t.Errorf("request %d: remaining = %d, want %d", i+1, res.Remaining, wantRemaining)
		}
		if res.ResetAt.IsZero() {
			t.Errorf("request %d: resetAt is zero", i+1)
		}
		if res.ResetAt.Location() != time.UTC {
			t.Errorf("request %d: resetAt not in UTC: %v", i+1, res.ResetAt.Location())
		}
	}
}

func TestAllow_OverLimit(t *testing.T) {
	lim := ratelimit.New(3, time.Minute)
	defer lim.Stop()

	ctx := context.Background()

	// Exhaust the budget.
	for range 3 {
		lim.Allow(ctx, "user1")
	}

	// The 4th request must be denied.
	res, err := lim.Allow(ctx, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Allowed {
		t.Fatal("expected denied after exceeding limit, got allowed")
	}
	if res.Remaining != 0 {
		t.Errorf("remaining = %d, want 0", res.Remaining)
	}
	if res.ResetAt.IsZero() {
		t.Error("resetAt is zero on denied request")
	}

	// A 5th request should also be denied.
	res, err = lim.Allow(ctx, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Allowed {
		t.Fatal("expected continued denial, got allowed")
	}
	if res.Remaining != 0 {
		t.Errorf("remaining = %d after continued denial, want 0", res.Remaining)
	}
}

func TestAllow_WindowReset(t *testing.T) {
	// Use a tiny window so the test runs fast.
	window := 50 * time.Millisecond
	lim := ratelimit.New(2, window)
	defer lim.Stop()

	ctx := context.Background()

	// Exhaust the budget.
	lim.Allow(ctx, "k")
	lim.Allow(ctx, "k")

	res, err := lim.Allow(ctx, "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Allowed {
		t.Fatal("expected denied before window reset")
	}

	// Wait for the window to expire.
	time.Sleep(window + 10*time.Millisecond)

	// Should be allowed again in the new window.
	res, err = lim.Allow(ctx, "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Fatal("expected allowed after window reset, got denied")
	}
	if res.Remaining != 1 {
		t.Errorf("remaining = %d after reset, want 1", res.Remaining)
	}
}

func TestAllow_SlidingWindowExpiresOldestRequestFirst(t *testing.T) {
	window := 80 * time.Millisecond
	lim := ratelimit.New(2, window)
	defer lim.Stop()

	ctx := context.Background()

	res, err := lim.Allow(ctx, "k")
	if err != nil {
		t.Fatalf("first allow: %v", err)
	}
	if !res.Allowed || res.Remaining != 1 {
		t.Fatalf("first result = %+v, want allowed with remaining 1", res)
	}

	time.Sleep(50 * time.Millisecond)

	res, err = lim.Allow(ctx, "k")
	if err != nil {
		t.Fatalf("second allow: %v", err)
	}
	if !res.Allowed || res.Remaining != 0 {
		t.Fatalf("second result = %+v, want allowed with remaining 0", res)
	}

	res, err = lim.Allow(ctx, "k")
	if err != nil {
		t.Fatalf("third allow: %v", err)
	}
	if res.Allowed {
		t.Fatalf("third result = %+v, want denied", res)
	}

	time.Sleep(35 * time.Millisecond)

	res, err = lim.Allow(ctx, "k")
	if err != nil {
		t.Fatalf("post-oldest-expiry allow: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("post-oldest-expiry result = %+v, want allowed", res)
	}
	if res.Remaining != 0 {
		t.Fatalf("post-oldest-expiry remaining = %d, want 0", res.Remaining)
	}
}

func TestAllow_DifferentKeys(t *testing.T) {
	lim := ratelimit.New(1, time.Minute)
	defer lim.Stop()

	ctx := context.Background()

	res1, _ := lim.Allow(ctx, "alice")
	res2, _ := lim.Allow(ctx, "bob")

	if !res1.Allowed {
		t.Error("alice's first request should be allowed")
	}
	if !res2.Allowed {
		t.Error("bob's first request should be allowed")
	}

	// Both keys have exhausted their individual budget of 1 request.
	denied1, _ := lim.Allow(ctx, "alice")
	denied2, _ := lim.Allow(ctx, "bob")

	if denied1.Allowed {
		t.Error("alice's second request should be denied")
	}
	if denied2.Allowed {
		t.Error("bob's second request should be denied")
	}

	// A third distinct key should still be allowed.
	res3, _ := lim.Allow(ctx, "charlie")
	if !res3.Allowed {
		t.Error("charlie's first request should be allowed")
	}
}

func TestAllow_ConcurrentAccess(t *testing.T) {
	const (
		rate       = 100
		goroutines = 50
		perG       = 10 // each goroutine makes 10 requests
	)

	lim := ratelimit.New(rate, time.Minute)
	defer lim.Stop()

	ctx := context.Background()

	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		totalAllow int
		totalDeny  int
	)

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perG {
				res, _ := lim.Allow(ctx, "shared")
				mu.Lock()
				if res.Allowed {
					totalAllow++
				} else {
					totalDeny++
				}
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	totalReqs := goroutines * perG // 500
	if totalAllow+totalDeny != totalReqs {
		t.Fatalf("total responses = %d, want %d", totalAllow+totalDeny, totalReqs)
	}
	if totalAllow != rate {
		t.Errorf("allowed = %d, want exactly %d", totalAllow, rate)
	}
	if totalDeny != totalReqs-rate {
		t.Errorf("denied = %d, want %d", totalDeny, totalReqs-rate)
	}
}

func TestStop_Idempotent(t *testing.T) {
	lim := ratelimit.New(5, time.Minute)
	// Multiple calls to Stop should not panic.
	lim.Stop()
	lim.Stop()
}

// Compile-time check: *MemoryLimiter implements Limiter.
var _ ratelimit.Limiter = (*ratelimit.MemoryLimiter)(nil)
