//go:build integration

// cleanup_integration_test.go — verifies that cleanup SQL predicates
// correctly identify and delete only the rows that should be cleaned up,
// leaving fresh/valid rows untouched. These tests run against a real
// Postgres instance and are gated behind the "integration" build tag.

package database

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/migrations"
)

// cleanupTestLogger returns a silent logger for cleanup tests.
func cleanupTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// setupCleanupPool creates a test pool with migrations applied, ready for
// cleanup integration tests.
func setupCleanupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, url := testPool(t)
	if err := RunMigrations(testLogger(), url, migrations.FS); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}
	return pool
}

// insertCleanupUser is a helper that inserts a verified user and returns the
// user's UUID. It uses a reduced bcrypt cost for speed.
func insertCleanupUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, username string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hashing password: %v", err)
	}
	var userID string
	err = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, name, username, email_verified)
		 VALUES ($1, $2, '', $3, TRUE)
		 RETURNING id`,
		email, string(hash), username,
	).Scan(&userID)
	if err != nil {
		t.Fatalf("inserting cleanup test user %q: %v", email, err)
	}
	return userID
}

// ---------------------------------------------------------------------------
// OTP cleanup predicate tests
// ---------------------------------------------------------------------------

func TestCleanup_OTP_DeletesExpiredBeyondRetention(t *testing.T) {
	pool := setupCleanupPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertCleanupUser(t, ctx, pool, "cleanup-otp@example.com", "user_cleanup_otp")
	codeHash, err := auth.HashOTP("111111")
	if err != nil {
		t.Fatalf("hashing OTP: %v", err)
	}
	purpose := mail.PurposeEmailVerification

	// Insert an OTP that expired 48 hours ago.
	_, err = pool.Exec(ctx,
		`INSERT INTO otp_codes (user_id, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, now() - interval '48 hours')`,
		userID, codeHash, purpose)
	if err != nil {
		t.Fatalf("inserting old expired OTP: %v", err)
	}

	// Retention of 24h should delete rows expired more than 24h ago.
	deleted, err := cleanupOTPCodes(ctx, pool, 24*time.Hour)
	if err != nil {
		t.Fatalf("cleanupOTPCodes: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 OTP deleted, got %d", deleted)
	}
}

func TestCleanup_OTP_PreservesRecentlyExpired(t *testing.T) {
	pool := setupCleanupPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertCleanupUser(t, ctx, pool, "cleanup-otp-recent@example.com", "user_cleanup_otp2")
	codeHash, err := auth.HashOTP("222222")
	if err != nil {
		t.Fatalf("hashing OTP: %v", err)
	}
	purpose := mail.PurposeEmailVerification

	// Insert an OTP that expired 1 hour ago.
	_, err = pool.Exec(ctx,
		`INSERT INTO otp_codes (user_id, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, now() - interval '1 hour')`,
		userID, codeHash, purpose)
	if err != nil {
		t.Fatalf("inserting recently expired OTP: %v", err)
	}

	// Retention of 24h — this OTP expired only 1h ago, so it should be kept.
	deleted, err := cleanupOTPCodes(ctx, pool, 24*time.Hour)
	if err != nil {
		t.Fatalf("cleanupOTPCodes: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 OTPs deleted (within retention), got %d", deleted)
	}
}

func TestCleanup_OTP_PreservesUnexpired(t *testing.T) {
	pool := setupCleanupPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertCleanupUser(t, ctx, pool, "cleanup-otp-fresh@example.com", "user_cleanup_otp3")
	codeHash, err := auth.HashOTP("333333")
	if err != nil {
		t.Fatalf("hashing OTP: %v", err)
	}
	purpose := mail.PurposeEmailVerification

	// Insert an OTP that has not yet expired.
	_, err = pool.Exec(ctx,
		`INSERT INTO otp_codes (user_id, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, now() + interval '10 minutes')`,
		userID, codeHash, purpose)
	if err != nil {
		t.Fatalf("inserting unexpired OTP: %v", err)
	}

	// Even with zero retention, an unexpired OTP should not be deleted.
	deleted, err := cleanupOTPCodes(ctx, pool, 0)
	if err != nil {
		t.Fatalf("cleanupOTPCodes: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 OTPs deleted (not yet expired), got %d", deleted)
	}
}

// ---------------------------------------------------------------------------
// Expired refresh-token cleanup predicate tests
// ---------------------------------------------------------------------------

func TestCleanup_ExpiredRefreshToken_DeletesOldExpired(t *testing.T) {
	pool := setupCleanupPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertCleanupUser(t, ctx, pool, "cleanup-rt-exp@example.com", "user_cleanup_rt1")

	// Insert a refresh token that expired 10 days ago.
	_, err := pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, 'hash-expired-old', now() - interval '10 days')`,
		userID)
	if err != nil {
		t.Fatalf("inserting old expired refresh token: %v", err)
	}

	// Max age of 7 days — token expired 10 days ago, so it should be deleted.
	deleted, err := cleanupExpiredRefreshTokens(ctx, pool, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("cleanupExpiredRefreshTokens: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 expired refresh token deleted, got %d", deleted)
	}
}

func TestCleanup_ExpiredRefreshToken_PreservesRecentlyExpired(t *testing.T) {
	pool := setupCleanupPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertCleanupUser(t, ctx, pool, "cleanup-rt-recent@example.com", "user_cleanup_rt2")

	// Insert a refresh token that expired 2 days ago.
	_, err := pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, 'hash-expired-recent', now() - interval '2 days')`,
		userID)
	if err != nil {
		t.Fatalf("inserting recently expired refresh token: %v", err)
	}

	// Max age of 7 days — token expired only 2 days ago, within retention.
	deleted, err := cleanupExpiredRefreshTokens(ctx, pool, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("cleanupExpiredRefreshTokens: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 expired refresh tokens deleted (within retention), got %d", deleted)
	}
}

func TestCleanup_ExpiredRefreshToken_PreservesActive(t *testing.T) {
	pool := setupCleanupPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertCleanupUser(t, ctx, pool, "cleanup-rt-active@example.com", "user_cleanup_rt3")

	// Insert a refresh token that has not yet expired.
	_, err := pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, 'hash-active', now() + interval '7 days')`,
		userID)
	if err != nil {
		t.Fatalf("inserting active refresh token: %v", err)
	}

	// Even with zero max age, an active token should not be deleted.
	deleted, err := cleanupExpiredRefreshTokens(ctx, pool, 0)
	if err != nil {
		t.Fatalf("cleanupExpiredRefreshTokens: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 expired refresh tokens deleted (still active), got %d", deleted)
	}
}

// ---------------------------------------------------------------------------
// Revoked refresh-token cleanup predicate tests
// ---------------------------------------------------------------------------

func TestCleanup_RevokedRefreshToken_DeletesOldRevoked(t *testing.T) {
	pool := setupCleanupPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertCleanupUser(t, ctx, pool, "cleanup-revoked-old@example.com", "user_cleanup_rev1")

	// Insert a revoked token with created_at 10 days ago.
	_, err := pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at, revoked, created_at)
		 VALUES ($1, 'hash-revoked-old', now() + interval '30 days', true, now() - interval '10 days')`,
		userID)
	if err != nil {
		t.Fatalf("inserting old revoked refresh token: %v", err)
	}

	// Max age of 7 days — token was created 10 days ago and revoked, should be deleted.
	deleted, err := cleanupRevokedRefreshTokens(ctx, pool, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("cleanupRevokedRefreshTokens: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 revoked refresh token deleted, got %d", deleted)
	}
}

func TestCleanup_RevokedRefreshToken_PreservesRecentlyRevoked(t *testing.T) {
	pool := setupCleanupPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertCleanupUser(t, ctx, pool, "cleanup-revoked-recent@example.com", "user_cleanup_rev2")

	// Insert a revoked token created 2 days ago.
	_, err := pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at, revoked, created_at)
		 VALUES ($1, 'hash-revoked-recent', now() + interval '30 days', true, now() - interval '2 days')`,
		userID)
	if err != nil {
		t.Fatalf("inserting recently revoked refresh token: %v", err)
	}

	// Max age of 7 days — token was created only 2 days ago, within retention.
	deleted, err := cleanupRevokedRefreshTokens(ctx, pool, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("cleanupRevokedRefreshTokens: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 revoked refresh tokens deleted (within retention), got %d", deleted)
	}
}

func TestCleanup_RevokedRefreshToken_PreservesNonRevoked(t *testing.T) {
	pool := setupCleanupPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertCleanupUser(t, ctx, pool, "cleanup-nonrevoked@example.com", "user_cleanup_rev3")

	// Insert a NON-revoked token with created_at 10 days ago.
	// Even though it is old, it is not revoked so the revoked-cleanup
	// predicate must not touch it.
	_, err := pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at, revoked, created_at)
		 VALUES ($1, 'hash-notrevoked-old', now() + interval '30 days', false, now() - interval '10 days')`,
		userID)
	if err != nil {
		t.Fatalf("inserting non-revoked refresh token: %v", err)
	}

	deleted, err := cleanupRevokedRefreshTokens(ctx, pool, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("cleanupRevokedRefreshTokens: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 tokens deleted (not revoked), got %d", deleted)
	}
}

// ---------------------------------------------------------------------------
// Full runCleanup pass integration
// ---------------------------------------------------------------------------

func TestCleanup_FullPass_MixedRows(t *testing.T) {
	pool := setupCleanupPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	logger := cleanupTestLogger()
	userID := insertCleanupUser(t, ctx, pool, "cleanup-full@example.com", "user_cleanup_full")

	codeHash, err := auth.HashOTP("999999")
	if err != nil {
		t.Fatalf("hashing OTP: %v", err)
	}
	purpose := mail.PurposeEmailVerification

	// --- Seed rows ---

	// OTP: expired 48h ago (should be cleaned with 24h retention).
	_, err = pool.Exec(ctx,
		`INSERT INTO otp_codes (user_id, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, now() - interval '48 hours')`,
		userID, codeHash, purpose)
	if err != nil {
		t.Fatalf("inserting old OTP: %v", err)
	}

	// OTP: expired 1h ago (should survive 24h retention).
	codeHash2, _ := auth.HashOTP("888888")
	_, err = pool.Exec(ctx,
		`INSERT INTO otp_codes (user_id, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, now() - interval '1 hour')`,
		userID, codeHash2, purpose)
	if err != nil {
		t.Fatalf("inserting recent OTP: %v", err)
	}

	// OTP: not yet expired (should survive).
	codeHash3, _ := auth.HashOTP("777777")
	_, err = pool.Exec(ctx,
		`INSERT INTO otp_codes (user_id, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, now() + interval '10 minutes')`,
		userID, codeHash3, purpose)
	if err != nil {
		t.Fatalf("inserting fresh OTP: %v", err)
	}

	// Refresh: expired 10 days ago (should be cleaned with 7d max age).
	_, err = pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, 'hash-full-exp-old', now() - interval '10 days')`,
		userID)
	if err != nil {
		t.Fatalf("inserting old expired token: %v", err)
	}

	// Refresh: active (should survive).
	_, err = pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, 'hash-full-active', now() + interval '7 days')`,
		userID)
	if err != nil {
		t.Fatalf("inserting active token: %v", err)
	}

	// Refresh: revoked, created 10 days ago (should be cleaned).
	_, err = pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at, revoked, created_at)
		 VALUES ($1, 'hash-full-revoked-old', now() + interval '30 days', true, now() - interval '10 days')`,
		userID)
	if err != nil {
		t.Fatalf("inserting old revoked token: %v", err)
	}

	// Refresh: revoked, created 2 days ago (should survive).
	_, err = pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at, revoked, created_at)
		 VALUES ($1, 'hash-full-revoked-recent', now() + interval '30 days', true, now() - interval '2 days')`,
		userID)
	if err != nil {
		t.Fatalf("inserting recent revoked token: %v", err)
	}

	// --- Run full cleanup pass ---
	cfg := CleanupConfig{
		Interval:           1 * time.Hour,
		OTPRetention:       24 * time.Hour,
		RefreshTokenMaxAge: 7 * 24 * time.Hour,
	}
	runCleanup(ctx, pool, logger, cfg)

	// --- Verify surviving rows ---

	// OTPs: 2 should survive (recently expired + unexpired).
	var otpCount int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM otp_codes WHERE user_id = $1`, userID).Scan(&otpCount)
	if err != nil {
		t.Fatalf("counting remaining OTPs: %v", err)
	}
	if otpCount != 2 {
		t.Errorf("expected 2 OTPs remaining, got %d", otpCount)
	}

	// Refresh tokens: 2 should survive (active + recently revoked).
	var rtCount int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE user_id = $1`, userID).Scan(&rtCount)
	if err != nil {
		t.Fatalf("counting remaining refresh tokens: %v", err)
	}
	if rtCount != 2 {
		t.Errorf("expected 2 refresh tokens remaining, got %d", rtCount)
	}
}
