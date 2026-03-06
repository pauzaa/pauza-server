//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/migrations"

	"golang.org/x/crypto/bcrypt"
)

// TestSeedAdmin_CreatesAdminRow verifies that calling SeedAdmin on an empty
// admin_credentials table inserts exactly one row with the correct username
// and a valid bcrypt hash of the supplied password.
func TestSeedAdmin_CreatesAdminRow(t *testing.T) {
	pool, url := testPool(t)

	if err := RunMigrations(url, migrations.FS); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const username = "admin"
	const password = "s3cret-pa55word"

	if err := SeedAdmin(ctx, pool, username, password); err != nil {
		t.Fatalf("SeedAdmin returned unexpected error: %v", err)
	}

	// Verify exactly one row was created.
	if n := rowCount(t, pool, "admin_credentials"); n != 1 {
		t.Fatalf("expected 1 row in admin_credentials, got %d", n)
	}

	// Verify the stored username and password hash.
	var storedUsername, storedHash string
	err := pool.QueryRow(ctx,
		"SELECT username, password_hash FROM admin_credentials LIMIT 1",
	).Scan(&storedUsername, &storedHash)
	if err != nil {
		t.Fatalf("querying admin_credentials: %v", err)
	}

	if storedUsername != username {
		t.Errorf("expected username %q, got %q", username, storedUsername)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password)); err != nil {
		t.Errorf("stored hash does not match password: %v", err)
	}
}

// TestSeedAdmin_Idempotent verifies that calling SeedAdmin twice does not
// duplicate the admin row and preserves the original password hash.
func TestSeedAdmin_Idempotent(t *testing.T) {
	pool, url := testPool(t)

	if err := RunMigrations(url, migrations.FS); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}

	const username = "admin"
	const password = "original-password"

	// First seed.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := SeedAdmin(ctx, pool, username, password); err != nil {
			t.Fatalf("first SeedAdmin: %v", err)
		}
	}

	// Capture the original hash.
	var originalHash string
	{
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := pool.QueryRow(ctx,
			"SELECT password_hash FROM admin_credentials LIMIT 1",
		).Scan(&originalHash)
		if err != nil {
			t.Fatalf("querying original hash: %v", err)
		}
	}

	// Second seed with a different username and password — should be a no-op
	// because SeedAdmin skips the INSERT when admin_credentials already has a
	// row, regardless of the credentials supplied.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := SeedAdmin(ctx, pool, "other-admin", "different-password"); err != nil {
			t.Fatalf("second SeedAdmin: %v", err)
		}
	}

	// Still exactly one row.
	if n := rowCount(t, pool, "admin_credentials"); n != 1 {
		t.Fatalf("expected 1 row in admin_credentials after second seed, got %d", n)
	}

	// Hash unchanged (the second call must not have updated the row).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var currentHash string
	err := pool.QueryRow(ctx,
		"SELECT password_hash FROM admin_credentials LIMIT 1",
	).Scan(&currentHash)
	if err != nil {
		t.Fatalf("querying current hash: %v", err)
	}

	if currentHash != originalHash {
		t.Error("password hash changed after second SeedAdmin call; expected idempotent behaviour")
	}
}

// TestSeedAdmin_CancelledContext verifies that SeedAdmin returns an error
// when the supplied context is already cancelled.
func TestSeedAdmin_CancelledContext(t *testing.T) {
	pool, url := testPool(t)

	if err := RunMigrations(url, migrations.FS); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := SeedAdmin(ctx, pool, "admin", "password")
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}
