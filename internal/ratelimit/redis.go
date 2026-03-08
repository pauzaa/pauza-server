package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisLimiter is a fixed-window rate limiter backed by Redis. It uses a
// Lua script executed atomically so that concurrent requests from multiple
// server instances share a single counter per key.
type RedisLimiter struct {
	client *redis.Client
	rate   int
	window time.Duration
	prefix string
}

// redisScript is a Lua script that atomically increments a counter for the
// given key and returns the current count and the TTL remaining (seconds).
// If the key does not exist, it is created with an expiry equal to the
// window duration. The script returns {count, ttl_seconds}.
var redisScript = redis.NewScript(`
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])

local current = redis.call("INCR", key)
if current == 1 then
    redis.call("EXPIRE", key, window)
end
local ttl = redis.call("TTL", key)
if ttl < 0 then
    redis.call("EXPIRE", key, window)
    ttl = window
end
return {current, ttl}
`)

// RedisOption configures optional parameters for NewRedisLimiter.
type RedisOption func(*RedisLimiter)

// WithPrefix sets a key prefix for the Redis keys used by the limiter.
// This is useful to namespace keys when multiple limiters share the same
// Redis instance.
func WithPrefix(prefix string) RedisOption {
	return func(rl *RedisLimiter) {
		rl.prefix = prefix
	}
}

// NewRedisLimiter creates a RedisLimiter that allows rate requests per window
// for each key. The caller owns the *redis.Client lifetime; Stop is a no-op
// because the client is shared.
func NewRedisLimiter(client *redis.Client, rate int, window time.Duration, opts ...RedisOption) *RedisLimiter {
	rl := &RedisLimiter{
		client: client,
		rate:   rate,
		window: window,
		prefix: "rl:",
	}
	for _, o := range opts {
		o(rl)
	}
	return rl
}

// Allow records a request for key and reports whether the request is allowed.
// It executes a Lua script atomically in Redis. If Redis is unreachable the
// error is returned so the caller can decide the fail-open/closed policy.
func (rl *RedisLimiter) Allow(ctx context.Context, key string) (Result, error) {
	fullKey := rl.prefix + key
	windowSec := int(rl.window.Seconds())
	if windowSec < 1 {
		windowSec = 1
	}

	vals, err := redisScript.Run(ctx, rl.client, []string{fullKey}, rl.rate, windowSec).Int64Slice()
	if err != nil {
		return Result{}, fmt.Errorf("redis rate limit script: %w", err)
	}

	count := int(vals[0])
	ttl := time.Duration(vals[1]) * time.Second
	resetAt := time.Now().UTC().Add(ttl)

	if count > rl.rate {
		return Result{
			Allowed:   false,
			Remaining: 0,
			ResetAt:   resetAt,
		}, nil
	}

	return Result{
		Allowed:   true,
		Remaining: rl.rate - count,
		ResetAt:   resetAt,
	}, nil
}

// Stop is a no-op for RedisLimiter. The caller owns the Redis client
// lifecycle.
func (rl *RedisLimiter) Stop() {}
