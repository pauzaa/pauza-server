package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/database"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load configuration", "err", err)
		os.Exit(1)
	}

	if cfg.AdminSeedUsername == "" || cfg.AdminSeedPassword == "" {
		logger.Error("ADMIN_SEED_USERNAME and ADMIN_SEED_PASSWORD must be set")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := database.Connect(ctx, logger, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := database.SeedAdmin(ctx, logger, pool, cfg.AdminSeedUsername, cfg.AdminSeedPassword); err != nil {
		logger.Error("failed to seed admin", "err", err)
		os.Exit(1)
	}

	logger.Info("admin seeded successfully")
}
