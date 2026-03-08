package main

import (
	"log/slog"
	"os"

	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/database"
	"github.com/IsorilovA/pauza-server/migrations"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.LoadMigrate()
	if err != nil {
		logger.Error("failed to load configuration", "err", err)
		os.Exit(1)
	}

	if err := database.RunMigrations(logger, cfg.DatabaseURL, migrations.FS); err != nil {
		logger.Error("failed to run migrations", "err", err)
		os.Exit(1)
	}

	logger.Info("migrations completed successfully")
}
