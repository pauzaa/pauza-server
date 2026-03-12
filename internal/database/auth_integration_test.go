//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/migrations"
)

const testQueryTimeout = 10 * time.Second

func setupTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, url := testPool(t)
	if err := RunMigrations(testLogger(), url, migrations.FS); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}
	return pool
}

func insertTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, username string) string {
	t.Helper()
	var userID string
	err := pool.QueryRow(ctx,
		`INSERT INTO users (email, username)
		 VALUES ($1, $2)
		 RETURNING id`,
		email, username,
	).Scan(&userID)
	if err != nil {
		t.Fatalf("inserting test user: %v", err)
	}
	return userID
}

func TestUsersTable_HasNoPasswordHash(t *testing.T) {
	pool := setupTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	var count int
	err := pool.QueryRow(ctx,
		`SELECT count(*)
		 FROM information_schema.columns
		 WHERE table_name = 'users' AND column_name = 'password_hash'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("querying information_schema: %v", err)
	}
	if count != 0 {
		t.Fatalf("password_hash column count = %d, want 0", count)
	}
}

func TestOTP_InsertAndQueryByEmail(t *testing.T) {
	pool := setupTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	codeHash, err := auth.HashOTP("123456")
	if err != nil {
		t.Fatalf("hashing OTP: %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO otp_codes (email, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		"user@example.com", codeHash, mail.PurposeAuthLogin, time.Now().UTC().Add(10*time.Minute),
	)
	if err != nil {
		t.Fatalf("inserting otp: %v", err)
	}

	var storedHash string
	err = pool.QueryRow(ctx,
		`SELECT code_hash
		 FROM otp_codes
		 WHERE lower(email) = lower($1)
		   AND purpose = $2
		   AND used = false
		   AND expires_at > now()`,
		"USER@example.com", mail.PurposeAuthLogin,
	).Scan(&storedHash)
	if err != nil {
		t.Fatalf("querying otp: %v", err)
	}

	match, err := auth.VerifyOTP(storedHash, "123456")
	if err != nil {
		t.Fatalf("verifying otp hash: %v", err)
	}
	if !match {
		t.Fatal("expected stored hash to match original OTP")
	}
}

func TestOTPFailedAttempts_StoresEmailAndOptionalUserID(t *testing.T) {
	pool := setupTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertTestUser(t, ctx, pool, "user@example.com", "user_test123")
	_, err := pool.Exec(ctx,
		`INSERT INTO otp_failed_attempts (email, user_id, purpose)
		 VALUES ($1, $2, $3)`,
		"user@example.com", userID, mail.PurposeAuthLogin,
	)
	if err != nil {
		t.Fatalf("inserting failed attempt: %v", err)
	}

	var storedEmail, storedUserID string
	err = pool.QueryRow(ctx,
		`SELECT email, user_id FROM otp_failed_attempts LIMIT 1`,
	).Scan(&storedEmail, &storedUserID)
	if err != nil {
		t.Fatalf("querying failed attempt: %v", err)
	}
	if storedEmail != "user@example.com" || storedUserID != userID {
		t.Fatalf("stored failed attempt = (%q, %q)", storedEmail, storedUserID)
	}
}

func TestRefreshTokens_RetainRotationShape(t *testing.T) {
	pool := setupTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertTestUser(t, ctx, pool, "refresh@example.com", "user_refresh1")
	_, err := pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)`,
		userID, "hash-1", time.Now().UTC().Add(24*time.Hour),
	)
	if err != nil {
		t.Fatalf("inserting refresh token: %v", err)
	}

	var revoked bool
	err = pool.QueryRow(ctx,
		`SELECT revoked FROM refresh_tokens WHERE token_hash = 'hash-1'`,
	).Scan(&revoked)
	if err != nil {
		t.Fatalf("querying refresh token: %v", err)
	}
	if revoked {
		t.Fatal("expected inserted refresh token to start non-revoked")
	}
}
