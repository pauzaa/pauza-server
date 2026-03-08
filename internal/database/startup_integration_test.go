//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/migrations"
)

// TestMigrateSequence_ConnectMigrate exercises the realistic migration-command
// sequence: Connect -> RunMigrations. This mirrors the order used in
// cmd/migrate and verifies that the two steps compose correctly against a
// real Postgres instance. The server binary (cmd/server) does not run
// migrations; they are applied separately before startup. Admin seeding is
// handled by cmd/seed-admin.
func TestMigrateSequence_ConnectMigrate(t *testing.T) {
	// Reset the database to a clean state so we start from scratch, just like
	// a fresh deployment running cmd/migrate for the first time.
	_, url := testPool(t)

	// Step 1: Connect — obtain a connection pool using the production function.
	// In the real deployment this happens in both cmd/migrate and cmd/server.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := testLogger()

	pool, err := Connect(ctx, logger, url)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	// Step 2: RunMigrations — apply all migrations from the embedded FS.
	if err := RunMigrations(logger, url, migrations.FS); err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}

	// --- Verify the resulting database state ---

	// Migrations should have created the core tables (same as cmd/migrate).
	for _, tbl := range coreTables() {
		if !tableExists(t, pool, tbl) {
			t.Errorf("expected table %q to exist after startup sequence", tbl)
		}
	}

	// admin_credentials table exists but has no rows (seeding is separate).
	if n := rowCount(t, pool, "admin_credentials"); n != 0 {
		t.Errorf("expected 0 admin rows after connect+migrate (seeding is cmd/seed-admin), got %d", n)
	}
}

// TestMigrateSequence_Idempotent runs the full Connect -> RunMigrations
// sequence twice on the same database to confirm that the migration flow is
// re-entrant. This simulates running cmd/migrate again after a rolling
// deployment where the database already has the schema.
func TestMigrateSequence_Idempotent(t *testing.T) {
	// Reset the database once before the first run.
	_, url := testPool(t)

	// runMigrate performs the full migration sequence. The pool is created and
	// closed within each call so that every invocation is self-contained.
	logger := testLogger()

	runMigrate := func(t *testing.T, label string) {
		t.Helper()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		pool, err := Connect(ctx, logger, url)
		if err != nil {
			t.Fatalf("[%s] Connect failed: %v", label, err)
		}
		defer pool.Close()

		if err := RunMigrations(logger, url, migrations.FS); err != nil {
			t.Fatalf("[%s] RunMigrations failed: %v", label, err)
		}
	}

	// First run: populates the schema.
	runMigrate(t, "first run")

	// Second run: should succeed without errors — migrations report
	// "no change".
	runMigrate(t, "second run")

	// --- Verify the database state is unchanged after the second run ---

	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer verifyCancel()

	verifyPool, err := Connect(verifyCtx, logger, url)
	if err != nil {
		t.Fatalf("connecting for verification: %v", err)
	}
	defer verifyPool.Close()

	// Core tables still present (re-running cmd/migrate did not regress).
	for _, tbl := range coreTables() {
		if !tableExists(t, verifyPool, tbl) {
			t.Errorf("expected table %q to still exist after second run", tbl)
		}
	}
}
