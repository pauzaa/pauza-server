package database

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect creates a new pgxpool.Pool for the given database URL and verifies
// connectivity with a ping. The caller is responsible for calling pool.Close()
// when the pool is no longer needed. The provided logger is used for
// operational log messages; pass slog.Default() when a dedicated logger is
// not available.
func Connect(ctx context.Context, logger *slog.Logger, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	logger.Info("connected to database")

	return pool, nil
}
