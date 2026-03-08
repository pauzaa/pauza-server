package ratelimit

import (
	"context"
	"log/slog"
	"time"
)

// FailOpenLimiter wraps another Limiter and falls back to allowing requests
// when the underlying implementation returns an error (e.g. Redis is
// unreachable). Every backend failure is logged so operators can detect
// degradation.
type FailOpenLimiter struct {
	inner  Limiter
	logger *slog.Logger
}

// NewFailOpen wraps inner so that backend errors are logged and requests are
// allowed (fail-open). This is the recommended wrapper for production Redis
// limiters: it keeps the API available under transient backend failures at
// the cost of temporarily disabling rate limiting.
func NewFailOpen(inner Limiter, logger *slog.Logger) *FailOpenLimiter {
	return &FailOpenLimiter{inner: inner, logger: logger}
}

// Allow delegates to the inner limiter. If the inner limiter returns an error,
// the request is allowed and a warning is logged.
func (f *FailOpenLimiter) Allow(ctx context.Context, key string) (Result, error) {
	res, err := f.inner.Allow(ctx, key)
	if err != nil {
		f.logger.Warn("rate limiter backend error, failing open",
			"err", err,
			"key", key,
		)
		return Result{
			Allowed:   true,
			Remaining: 0,
			ResetAt:   time.Now().UTC().Add(time.Minute),
		}, nil
	}
	return res, nil
}

// Stop delegates to the inner limiter's Stop method.
func (f *FailOpenLimiter) Stop() {
	f.inner.Stop()
}
