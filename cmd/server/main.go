package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/database"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/push"
	"github.com/IsorilovA/pauza-server/internal/repository"
	"github.com/IsorilovA/pauza-server/internal/server"

	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Load configuration from environment variables
	cfg, err := config.Load()
	if err != nil {
		// Use the package-level slog default here because the project
		// logger has not been configured yet (it depends on cfg.LogLevel).
		slog.Error("failed to load configuration", "err", err)
		os.Exit(1)
	}

	// 2. Set up structured logger
	logger := setupLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	// 3. Connect to database
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer dbCancel()

	pool, err := database.Connect(dbCtx, logger, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}

	// 4. Create SMTP mailer
	emailSender := mail.NewSMTPSender(mail.SMTPConfig{
		Host:             cfg.SMTPHost,
		Port:             cfg.SMTPPort,
		Username:         cfg.SMTPUsername,
		Password:         cfg.SMTPPassword,
		From:             cfg.SMTPFrom,
		OTPExpiryMinutes: int(auth.OTPExpiry.Minutes()),
		Timeout:          cfg.SMTPTimeout,
		TLSPolicy:        cfg.SMTPTLSPolicy,
		Logger:           logger,
	})

	// 5. Create Redis client for shared rate limiting.
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Error("failed to parse REDIS_URL", "err", err)
		os.Exit(1)
	}
	redisClient := redis.NewClient(opts)

	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	if err := redisClient.Ping(rctx).Err(); err != nil {
		logger.Error("failed to connect to Redis for shared rate limiting", "err", err)
		_ = redisClient.Close()
		os.Exit(1)
	}
	logger.Info("redis connected for shared rate limiting")

	pushCtx, pushCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer pushCancel()

	var pushSender push.Sender
	if strings.TrimSpace(cfg.FirebaseServiceAccountJSON) == "" {
		logger.Info("FIREBASE_SERVICE_ACCOUNT_JSON is not set; push notifications disabled")
		pushSender = push.NewNoopSender(logger)
	} else {
		pushSender, err = push.NewFirebaseSender(
			pushCtx,
			pool,
			repository.NewSocialRepository(),
			logger,
			cfg.FirebaseServiceAccountJSON,
		)
		if err != nil {
			logger.Error("failed to initialize firebase push sender", "err", err)
			_ = redisClient.Close()
			pool.Close()
			os.Exit(1)
		}
	}

	// 6. Log effective trusted-proxy configuration.
	if nets := cfg.ParseTrustedProxies(); len(nets) > 0 {
		cidrs := make([]string, len(nets))
		for i, n := range nets {
			cidrs[i] = n.String()
		}
		logger.Info("trusted proxies configured", "networks", cidrs)
	} else {
		logger.Info("no trusted proxies configured; X-Forwarded-For headers will be ignored")
	}

	// 7. Create HTTP server
	srv, cleanup, err := server.New(context.Background(), cfg, logger, pool, emailSender, pushSender, redisClient)
	if err != nil {
		logger.Error("failed to create HTTP server", "err", err)
		_ = redisClient.Close()
		pool.Close()
		os.Exit(1)
	}

	// 8. Start background cleanup job for stale auth data.
	// The process context is cancelled during shutdown so the cleanup
	// goroutine observes cancellation promptly rather than relying
	// solely on the stop function.
	processCtx, processCancel := context.WithCancel(context.Background())
	defer processCancel()

	stopCleanup := database.StartCleanup(processCtx, pool, logger, database.CleanupConfig{
		Interval:           cfg.CleanupInterval,
		OTPRetention:       cfg.OTPRetentionPeriod,
		RefreshTokenMaxAge: cfg.RefreshTokenRevokedRetention,
	})

	// 9. Start HTTP server in a goroutine; report fatal errors via channel
	listenErr := make(chan error, 1)
	go func() {
		logger.Info("server starting", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			listenErr <- err
		}
	}()

	// 10. Wait for interrupt signal (SIGINT or SIGTERM) or listen error
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	exitCode := 0

	select {
	case sig := <-quit:
		logger.Info("received shutdown signal", "signal", sig.String())
	case err := <-listenErr:
		logger.Error("server listen error", "err", err)
		exitCode = 1
	}

	// 11. Graceful shutdown with 10-second timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server forced shutdown", "err", err)
		os.Exit(1)
	}

	// Cancel the process context so the cleanup goroutine observes
	// shutdown promptly, then wait for it to drain.
	processCancel()
	stopCleanup()
	cleanup()
	if redisClient != nil {
		redisClient.Close()
	}
	pool.Close()
	logger.Info("server stopped")

	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

// setupLogger creates a slog.Logger with JSON output at the specified level.
func setupLogger(level string) *slog.Logger {
	var logLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})
	return slog.New(handler)
}
