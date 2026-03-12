package ratelimit

import (
	"context"
	"time"
)

// Result holds the outcome of a rate-limit check.
type Result struct {
	Allowed   bool
	Remaining int
	ResetAt   time.Time
}

// Limiter is the interface implemented by Redis-backed runtime limiters and
// test fakes used by middleware and fail-open wrappers.
type Limiter interface {
	Allow(ctx context.Context, key string) (Result, error)
	Stop()
}
