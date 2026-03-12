package server

import (
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/ratelimit"
)

type appLimiters struct {
	auth      ratelimit.Limiter
	verifyOTP ratelimit.Limiter
	general   ratelimit.Limiter
	sync      ratelimit.Limiter
	webhook   ratelimit.Limiter
	admin     ratelimit.Limiter
}

func buildLimiters(cfg *config.Config, logger *slog.Logger, redisClient *redis.Client) (appLimiters, func()) {
	newLimiter := func(prefix string, rate int, window time.Duration) ratelimit.Limiter {
		inner := ratelimit.NewRedisLimiter(redisClient, rate, window, ratelimit.WithPrefix("rl:"+prefix+":"))
		return ratelimit.NewFailOpen(inner, logger)
	}

	limiters := appLimiters{
		auth:      newLimiter("auth", cfg.AuthRateLimit, cfg.AuthRateWindow),
		verifyOTP: newLimiter("verify-otp", cfg.VerifyOTPRateLimit, cfg.VerifyOTPRateWindow),
		general:   newLimiter("general-api", cfg.GeneralAPIRateLimit, cfg.GeneralAPIRateWindow),
		sync:      newLimiter("sync", cfg.SyncRateLimit, cfg.SyncRateWindow),
		webhook:   newLimiter("webhook", cfg.WebhookRateLimit, cfg.WebhookRateWindow),
		admin:     newLimiter("admin", cfg.AdminRateLimit, cfg.AdminRateWindow),
	}

	cleanup := func() {
		limiters.auth.Stop()
		limiters.verifyOTP.Stop()
		limiters.general.Stop()
		limiters.sync.Stop()
		limiters.webhook.Stop()
		limiters.admin.Stop()
	}

	return limiters, cleanup
}
