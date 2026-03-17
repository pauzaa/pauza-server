//go:build integration

// Package testdb provides helpers for integration tests that need an isolated
// Postgres database. Each call to New creates a temporary database, applies
// migrations, and schedules cleanup (DROP DATABASE) when the test finishes.
// This allows multiple test packages to run in parallel without interfering
// with each other.
package testdb

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/database"
	"github.com/IsorilovA/pauza-server/migrations"
)

// New creates a temporary Postgres database with a unique name derived from
// the test name, applies all migrations, and returns a connection pool pointing
// at it. The database is dropped automatically when the test finishes.
//
// It reads TEST_DATABASE_URL from the environment and skips the test if unset.
func New(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()

	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping integration test")
	}

	dbName := tempDBName(t)

	// Connect to the base database to issue CREATE DATABASE.
	adminPool := connect(t, baseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Database names are safe identifiers (alphanumeric + underscore).
	if _, err := adminPool.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", dbName)); err != nil {
		adminPool.Close()
		t.Fatalf("creating temp database %s: %v", dbName, err)
	}
	adminPool.Close()

	// Build connection URL for the new database.
	tempURL := replaceDBName(t, baseURL, dbName)

	// Apply migrations.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.RunMigrations(logger, tempURL, migrations.FS); err != nil {
		dropDB(t, baseURL, dbName)
		t.Fatalf("applying migrations to temp database: %v", err)
	}

	// Connect pool to temp database.
	pool := connect(t, tempURL)

	t.Cleanup(func() {
		pool.Close()
		dropDB(t, baseURL, dbName)
	})

	return pool, tempURL
}

// tempDBName generates a safe, unique database name from the test name.
func tempDBName(t *testing.T) string {
	t.Helper()
	// Replace slashes and other chars with underscores, truncate.
	name := strings.ToLower(t.Name())
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	safe := b.String()
	if len(safe) > 40 {
		safe = safe[:40]
	}
	return fmt.Sprintf("test_%s_%d", safe, time.Now().UnixNano()%1_000_000)
}

func connect(t *testing.T, dbURL string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connecting to %s: %v", dbURL, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("pinging %s: %v", dbURL, err)
	}
	return pool
}

func replaceDBName(t *testing.T, rawURL, newDB string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing database URL: %v", err)
	}
	u.Path = "/" + newDB
	return u.String()
}

func dropDB(t *testing.T, baseURL, dbName string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Logf("warning: could not connect to drop temp database %s: %v", dbName, err)
		return
	}
	defer pool.Close()

	// Terminate active connections before dropping.
	_, _ = pool.Exec(ctx, fmt.Sprintf(
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s'", dbName))
	if _, err := pool.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName)); err != nil {
		t.Logf("warning: could not drop temp database %s: %v", dbName, err)
	}
}
