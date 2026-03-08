package ratelimit_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/ratelimit"
)

// errLimiter is a stub Limiter that always returns an error from Allow.
type errLimiter struct {
	err error
}

func (e *errLimiter) Allow(_ context.Context, _ string) (ratelimit.Result, error) {
	return ratelimit.Result{}, e.err
}
func (e *errLimiter) Stop() {}

// passLimiter is a stub Limiter that always allows requests.
type passLimiter struct {
	result ratelimit.Result
}

func (p *passLimiter) Allow(_ context.Context, _ string) (ratelimit.Result, error) {
	return p.result, nil
}
func (p *passLimiter) Stop() {}

// denyLimiter is a stub Limiter that always denies requests.
type denyLimiter struct {
	result ratelimit.Result
}

func (d *denyLimiter) Allow(_ context.Context, _ string) (ratelimit.Result, error) {
	return d.result, nil
}
func (d *denyLimiter) Stop() {}

// stopRecorder records whether Stop was called.
type stopRecorder struct {
	stopped bool
}

func (s *stopRecorder) Allow(_ context.Context, _ string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}
func (s *stopRecorder) Stop() { s.stopped = true }

func TestFailOpen_BackendError_AllowsAndLogs(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	inner := &errLimiter{err: errors.New("redis unreachable")}
	lim := ratelimit.NewFailOpen(inner, logger)

	res, err := lim.Allow(context.Background(), "1.2.3.4")
	if err != nil {
		t.Fatalf("expected nil error from fail-open limiter, got: %v", err)
	}
	if !res.Allowed {
		t.Error("expected request to be allowed on backend error")
	}
	if res.ResetAt.IsZero() {
		t.Error("expected non-zero ResetAt")
	}

	// Verify warning was logged.
	logged := buf.String()
	if !strings.Contains(logged, "rate limiter backend error") {
		t.Errorf("expected warning log about backend error, got: %s", logged)
	}
	if !strings.Contains(logged, "redis unreachable") {
		t.Errorf("expected log to contain error message, got: %s", logged)
	}
}

func TestFailOpen_BackendSuccess_PassesThrough(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	expected := ratelimit.Result{
		Allowed:   true,
		Remaining: 4,
		ResetAt:   time.Now().UTC().Add(time.Minute),
	}
	inner := &passLimiter{result: expected}
	lim := ratelimit.NewFailOpen(inner, logger)

	res, err := lim.Allow(context.Background(), "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Allowed {
		t.Error("expected allowed=true")
	}
	if res.Remaining != expected.Remaining {
		t.Errorf("remaining = %d, want %d", res.Remaining, expected.Remaining)
	}
}

func TestFailOpen_BackendDenied_PassesThrough(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	expected := ratelimit.Result{
		Allowed:   false,
		Remaining: 0,
		ResetAt:   time.Now().UTC().Add(30 * time.Second),
	}
	inner := &denyLimiter{result: expected}
	lim := ratelimit.NewFailOpen(inner, logger)

	res, err := lim.Allow(context.Background(), "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Allowed {
		t.Error("expected allowed=false when backend explicitly denies")
	}
	if res.Remaining != 0 {
		t.Errorf("remaining = %d, want 0", res.Remaining)
	}
}

func TestFailOpen_StopDelegatesToInner(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	inner := &stopRecorder{}
	lim := ratelimit.NewFailOpen(inner, logger)

	lim.Stop()
	if !inner.stopped {
		t.Error("expected Stop to be delegated to inner limiter")
	}
}

// Compile-time check: *FailOpenLimiter implements Limiter.
var _ ratelimit.Limiter = (*ratelimit.FailOpenLimiter)(nil)
