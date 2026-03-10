package ratelimit

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisLimiter is a sliding-window rate limiter backed by Redis. It uses a
// Lua script executed atomically so that concurrent requests from multiple
// server instances share a single request log per key.
type RedisLimiter struct {
	client *redis.Client
	rate   int
	window time.Duration
	prefix string
	seq    atomic.Uint64
}

// redisScript is a Lua script that atomically applies a sliding-window check
// for the given key and returns {allowed, remaining, reset_at_ms}. Request
// timestamps are stored in a sorted set so the budget is shared across all
// server instances.
var redisScript = redis.NewScript(`
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local now_ms = tonumber(ARGV[3])
local member = ARGV[4]

local cutoff = now_ms - window_ms
redis.call("ZREMRANGEBYSCORE", key, "-inf", cutoff)

local current = redis.call("ZCARD", key)
if current >= limit then
    local oldest = redis.call("ZRANGE", key, 0, 0, "WITHSCORES")
    local reset_at_ms = now_ms + window_ms
    if oldest[2] then
        reset_at_ms = tonumber(oldest[2]) + window_ms
    end
    local ttl_ms = reset_at_ms - now_ms
    if ttl_ms < 1 then
        ttl_ms = 1
    end
    redis.call("PEXPIRE", key, ttl_ms)
    return {0, 0, reset_at_ms}
end

redis.call("ZADD", key, now_ms, member)
current = current + 1

local oldest = redis.call("ZRANGE", key, 0, 0, "WITHSCORES")
local reset_at_ms = now_ms + window_ms
if oldest[2] then
    reset_at_ms = tonumber(oldest[2]) + window_ms
end

local ttl_ms = reset_at_ms - now_ms
if ttl_ms < 1 then
    ttl_ms = 1
end
redis.call("PEXPIRE", key, ttl_ms)

return {1, limit - current, reset_at_ms}
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
// for each key using a sliding-window algorithm. The caller owns the
// *redis.Client lifetime; Stop is a no-op because the client is shared.
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
	windowMS := rl.window.Milliseconds()
	if windowMS < 1 {
		windowMS = 1
	}
	now := time.Now().UTC()
	member := fmt.Sprintf("%d-%d", now.UnixNano(), rl.seq.Add(1))

	vals, err := redisScript.Run(ctx, rl.client, []string{fullKey}, rl.rate, windowMS, now.UnixMilli(), member).Int64Slice()
	if err != nil {
		return Result{}, fmt.Errorf("redis rate limit script: %w", err)
	}

	allowed := vals[0] == 1
	remaining := int(vals[1])
	resetAt := time.UnixMilli(vals[2]).UTC()

	if !allowed {
		return Result{
			Allowed:   false,
			Remaining: 0,
			ResetAt:   resetAt,
		}, nil
	}

	return Result{
		Allowed:   true,
		Remaining: remaining,
		ResetAt:   resetAt,
	}, nil
}

// Stop is a no-op for RedisLimiter. The caller owns the Redis client
// lifecycle.
func (rl *RedisLimiter) Stop() {}
