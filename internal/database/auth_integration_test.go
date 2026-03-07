//go:build integration

// auth_integration_test.go — DB-layer integration tests for auth-related
// tables (users, otp_codes, refresh_tokens). Every test resets the shared
// Postgres instance via testPool, so these tests MUST NOT run in parallel
// (no t.Parallel calls). The go test runner executes them sequentially within
// this package by default; keep it that way.

package database

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/migrations"
)

const (
	// testBcryptCost uses bcrypt.MinCost so that integration tests stay fast.
	// Production code uses cost 12 (see internal/database/seed.go); the minimum
	// cost is sufficient here because we only need valid hashes for storage and
	// comparison, not brute-force resistance.
	testBcryptCost = bcrypt.MinCost

	// testQueryTimeout is the per-query context timeout used across tests in
	// this file. It is generous enough for CI runners while still catching
	// runaway queries.
	testQueryTimeout = 10 * time.Second
)

// setupTestPool creates a pool, resets the DB, applies migrations, and returns
// the ready-to-use pool. It consolidates the repeated testPool + RunMigrations
// boilerplate used by every test in this file.
func setupTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, url := testPool(t)

	if err := RunMigrations(url, migrations.FS); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}

	return pool
}

// ---------------------------------------------------------------------------
// User operations
// ---------------------------------------------------------------------------

// TestUser_InsertAndQueryByEmail inserts a user with email_verified = false
// and verifies the row can be retrieved by email.
func TestUser_InsertAndQueryByEmail(t *testing.T) {
	pool := setupTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	const (
		email    = "alice@example.com"
		username = "user_abcd1234"
		name     = "Alice"
		password = "s3cret-password"
	)

	hash, err := bcrypt.GenerateFromPassword([]byte(password), testBcryptCost)
	if err != nil {
		t.Fatalf("hashing password: %v", err)
	}

	// Insert a user with email_verified = false.
	_, err = pool.Exec(ctx,
		`INSERT INTO users (email, password_hash, name, username, email_verified)
		 VALUES ($1, $2, $3, $4, FALSE)`,
		email, string(hash), name, username)
	if err != nil {
		t.Fatalf("inserting user: %v", err)
	}

	// Query by email.
	var (
		storedID            string
		storedEmail         string
		storedHash          string
		storedName          string
		storedUsername      string
		storedEmailVerified bool
	)
	err = pool.QueryRow(ctx,
		`SELECT id, email, password_hash, name, username, email_verified
		 FROM users WHERE lower(email) = $1`, email,
	).Scan(&storedID, &storedEmail, &storedHash, &storedName, &storedUsername, &storedEmailVerified)
	if err != nil {
		t.Fatalf("querying user by email: %v", err)
	}

	if storedID == "" {
		t.Error("expected non-empty user id")
	}
	if storedEmail != email {
		t.Errorf("expected email %q, got %q", email, storedEmail)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password)); err != nil {
		t.Errorf("stored password hash does not match original password: %v", err)
	}
	if storedName != name {
		t.Errorf("expected name %q, got %q", name, storedName)
	}
	if storedUsername != username {
		t.Errorf("expected username %q, got %q", username, storedUsername)
	}
	if storedEmailVerified {
		t.Error("expected email_verified = false for newly inserted user")
	}
}

// TestUser_UpdateEmailVerified inserts an unverified user and then sets
// email_verified = true, confirming the update persists.
func TestUser_UpdateEmailVerified(t *testing.T) {
	pool := setupTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	const email = "bob@example.com"

	hash, err := bcrypt.GenerateFromPassword([]byte("password"), testBcryptCost)
	if err != nil {
		t.Fatalf("hashing password: %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO users (email, password_hash, name, username, email_verified)
		 VALUES ($1, $2, 'Bob', 'user_bob12345', FALSE)`,
		email, string(hash))
	if err != nil {
		t.Fatalf("inserting user: %v", err)
	}

	// Update email_verified to true (mirrors production shape: no updated_at).
	tag, err := pool.Exec(ctx,
		`UPDATE users SET email_verified = true WHERE lower(email) = $1`, email)
	if err != nil {
		t.Fatalf("updating email_verified: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected 1 row affected, got %d", tag.RowsAffected())
	}

	// Verify the update persisted.
	var verified bool
	err = pool.QueryRow(ctx,
		`SELECT email_verified FROM users WHERE lower(email) = $1`, email,
	).Scan(&verified)
	if err != nil {
		t.Fatalf("querying email_verified: %v", err)
	}
	if !verified {
		t.Error("expected email_verified = true after update")
	}
}

// TestUser_CaseInsensitiveEmailLookup verifies that an email lookup is
// case-insensitive by using lower() in the query.
func TestUser_CaseInsensitiveEmailLookup(t *testing.T) {
	pool := setupTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	const email = "carol@example.com"

	hash, err := bcrypt.GenerateFromPassword([]byte("password"), testBcryptCost)
	if err != nil {
		t.Fatalf("hashing password: %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO users (email, password_hash, name, username, email_verified)
		 VALUES ($1, $2, 'Carol', 'user_carol123', FALSE)`,
		email, string(hash))
	if err != nil {
		t.Fatalf("inserting user: %v", err)
	}

	// Query with a different case.
	var storedEmail string
	err = pool.QueryRow(ctx,
		`SELECT email FROM users WHERE lower(email) = lower($1)`,
		"CAROL@EXAMPLE.COM",
	).Scan(&storedEmail)
	if err != nil {
		t.Fatalf("case-insensitive email lookup failed: %v", err)
	}
	if storedEmail != email {
		t.Errorf("expected email %q, got %q", email, storedEmail)
	}
}

// ---------------------------------------------------------------------------
// OTP operations
// ---------------------------------------------------------------------------

// TestOTP_InsertAndQueryValid inserts an OTP and queries for a valid one
// (not expired, not used, matching user_id and purpose).
func TestOTP_InsertAndQueryValid(t *testing.T) {
	pool := setupTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	const (
		email = "otp-user@example.com"
		code  = "123456"
	)
	userID := insertTestUser(t, ctx, pool, email, "user_otpvalid1")
	codeHash, err := auth.HashOTP(code)
	if err != nil {
		t.Fatalf("hashing OTP: %v", err)
	}
	purpose := mail.PurposeEmailVerification
	expiresAt := time.Now().UTC().Add(10 * time.Minute)

	_, err = pool.Exec(ctx,
		`INSERT INTO otp_codes (user_id, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		userID, codeHash, purpose, expiresAt)
	if err != nil {
		t.Fatalf("inserting OTP: %v", err)
	}

	// Query for a valid OTP.
	var storedCodeHash string
	var storedUsed bool
	var storedAttempts int
	err = pool.QueryRow(ctx,
		`SELECT code_hash, used, attempts FROM otp_codes
		 WHERE user_id = $1
		   AND purpose = $2
		   AND used = FALSE
		   AND expires_at > now()
		 ORDER BY created_at DESC
		 LIMIT 1`,
		userID, purpose,
	).Scan(&storedCodeHash, &storedUsed, &storedAttempts)
	if err != nil {
		t.Fatalf("querying valid OTP: %v", err)
	}

	if match, verr := auth.VerifyOTP(storedCodeHash, code); verr != nil {
		t.Errorf("verifying stored OTP hash: %v", verr)
	} else if !match {
		t.Error("stored code_hash does not match original OTP code")
	}
	if storedUsed {
		t.Error("expected used = false")
	}
	if storedAttempts != 0 {
		t.Errorf("expected attempts = 0, got %d", storedAttempts)
	}
}

// TestOTP_MarkUsed inserts an OTP, marks it as used, and verifies it is no
// longer returned by the valid-OTP query.
func TestOTP_MarkUsed(t *testing.T) {
	pool := setupTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	const (
		email = "mark-used@example.com"
		code  = "654321"
	)
	userID := insertTestUser(t, ctx, pool, email, "user_markused1")
	codeHash, err := auth.HashOTP(code)
	if err != nil {
		t.Fatalf("hashing OTP: %v", err)
	}
	purpose := mail.PurposeEmailVerification
	expiresAt := time.Now().UTC().Add(10 * time.Minute)

	// Insert an OTP.
	var otpID string
	err = pool.QueryRow(ctx,
		`INSERT INTO otp_codes (user_id, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		userID, codeHash, purpose, expiresAt,
	).Scan(&otpID)
	if err != nil {
		t.Fatalf("inserting OTP: %v", err)
	}

	// Mark it as used.
	tag, err := pool.Exec(ctx,
		`UPDATE otp_codes SET used = TRUE WHERE id = $1`, otpID)
	if err != nil {
		t.Fatalf("marking OTP as used: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected 1 row affected, got %d", tag.RowsAffected())
	}

	// Verify it is no longer returned by the valid-OTP query.
	var count int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM otp_codes
		 WHERE user_id = $1
		   AND purpose = $2
		   AND used = FALSE
		   AND expires_at > now()`,
		userID, purpose,
	).Scan(&count)
	if err != nil {
		t.Fatalf("counting valid OTPs: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 valid OTPs after marking used, got %d", count)
	}
}

// TestOTP_IncrementAttempts inserts an OTP and increments the attempts
// counter, verifying the value after each increment.
func TestOTP_IncrementAttempts(t *testing.T) {
	pool := setupTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	const (
		email = "attempts@example.com"
		code  = "111222"
	)
	userID := insertTestUser(t, ctx, pool, email, "user_attempts1")
	codeHash, err := auth.HashOTP(code)
	if err != nil {
		t.Fatalf("hashing OTP: %v", err)
	}
	purpose := mail.PurposePasswordReset
	expiresAt := time.Now().UTC().Add(10 * time.Minute)

	var otpID string
	err = pool.QueryRow(ctx,
		`INSERT INTO otp_codes (user_id, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		userID, codeHash, purpose, expiresAt,
	).Scan(&otpID)
	if err != nil {
		t.Fatalf("inserting OTP: %v", err)
	}

	// Increment attempts three times.
	for i := 1; i <= 3; i++ {
		_, err := pool.Exec(ctx,
			`UPDATE otp_codes SET attempts = attempts + 1 WHERE id = $1`, otpID)
		if err != nil {
			t.Fatalf("incrementing attempts (round %d): %v", i, err)
		}

		var attempts int
		err = pool.QueryRow(ctx,
			`SELECT attempts FROM otp_codes WHERE id = $1`, otpID,
		).Scan(&attempts)
		if err != nil {
			t.Fatalf("querying attempts (round %d): %v", i, err)
		}
		if attempts != i {
			t.Errorf("round %d: expected attempts = %d, got %d", i, i, attempts)
		}
	}
}

// TestOTP_ExpiredNotReturned inserts an OTP with expires_at in the past and
// confirms it is not returned by the valid-OTP query.
func TestOTP_ExpiredNotReturned(t *testing.T) {
	pool := setupTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	const (
		email = "expired@example.com"
		code  = "999888"
	)
	userID := insertTestUser(t, ctx, pool, email, "user_expired1")
	codeHash, err := auth.HashOTP(code)
	if err != nil {
		t.Fatalf("hashing OTP: %v", err)
	}
	purpose := mail.PurposeEmailVerification
	// Already expired.
	expiresAt := time.Now().UTC().Add(-1 * time.Minute)

	_, err = pool.Exec(ctx,
		`INSERT INTO otp_codes (user_id, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		userID, codeHash, purpose, expiresAt)
	if err != nil {
		t.Fatalf("inserting expired OTP: %v", err)
	}

	// Valid-OTP query should return nothing.
	var count int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM otp_codes
		 WHERE user_id = $1
		   AND purpose = $2
		   AND used = FALSE
		   AND expires_at > now()`,
		userID, purpose,
	).Scan(&count)
	if err != nil {
		t.Fatalf("counting valid OTPs: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 valid OTPs for expired code, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Refresh token operations
// ---------------------------------------------------------------------------

// insertTestUser is a helper that inserts a verified user and returns the
// user's UUID. It uses a reduced bcrypt cost for speed.
func insertTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, username string) string {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte("password"), testBcryptCost)
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
		t.Fatalf("inserting test user %q: %v", email, err)
	}
	return userID
}

// TestRefreshToken_InsertAndLookupByHash inserts a refresh token and looks it
// up by its token_hash, verifying all stored fields.
func TestRefreshToken_InsertAndLookupByHash(t *testing.T) {
	pool := setupTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertTestUser(t, ctx, pool, "token-insert@example.com", "user_tkinsert1")

	const tokenHash = "sha256-hash-insert-test"
	expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour)

	_, err := pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt)
	if err != nil {
		t.Fatalf("inserting refresh token: %v", err)
	}

	// Look up by hash.
	var (
		storedID      string
		storedUserID  string
		storedRevoked bool
	)
	err = pool.QueryRow(ctx,
		`SELECT id, user_id, revoked FROM refresh_tokens
		 WHERE token_hash = $1`, tokenHash,
	).Scan(&storedID, &storedUserID, &storedRevoked)
	if err != nil {
		t.Fatalf("looking up refresh token by hash: %v", err)
	}

	if storedID == "" {
		t.Error("expected non-empty token id")
	}
	if storedUserID != userID {
		t.Errorf("expected user_id %q, got %q", userID, storedUserID)
	}
	if storedRevoked {
		t.Error("expected revoked = false for newly inserted token")
	}
}

// TestRefreshToken_RevokeSingle inserts a token and revokes it, verifying
// that a lookup by hash now returns revoked = true.
func TestRefreshToken_RevokeSingle(t *testing.T) {
	pool := setupTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertTestUser(t, ctx, pool, "token-revoke@example.com", "user_tkrevoke1")

	const tokenHash = "sha256-hash-revoke-single"
	expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour)

	var tokenID string
	err := pool.QueryRow(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
		userID, tokenHash, expiresAt,
	).Scan(&tokenID)
	if err != nil {
		t.Fatalf("inserting refresh token: %v", err)
	}

	// Revoke the token.
	tag, err := pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE WHERE id = $1`, tokenID)
	if err != nil {
		t.Fatalf("revoking refresh token: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected 1 row affected, got %d", tag.RowsAffected())
	}

	// Verify lookup returns revoked = true.
	var revoked bool
	err = pool.QueryRow(ctx,
		`SELECT revoked FROM refresh_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&revoked)
	if err != nil {
		t.Fatalf("looking up revoked token: %v", err)
	}
	if !revoked {
		t.Error("expected revoked = true after revoking token")
	}
}

// TestRefreshToken_RevokeAllForUser inserts multiple tokens for one user and
// revokes them all via a user_id-scoped UPDATE, verifying every token is
// revoked.
func TestRefreshToken_RevokeAllForUser(t *testing.T) {
	pool := setupTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertTestUser(t, ctx, pool, "token-revokeall@example.com", "user_tkrall1")

	expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour)

	hashes := []string{"hash-revokeall-1", "hash-revokeall-2", "hash-revokeall-3"}
	for _, h := range hashes {
		_, err := pool.Exec(ctx,
			`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
			 VALUES ($1, $2, $3)`,
			userID, h, expiresAt)
		if err != nil {
			t.Fatalf("inserting refresh token %q: %v", h, err)
		}
	}

	// Revoke all tokens for the user.
	tag, err := pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE WHERE user_id = $1`, userID)
	if err != nil {
		t.Fatalf("revoking all tokens for user: %v", err)
	}
	if tag.RowsAffected() != int64(len(hashes)) {
		t.Fatalf("expected %d rows affected, got %d", len(hashes), tag.RowsAffected())
	}

	// Verify all tokens are revoked.
	var activeCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens
		 WHERE user_id = $1 AND revoked = FALSE`, userID,
	).Scan(&activeCount)
	if err != nil {
		t.Fatalf("counting active tokens: %v", err)
	}
	if activeCount != 0 {
		t.Errorf("expected 0 active tokens after revoke-all, got %d", activeCount)
	}
}

// TestRefreshToken_Rotation inserts a token, revokes it (simulating rotation),
// inserts a new token, and verifies the old is revoked while the new is valid.
func TestRefreshToken_Rotation(t *testing.T) {
	pool := setupTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertTestUser(t, ctx, pool, "token-rotation@example.com", "user_tkrot1")

	expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour)

	const (
		oldHash = "sha256-hash-rotation-old"
		newHash = "sha256-hash-rotation-new"
	)

	// Insert the original token.
	var oldTokenID string
	err := pool.QueryRow(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
		userID, oldHash, expiresAt,
	).Scan(&oldTokenID)
	if err != nil {
		t.Fatalf("inserting old refresh token: %v", err)
	}

	// Rotate: revoke old token.
	tag, err := pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE WHERE id = $1`, oldTokenID)
	if err != nil {
		t.Fatalf("revoking old token: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected 1 row affected revoking old token, got %d", tag.RowsAffected())
	}

	// Insert new token.
	_, err = pool.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)`,
		userID, newHash, expiresAt)
	if err != nil {
		t.Fatalf("inserting new refresh token: %v", err)
	}

	// Verify old token is revoked.
	var oldRevoked bool
	err = pool.QueryRow(ctx,
		`SELECT revoked FROM refresh_tokens WHERE token_hash = $1`, oldHash,
	).Scan(&oldRevoked)
	if err != nil {
		t.Fatalf("querying old token: %v", err)
	}
	if !oldRevoked {
		t.Error("expected old token to be revoked after rotation")
	}

	// Verify new token is valid.
	var newRevoked bool
	err = pool.QueryRow(ctx,
		`SELECT revoked FROM refresh_tokens WHERE token_hash = $1`, newHash,
	).Scan(&newRevoked)
	if err != nil {
		t.Fatalf("querying new token: %v", err)
	}
	if newRevoked {
		t.Error("expected new token to be valid (not revoked) after rotation")
	}
}

// TestRefreshToken_TheftDetection simulates theft detection: when a revoked
// token hash is presented, all tokens for that user should be revoked.
// The sequence is:
//  1. Insert two tokens for the same user.
//  2. Revoke token A (simulating normal rotation).
//  3. "Present" the revoked token A — detect it is revoked.
//  4. Revoke ALL tokens for the user (theft response).
//  5. Verify both tokens are now revoked.
func TestRefreshToken_TheftDetection(t *testing.T) {
	pool := setupTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	userID := insertTestUser(t, ctx, pool, "token-theft@example.com", "user_tktheft1")

	expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour)

	const (
		hashA = "sha256-hash-theft-a"
		hashB = "sha256-hash-theft-b"
	)

	// Step 1: Insert two tokens.
	for _, h := range []string{hashA, hashB} {
		_, err := pool.Exec(ctx,
			`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
			 VALUES ($1, $2, $3)`,
			userID, h, expiresAt)
		if err != nil {
			t.Fatalf("inserting refresh token %q: %v", h, err)
		}
	}

	// Step 2: Revoke token A (normal rotation).
	tag, err := pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE WHERE token_hash = $1`, hashA)
	if err != nil {
		t.Fatalf("revoking token A: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected 1 row affected revoking token A, got %d", tag.RowsAffected())
	}

	// Precondition: token B must still be active before the theft response
	// so the test actually proves the revoke-all flipped it.
	var preBRevoked bool
	err = pool.QueryRow(ctx,
		`SELECT revoked FROM refresh_tokens WHERE token_hash = $1`, hashB,
	).Scan(&preBRevoked)
	if err != nil {
		t.Fatalf("precondition check — querying token B: %v", err)
	}
	if preBRevoked {
		t.Fatal("precondition failed: token B should still be active before theft response")
	}

	// Step 3: Simulate presenting the revoked token A — look it up and check.
	var revokedA bool
	var tokenAUserID string
	err = pool.QueryRow(ctx,
		`SELECT user_id, revoked FROM refresh_tokens WHERE token_hash = $1`, hashA,
	).Scan(&tokenAUserID, &revokedA)
	if err != nil {
		t.Fatalf("looking up revoked token A: %v", err)
	}
	if !revokedA {
		t.Fatal("expected token A to be revoked")
	}

	// Step 4: Theft detected! Revoke ALL tokens for this user.
	tag, err = pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = TRUE WHERE user_id = $1`, tokenAUserID)
	if err != nil {
		t.Fatalf("revoking all tokens for user (theft response): %v", err)
	}
	// Both tokens should be affected (A was already revoked, B was not).
	// Postgres UPDATE sets the value even if it was already TRUE, so both
	// rows are "affected".
	if tag.RowsAffected() != 2 {
		t.Fatalf("expected 2 rows affected (theft revoke-all), got %d", tag.RowsAffected())
	}

	// Step 5: Verify no active tokens remain.
	var activeCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens
		 WHERE user_id = $1 AND revoked = FALSE`, tokenAUserID,
	).Scan(&activeCount)
	if err != nil {
		t.Fatalf("counting active tokens after theft detection: %v", err)
	}
	if activeCount != 0 {
		t.Errorf("expected 0 active tokens after theft detection, got %d", activeCount)
	}

	// Verify token B specifically is revoked.
	var revokedB bool
	err = pool.QueryRow(ctx,
		`SELECT revoked FROM refresh_tokens WHERE token_hash = $1`, hashB,
	).Scan(&revokedB)
	if err != nil {
		t.Fatalf("querying token B: %v", err)
	}
	if !revokedB {
		t.Error("expected token B to be revoked after theft detection")
	}
}

// ---------------------------------------------------------------------------
// Registration DB flow
// ---------------------------------------------------------------------------

// TestRegistrationFlow_InsertUserAndOTP_VerifyOTP exercises the database-level
// registration flow: insert an unverified user and an OTP, then simulate OTP
// verification by marking the OTP as used and setting email_verified = true.
// Token generation is not tested here (that is auth service unit test territory).
func TestRegistrationFlow_InsertUserAndOTP_VerifyOTP(t *testing.T) {
	pool := setupTestPool(t)

	ctx, cancel := context.WithTimeout(context.Background(), testQueryTimeout)
	defer cancel()

	const (
		email    = "flow@example.com"
		username = "user_flow1234"
		password = "flow-password"
		otpCode  = "777666"
	)
	purpose := mail.PurposeEmailVerification
	expiresAt := time.Now().UTC().Add(10 * time.Minute)

	hash, err := bcrypt.GenerateFromPassword([]byte(password), testBcryptCost)
	if err != nil {
		t.Fatalf("hashing password: %v", err)
	}

	// Step 1: Insert an unverified user.
	// Mirrors production INSERT shape (see handler auth.go Register): the
	// handler omits the name column, relying on the DB default ('').
	var userID string
	err = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, username, email_verified)
		 VALUES ($1, $2, $3, false)
		 RETURNING id`,
		email, string(hash), username,
	).Scan(&userID)
	if err != nil {
		t.Fatalf("inserting user: %v", err)
	}

	// Step 2: Insert an OTP for the user.
	var otpID string
	otpCodeHash, err := auth.HashOTP(otpCode)
	if err != nil {
		t.Fatalf("hashing OTP: %v", err)
	}
	err = pool.QueryRow(ctx,
		`INSERT INTO otp_codes (user_id, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		userID, otpCodeHash, purpose, expiresAt,
	).Scan(&otpID)
	if err != nil {
		t.Fatalf("inserting OTP: %v", err)
	}

	// Step 3: Simulate successful OTP verification — mark OTP as used.
	tag, err := pool.Exec(ctx,
		`UPDATE otp_codes SET used = TRUE WHERE id = $1`, otpID)
	if err != nil {
		t.Fatalf("marking OTP as used: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected 1 OTP row affected, got %d", tag.RowsAffected())
	}

	// Step 4: Set email_verified = true on the user.
	// Mirrors production shape (see handler auth.go VerifyOTP): the handler
	// updates by email without touching updated_at.
	tag, err = pool.Exec(ctx,
		`UPDATE users SET email_verified = true WHERE lower(email) = $1`, email)
	if err != nil {
		t.Fatalf("setting email_verified: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected 1 user row affected, got %d", tag.RowsAffected())
	}

	// --- Verify final state ---

	// OTP should be marked used.
	var otpUsed bool
	err = pool.QueryRow(ctx,
		`SELECT used FROM otp_codes WHERE id = $1`, otpID,
	).Scan(&otpUsed)
	if err != nil {
		t.Fatalf("querying OTP used flag: %v", err)
	}
	if !otpUsed {
		t.Error("expected OTP to be marked as used")
	}

	// User should be verified and password hash should match.
	var emailVerified bool
	var storedHash string
	err = pool.QueryRow(ctx,
		`SELECT email_verified, password_hash FROM users WHERE id = $1`, userID,
	).Scan(&emailVerified, &storedHash)
	if err != nil {
		t.Fatalf("querying user after verification: %v", err)
	}
	if !emailVerified {
		t.Error("expected email_verified = true after OTP verification")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password)); err != nil {
		t.Errorf("stored password hash does not match original password: %v", err)
	}

	// No valid (unused, unexpired) OTPs should remain for this user/purpose.
	var validCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM otp_codes
		 WHERE user_id = $1
		   AND purpose = $2
		   AND used = FALSE
		   AND expires_at > now()`,
		userID, purpose,
	).Scan(&validCount)
	if err != nil {
		t.Fatalf("counting remaining valid OTPs: %v", err)
	}
	if validCount != 0 {
		t.Errorf("expected 0 valid OTPs after verification, got %d", validCount)
	}
}
