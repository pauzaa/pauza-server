//go:build integration

package ratelimit_test

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/IsorilovA/pauza-server/internal/ratelimit"
)

func testRedisClient(t *testing.T) *redis.Client {
	t.Helper()

	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not set; skipping integration test")
	}

	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parsing TEST_REDIS_URL: %v", err)
	}

	client := redis.NewClient(opts)
	t.Cleanup(func() {
		client.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("pinging redis: %v", err)
	}

	return client
}

func testRedisPrefix(t *testing.T) string {
	t.Helper()
	return "test:rl:" + strconv.FormatInt(time.Now().UnixNano(), 10) + ":"
}

func TestRedisLimiter_AllowsThenDenies(t *testing.T) {
	client := testRedisClient(t)
	ctx := context.Background()
	lim := ratelimit.NewRedisLimiter(client, 2, time.Second, ratelimit.WithPrefix(testRedisPrefix(t)))

	res, err := lim.Allow(ctx, "login:127.0.0.1")
	if err != nil {
		t.Fatalf("first allow: %v", err)
	}
	if !res.Allowed || res.Remaining != 1 {
		t.Fatalf("first result = %+v, want allowed with remaining 1", res)
	}

	res, err = lim.Allow(ctx, "login:127.0.0.1")
	if err != nil {
		t.Fatalf("second allow: %v", err)
	}
	if !res.Allowed || res.Remaining != 0 {
		t.Fatalf("second result = %+v, want allowed with remaining 0", res)
	}

	res, err = lim.Allow(ctx, "login:127.0.0.1")
	if err != nil {
		t.Fatalf("third allow: %v", err)
	}
	if res.Allowed || res.Remaining != 0 {
		t.Fatalf("third result = %+v, want denied with remaining 0", res)
	}
}

func TestRedisLimiter_WindowReset(t *testing.T) {
	client := testRedisClient(t)
	ctx := context.Background()
	lim := ratelimit.NewRedisLimiter(client, 1, time.Second, ratelimit.WithPrefix(testRedisPrefix(t)))

	res, err := lim.Allow(ctx, "register:127.0.0.1")
	if err != nil {
		t.Fatalf("first allow: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("first result = %+v, want allowed", res)
	}

	res, err = lim.Allow(ctx, "register:127.0.0.1")
	if err != nil {
		t.Fatalf("second allow: %v", err)
	}
	if res.Allowed {
		t.Fatalf("second result = %+v, want denied", res)
	}

	time.Sleep(1100 * time.Millisecond)

	res, err = lim.Allow(ctx, "register:127.0.0.1")
	if err != nil {
		t.Fatalf("post-reset allow: %v", err)
	}
	if !res.Allowed {
		t.Fatalf("post-reset result = %+v, want allowed", res)
	}
}

func TestRedisLimiter_KeysAreIndependent(t *testing.T) {
	client := testRedisClient(t)
	ctx := context.Background()
	lim := ratelimit.NewRedisLimiter(client, 1, time.Second, ratelimit.WithPrefix(testRedisPrefix(t)))

	resA, err := lim.Allow(ctx, "forgot:127.0.0.1")
	if err != nil {
		t.Fatalf("allow key A: %v", err)
	}
	if !resA.Allowed {
		t.Fatalf("key A first result = %+v, want allowed", resA)
	}

	resA2, err := lim.Allow(ctx, "forgot:127.0.0.1")
	if err != nil {
		t.Fatalf("deny key A: %v", err)
	}
	if resA2.Allowed {
		t.Fatalf("key A second result = %+v, want denied", resA2)
	}

	resB, err := lim.Allow(ctx, "forgot:127.0.0.2")
	if err != nil {
		t.Fatalf("allow key B: %v", err)
	}
	if !resB.Allowed {
		t.Fatalf("key B first result = %+v, want allowed", resB)
	}
}

var _ ratelimit.Limiter = (*ratelimit.RedisLimiter)(nil)
