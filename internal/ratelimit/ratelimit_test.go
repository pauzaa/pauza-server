package ratelimit_test

import (
	"sync"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/ratelimit"
)

func TestAllow_UnderLimit(t *testing.T) {
	lim := ratelimit.New(5, time.Minute)
	defer lim.Stop()

	for i := range 5 {
		allowed, remaining, resetAt := lim.Allow("user1")
		if !allowed {
			t.Fatalf("request %d: expected allowed, got denied", i+1)
		}
		wantRemaining := 5 - (i + 1)
		if remaining != wantRemaining {
			t.Errorf("request %d: remaining = %d, want %d", i+1, remaining, wantRemaining)
		}
		if resetAt.IsZero() {
			t.Errorf("request %d: resetAt is zero", i+1)
		}
		if resetAt.Location() != time.UTC {
			t.Errorf("request %d: resetAt not in UTC: %v", i+1, resetAt.Location())
		}
	}
}

func TestAllow_OverLimit(t *testing.T) {
	lim := ratelimit.New(3, time.Minute)
	defer lim.Stop()

	// Exhaust the budget.
	for range 3 {
		lim.Allow("user1")
	}

	// The 4th request must be denied.
	allowed, remaining, resetAt := lim.Allow("user1")
	if allowed {
		t.Fatal("expected denied after exceeding limit, got allowed")
	}
	if remaining != 0 {
		t.Errorf("remaining = %d, want 0", remaining)
	}
	if resetAt.IsZero() {
		t.Error("resetAt is zero on denied request")
	}

	// A 5th request should also be denied.
	allowed, remaining, _ = lim.Allow("user1")
	if allowed {
		t.Fatal("expected continued denial, got allowed")
	}
	if remaining != 0 {
		t.Errorf("remaining = %d after continued denial, want 0", remaining)
	}
}

func TestAllow_WindowReset(t *testing.T) {
	// Use a tiny window so the test runs fast.
	window := 50 * time.Millisecond
	lim := ratelimit.New(2, window)
	defer lim.Stop()

	// Exhaust the budget.
	lim.Allow("k")
	lim.Allow("k")

	allowed, _, _ := lim.Allow("k")
	if allowed {
		t.Fatal("expected denied before window reset")
	}

	// Wait for the window to expire.
	time.Sleep(window + 10*time.Millisecond)

	// Should be allowed again in the new window.
	allowed, remaining, _ := lim.Allow("k")
	if !allowed {
		t.Fatal("expected allowed after window reset, got denied")
	}
	if remaining != 1 {
		t.Errorf("remaining = %d after reset, want 1", remaining)
	}
}

func TestAllow_DifferentKeys(t *testing.T) {
	lim := ratelimit.New(1, time.Minute)
	defer lim.Stop()

	allowed1, _, _ := lim.Allow("alice")
	allowed2, _, _ := lim.Allow("bob")

	if !allowed1 {
		t.Error("alice's first request should be allowed")
	}
	if !allowed2 {
		t.Error("bob's first request should be allowed")
	}

	// Both keys have exhausted their individual budget of 1 request.
	denied1, _, _ := lim.Allow("alice")
	denied2, _, _ := lim.Allow("bob")

	if denied1 {
		t.Error("alice's second request should be denied")
	}
	if denied2 {
		t.Error("bob's second request should be denied")
	}

	// A third distinct key should still be allowed.
	allowed3, _, _ := lim.Allow("charlie")
	if !allowed3 {
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
				allowed, _, _ := lim.Allow("shared")
				mu.Lock()
				if allowed {
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
