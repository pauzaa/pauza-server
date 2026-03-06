package database

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	// Register the pgx/v5 database driver for golang-migrate.
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
)

// migrateDSN rewrites a postgres:// or postgresql:// URL to the pgx5://
// scheme required by golang-migrate's pgx/v5 driver. It parses the URL
// so only the scheme is touched, avoiding fragile string replacement.
func migrateDSN(databaseURL string) (string, error) {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", fmt.Errorf("parsing database URL: %w", err)
	}
	switch u.Scheme {
	case "postgres", "postgresql":
		u.Scheme = "pgx5"
	default:
		return "", fmt.Errorf("unsupported database URL scheme: %q", u.Scheme)
	}
	return u.String(), nil
}

// RunMigrations applies all pending database migrations from the given
// embedded filesystem. It rewrites the postgres:// or postgresql:// URL
// scheme to pgx5:// as required by golang-migrate's pgx/v5 driver.
func RunMigrations(databaseURL string, migrationsFS fs.FS) error {
	pgx5URL, err := migrateDSN(databaseURL)
	if err != nil {
		return err
	}

	src, err := iofs.New(migrationsFS, ".")
	if err != nil {
		return fmt.Errorf("opening migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, pgx5URL)
	if err != nil {
		return fmt.Errorf("creating migrate instance: %w", err)
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
		return fmt.Errorf("applying migrations: %w", err)
	}

	slog.Info("migrations applied successfully")
	return nil
}
