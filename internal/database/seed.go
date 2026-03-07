package database

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/auth"
)

// SeedAdmin creates an initial admin account when admin_credentials is empty.
// It is idempotent and safe across concurrent startups: a single atomic
// INSERT … WHERE NOT EXISTS is used so only the first writer succeeds.
// A preliminary existence check avoids the cost of bcrypt hashing on
// every no-op startup.
func SeedAdmin(ctx context.Context, pool *pgxpool.Pool, username string, password string) error {
	if strings.TrimSpace(username) == "" {
		return fmt.Errorf("admin seed username must not be empty")
	}
	if password == "" {
		return fmt.Errorf("admin seed password must not be empty")
	}

	var exists bool
	err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM admin_credentials)`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("checking admin_credentials: %w", err)
	}
	if exists {
		slog.InfoContext(ctx, "admin_credentials table is not empty, skipping seed")
		return nil
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hashing admin password: %w", err)
	}

	tag, err := pool.Exec(ctx,
		`INSERT INTO admin_credentials (username, password_hash)
		 SELECT $1, $2
		 WHERE NOT EXISTS (SELECT 1 FROM admin_credentials)`,
		username, hash)
	if err != nil {
		return fmt.Errorf("inserting admin credentials: %w", err)
	}

	if tag.RowsAffected() == 0 {
		// Lost the race: another instance inserted between our check and INSERT.
		slog.InfoContext(ctx, "admin_credentials table is not empty, skipping seed")
	} else {
		slog.InfoContext(ctx, "admin account seeded successfully")
	}

	return nil
}
