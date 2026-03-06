//go:build integration

package database

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/IsorilovA/pauza-server/migrations"
)

func TestRunMigrations_FreshApply(t *testing.T) {
	pool, url := testPool(t) // resets the database to a clean state

	if err := RunMigrations(url, migrations.FS); err != nil {
		t.Fatalf("unexpected error on fresh migration: %v", err)
	}

	// Verify that every table created by the initial migration exists.
	for _, tbl := range coreTables() {
		if !tableExists(t, pool, tbl) {
			t.Errorf("expected table %q to exist after migration", tbl)
		}
	}
}

func TestRunMigrations_IdempotentRerun(t *testing.T) {
	_, url := testPool(t) // reset

	// First run: apply all migrations.
	if err := RunMigrations(url, migrations.FS); err != nil {
		t.Fatalf("first migration run failed: %v", err)
	}

	// Second run: should succeed with no error (ErrNoChange is swallowed).
	if err := RunMigrations(url, migrations.FS); err != nil {
		t.Fatalf("idempotent rerun failed: %v", err)
	}
}

func TestRunMigrations_InvalidSource(t *testing.T) {
	// We only need a valid DATABASE_URL to satisfy the skip-guard; no pool or
	// DB reset is required because RunMigrations will fail while opening the
	// empty migration source before it ever contacts the database.
	url := testDatabaseURL(t)

	// An empty in-memory FS has no migration files, so iofs.New should fail.
	emptyFS := fstest.MapFS{}

	err := RunMigrations(url, emptyFS)
	if err == nil {
		t.Fatal("expected error for empty migration source, got nil")
	}
}

func TestRunMigrations_InvalidDatabaseURL(t *testing.T) {
	// Use fs.Sub on the real migrations FS so the source is valid; only the
	// database URL is broken. We use an unsupported scheme so migrateDSN
	// returns an error before any network I/O.
	subFS, err := fs.Sub(migrations.FS, ".")
	if err != nil {
		t.Fatalf("creating sub FS: %v", err)
	}

	err = RunMigrations("mysql://user:pass@localhost/db", subFS)
	if err == nil {
		t.Fatal("expected error for invalid database URL, got nil")
	}
}
