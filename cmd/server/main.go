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

	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/database"
	"github.com/IsorilovA/pauza-server/internal/server"
)

func main() {
	// 1. Load configuration from environment variables
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	// 2. Set up structured logger
	logger := setupLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	// 3. Connect to database
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer startupCancel()

	pool, err := database.Connect(startupCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}

	// 4. Run migrations
	if err := database.RunMigrations(cfg.DatabaseURL, "migrations"); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	// 5. Seed admin
	if err := database.SeedAdmin(startupCtx, pool, cfg.AdminSeedUsername, cfg.AdminSeedPassword); err != nil {
		logger.Error("failed to seed admin", "error", err)
		os.Exit(1)
	}

	// 6. Create HTTP server
	srv := server.New(cfg, logger, pool)

	// 7. Start server in a goroutine; report fatal errors via channel
	listenErr := make(chan error, 1)
	go func() {
		logger.Info("server starting", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			listenErr <- err
		}
	}()

	// 8. Wait for interrupt signal (SIGINT or SIGTERM) or listen error
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.Info("received shutdown signal", "signal", sig.String())
	case err := <-listenErr:
		logger.Error("server listen error", "error", err)
	}

	// 9. Graceful shutdown with 10-second timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server forced shutdown", "error", err)
		os.Exit(1)
	}

	pool.Close()
	logger.Info("server stopped")
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
