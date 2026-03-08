package database

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestStartCleanup_StartStop(t *testing.T) {
	// Verify that StartCleanup starts and stops cleanly without a real DB.
	// The goroutine should launch and the returned stop function should
	// return promptly without hanging or panicking.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := CleanupConfig{
		Interval:           1 * time.Hour,
		OTPRetention:       24 * time.Hour,
		RefreshTokenMaxAge: 7 * 24 * time.Hour,
	}

	ctx := context.Background()

	// Pass a nil pool — runCleanup detects the nil pool, logs a warning,
	// and skips the DB calls. The stop function cancels the goroutine.
	stop := StartCleanup(ctx, nil, logger, cfg)

	// stop must return without hanging. No sleep required because the
	// goroutine performs the initial pass synchronously (nil-pool fast
	// path) and then blocks on the ticker select. Cancellation via stop
	// unblocks the select immediately.
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("stop function did not return within 2 seconds")
	}
}

func TestStartCleanup_CancelledContext(t *testing.T) {
	// When the parent context is already cancelled, StartCleanup should
	// still return a valid stop function that completes promptly, and
	// the goroutine should exit without attempting any cleanup work.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := CleanupConfig{
		Interval:           1 * time.Hour,
		OTPRetention:       24 * time.Hour,
		RefreshTokenMaxAge: 7 * 24 * time.Hour,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	stop := StartCleanup(ctx, nil, logger, cfg)

	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("stop function did not return within 2 seconds")
	}
}

func TestStartCleanup_StopIsIdempotent(t *testing.T) {
	// Calling stop multiple times must not panic.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := CleanupConfig{
		Interval:           1 * time.Hour,
		OTPRetention:       24 * time.Hour,
		RefreshTokenMaxAge: 7 * 24 * time.Hour,
	}

	stop := StartCleanup(context.Background(), nil, logger, cfg)
	stop()
	stop() // second call must not panic or hang
}
