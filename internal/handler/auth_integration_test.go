//go:build integration

// auth_integration_test.go — HTTP-layer integration tests for the auth
// endpoints. Every test calls setupTestServer which resets the shared Postgres
// instance, so these tests MUST NOT run in parallel (no t.Parallel calls).
// The go test runner executes them sequentially within this package by default;
// keep it that way.

package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/database"
	"github.com/IsorilovA/pauza-server/internal/handler"
	"github.com/IsorilovA/pauza-server/internal/mail"
	authmw "github.com/IsorilovA/pauza-server/internal/middleware"
	"github.com/IsorilovA/pauza-server/migrations"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

const (
	testJWTSecret          = "integration-test-secret-key"
	testJWTAccessTokenTTL  = 15 * time.Minute
	testJWTRefreshTokenTTL = 7 * 24 * time.Hour

	// testMaxBodySize mirrors the production request body limit in
	// internal/server/server.go so the integration-test router has the
	// same defense-in-depth behaviour.
	testMaxBodySize = 1 << 20 // 1 MiB

	// msgInvalidCredentials is the anti-enumeration message returned by
	// the Login handler for wrong-password and non-existent-email cases.
	msgInvalidCredentials = "Invalid email or password"
)

// captureSender is an in-memory mail.EmailSender that records every OTP sent.
type captureSender struct {
	mu    sync.Mutex
	calls []capturedOTP
}

// Compile-time interface check: captureSender must implement mail.EmailSender
// (the type used by AuthHandler's constructor and struct field).
// EmailSender is a type alias for mail.Sender, so both names work identically;
// we use EmailSender here for consistency with the handler package.
var _ mail.EmailSender = (*captureSender)(nil)

type capturedOTP struct {
	To      string
	OTP     string
	Purpose string
}

func (s *captureSender) SendOTP(_ context.Context, to, otp, purpose string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, capturedOTP{To: to, OTP: otp, Purpose: purpose})
	return nil
}

// lastOTP returns the most recently captured OTP for the given email and
// purpose, or an error if none was found.
func (s *captureSender) lastOTP(email, purpose string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.calls) - 1; i >= 0; i-- {
		c := s.calls[i]
		if c.To == email && c.Purpose == purpose {
			return c.OTP, nil
		}
	}
	return "", fmt.Errorf("no OTP found for %s / %s", email, purpose)
}

// testDatabaseURL reads TEST_DATABASE_URL from the environment and skips the
// test when unset.
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping integration test")
	}
	return url
}

// setupTestServer resets the test database, applies migrations, and returns
// an httptest.Server backed by the full chi router, the underlying
// pgxpool.Pool for direct DB access, and the capture sender so tests can
// retrieve OTPs.
func setupTestServer(t *testing.T) (*httptest.Server, *pgxpool.Pool, *captureSender) {
	t.Helper()

	dbURL := testDatabaseURL(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("creating test pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("pinging test database: %v", err)
	}

	// Reset database to a clean state.
	resetDatabase(t, pool)

	// Apply migrations.
	if err := database.RunMigrations(dbURL, migrations.FS); err != nil {
		pool.Close()
		t.Fatalf("applying migrations: %v", err)
	}

	mailer := &captureSender{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Build a chi router that mirrors the production route tree in
	// internal/server/server.go: a single /api/v1 route containing both
	// public auth routes and a protected group gated by JWT middleware.
	// The middleware stack is minimal (no respondRequestID or requestLogger)
	// and the mailer is an in-memory stub.
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	// Defense-in-depth: limit request bodies (mirrors production server.go).
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, testMaxBodySize)
			next.ServeHTTP(w, r)
		})
	})

	r.Get("/health", handler.Health(pool))

	authHandler := handler.NewAuthHandler(
		pool, mailer, testJWTSecret,
		testJWTAccessTokenTTL, testJWTRefreshTokenTTL, logger,
	)

	r.Route("/api/v1", func(r chi.Router) {
		// Public auth routes (no JWT required).
		r.Route("/auth", func(r chi.Router) {
			r.Post("/register", authHandler.Register)
			r.Post("/verify-otp", authHandler.VerifyOTP)
			r.Post("/login", authHandler.Login)
			r.Post("/refresh", authHandler.Refresh)
			r.Post("/forgot-password", authHandler.ForgotPassword)
			r.Post("/reset-password", authHandler.ResetPassword)
		})

		// Protected routes (JWT required).
		r.Group(func(r chi.Router) {
			r.Use(authmw.JWTAuth(testJWTSecret))

			// Placeholder: mirrors production server.go /me stub.
			r.Get("/me", func(w http.ResponseWriter, _ *http.Request) {
				apperror.NotFound(w, "user profile endpoint not yet implemented")
			})
		})
	})

	ts := httptest.NewServer(r)
	t.Cleanup(func() {
		ts.Close()
		pool.Close()
	})

	return ts, pool, mailer
}

// resetDatabase drops the public schema and recreates it so that every test
// starts from a clean slate. This mirrors the logic in
// internal/database/testhelper_test.go but lives here because it is in a
// different test package and cannot be shared directly.
func resetDatabase(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for _, q := range []string{
		"DROP SCHEMA public CASCADE",
		"CREATE SCHEMA public",
		"GRANT ALL ON SCHEMA public TO current_user",
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			pool.Close()
			t.Fatalf("resetting database (%s): %v", q, err)
		}
	}
}

// postJSON sends a POST request with a JSON body to the test server and
// returns the response.
func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()

	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling request body: %v", err)
	}

	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

// decodeJSON reads and decodes the response body into the target.
func decodeJSON(t *testing.T, resp *http.Response, target any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
}

// readBody reads and returns the entire response body as bytes.
func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	return b
}

// authResponse mirrors the handler's auth response shape for decoding.
type authResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	User         struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
}

// refreshResponse mirrors the handler's refresh response shape for decoding.
type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// discardBody reads and closes the response body without decoding it.
// Use this instead of bare resp.Body.Close() so the full body is drained,
// ensuring the underlying transport can reuse the connection.
func discardBody(t *testing.T, resp *http.Response) {
	t.Helper()
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("draining response body: %v", err)
	}
}

// registerAndVerify is a convenience helper that runs the Register + Verify-OTP
// flow for the given email/password and returns the authResponse. It fails the
// test immediately if any step does not succeed.
func registerAndVerify(t *testing.T, tsURL string, mailer *captureSender, email, password string) authResponse {
	t.Helper()

	// Register.
	resp := postJSON(t, tsURL+"/api/v1/auth/register", map[string]string{
		"email": email, "password": password,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("register: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Retrieve OTP and verify.
	otp, err := mailer.lastOTP(email, mail.PurposeEmailVerification)
	if err != nil {
		t.Fatalf("no OTP captured for %s: %v", email, err)
	}

	resp = postJSON(t, tsURL+"/api/v1/auth/verify-otp", map[string]string{
		"email": email, "otp": otp,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("verify-otp: expected 200, got %d: %s", resp.StatusCode, body)
	}
	var ar authResponse
	decodeJSON(t, resp, &ar)
	return ar
}

// ---------------------------------------------------------------------------
// Happy path tests
// ---------------------------------------------------------------------------

// TestIntegration_RegisterVerifyLogin exercises the full happy-path flow:
// Register -> Verify-OTP -> Login.
func TestIntegration_RegisterVerifyLogin(t *testing.T) {
	ts, _, mailer := setupTestServer(t)

	const email = "happy@example.com"
	const password = "StrongPass123!"

	// Step 1: Register.
	resp := postJSON(t, ts.URL+"/api/v1/auth/register", map[string]string{
		"email":    email,
		"password": password,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("register: expected 200, got %d: %s", resp.StatusCode, body)
	}
	var regResp struct {
		OTPRequired bool `json:"otp_required"`
	}
	decodeJSON(t, resp, &regResp)
	if !regResp.OTPRequired {
		t.Fatal("register: expected otp_required = true")
	}

	// Step 2: Retrieve captured OTP and verify.
	otp, err := mailer.lastOTP(email, mail.PurposeEmailVerification)
	if err != nil {
		t.Fatalf("no OTP captured: %v", err)
	}

	resp = postJSON(t, ts.URL+"/api/v1/auth/verify-otp", map[string]string{
		"email": email,
		"otp":   otp,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("verify-otp: expected 200, got %d: %s", resp.StatusCode, body)
	}
	var verifyResp authResponse
	decodeJSON(t, resp, &verifyResp)
	if verifyResp.AccessToken == "" {
		t.Error("verify-otp: expected non-empty access_token")
	}
	if verifyResp.RefreshToken == "" {
		t.Error("verify-otp: expected non-empty refresh_token")
	}
	if verifyResp.User.Email != email {
		t.Errorf("verify-otp: expected email %q, got %q", email, verifyResp.User.Email)
	}

	// Step 3: Login with the same credentials.
	resp = postJSON(t, ts.URL+"/api/v1/auth/login", map[string]string{
		"email":    email,
		"password": password,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("login: expected 200, got %d: %s", resp.StatusCode, body)
	}
	var loginResp authResponse
	decodeJSON(t, resp, &loginResp)
	if loginResp.AccessToken == "" {
		t.Error("login: expected non-empty access_token")
	}
	if loginResp.RefreshToken == "" {
		t.Error("login: expected non-empty refresh_token")
	}
	if loginResp.User.Email != email {
		t.Errorf("login: expected email %q, got %q", email, loginResp.User.Email)
	}
}

// TestIntegration_RefreshTokenRotation exercises refresh token rotation:
// Register -> Verify -> Login -> Refresh -> old token rejected.
func TestIntegration_RefreshTokenRotation(t *testing.T) {
	ts, _, mailer := setupTestServer(t)

	const email = "refresh@example.com"
	const password = "StrongPass123!"

	// Register and verify.
	verifyResp := registerAndVerify(t, ts.URL, mailer, email, password)
	originalRefreshToken := verifyResp.RefreshToken

	// Step 1: Refresh using the original token.
	resp := postJSON(t, ts.URL+"/api/v1/auth/refresh", map[string]string{
		"refresh_token": originalRefreshToken,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("refresh: expected 200, got %d: %s", resp.StatusCode, body)
	}
	var refreshResp refreshResponse
	decodeJSON(t, resp, &refreshResp)
	if refreshResp.AccessToken == "" {
		t.Error("refresh: expected non-empty access_token")
	}
	if refreshResp.RefreshToken == "" {
		t.Error("refresh: expected non-empty refresh_token")
	}
	newRefreshToken := refreshResp.RefreshToken

	// Step 2: The old token should now be rejected (it was rotated/revoked).
	resp = postJSON(t, ts.URL+"/api/v1/auth/refresh", map[string]string{
		"refresh_token": originalRefreshToken,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("refresh with old token: expected 401, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Step 3: The new token should still work.
	resp = postJSON(t, ts.URL+"/api/v1/auth/refresh", map[string]string{
		"refresh_token": newRefreshToken,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("refresh with new token: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)
}

// TestIntegration_ForgotResetLogin exercises the password reset flow:
// Register -> Verify -> Forgot-password -> Reset-password -> Login with new password.
func TestIntegration_ForgotResetLogin(t *testing.T) {
	ts, _, mailer := setupTestServer(t)

	const email = "reset@example.com"
	const originalPassword = "OriginalPass123!"
	const newPassword = "BrandNewPass456!"

	// Register and verify.
	registerAndVerify(t, ts.URL, mailer, email, originalPassword)

	// Step 1: Forgot password.
	resp := postJSON(t, ts.URL+"/api/v1/auth/forgot-password", map[string]string{
		"email": email,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("forgot-password: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Step 2: Retrieve the password reset OTP.
	resetOTP, err := mailer.lastOTP(email, mail.PurposePasswordReset)
	if err != nil {
		t.Fatalf("no password reset OTP captured: %v", err)
	}

	// Step 3: Reset password.
	resp = postJSON(t, ts.URL+"/api/v1/auth/reset-password", map[string]string{
		"email":        email,
		"otp":          resetOTP,
		"new_password": newPassword,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("reset-password: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Step 4: Login with the old password should fail.
	resp = postJSON(t, ts.URL+"/api/v1/auth/login", map[string]string{
		"email": email, "password": originalPassword,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("login with old password: expected 401, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Step 5: Login with the new password should succeed.
	resp = postJSON(t, ts.URL+"/api/v1/auth/login", map[string]string{
		"email": email, "password": newPassword,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("login with new password: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)
}

// ---------------------------------------------------------------------------
// Error path tests
// ---------------------------------------------------------------------------

// TestIntegration_RegisterDuplicateVerifiedEmail verifies that registering with
// an email that already belongs to a verified user returns 409 CONFLICT.
func TestIntegration_RegisterDuplicateVerifiedEmail(t *testing.T) {
	ts, _, mailer := setupTestServer(t)

	const email = "dup@example.com"
	const password = "StrongPass123!"

	// Register and verify the first user.
	registerAndVerify(t, ts.URL, mailer, email, password)

	// Attempt to register the same email again -> 409.
	resp := postJSON(t, ts.URL+"/api/v1/auth/register", map[string]string{
		"email": email, "password": password,
	})
	if resp.StatusCode != http.StatusConflict {
		body := readBody(t, resp)
		t.Fatalf("duplicate register: expected 409, got %d: %s", resp.StatusCode, body)
	}
	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeConflict {
		t.Errorf("duplicate register: expected code %q, got %q", apperror.CodeConflict, errResp.Error.Code)
	}
}

// TestIntegration_RegisterStaleUnverifiedCleanup verifies that re-registering
// with an email that has an unverified user cleans up the stale record and
// allows a fresh registration.
func TestIntegration_RegisterStaleUnverifiedCleanup(t *testing.T) {
	ts, _, mailer := setupTestServer(t)

	const email = "stale@example.com"
	const password = "StrongPass123!"

	// First registration (unverified – we never verify the OTP).
	resp := postJSON(t, ts.URL+"/api/v1/auth/register", map[string]string{
		"email": email, "password": password,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("first register: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Second registration with the same email — should clean up the stale
	// unverified user and succeed.
	resp = postJSON(t, ts.URL+"/api/v1/auth/register", map[string]string{
		"email": email, "password": password,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("re-register: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Verify the new registration works end-to-end.
	otp, err := mailer.lastOTP(email, mail.PurposeEmailVerification)
	if err != nil {
		t.Fatalf("no OTP captured after re-register: %v", err)
	}

	resp = postJSON(t, ts.URL+"/api/v1/auth/verify-otp", map[string]string{
		"email": email, "otp": otp,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("verify-otp after re-register: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)
}

// TestIntegration_LoginWrongPassword verifies that logging in with a wrong
// password returns 401.
func TestIntegration_LoginWrongPassword(t *testing.T) {
	ts, _, mailer := setupTestServer(t)

	const email = "wrongpw@example.com"
	const password = "CorrectPass123!"

	// Register and verify.
	registerAndVerify(t, ts.URL, mailer, email, password)

	// Login with wrong password -> 401.
	resp := postJSON(t, ts.URL+"/api/v1/auth/login", map[string]string{
		"email": email, "password": "WrongPassword999!",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("login wrong password: expected 401, got %d: %s", resp.StatusCode, body)
	}
	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeUnauthorized {
		t.Errorf("expected code %q, got %q", apperror.CodeUnauthorized, errResp.Error.Code)
	}
	if errResp.Error.Message != msgInvalidCredentials {
		t.Errorf("expected anti-enumeration message, got %q", errResp.Error.Message)
	}
}

// TestIntegration_LoginNonExistentEmail verifies that logging in with a
// non-existent email returns 401 with the same message as wrong password
// (anti-enumeration).
func TestIntegration_LoginNonExistentEmail(t *testing.T) {
	ts, _, _ := setupTestServer(t)

	resp := postJSON(t, ts.URL+"/api/v1/auth/login", map[string]string{
		"email": "nobody@example.com", "password": "SomePass123!",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("login non-existent: expected 401, got %d: %s", resp.StatusCode, body)
	}
	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeUnauthorized {
		t.Errorf("expected code %q, got %q", apperror.CodeUnauthorized, errResp.Error.Code)
	}
	if errResp.Error.Message != msgInvalidCredentials {
		t.Errorf("expected anti-enumeration message, got %q", errResp.Error.Message)
	}
}

// TestIntegration_LoginUnverifiedUser verifies that logging in with an
// unverified user returns 401.
func TestIntegration_LoginUnverifiedUser(t *testing.T) {
	ts, _, _ := setupTestServer(t)

	const email = "unverified@example.com"
	const password = "StrongPass123!"

	// Register but do NOT verify.
	resp := postJSON(t, ts.URL+"/api/v1/auth/register", map[string]string{
		"email": email, "password": password,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("register: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Login should fail because the user is not verified.
	resp = postJSON(t, ts.URL+"/api/v1/auth/login", map[string]string{
		"email": email, "password": password,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("login unverified: expected 401, got %d: %s", resp.StatusCode, body)
	}
	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeUnauthorized {
		t.Errorf("expected code %q, got %q", apperror.CodeUnauthorized, errResp.Error.Code)
	}
	// The handler queries WHERE email_verified = true, so an unverified user
	// is indistinguishable from a non-existent one — same anti-enumeration
	// message must be returned.
	if errResp.Error.Message != msgInvalidCredentials {
		t.Errorf("expected anti-enumeration message, got %q", errResp.Error.Message)
	}
}

// TestIntegration_RefreshRevokedTokenRevokesAll verifies theft detection:
// using a revoked refresh token causes all tokens for that user to be revoked.
func TestIntegration_RefreshRevokedTokenRevokesAll(t *testing.T) {
	ts, _, mailer := setupTestServer(t)

	const email = "theft@example.com"
	const password = "StrongPass123!"

	// Register and verify.
	verifyResp := registerAndVerify(t, ts.URL, mailer, email, password)
	tokenA := verifyResp.RefreshToken

	// Rotate: use token A to get token B.
	resp := postJSON(t, ts.URL+"/api/v1/auth/refresh", map[string]string{
		"refresh_token": tokenA,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("refresh A->B: expected 200, got %d: %s", resp.StatusCode, body)
	}
	var refreshResp refreshResponse
	decodeJSON(t, resp, &refreshResp)

	tokenB := refreshResp.RefreshToken

	// Simulate theft: replay the already-revoked token A.
	resp = postJSON(t, ts.URL+"/api/v1/auth/refresh", map[string]string{
		"refresh_token": tokenA,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("replay revoked token A: expected 401, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Token B should now also be revoked (theft detection revoked all).
	resp = postJSON(t, ts.URL+"/api/v1/auth/refresh", map[string]string{
		"refresh_token": tokenB,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("token B after theft detection: expected 401, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)
}

// TestIntegration_ForgotPasswordNonExistentEmail verifies that forgot-password
// with a non-existent email returns 200 (anti-enumeration).
func TestIntegration_ForgotPasswordNonExistentEmail(t *testing.T) {
	ts, _, _ := setupTestServer(t)

	resp := postJSON(t, ts.URL+"/api/v1/auth/forgot-password", map[string]string{
		"email": "ghost@example.com",
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("forgot-password non-existent: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)
}

// TestIntegration_VerifyOTPWrongCode3x verifies that auth.MaxOTPAttempts wrong
// OTP attempts exhaust the code and a subsequent attempt returns 429 (rate limited).
func TestIntegration_VerifyOTPWrongCode3x(t *testing.T) {
	ts, _, mailer := setupTestServer(t)

	const email = "ratelimit@example.com"
	const password = "StrongPass123!"

	// Register (creates user + OTP).
	resp := postJSON(t, ts.URL+"/api/v1/auth/register", map[string]string{
		"email": email, "password": password,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("register: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Retrieve the real OTP so we can deterministically pick a different one.
	realOTP, err := mailer.lastOTP(email, mail.PurposeEmailVerification)
	if err != nil {
		t.Fatalf("no OTP captured: %v", err)
	}
	wrongOTP := "000000"
	if wrongOTP == realOTP {
		wrongOTP = "999999"
	}

	// Submit wrong OTP auth.MaxOTPAttempts times to exhaust attempts.
	for i := 1; i <= auth.MaxOTPAttempts; i++ {
		resp = postJSON(t, ts.URL+"/api/v1/auth/verify-otp", map[string]string{
			"email": email, "otp": wrongOTP,
		})
		if resp.StatusCode != http.StatusUnauthorized {
			body := readBody(t, resp)
			t.Fatalf("wrong OTP attempt %d: expected 401, got %d: %s", i, resp.StatusCode, body)
		}
		discardBody(t, resp)
	}

	// Next attempt should return 429 (too many attempts).
	resp = postJSON(t, ts.URL+"/api/v1/auth/verify-otp", map[string]string{
		"email": email, "otp": wrongOTP,
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		body := readBody(t, resp)
		t.Fatalf("attempt after max: expected 429, got %d: %s", resp.StatusCode, body)
	}
	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeRateLimited {
		t.Errorf("expected code %q, got %q", apperror.CodeRateLimited, errResp.Error.Code)
	}
}

// TestIntegration_VerifyOTPExpired verifies that an expired OTP returns 401.
func TestIntegration_VerifyOTPExpired(t *testing.T) {
	ts, pool, _ := setupTestServer(t)

	const email = "expired-otp@example.com"
	const password = "StrongPass123!"

	// Register to create user + OTP.
	resp := postJSON(t, ts.URL+"/api/v1/auth/register", map[string]string{
		"email": email, "password": password,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("register: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Expire the OTP by updating its expires_at in the database directly.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Set the OTP's expires_at to the past.
	tag, err := pool.Exec(ctx,
		`UPDATE otp_codes SET expires_at = now() - interval '1 hour'
		 WHERE user_email = $1 AND purpose = 'email_verification'`,
		email,
	)
	if err != nil {
		t.Fatalf("expiring OTP: %v", err)
	}
	if tag.RowsAffected() == 0 {
		t.Fatal("no OTP rows updated — expected at least one")
	}

	// Attempt to verify with any OTP code — should fail because it's expired.
	resp = postJSON(t, ts.URL+"/api/v1/auth/verify-otp", map[string]string{
		"email": email, "otp": "123456",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("verify expired OTP: expected 401, got %d: %s", resp.StatusCode, body)
	}
	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeUnauthorized {
		t.Errorf("expected code %q, got %q", apperror.CodeUnauthorized, errResp.Error.Code)
	}
}
