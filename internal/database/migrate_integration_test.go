//go:build integration

package database

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/IsorilovA/pauza-server/migrations"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func TestRunMigrations_FreshApply(t *testing.T) {
	pool, url := testPool(t)

	if err := RunMigrations(testLogger(), url, migrations.FS); err != nil {
		t.Fatalf("unexpected error on fresh migration: %v", err)
	}

	for _, tbl := range coreTables() {
		if !tableExists(t, pool, tbl) {
			t.Errorf("expected table %q to exist after migration", tbl)
		}
	}
}

func TestRunMigrations_IdempotentRerun(t *testing.T) {
	_, url := testPool(t)

	if err := RunMigrations(testLogger(), url, migrations.FS); err != nil {
		t.Fatalf("first migration run failed: %v", err)
	}

	if err := RunMigrations(testLogger(), url, migrations.FS); err != nil {
		t.Fatalf("idempotent rerun failed: %v", err)
	}
}

func TestRunMigrations_UsesCurrentPreReleaseSchema(t *testing.T) {
	pool, url := testPool(t)

	if err := RunMigrations(testLogger(), url, migrations.FS); err != nil {
		t.Fatalf("unexpected error on fresh migration: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, legacy := range []string{"subscription_plans", "subscription_plan_discounts", "user_subscriptions"} {
		if tableExists(t, pool, legacy) {
			t.Errorf("expected legacy table %q to be absent from initial schema", legacy)
		}
	}

	const userID = "11111111-1111-1111-1111-111111111111"
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, username)
		VALUES ($1, 'schema-check@example.com', 'hash', 'schema-check-user')
	`, userID); err != nil {
		t.Fatalf("inserting user: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO user_entitlements (user_id, entitlement, is_active)
		VALUES ($1, 'premium', TRUE)
	`, userID); err != nil {
		t.Fatalf("inserting entitlement: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_entitlements (user_id, entitlement, is_active)
		VALUES ($1, 'premium', FALSE)
	`, userID); err == nil {
		t.Fatal("expected duplicate entitlement insert to fail, got nil error")
	}

	var attemptedAt time.Time
	if err := pool.QueryRow(ctx, `
		INSERT INTO otp_failed_attempts (user_id, purpose)
		VALUES ($1, 'email_verification')
		RETURNING attempted_at
	`, userID).Scan(&attemptedAt); err != nil {
		t.Fatalf("inserting otp_failed_attempts row: %v", err)
	}
	if attemptedAt.IsZero() {
		t.Fatal("expected attempted_at default to be populated")
	}

	var revokedAtExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = 'refresh_tokens'
			  AND column_name = 'revoked_at'
		)
	`).Scan(&revokedAtExists); err != nil {
		t.Fatalf("checking refresh_tokens.revoked_at column: %v", err)
	}
	if !revokedAtExists {
		t.Fatal("expected refresh_tokens.revoked_at column to exist")
	}

	var revokedAtIndexExists bool
	if err := pool.QueryRow(ctx, `
		SELECT to_regclass('public.idx_refresh_tokens_revoked_at') IS NOT NULL
	`).Scan(&revokedAtIndexExists); err != nil {
		t.Fatalf("checking refresh_tokens revoked_at index: %v", err)
	}
	if !revokedAtIndexExists {
		t.Fatal("expected idx_refresh_tokens_revoked_at to exist")
	}
}

func TestRunMigrations_DownDropsCurrentSchema(t *testing.T) {
	pool, url := testPool(t)

	if err := RunMigrations(testLogger(), url, migrations.FS); err != nil {
		t.Fatalf("unexpected error on fresh migration: %v", err)
	}

	pgx5URL, err := migrateDSN(url)
	if err != nil {
		t.Fatalf("rewriting database URL: %v", err)
	}

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("opening migration source: %v", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, pgx5URL)
	if err != nil {
		t.Fatalf("creating migrate instance: %v", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			t.Errorf("closing migration source: %v", srcErr)
		}
		if dbErr != nil {
			t.Errorf("closing migration database: %v", dbErr)
		}
	}()

	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("running full down migration: %v", err)
	}

	for _, tbl := range coreTables() {
		if tableExists(t, pool, tbl) {
			t.Errorf("expected table %q to be removed after down migration", tbl)
		}
	}
}

func TestRunMigrations_InvalidSource(t *testing.T) {
	url := testDatabaseURL(t)
	emptyFS := fstest.MapFS{}

	err := RunMigrations(testLogger(), url, emptyFS)
	if err == nil {
		t.Fatal("expected error for empty migration source, got nil")
	}
}

func TestRunMigrations_InvalidDatabaseURL(t *testing.T) {
	subFS, err := fs.Sub(migrations.FS, ".")
	if err != nil {
		t.Fatalf("creating sub FS: %v", err)
	}

	err = RunMigrations(testLogger(), "mysql://user:pass@localhost/db", subFS)
	if err == nil {
		t.Fatal("expected error for invalid database URL, got nil")
	}
}
