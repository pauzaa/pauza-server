package database

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// SeedAdmin creates an initial admin account in admin_credentials if none exists.
// It is idempotent: subsequent calls after the first seed are no-ops.
func SeedAdmin(ctx context.Context, pool *pgxpool.Pool, username string, password string) error {
	var count int
	err := pool.QueryRow(ctx, "SELECT count(*) FROM admin_credentials").Scan(&count)
	if err != nil {
		return fmt.Errorf("querying admin_credentials count: %w", err)
	}

	if count > 0 {
		slog.Info("admin account already exists, skipping seed")
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return fmt.Errorf("hashing admin password: %w", err)
	}

	_, err = pool.Exec(ctx, "INSERT INTO admin_credentials (username, password_hash) VALUES ($1, $2)", username, hash)
	if err != nil {
		return fmt.Errorf("inserting admin credentials: %w", err)
	}

	slog.Info("admin account seeded successfully")
	return nil
}
