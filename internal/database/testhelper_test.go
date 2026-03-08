//go:build integration

package database

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testLogger returns a silent logger suitable for integration tests. It
// discards all output so that operational log lines from Connect and
// RunMigrations do not pollute test output.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testDatabaseURL reads TEST_DATABASE_URL from the environment and skips the
// test when unset. This keeps integration tests opt-in: they only run when a
// real Postgres instance is available.
func testDatabaseURL(t *testing.T) string {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping integration test")
	}
	return url
}

// testPool connects to the test database, resets it to a clean state by
// dropping the public schema and recreating it, and returns a ready-to-use
// connection pool along with the database URL. The pool is closed
// automatically when the test finishes.
//
// Returning the URL alongside the pool avoids a redundant second call to
// testDatabaseURL in callers that also need the raw connection string (e.g.
// for RunMigrations).
//
// The reset is intentionally aggressive (DROP SCHEMA … CASCADE) so that every
// test starts with an empty database regardless of what a previous run left
// behind. This makes the helper safe for repeated use.
func testPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()

	url := testDatabaseURL(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("creating test connection pool: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("pinging test database: %v", err)
	}

	resetDatabase(t, pool)

	t.Cleanup(func() {
		pool.Close()
	})

	return pool, url
}

// resetDatabase drops the public schema and recreates it, removing all tables,
// indexes, and data. This also removes the schema_migrations table used by
// golang-migrate so migrations can be re-applied cleanly.
func resetDatabase(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	queries := []string{
		"DROP SCHEMA public CASCADE",
		"CREATE SCHEMA public",
		// current_user is a SQL keyword that resolves to the session's role;
		// it restores the default privileges lost when the schema was dropped.
		"GRANT ALL ON SCHEMA public TO current_user",
	}

	for _, q := range queries {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("resetting database (%s): %v", q, err)
		}
	}
}

// tableExists reports whether a table with the given name exists in the public
// schema. It is a convenience helper for migration integration tests.
func tableExists(t *testing.T, pool *pgxpool.Pool, table string) bool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`, table).Scan(&exists)
	if err != nil {
		t.Fatalf("checking if table %q exists: %v", table, err)
	}
	return exists
}

// rowCount returns the number of rows in the given table. It is a convenience
// helper for seed integration tests. The table name is quoted via
// pgx.Identifier to avoid SQL injection even though callers are test code.
func rowCount(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sanitized := pgx.Identifier{table}.Sanitize()

	var count int
	err := pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s", sanitized)).Scan(&count)
	if err != nil {
		t.Fatalf("counting rows in %q: %v", table, err)
	}
	return count
}

// coreTables returns every application table that the initial migration
// (000001_initial_schema) is expected to create. Both migration and startup
// integration tests use this to avoid maintaining duplicate table lists.
//
// Keep this list in sync with migrations/000001_initial_schema.up.sql.
// When adding a new migration that creates tables, add the names here and
// update tests that iterate over this slice. The order follows the FK
// dependency order used in the migration file.
func coreTables() []string {
	return []string{
		"users",
		"otp_codes",
		"refresh_tokens",
		"admin_credentials",
		"subscription_plans",
		"subscription_plan_discounts",
		"user_subscriptions",
		"friendships",
		"device_tokens",
		"sync_tombstones",
		"modes",
		"mode_blocked_apps",
		"schedules",
		"restriction_sessions",
		"restriction_lifecycle_events",
		"nfc_linked_chips",
		"qr_linked_codes",
		"streak_session_daily_rollups",
		"streak_daily_aggregates",
	}
}
