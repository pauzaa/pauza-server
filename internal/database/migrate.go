package database

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/golang-migrate/migrate/v4"

	// Register the pgx/v5 database driver for golang-migrate.
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	// Register the file source driver for golang-migrate.
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// RunMigrations applies all pending database migrations from the given
// directory. It rewrites the postgres:// URL scheme to pgx5:// as required
// by golang-migrate's pgx/v5 driver registration.
func RunMigrations(databaseURL string, migrationsPath string) error {
	pgx5URL := strings.Replace(databaseURL, "postgres://", "pgx5://", 1)

	m, err := migrate.New("file://"+migrationsPath, pgx5URL)
	if err != nil {
		return err
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			slog.Warn("failed to close migration source", "err", srcErr)
		}
		if dbErr != nil {
			slog.Warn("failed to close migration database", "err", dbErr)
		}
	}()

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			slog.Info("no new migrations to apply")
			return nil
		}
		return err
	}

	slog.Info("migrations applied successfully")
	return nil
}
