package database

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// SeedAdmin creates an initial admin account when admin_credentials is empty.
// It is idempotent and safe across concurrent startups: a single atomic
// INSERT … WHERE NOT EXISTS is used so only the first writer succeeds.
func SeedAdmin(ctx context.Context, pool *pgxpool.Pool, username string, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
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
		slog.Info("admin_credentials table is not empty, skipping seed")
	} else {
		slog.Info("admin account seeded successfully")
	}

	return nil
}
