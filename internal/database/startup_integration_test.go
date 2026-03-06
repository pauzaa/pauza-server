//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/migrations"

	"golang.org/x/crypto/bcrypt"
)

// TestStartupSequence_ConnectMigrateSeed exercises the realistic server startup
// sequence: Connect -> RunMigrations -> SeedAdmin. This mirrors the order used
// in cmd/server/main.go and verifies that the three steps compose correctly
// against a real Postgres instance.
func TestStartupSequence_ConnectMigrateSeed(t *testing.T) {
	// Reset the database to a clean state so we start from scratch, just like
	// a fresh deployment.
	_, url := testPool(t)

	// Step 1: Connect — obtain a connection pool using the production function.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := Connect(ctx, url)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	// Step 2: RunMigrations — apply all migrations from the embedded FS.
	if err := RunMigrations(url, migrations.FS); err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}

	// Step 3: SeedAdmin — insert the initial admin account.
	const username = "admin"
	const password = "startup-test-password"

	if err := SeedAdmin(ctx, pool, username, password); err != nil {
		t.Fatalf("SeedAdmin failed: %v", err)
	}

	// --- Verify the resulting database state ---

	// Migrations should have created the core tables.
	for _, tbl := range coreTables() {
		if !tableExists(t, pool, tbl) {
			t.Errorf("expected table %q to exist after startup sequence", tbl)
		}
	}

	// SeedAdmin should have created exactly one admin row.
	if n := rowCount(t, pool, "admin_credentials"); n != 1 {
		t.Fatalf("expected 1 admin row, got %d", n)
	}

	// The stored credentials should match what was seeded.
	var storedUsername, storedHash string
	err = pool.QueryRow(ctx,
		"SELECT username, password_hash FROM admin_credentials LIMIT 1",
	).Scan(&storedUsername, &storedHash)
	if err != nil {
		t.Fatalf("querying admin_credentials: %v", err)
	}

	if storedUsername != username {
		t.Errorf("expected username %q, got %q", username, storedUsername)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password)); err != nil {
		t.Errorf("stored hash does not match seeded password: %v", err)
	}
}

// TestStartupSequence_Idempotent runs the full Connect -> RunMigrations ->
// SeedAdmin sequence twice on the same database to confirm that the entire
// startup flow is re-entrant. This simulates a server restart or rolling
// deployment where the database already has the schema and seed data.
func TestStartupSequence_Idempotent(t *testing.T) {
	// Reset the database once before the first run.
	_, url := testPool(t)

	const username = "admin"
	const password = "idempotent-test-password"

	// runStartup performs the full startup sequence. The pool is created and
	// closed within each call so that every invocation is self-contained.
	runStartup := func(t *testing.T, label string) {
		t.Helper()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		pool, err := Connect(ctx, url)
		if err != nil {
			t.Fatalf("[%s] Connect failed: %v", label, err)
		}
		defer pool.Close()

		if err := RunMigrations(url, migrations.FS); err != nil {
			t.Fatalf("[%s] RunMigrations failed: %v", label, err)
		}

		if err := SeedAdmin(ctx, pool, username, password); err != nil {
			t.Fatalf("[%s] SeedAdmin failed: %v", label, err)
		}
	}

	// First run: populates the schema and seeds the admin.
	runStartup(t, "first run")

	// Capture the admin password hash after the first run so we can verify
	// that the second run does not alter it. The pool is opened and closed
	// promptly so it does not stay open across the second runStartup call.
	var originalHash string
	{
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		inspectPool, err := Connect(ctx, url)
		if err != nil {
			t.Fatalf("connecting for inspection: %v", err)
		}

		err = inspectPool.QueryRow(ctx,
			"SELECT password_hash FROM admin_credentials LIMIT 1",
		).Scan(&originalHash)
		inspectPool.Close()
		if err != nil {
			t.Fatalf("querying original hash: %v", err)
		}
	}

	// Second run: should succeed without errors — migrations report
	// "no change" and SeedAdmin skips insertion because a row already exists.
	runStartup(t, "second run")

	// --- Verify the database state is unchanged after the second run ---

	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer verifyCancel()

	verifyPool, err := Connect(verifyCtx, url)
	if err != nil {
		t.Fatalf("connecting for verification: %v", err)
	}
	defer verifyPool.Close()

	// Still exactly one admin row (no duplicates).
	if n := rowCount(t, verifyPool, "admin_credentials"); n != 1 {
		t.Fatalf("expected 1 admin row after second run, got %d", n)
	}

	// Password hash is identical to the first run (seed was a no-op).
	var currentHash string
	err = verifyPool.QueryRow(verifyCtx,
		"SELECT password_hash FROM admin_credentials LIMIT 1",
	).Scan(&currentHash)
	if err != nil {
		t.Fatalf("querying current hash: %v", err)
	}

	if currentHash != originalHash {
		t.Error("password hash changed after second startup; expected idempotent behaviour")
	}

	// Core tables still present (migrations did not regress).
	for _, tbl := range coreTables() {
		if !tableExists(t, verifyPool, tbl) {
			t.Errorf("expected table %q to still exist after second run", tbl)
		}
	}
}
