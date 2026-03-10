//go:build integration

// auth_integration_test.go — HTTP-layer integration tests for the auth
// endpoints. Every test calls setupTestServer which resets the shared Postgres
// instance, so these tests MUST NOT run in parallel (no t.Parallel calls).
// The go test runner executes them sequentially within this package by default;
// keep it that way.
//
// The test server is built using the real production server.New(...) so that
// integration tests exercise the full middleware stack (rate limiting, request
// ID, structured recovery, body limits, etc.).

package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/database"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/server"
	"github.com/IsorilovA/pauza-server/migrations"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

const (
	testJWTSecret          = "integration-test-secret-key-32b!"
	testJWTAccessTokenTTL  = 15 * time.Minute
	testJWTRefreshTokenTTL = 7 * 24 * time.Hour

	// msgInvalidCredentials is the anti-enumeration message returned by
	// the Login handler for wrong-password and non-existent-email cases.
	msgInvalidCredentials = "Invalid email or password"
)

// captureSender is an in-memory mail.Sender that records every OTP sent.
type captureSender struct {
	mu       sync.Mutex
	calls    []capturedOTP
	probeErr error
	sendErr  error
}

// Compile-time interface check: captureSender must implement mail.Sender
// (the type used by AuthHandler's constructor and struct field).
var _ mail.Sender = (*captureSender)(nil)

type capturedOTP struct {
	To      string
	OTP     string
	Purpose string
}

func (s *captureSender) Probe(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.probeErr
}

func (s *captureSender) SendOTP(_ context.Context, to, otp, purpose string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, capturedOTP{To: to, OTP: otp, Purpose: purpose})
	return s.sendErr
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

// testConfig returns a config.Config suitable for integration tests. Rate
// limits are set high so that rapid sequential test requests are not throttled.
func testConfig() *config.Config {
	return &config.Config{
		Port:                 8080,
		LogLevel:             "info",
		JWTSecret:            testJWTSecret,
		JWTAccessTokenTTL:    testJWTAccessTokenTTL,
		JWTRefreshTokenTTL:   testJWTRefreshTokenTTL,
		SMTPHost:             "localhost",
		SMTPPort:             1025,
		SMTPUsername:         "test",
		SMTPPassword:         "test",
		SMTPFrom:             "test@example.com",
		SMTPTimeout:          30 * time.Second,
		SMTPTLSPolicy:        "none",
		AuthRateLimit:        10000,
		AuthRateWindow:       time.Minute,
		VerifyOTPRateLimit:   10000,
		VerifyOTPRateWindow:  time.Minute,
		GeneralAPIRateLimit:  10000,
		GeneralAPIRateWindow: time.Minute,
		SyncRateLimit:        10000,
		SyncRateWindow:       time.Minute,
	}
}

// setupTestServerWithMailer resets the test database, applies migrations, and
// returns an httptest.Server backed by the real production server.New(...)
// router and the underlying pgxpool.Pool for direct DB access. The caller
// provides the mail.Sender implementation, which allows tests to inject
// controllable or failing senders.
func setupTestServerWithMailer(t *testing.T, mailer mail.Sender) (*httptest.Server, *pgxpool.Pool) {
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
	if err := database.RunMigrations(slog.New(slog.NewTextHandler(io.Discard, nil)), dbURL, migrations.FS); err != nil {
		pool.Close()
		t.Fatalf("applying migrations: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testConfig()

	// Use the production server.New to build the full HTTP stack (router,
	// middleware, rate limiters, etc.) so integration tests exercise the
	// same code path as production.
	srv, cleanup := server.New(cfg, logger, pool, mailer, nil)

	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(func() {
		ts.Close()
		cleanup()
		pool.Close()
	})

	return ts, pool
}

// setupTestServer resets the test database, applies migrations, and returns
// an httptest.Server backed by the full chi router, the underlying
// pgxpool.Pool for direct DB access, and the capture sender so tests can
// retrieve OTPs.
func setupTestServer(t *testing.T) (*httptest.Server, *pgxpool.Pool, *captureSender) {
	t.Helper()
	mailer := &captureSender{}
	ts, pool := setupTestServerWithMailer(t, mailer)
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

// getWithAuth sends a GET request with a Bearer token Authorization header
// and returns the response.
func getWithAuth(t *testing.T, url, accessToken string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("creating GET request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// patchJSONWithAuth sends a PATCH request with a JSON body and Bearer token.
func patchJSONWithAuth(t *testing.T, url, accessToken string, body any) *http.Response {
	t.Helper()

	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling PATCH body: %v", err)
	}

	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("creating PATCH request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", url, err)
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
type authUserResponse struct {
	ID                 string                  `json:"id"`
	Email              string                  `json:"email"`
	Name               string                  `json:"name"`
	Username           string                  `json:"username"`
	ProfilePictureURL  *string                 `json:"profile_picture_url"`
	LeaderboardVisible bool                    `json:"leaderboard_visible"`
	CreatedAt          string                  `json:"created_at"`
	Subscription       *meSubscriptionResponse `json:"subscription"`
}

type authResponse struct {
	AccessToken  string           `json:"access_token"`
	RefreshToken string           `json:"refresh_token"`
	User         authUserResponse `json:"user"`
}

// registerResponse mirrors the handler's register response shape for decoding.
type registerResponse struct {
	OTPRequired bool `json:"otp_required"`
}

// meSubscriptionResponse mirrors the handler's /me entitlement shape.
type meSubscriptionResponse struct {
	Entitlement      string  `json:"entitlement"`
	IsActive         bool    `json:"is_active"`
	CurrentPeriodEnd *string `json:"current_period_end"`
}

// meResponse mirrors the handler's /me response shape for decoding.
type meResponse struct {
	ID                 string                  `json:"id"`
	Email              string                  `json:"email"`
	Name               string                  `json:"name"`
	Username           string                  `json:"username"`
	ProfilePictureURL  *string                 `json:"profile_picture_url"`
	LeaderboardVisible bool                    `json:"leaderboard_visible"`
	CreatedAt          string                  `json:"created_at"`
	Subscription       *meSubscriptionResponse `json:"subscription"`
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
	var regResp registerResponse
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

// TestIntegration_GetMe exercises the GET /api/v1/me happy path end-to-end:
// Register -> Verify-OTP -> GET /me. It verifies the response shape matches
// the BACKEND_SPEC Section 5.3 contract: all top-level user fields are
// present and subscription is null for a newly verified user with no plan.
func TestIntegration_GetMe(t *testing.T) {
	ts, _, mailer := setupTestServer(t)

	const email = "getme@example.com"
	const password = "StrongPass123!"

	// Register and verify to obtain an access token.
	ar := registerAndVerify(t, ts.URL, mailer, email, password)
	if ar.AccessToken == "" {
		t.Fatal("registerAndVerify: expected non-empty access_token")
	}

	// Call GET /api/v1/me with the access token.
	resp := getWithAuth(t, ts.URL+"/api/v1/me", ar.AccessToken)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("GET /me: expected 200, got %d: %s", resp.StatusCode, body)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("GET /me: Content-Type = %q, want %q", ct, "application/json")
	}

	// Decode into a map to verify the full response shape without
	// depending on internal struct definitions.
	var me map[string]any
	decodeJSON(t, resp, &me)

	// Required top-level string fields per BACKEND_SPEC §5.3.
	for _, field := range []string{"id", "email", "name", "username", "created_at"} {
		val, ok := me[field]
		if !ok {
			t.Errorf("GET /me: missing field %q", field)
			continue
		}
		if _, isStr := val.(string); !isStr {
			t.Errorf("GET /me: field %q should be a string, got %T", field, val)
		}
	}

	// email must match the registered address.
	if got, _ := me["email"].(string); got != email {
		t.Errorf("GET /me: email = %q, want %q", got, email)
	}

	// id must be a non-empty UUID.
	if got, _ := me["id"].(string); got == "" {
		t.Error("GET /me: id is empty")
	}

	// created_at must be a valid RFC3339 timestamp.
	if createdAt, _ := me["created_at"].(string); createdAt != "" {
		if _, err := time.Parse(time.RFC3339, createdAt); err != nil {
			t.Errorf("GET /me: created_at %q is not valid RFC3339: %v", createdAt, err)
		}
	}

	// leaderboard_visible must be a boolean.
	if _, ok := me["leaderboard_visible"].(bool); !ok {
		t.Errorf("GET /me: leaderboard_visible should be a bool, got %T", me["leaderboard_visible"])
	}

	// profile_picture_url must be present (null is fine for a new user).
	if _, ok := me["profile_picture_url"]; !ok {
		t.Error("GET /me: missing field profile_picture_url")
	}

	// subscription must be present and null for a newly verified user
	// with no active subscription.
	subVal, ok := me["subscription"]
	if !ok {
		t.Error("GET /me: missing field subscription")
	} else if subVal != nil {
		t.Errorf("GET /me: subscription should be null for new user, got %v", subVal)
	}
}

// TestIntegration_GetMe_WithActiveSubscription verifies that GET /api/v1/me
// includes the active entitlement contract when the user has an entitlement.
func TestIntegration_GetMe_WithActiveSubscription(t *testing.T) {
	ts, pool, mailer := setupTestServer(t)

	const email = "getme-sub@example.com"
	const password = "StrongPass123!"

	ar := registerAndVerify(t, ts.URL, mailer, email, password)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	periodEnd := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Second)

	_, err := pool.Exec(ctx, `
		INSERT INTO user_entitlements (id, user_id, entitlement, is_active, current_period_end, created_at, updated_at)
		VALUES ($1, $2, 'premium', true, $3, now(), now())
	`, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", ar.User.ID, periodEnd)
	if err != nil {
		t.Fatalf("inserting user entitlement: %v", err)
	}

	resp := getWithAuth(t, ts.URL+"/api/v1/me", ar.AccessToken)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("GET /me with subscription: expected 200, got %d: %s", resp.StatusCode, body)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("GET /me with subscription: Content-Type = %q, want %q", ct, "application/json")
	}

	var me meResponse
	decodeJSON(t, resp, &me)

	if me.Email != email {
		t.Errorf("GET /me with subscription: email = %q, want %q", me.Email, email)
	}
	if me.Subscription == nil {
		t.Fatal("GET /me with subscription: subscription = nil, want value")
	}
	if me.Subscription.Entitlement != "premium" {
		t.Errorf("subscription.entitlement = %q, want %q", me.Subscription.Entitlement, "premium")
	}
	if !me.Subscription.IsActive {
		t.Error("subscription.is_active = false, want true")
	}
	if me.Subscription.CurrentPeriodEnd == nil {
		t.Fatal("subscription.current_period_end = nil, want value")
	}
	if *me.Subscription.CurrentPeriodEnd != periodEnd.Format(time.RFC3339) {
		t.Errorf("subscription.current_period_end = %q, want %q", *me.Subscription.CurrentPeriodEnd, periodEnd.Format(time.RFC3339))
	}
}

// TestIntegration_GetMe_WithInactiveSubscriptionSnapshot verifies that stored
// inactive premium snapshots still appear in the profile response.
func TestIntegration_GetMe_WithInactiveSubscriptionSnapshot(t *testing.T) {
	ts, pool, mailer := setupTestServer(t)

	const email = "getme-inactive-sub@example.com"
	const password = "StrongPass123!"

	ar := registerAndVerify(t, ts.URL, mailer, email, password)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	periodEnd := time.Now().UTC().Add(14 * 24 * time.Hour).Truncate(time.Second)

	_, err := pool.Exec(ctx, `
		INSERT INTO user_entitlements (id, user_id, entitlement, is_active, current_period_end, created_at, updated_at)
		VALUES ($1, $2, 'premium', false, $3, now(), now())
	`, "cccccccc-cccc-cccc-cccc-cccccccccccc", ar.User.ID, periodEnd)
	if err != nil {
		t.Fatalf("inserting inactive user entitlement: %v", err)
	}

	resp := getWithAuth(t, ts.URL+"/api/v1/me", ar.AccessToken)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("GET /me with inactive subscription: expected 200, got %d: %s", resp.StatusCode, body)
	}

	var me meResponse
	decodeJSON(t, resp, &me)

	if me.Subscription == nil {
		t.Fatal("GET /me with inactive subscription: subscription = nil, want value")
	}
	if me.Subscription.Entitlement != "premium" {
		t.Errorf("subscription.entitlement = %q, want %q", me.Subscription.Entitlement, "premium")
	}
	if me.Subscription.IsActive {
		t.Error("subscription.is_active = true, want false")
	}
	if me.Subscription.CurrentPeriodEnd == nil {
		t.Fatal("subscription.current_period_end = nil, want value")
	}
	if *me.Subscription.CurrentPeriodEnd != periodEnd.Format(time.RFC3339) {
		t.Errorf("subscription.current_period_end = %q, want %q", *me.Subscription.CurrentPeriodEnd, periodEnd.Format(time.RFC3339))
	}
}

// TestIntegration_UpdateMe exercises the PATCH /api/v1/me happy path through
// the real server stack and verifies the updated profile contract.
func TestIntegration_UpdateMe(t *testing.T) {
	ts, _, mailer := setupTestServer(t)

	const email = "updateme@example.com"
	const password = "StrongPass123!"

	ar := registerAndVerify(t, ts.URL, mailer, email, password)

	resp := patchJSONWithAuth(t, ts.URL+"/api/v1/me", ar.AccessToken, map[string]any{
		"name":                "Alice Updated",
		"username":            "alice_updated",
		"leaderboard_visible": false,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("PATCH /me: expected 200, got %d: %s", resp.StatusCode, body)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("PATCH /me: Content-Type = %q, want %q", ct, "application/json")
	}

	var updated meResponse
	decodeJSON(t, resp, &updated)

	if updated.ID != ar.User.ID {
		t.Errorf("PATCH /me: id = %q, want %q", updated.ID, ar.User.ID)
	}
	if updated.Email != email {
		t.Errorf("PATCH /me: email = %q, want %q", updated.Email, email)
	}
	if updated.Name != "Alice Updated" {
		t.Errorf("PATCH /me: name = %q, want %q", updated.Name, "Alice Updated")
	}
	if updated.Username != "alice_updated" {
		t.Errorf("PATCH /me: username = %q, want %q", updated.Username, "alice_updated")
	}
	if updated.LeaderboardVisible {
		t.Error("PATCH /me: leaderboard_visible = true, want false")
	}
	if updated.Subscription != nil {
		t.Errorf("PATCH /me: subscription = %v, want nil", updated.Subscription)
	}
	if _, err := time.Parse(time.RFC3339, updated.CreatedAt); err != nil {
		t.Errorf("PATCH /me: created_at %q is not valid RFC3339: %v", updated.CreatedAt, err)
	}

	meResp := getWithAuth(t, ts.URL+"/api/v1/me", ar.AccessToken)
	if meResp.StatusCode != http.StatusOK {
		body := readBody(t, meResp)
		t.Fatalf("GET /me after PATCH: expected 200, got %d: %s", meResp.StatusCode, body)
	}

	var me meResponse
	decodeJSON(t, meResp, &me)
	if me.Name != "Alice Updated" {
		t.Errorf("GET /me after PATCH: name = %q, want %q", me.Name, "Alice Updated")
	}
	if me.Username != "alice_updated" {
		t.Errorf("GET /me after PATCH: username = %q, want %q", me.Username, "alice_updated")
	}
	if me.LeaderboardVisible {
		t.Errorf("GET /me after PATCH: leaderboard_visible = %v, want false", me.LeaderboardVisible)
	}
}

// TestIntegration_GetMe_DeletedUser verifies that a valid JWT for a user whose
// row has been deleted from the database returns 401 Unauthorized, not 404.
// This exercises the pgx.ErrNoRows → 401 mapping in GetMe.
func TestIntegration_GetMe_DeletedUser(t *testing.T) {
	ts, pool, mailer := setupTestServer(t)

	const email = "deleted-getme@example.com"
	const password = "StrongPass123!"

	// Register and verify to obtain a valid access token.
	ar := registerAndVerify(t, ts.URL, mailer, email, password)
	if ar.AccessToken == "" {
		t.Fatal("registerAndVerify: expected non-empty access_token")
	}

	// Sanity check: the token works before deletion.
	resp := getWithAuth(t, ts.URL+"/api/v1/me", ar.AccessToken)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("GET /me before delete: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Delete the user row directly in the database.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", ar.User.ID)
	if err != nil {
		t.Fatalf("deleting user row: %v", err)
	}

	// Call GET /me with the now-stale token; expect 401.
	resp = getWithAuth(t, ts.URL+"/api/v1/me", ar.AccessToken)
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("GET /me after delete: expected 401, got %d: %s", resp.StatusCode, body)
	}

	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeUnauthorized {
		t.Errorf("GET /me after delete: code = %q, want %q", errResp.Error.Code, apperror.CodeUnauthorized)
	}
}

// TestIntegration_UsernameAvailable_DeletedUser verifies that a valid JWT for a
// deleted user is rejected by username availability checks.
func TestIntegration_UsernameAvailable_DeletedUser(t *testing.T) {
	ts, pool, mailer := setupTestServer(t)

	const email = "deleted-username-available@example.com"
	const password = "StrongPass123!"

	ar := registerAndVerify(t, ts.URL, mailer, email, password)

	resp := getWithAuth(t, ts.URL+"/api/v1/me/username-available?username=still_free", ar.AccessToken)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("GET /me/username-available before delete: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", ar.User.ID)
	if err != nil {
		t.Fatalf("deleting user row: %v", err)
	}

	resp = getWithAuth(t, ts.URL+"/api/v1/me/username-available?username=still_free", ar.AccessToken)
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("GET /me/username-available after delete: expected 401, got %d: %s", resp.StatusCode, body)
	}

	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeUnauthorized {
		t.Errorf("GET /me/username-available after delete: code = %q, want %q", errResp.Error.Code, apperror.CodeUnauthorized)
	}
}

// TestIntegration_RefreshTokenRotation exercises refresh token rotation and
// theft detection: Register -> Verify -> Refresh -> validate new token ->
// replay old token -> all tokens revoked (BACKEND_SPEC §4 theft detection).
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

	// Step 2: Validate the new token works BEFORE replaying the old one.
	// This must come first because replaying the old token triggers theft
	// detection which revokes all tokens for the user.
	resp = postJSON(t, ts.URL+"/api/v1/auth/refresh", map[string]string{
		"refresh_token": newRefreshToken,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("refresh with new token: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Step 3: Replay the original (revoked) token — must be rejected.
	resp = postJSON(t, ts.URL+"/api/v1/auth/refresh", map[string]string{
		"refresh_token": originalRefreshToken,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("replay revoked token: expected 401, got %d: %s", resp.StatusCode, body)
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

// TestIntegration_ForgotPassword_NewIssuanceInvalidatesOlderResetOTP verifies
// that only the latest password-reset OTP remains usable after a second
// forgot-password request.
func TestIntegration_ForgotPassword_NewIssuanceInvalidatesOlderResetOTP(t *testing.T) {
	ts, pool, mailer := setupTestServer(t)

	const email = "reset-latest@example.com"
	const password = "OriginalPass123!"
	const newPassword = "BrandNewPass456!"

	registerAndVerify(t, ts.URL, mailer, email, password)

	resp := postJSON(t, ts.URL+"/api/v1/auth/forgot-password", map[string]string{"email": email})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("first forgot-password: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	firstOTP, err := mailer.lastOTP(email, mail.PurposePasswordReset)
	if err != nil {
		t.Fatalf("capturing first reset OTP: %v", err)
	}

	resp = postJSON(t, ts.URL+"/api/v1/auth/forgot-password", map[string]string{"email": email})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("second forgot-password: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	secondOTP, err := mailer.lastOTP(email, mail.PurposePasswordReset)
	if err != nil {
		t.Fatalf("capturing second reset OTP: %v", err)
	}
	if secondOTP == firstOTP {
		t.Fatal("expected second reset OTP to differ from first")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var activeResetOTPCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM otp_codes oc
		 JOIN users u ON u.id = oc.user_id
		 WHERE lower(u.email) = $1
		   AND oc.purpose = 'password_reset'
		   AND oc.used = false
		   AND oc.expires_at > now()`,
		email,
	).Scan(&activeResetOTPCount)
	if err != nil {
		t.Fatalf("counting active reset OTP rows: %v", err)
	}
	if activeResetOTPCount != 1 {
		t.Fatalf("active reset OTP count = %d, want 1", activeResetOTPCount)
	}

	resp = postJSON(t, ts.URL+"/api/v1/auth/reset-password", map[string]string{
		"email":        email,
		"otp":          firstOTP,
		"new_password": newPassword,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("reset with first OTP: expected 401, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	resp = postJSON(t, ts.URL+"/api/v1/auth/reset-password", map[string]string{
		"email":        email,
		"otp":          secondOTP,
		"new_password": newPassword,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("reset with second OTP: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)
}

// TestIntegration_ResetPassword_SuccessClearsRemainingResetOTPs verifies that
// after a successful password reset, no password-reset OTP rows remain for the
// user.
func TestIntegration_ResetPassword_SuccessClearsRemainingResetOTPs(t *testing.T) {
	ts, pool, mailer := setupTestServer(t)

	const email = "reset-clears@example.com"
	const password = "OriginalPass123!"
	const newPassword = "BrandNewPass456!"

	registerAndVerify(t, ts.URL, mailer, email, password)

	resp := postJSON(t, ts.URL+"/api/v1/auth/forgot-password", map[string]string{"email": email})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("forgot-password: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	resetOTP, err := mailer.lastOTP(email, mail.PurposePasswordReset)
	if err != nil {
		t.Fatalf("capturing reset OTP: %v", err)
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var resetOTPCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM otp_codes oc
		 JOIN users u ON u.id = oc.user_id
		 WHERE lower(u.email) = $1
		   AND oc.purpose = 'password_reset'`,
		email,
	).Scan(&resetOTPCount)
	if err != nil {
		t.Fatalf("counting remaining reset OTP rows: %v", err)
	}
	if resetOTPCount != 0 {
		t.Fatalf("remaining reset OTP rows = %d, want 0", resetOTPCount)
	}
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

// TestIntegration_RegisterDuplicateUnverifiedEmail verifies that registering
// with an email that already belongs to an unverified account now returns 409.
func TestIntegration_RegisterDuplicateUnverifiedEmail(t *testing.T) {
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

	// Second registration with the same email should now return 409.
	resp = postJSON(t, ts.URL+"/api/v1/auth/register", map[string]string{
		"email": email, "password": password,
	})
	if resp.StatusCode != http.StatusConflict {
		body := readBody(t, resp)
		t.Fatalf("duplicate unverified register: expected 409, got %d: %s", resp.StatusCode, body)
	}
	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeConflict {
		t.Errorf("duplicate unverified register: expected code %q, got %q", apperror.CodeConflict, errResp.Error.Code)
	}

	mailer.mu.Lock()
	callCount := len(mailer.calls)
	mailer.mu.Unlock()
	if callCount != 1 {
		t.Fatalf("SendOTP call count = %d, want %d", callCount, 1)
	}
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

// TestIntegration_ConcurrentRefreshSameToken verifies that when multiple
// goroutines attempt to refresh using the same token concurrently, exactly one
// succeeds (200) and the rest fail (401). Per BACKEND_SPEC §4, the losing
// goroutines' replay of the (now-revoked) original token triggers theft
// detection, which revokes all tokens for the user — so the winning new token
// is NOT expected to remain usable afterward.
func TestIntegration_ConcurrentRefreshSameToken(t *testing.T) {
	ts, _, mailer := setupTestServer(t)

	const email = "concurrent-refresh@example.com"
	const password = "StrongPass123!"
	const concurrency = 5

	// Register and verify to obtain an initial refresh token.
	verifyResp := registerAndVerify(t, ts.URL, mailer, email, password)
	originalRefreshToken := verifyResp.RefreshToken

	// Launch N goroutines that all try to refresh with the same token,
	// synchronized by a sync.WaitGroup acting as a start gate.
	type result struct {
		status  int
		body    refreshResponse
		rawBody string // captured on 200 for diagnostics if decode fails
	}
	results := make([]result, concurrency)

	var ready sync.WaitGroup
	ready.Add(concurrency)
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	done.Add(concurrency)

	for i := range concurrency {
		go func(idx int) {
			defer done.Done()

			// Marshal the request body before waiting so only the HTTP
			// call itself is synchronized.
			bodyBytes, err := json.Marshal(map[string]string{
				"refresh_token": originalRefreshToken,
			})
			if err != nil {
				t.Errorf("goroutine %d: marshal: %v", idx, err)
				ready.Done()
				return
			}

			ready.Done()
			start.Wait() // all goroutines fire at once

			resp, err := http.Post(
				ts.URL+"/api/v1/auth/refresh",
				"application/json",
				bytes.NewReader(bodyBytes),
			)
			if err != nil {
				t.Errorf("goroutine %d: POST: %v", idx, err)
				return
			}
			defer resp.Body.Close()

			results[idx].status = resp.StatusCode
			if resp.StatusCode == http.StatusOK {
				raw, readErr := io.ReadAll(resp.Body)
				if readErr != nil {
					t.Errorf("goroutine %d: reading 200 body: %v", idx, readErr)
					return
				}
				results[idx].rawBody = string(raw)
				if decErr := json.Unmarshal(raw, &results[idx].body); decErr != nil {
					t.Errorf("goroutine %d: malformed JSON in 200 response: %v\nbody: %s", idx, decErr, raw)
				}
			} else {
				if _, discardErr := io.Copy(io.Discard, resp.Body); discardErr != nil {
					t.Errorf("goroutine %d: draining non-200 body: %v", idx, discardErr)
				}
			}
		}(i)
	}

	ready.Wait() // wait for all goroutines to be ready
	start.Done() // release them all at once
	done.Wait()  // wait for all to finish

	// Count successes and failures. Every non-winning request must be 401;
	// a 500 would indicate a serialization bug in the handler and should
	// not be silently accepted.
	successCount := 0
	winnerIdx := -1
	for i, r := range results {
		switch r.status {
		case http.StatusOK:
			successCount++
			winnerIdx = i
		case http.StatusUnauthorized:
			// expected for losers
		default:
			t.Errorf("goroutine %d: unexpected status %d (want 200 or 401)", i, r.status)
		}
	}

	if successCount != 1 {
		for i, r := range results {
			t.Logf("  goroutine %d: status=%d", i, r.status)
		}
		t.Fatalf("expected exactly 1 success, got %d", successCount)
	}

	// The winner must have received valid tokens.
	winner := results[winnerIdx]
	if winner.body.AccessToken == "" {
		t.Errorf("winning response has empty access_token (raw body: %s)", winner.rawBody)
	}
	if winner.body.RefreshToken == "" {
		t.Errorf("winning response has empty refresh_token (raw body: %s)", winner.rawBody)
	}

	// NOTE: we intentionally do NOT assert that the winning refresh token
	// remains usable. The losing goroutines replayed the same (now-revoked)
	// original token, which triggers theft detection and revokes ALL tokens
	// for the user (BACKEND_SPEC §4). The important invariant is the
	// exactly-one-winner property verified above.
}

// TestIntegration_ConcurrentVerifyOTPSameCode fires N concurrent verify-otp
// requests using the same valid OTP code and asserts that exactly one succeeds
// (200) while the rest fail (401). No request may return 500, which would
// indicate a serialisation bug in the handler's FOR UPDATE locking. The winning
// response's access token must be usable against GET /api/v1/me.
func TestIntegration_ConcurrentVerifyOTPSameCode(t *testing.T) {
	ts, _, mailer := setupTestServer(t)

	const email = "concurrent-verify@example.com"
	const password = "StrongPass123!"
	const concurrency = 5

	// Register to create an unverified user + OTP.
	resp := postJSON(t, ts.URL+"/api/v1/auth/register", map[string]string{
		"email": email, "password": password,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("register: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Retrieve the OTP that was captured by the in-memory mailer.
	otp, err := mailer.lastOTP(email, mail.PurposeEmailVerification)
	if err != nil {
		t.Fatalf("no OTP captured: %v", err)
	}

	// Launch N goroutines that all try to verify-otp with the same code,
	// synchronised by a barrier so they fire at (roughly) the same instant.
	type result struct {
		status  int
		body    authResponse
		rawBody string
	}
	results := make([]result, concurrency)

	var ready sync.WaitGroup
	ready.Add(concurrency)
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	done.Add(concurrency)

	for i := range concurrency {
		go func(idx int) {
			defer done.Done()

			bodyBytes, marshalErr := json.Marshal(map[string]string{
				"email": email, "otp": otp,
			})
			if marshalErr != nil {
				t.Errorf("goroutine %d: marshal: %v", idx, marshalErr)
				ready.Done()
				return
			}

			ready.Done()
			start.Wait() // all goroutines fire at once

			r, postErr := http.Post(
				ts.URL+"/api/v1/auth/verify-otp",
				"application/json",
				bytes.NewReader(bodyBytes),
			)
			if postErr != nil {
				t.Errorf("goroutine %d: POST: %v", idx, postErr)
				return
			}
			defer r.Body.Close()

			results[idx].status = r.StatusCode
			if r.StatusCode == http.StatusOK {
				raw, readErr := io.ReadAll(r.Body)
				if readErr != nil {
					t.Errorf("goroutine %d: reading 200 body: %v", idx, readErr)
					return
				}
				results[idx].rawBody = string(raw)
				if decErr := json.Unmarshal(raw, &results[idx].body); decErr != nil {
					t.Errorf("goroutine %d: malformed JSON in 200 response: %v\nbody: %s", idx, decErr, raw)
				}
			} else {
				if _, discardErr := io.Copy(io.Discard, r.Body); discardErr != nil {
					t.Errorf("goroutine %d: draining non-200 body: %v", idx, discardErr)
				}
			}
		}(i)
	}

	ready.Wait() // wait for all goroutines to be ready
	start.Done() // release them all at once
	done.Wait()  // wait for all to finish

	// Count successes and failures. Every non-winning request must be 401;
	// a 500 would indicate a serialisation bug in the handler and must fail
	// the test loudly.
	successCount := 0
	winnerIdx := -1
	for i, r := range results {
		switch r.status {
		case http.StatusOK:
			successCount++
			winnerIdx = i
		case http.StatusUnauthorized:
			// expected for losers — the OTP was already consumed
		default:
			t.Errorf("goroutine %d: unexpected status %d (want 200 or 401)", i, r.status)
		}
	}

	if successCount != 1 {
		for i, r := range results {
			t.Logf("  goroutine %d: status=%d", i, r.status)
		}
		t.Fatalf("expected exactly 1 success, got %d", successCount)
	}

	// The winner must have received valid tokens and user info.
	winner := results[winnerIdx]
	if winner.body.AccessToken == "" {
		t.Errorf("winning response has empty access_token (raw body: %s)", winner.rawBody)
	}
	if winner.body.RefreshToken == "" {
		t.Errorf("winning response has empty refresh_token (raw body: %s)", winner.rawBody)
	}
	if winner.body.User.Email != email {
		t.Errorf("winning response email = %q, want %q (raw body: %s)", winner.body.User.Email, email, winner.rawBody)
	}

	// The winning access token must be usable against GET /api/v1/me.
	meResp := getWithAuth(t, ts.URL+"/api/v1/me", winner.body.AccessToken)
	if meResp.StatusCode != http.StatusOK {
		body := readBody(t, meResp)
		t.Fatalf("GET /me with winning token: expected 200, got %d: %s", meResp.StatusCode, body)
	}
	var me meResponse
	decodeJSON(t, meResp, &me)
	if me.Email != email {
		t.Errorf("GET /me: email = %q, want %q", me.Email, email)
	}
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

// TestIntegration_ForgotPassword_MailProbeOutageIsAntiEnumerating verifies
// that forgot-password returns the same 500 response for existing and unknown
// emails when the mail transport probe is down.
func TestIntegration_ForgotPassword_MailProbeOutageIsAntiEnumerating(t *testing.T) {
	mailer := &captureSender{}
	ts, _ := setupTestServerWithMailer(t, mailer)

	const email = "forgot-outage@example.com"
	const password = "StrongPass123!"

	registerAndVerify(t, ts.URL, mailer, email, password)

	mailer.mu.Lock()
	mailer.probeErr = errors.New("smtp down")
	mailer.mu.Unlock()

	respKnown := postJSON(t, ts.URL+"/api/v1/auth/forgot-password", map[string]string{"email": email})
	respUnknown := postJSON(t, ts.URL+"/api/v1/auth/forgot-password", map[string]string{"email": "ghost@example.com"})

	if respKnown.StatusCode != http.StatusInternalServerError {
		body := readBody(t, respKnown)
		t.Fatalf("forgot-password existing during outage: expected 500, got %d: %s", respKnown.StatusCode, body)
	}
	if respUnknown.StatusCode != http.StatusInternalServerError {
		body := readBody(t, respUnknown)
		t.Fatalf("forgot-password unknown during outage: expected 500, got %d: %s", respUnknown.StatusCode, body)
	}

	bodyKnown := string(readBody(t, respKnown))
	bodyUnknown := string(readBody(t, respUnknown))
	if bodyKnown != bodyUnknown {
		t.Fatalf("outage responses differ for existing vs unknown email:\nknown: %s\nunknown: %s", bodyKnown, bodyUnknown)
	}

	mailer.mu.Lock()
	callCount := len(mailer.calls)
	mailer.mu.Unlock()
	if callCount < 2 {
		t.Fatalf("expected registration and verification OTP sends before outage, got %d", callCount)
	}
}

// TestIntegration_ForgotPassword_SendFailureAfterProbe_Returns500 verifies
// that password-reset delivery failures now surface as 500 responses.
func TestIntegration_ForgotPassword_SendFailureAfterProbe_Returns500(t *testing.T) {
	mailer := &captureSender{}
	ts, _ := setupTestServerWithMailer(t, mailer)

	const email = "forgot-send-failure@example.com"
	const password = "StrongPass123!"

	registerAndVerify(t, ts.URL, mailer, email, password)

	mailer.mu.Lock()
	baselineCalls := len(mailer.calls)
	mailer.sendErr = errors.New("smtp send failed")
	mailer.mu.Unlock()

	respKnown := postJSON(t, ts.URL+"/api/v1/auth/forgot-password", map[string]string{"email": email})
	respUnknown := postJSON(t, ts.URL+"/api/v1/auth/forgot-password", map[string]string{"email": "ghost@example.com"})

	if respKnown.StatusCode != http.StatusInternalServerError {
		body := readBody(t, respKnown)
		t.Fatalf("forgot-password existing during send failure: expected 500, got %d: %s", respKnown.StatusCode, body)
	}
	if respUnknown.StatusCode != http.StatusInternalServerError {
		body := readBody(t, respUnknown)
		t.Fatalf("forgot-password unknown during send failure: expected 500, got %d: %s", respUnknown.StatusCode, body)
	}

	bodyKnown := string(readBody(t, respKnown))
	bodyUnknown := string(readBody(t, respUnknown))
	if bodyKnown != bodyUnknown {
		t.Fatalf("send-failure responses differ for existing vs unknown email:\nknown: %s\nunknown: %s", bodyKnown, bodyUnknown)
	}

	mailer.mu.Lock()
	defer mailer.mu.Unlock()
	if got := len(mailer.calls); got != baselineCalls+1 {
		t.Fatalf("SendOTP call count = %d, want %d", got, baselineCalls+1)
	}
	last := mailer.calls[len(mailer.calls)-1]
	if last.To != email {
		t.Fatalf("last SendOTP recipient = %q, want %q", last.To, email)
	}
	if last.Purpose != mail.PurposePasswordReset {
		t.Fatalf("last SendOTP purpose = %q, want %q", last.Purpose, mail.PurposePasswordReset)
	}
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

// TestIntegration_VerifyOTPAttemptBudgetNotResetByDuplicateRegisterConflict
// verifies that a duplicate registration conflict does not reset the existing
// verification OTP attempt budget or mint a fresh OTP.
func TestIntegration_VerifyOTPAttemptBudgetNotResetByDuplicateRegisterConflict(t *testing.T) {
	ts, _, mailer := setupTestServer(t)

	const email = "verify-otp-budget@example.com"
	const password = "StrongPass123!"

	resp := postJSON(t, ts.URL+"/api/v1/auth/register", map[string]string{
		"email": email, "password": password,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("first register: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	firstOTP, err := mailer.lastOTP(email, mail.PurposeEmailVerification)
	if err != nil {
		t.Fatalf("no first OTP captured: %v", err)
	}
	wrongOTP := "000000"
	if wrongOTP == firstOTP {
		wrongOTP = "999999"
	}

	for i := 1; i <= auth.MaxOTPAttempts; i++ {
		resp = postJSON(t, ts.URL+"/api/v1/auth/verify-otp", map[string]string{
			"email": email, "otp": wrongOTP,
		})
		if resp.StatusCode != http.StatusUnauthorized {
			body := readBody(t, resp)
			t.Fatalf("wrong OTP before duplicate register attempt %d: expected 401, got %d: %s", i, resp.StatusCode, body)
		}
		discardBody(t, resp)
	}

	resp = postJSON(t, ts.URL+"/api/v1/auth/register", map[string]string{
		"email": email, "password": password,
	})
	if resp.StatusCode != http.StatusConflict {
		body := readBody(t, resp)
		t.Fatalf("duplicate register after exhausted attempts: expected 409, got %d: %s", resp.StatusCode, body)
	}
	var conflictResp apperror.ErrorResponse
	decodeJSON(t, resp, &conflictResp)
	if conflictResp.Error.Code != apperror.CodeConflict {
		t.Errorf("expected code %q, got %q", apperror.CodeConflict, conflictResp.Error.Code)
	}

	resp = postJSON(t, ts.URL+"/api/v1/auth/verify-otp", map[string]string{
		"email": email, "otp": firstOTP,
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		body := readBody(t, resp)
		t.Fatalf("correct original OTP after duplicate conflict: expected 429, got %d: %s", resp.StatusCode, body)
	}
	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeRateLimited {
		t.Errorf("expected code %q, got %q", apperror.CodeRateLimited, errResp.Error.Code)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429 verify-otp response")
	}

	mailer.mu.Lock()
	callCount := len(mailer.calls)
	mailer.mu.Unlock()
	if callCount != 1 {
		t.Fatalf("SendOTP call count = %d, want %d", callCount, 1)
	}
}

// TestIntegration_ResetPasswordAttemptsPersistAcrossFreshOTP verifies that
// reset-password attempts made against an older OTP still exhaust the shared
// attempt budget for a fresh OTP issued within the same expiry window.
func TestIntegration_ResetPasswordAttemptsPersistAcrossFreshOTP(t *testing.T) {
	ts, _, mailer := setupTestServer(t)

	const email = "reset-otp-budget@example.com"
	const password = "StrongPass123!"

	registerAndVerify(t, ts.URL, mailer, email, password)

	resp := postJSON(t, ts.URL+"/api/v1/auth/forgot-password", map[string]string{"email": email})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("first forgot-password: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	firstOTP, err := mailer.lastOTP(email, mail.PurposePasswordReset)
	if err != nil {
		t.Fatalf("no first password reset OTP captured: %v", err)
	}
	wrongOTP := "000000"
	if wrongOTP == firstOTP {
		wrongOTP = "999999"
	}

	for i := 1; i < auth.MaxOTPAttempts; i++ {
		resp = postJSON(t, ts.URL+"/api/v1/auth/reset-password", map[string]string{
			"email": email, "otp": wrongOTP, "new_password": "NewStrongPass123!",
		})
		if resp.StatusCode != http.StatusUnauthorized {
			body := readBody(t, resp)
			t.Fatalf("wrong reset OTP before refresh attempt %d: expected 401, got %d: %s", i, resp.StatusCode, body)
		}
		discardBody(t, resp)
	}

	resp = postJSON(t, ts.URL+"/api/v1/auth/forgot-password", map[string]string{"email": email})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("second forgot-password: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	freshOTP, err := mailer.lastOTP(email, mail.PurposePasswordReset)
	if err != nil {
		t.Fatalf("no fresh password reset OTP captured: %v", err)
	}
	if freshOTP == wrongOTP {
		wrongOTP = "111111"
	}

	resp = postJSON(t, ts.URL+"/api/v1/auth/reset-password", map[string]string{
		"email": email, "otp": wrongOTP, "new_password": "NewStrongPass123!",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("wrong reset OTP on fresh code: expected 401, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	resp = postJSON(t, ts.URL+"/api/v1/auth/reset-password", map[string]string{
		"email": email, "otp": freshOTP, "new_password": "NewestStrongPass123!",
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		body := readBody(t, resp)
		t.Fatalf("correct fresh reset OTP after budget exhausted: expected 429, got %d: %s", resp.StatusCode, body)
	}
	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeRateLimited {
		t.Errorf("expected code %q, got %q", apperror.CodeRateLimited, errResp.Error.Code)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429 reset-password response")
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

	// Look up the user_id for the registered email.
	var userID string
	err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE lower(email) = $1`, email,
	).Scan(&userID)
	if err != nil {
		t.Fatalf("looking up user_id: %v", err)
	}

	// Set the OTP's expires_at to the past.
	tag, err := pool.Exec(ctx,
		`UPDATE otp_codes SET expires_at = now() - interval '1 hour'
		 WHERE user_id = $1 AND purpose = 'email_verification'`,
		userID,
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

// ---------------------------------------------------------------------------
// Concurrent registration race tests
// ---------------------------------------------------------------------------

// TestIntegration_ConcurrentRegistrationSameEmail fires N concurrent
// registration requests for the same email and verifies the post-race state:
//   - Exactly one user row exists for the email.
//   - Exactly one active (unused, non-expired) email_verification OTP row
//     exists for that user.
//   - The surviving OTP can be successfully verified end-to-end.
//
// This test exercises the FOR UPDATE row locking in the Register handler that
// prevents concurrent registrations from duplicating user rows or leaving
// orphaned OTP records.
func TestIntegration_ConcurrentRegistrationSameEmail(t *testing.T) {
	ts, pool, mailer := setupTestServer(t)

	const email = "concurrent-reg@example.com"
	const password = "StrongPass123!"
	const concurrency = 8

	// Use a barrier so all goroutines fire their HTTP POST at (roughly) the
	// same instant.
	type result struct {
		status int
		err    error
	}
	results := make([]result, concurrency)

	var ready sync.WaitGroup
	ready.Add(concurrency)
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	done.Add(concurrency)

	for i := range concurrency {
		go func(idx int) {
			defer done.Done()

			bodyBytes, marshalErr := json.Marshal(map[string]string{
				"email": email, "password": password,
			})
			if marshalErr != nil {
				results[idx].err = fmt.Errorf("marshal: %w", marshalErr)
				ready.Done()
				return
			}

			ready.Done()
			start.Wait() // all goroutines fire at once

			resp, postErr := http.Post(
				ts.URL+"/api/v1/auth/register",
				"application/json",
				bytes.NewReader(bodyBytes),
			)
			if postErr != nil {
				results[idx].err = fmt.Errorf("POST register: %w", postErr)
				return
			}
			results[idx].status = resp.StatusCode
			if _, discardErr := io.Copy(io.Discard, resp.Body); discardErr != nil {
				t.Errorf("goroutine %d: draining body: %v", idx, discardErr)
			}
			resp.Body.Close()
		}(i)
	}

	ready.Wait() // wait for all goroutines to be ready
	start.Done() // release them all at once
	done.Wait()  // wait for all to finish

	// Tally results. Every request must return one of the two expected
	// outcomes:
	//   200 — this goroutine won (or serialised after cleaning up the
	//         previous unverified user).
	//   409 — another goroutine already committed a user with this email
	//         and that user is verified, or the concurrent insert hit
	//         the unique constraint.
	// Any other status (especially 500) indicates a bug in the handler's
	// serialisation logic and must fail the test loudly.
	successCount := 0
	for i, r := range results {
		if r.err != nil {
			t.Errorf("goroutine %d: %v", i, r.err)
			continue
		}
		switch r.status {
		case http.StatusOK:
			successCount++
		case http.StatusConflict:
			// Expected loser: another goroutine already owns the email.
		default:
			t.Errorf("goroutine %d: unexpected status %d; only 200 (OK) and 409 (Conflict) are valid outcomes for concurrent registration", i, r.status)
		}
	}

	if successCount == 0 {
		for i, r := range results {
			t.Logf("  goroutine %d: status=%d err=%v", i, r.status, r.err)
		}
		t.Fatal("expected at least one successful (200) registration, got none")
	}

	// --- Verify post-race database state ---

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Exactly one user row must exist for this email.
	var userCount int
	err := pool.QueryRow(ctx,
		"SELECT count(*) FROM users WHERE lower(email) = $1", email,
	).Scan(&userCount)
	if err != nil {
		t.Fatalf("counting user rows: %v", err)
	}
	if userCount != 1 {
		t.Fatalf("expected exactly 1 user row for %q, got %d", email, userCount)
	}

	// The surviving user must be unverified (no OTP verified yet).
	var survivingUserID string
	var emailVerified bool
	err = pool.QueryRow(ctx,
		"SELECT id, email_verified FROM users WHERE lower(email) = $1", email,
	).Scan(&survivingUserID, &emailVerified)
	if err != nil {
		t.Fatalf("querying surviving user: %v", err)
	}
	if emailVerified {
		t.Fatal("surviving user should be unverified before OTP verification")
	}

	// Exactly one active (unused, non-expired) email_verification OTP must
	// exist for the surviving user.
	var otpCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM otp_codes
		 WHERE user_id = $1
		   AND purpose = 'email_verification'
		   AND used = false
		   AND expires_at > now()`,
		survivingUserID,
	).Scan(&otpCount)
	if err != nil {
		t.Fatalf("counting active OTP rows: %v", err)
	}
	if otpCount != 1 {
		t.Fatalf("expected exactly 1 active OTP for surviving user, got %d", otpCount)
	}

	// No orphaned OTP rows should reference non-existent users.
	var orphanedOTPCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM otp_codes oc
		 WHERE NOT EXISTS (SELECT 1 FROM users u WHERE u.id = oc.user_id)`,
	).Scan(&orphanedOTPCount)
	if err != nil {
		t.Fatalf("counting orphaned OTP rows: %v", err)
	}
	if orphanedOTPCount != 0 {
		t.Errorf("found %d orphaned OTP rows referencing non-existent users", orphanedOTPCount)
	}

	// Retrieve the last OTP captured by the mailer and verify it end-to-end.
	otp, err := mailer.lastOTP(email, mail.PurposeEmailVerification)
	if err != nil {
		t.Fatalf("no OTP captured for %s: %v", email, err)
	}

	resp := postJSON(t, ts.URL+"/api/v1/auth/verify-otp", map[string]string{
		"email": email, "otp": otp,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("verify-otp: expected 200, got %d: %s", resp.StatusCode, body)
	}
	var ar authResponse
	decodeJSON(t, resp, &ar)
	if ar.AccessToken == "" {
		t.Error("verify-otp: expected non-empty access_token")
	}
	if ar.User.Email != email {
		t.Errorf("verify-otp: expected email %q, got %q", email, ar.User.Email)
	}

	// After verification, the user should be verified and the surviving user ID
	// should be the one in the auth response.
	if ar.User.ID != survivingUserID {
		t.Errorf("verify-otp: returned user ID %q does not match surviving user %q", ar.User.ID, survivingUserID)
	}
}

// TestIntegration_ConcurrentDuplicateUnverifiedRegistrationLifecycle exercises
// the full lifecycle when an initial unverified registration is followed by
// concurrent duplicate registrations that must all conflict. The scenario
// is:
//  1. Register email A (first registration, unverified).
//  2. Fire N concurrent duplicate registrations for the same email.
//  3. Verify the original registration can still verify OTP, login, and use the
//     refresh token — proving duplicate conflicts do not corrupt the lifecycle.
//
// This is more end-to-end than TestIntegration_RegisterDuplicateUnverifiedEmail
// because it adds concurrency and full post-verification lifecycle checks.
func TestIntegration_ConcurrentDuplicateUnverifiedRegistrationLifecycle(t *testing.T) {
	ts, pool, mailer := setupTestServer(t)

	const email = "concurrent-rereg@example.com"
	const password = "StrongPass123!"
	const concurrency = 5

	// Step 1: Create an initial unverified registration (we never verify it).
	resp := postJSON(t, ts.URL+"/api/v1/auth/register", map[string]string{
		"email": email, "password": password,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("initial register: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Step 2: Fire N concurrent duplicate registrations against the existing
	// unverified user.
	type result struct {
		status int
		err    error
	}
	results := make([]result, concurrency)

	var ready sync.WaitGroup
	ready.Add(concurrency)
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	done.Add(concurrency)

	for i := range concurrency {
		go func(idx int) {
			defer done.Done()

			bodyBytes, marshalErr := json.Marshal(map[string]string{
				"email": email, "password": password,
			})
			if marshalErr != nil {
				results[idx].err = fmt.Errorf("marshal: %w", marshalErr)
				ready.Done()
				return
			}

			ready.Done()
			start.Wait()

			r, postErr := http.Post(
				ts.URL+"/api/v1/auth/register",
				"application/json",
				bytes.NewReader(bodyBytes),
			)
			if postErr != nil {
				results[idx].err = fmt.Errorf("POST register: %w", postErr)
				return
			}
			results[idx].status = r.StatusCode
			if _, discardErr := io.Copy(io.Discard, r.Body); discardErr != nil {
				t.Errorf("goroutine %d: draining body: %v", idx, discardErr)
			}
			r.Body.Close()
		}(i)
	}

	ready.Wait()
	start.Done()
	done.Wait()

	for i, r := range results {
		if r.err != nil {
			t.Errorf("goroutine %d: %v", i, r.err)
			continue
		}
		if r.status != http.StatusConflict {
			t.Errorf("goroutine %d: status = %d, want %d", i, r.status, http.StatusConflict)
		}
	}

	mailer.mu.Lock()
	callCount := len(mailer.calls)
	mailer.mu.Unlock()
	if callCount != 1 {
		t.Fatalf("SendOTP call count = %d, want %d", callCount, 1)
	}

	// --- Verify post-race database state ---

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Exactly one user row for this email.
	var userCount int
	err := pool.QueryRow(ctx,
		"SELECT count(*) FROM users WHERE lower(email) = $1", email,
	).Scan(&userCount)
	if err != nil {
		t.Fatalf("counting user rows: %v", err)
	}
	if userCount != 1 {
		t.Fatalf("expected 1 user row, got %d", userCount)
	}

	// Step 3: Verify the surviving user's OTP.
	otp, err := mailer.lastOTP(email, mail.PurposeEmailVerification)
	if err != nil {
		t.Fatalf("no OTP captured: %v", err)
	}

	resp = postJSON(t, ts.URL+"/api/v1/auth/verify-otp", map[string]string{
		"email": email, "otp": otp,
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

	// Step 4: Login should work with the password.
	resp = postJSON(t, ts.URL+"/api/v1/auth/login", map[string]string{
		"email": email, "password": password,
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

	// Step 5: Refresh token from verification should still work.
	resp = postJSON(t, ts.URL+"/api/v1/auth/refresh", map[string]string{
		"refresh_token": verifyResp.RefreshToken,
	})
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("refresh: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	// Step 6: Verify the user is marked as verified in the database.
	var emailVerified bool
	err = pool.QueryRow(ctx,
		"SELECT email_verified FROM users WHERE lower(email) = $1", email,
	).Scan(&emailVerified)
	if err != nil {
		t.Fatalf("querying email_verified: %v", err)
	}
	if !emailVerified {
		t.Error("expected email_verified = true after OTP verification")
	}
}

// ---------------------------------------------------------------------------
// SMTP-failure cleanup race test
// ---------------------------------------------------------------------------

// smtpRaceSender is a mail.Sender that allows deterministic simulation
// of the SMTP-failure cleanup race condition. The first SendOTP call blocks
// until the gate channel is closed (simulating a slow/failing SMTP server),
// then returns errSMTP. The second and subsequent calls succeed immediately
// and record the OTP so the test can retrieve it for verification.
type smtpRaceSender struct {
	mu   sync.Mutex
	call int           // incremented on each SendOTP invocation
	gate chan struct{} // closed by the test to unblock the first call
	otps []capturedOTP // OTPs captured by successful calls
	err  error         // error to return from the first call
}

// Compile-time interface check.
var _ mail.Sender = (*smtpRaceSender)(nil)

func (s *smtpRaceSender) Probe(context.Context) error {
	return nil
}

func (s *smtpRaceSender) SendOTP(ctx context.Context, to, otp, purpose string) error {
	s.mu.Lock()
	// Snapshot the current call index before releasing the lock so the
	// first-call check below is race-free even when multiple goroutines
	// enter SendOTP concurrently.
	n := s.call
	s.call++
	s.mu.Unlock()

	if n == 0 {
		// First call: block until the gate is released or the context is
		// cancelled, whichever comes first. Honouring ctx.Done() prevents
		// an indefinite hang if the test tears down early.
		select {
		case <-s.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
		return s.err
	}

	// Subsequent calls: succeed and record the OTP.
	s.mu.Lock()
	defer s.mu.Unlock()
	s.otps = append(s.otps, capturedOTP{To: to, OTP: otp, Purpose: purpose})
	return nil
}

// lastOTP returns the most recently captured OTP for the given email and
// purpose from a successful send, or an error if none was found.
func (s *smtpRaceSender) lastOTP(email, purpose string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.otps) - 1; i >= 0; i-- {
		c := s.otps[i]
		if c.To == email && c.Purpose == purpose {
			return c.OTP, nil
		}
	}
	return "", fmt.Errorf("no OTP found for %s / %s", email, purpose)
}

// TestIntegration_RegisterSMTPFailureCleanupRace proves that when an in-flight
// registration later fails its SMTP send, a concurrent duplicate request still
// gets 409 and the failed registration's cleanup removes the temporary rows.
//
// Timeline:
//  1. Registration A commits user_A + otp_A to DB, then blocks on SendOTP.
//  2. Registration B arrives for the same email while A is blocked.
//     B sees the committed unverified user_A and returns 409.
//  3. Registration A's SendOTP unblocks and returns an error.
//     A's cleanup deletes user_A and otp_A by ID.
//  4. No rows remain for the email, and a fresh follow-up registration works.
func TestIntegration_RegisterSMTPFailureCleanupRace(t *testing.T) {
	sender := &smtpRaceSender{
		gate: make(chan struct{}),
		err:  errors.New("simulated SMTP failure"),
	}
	ts, pool := setupTestServerWithMailer(t, sender)

	const email = "smtp-race@example.com"
	const password = "StrongPass123!"

	// Launch Registration A in a goroutine. It will block inside SendOTP
	// until we close sender.gate.
	//
	// We intentionally avoid calling helpers like postJSON here because they
	// use t.Fatalf, which panics when called from a non-test goroutine.
	// Instead we do the HTTP work inline and report errors back via regAErr.
	var regAStatus int
	var regAErr error
	var regADone sync.WaitGroup
	regADone.Add(1)
	go func() {
		defer regADone.Done()
		body, marshalErr := json.Marshal(map[string]string{
			"email": email, "password": password,
		})
		if marshalErr != nil {
			regAErr = fmt.Errorf("marshal: %w", marshalErr)
			return
		}
		resp, postErr := http.Post(
			ts.URL+"/api/v1/auth/register",
			"application/json",
			bytes.NewReader(body),
		)
		if postErr != nil {
			regAErr = fmt.Errorf("POST register: %w", postErr)
			return
		}
		regAStatus = resp.StatusCode
		if _, discardErr := io.Copy(io.Discard, resp.Body); discardErr != nil {
			regAErr = fmt.Errorf("draining body: %w", discardErr)
		}
		resp.Body.Close()
	}()

	// Wait for Registration A to have committed its DB rows and entered
	// SendOTP (call count increments at the top of SendOTP, before blocking).
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		sender.mu.Lock()
		n := sender.call
		sender.mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("timed out waiting for Registration A to enter SendOTP")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Registration B: same email while A is still in-flight. This should see the
	// committed unverified user and return 409.
	respB := postJSON(t, ts.URL+"/api/v1/auth/register", map[string]string{
		"email": email, "password": password,
	})
	if respB.StatusCode != http.StatusConflict {
		body := readBody(t, respB)
		t.Fatalf("Registration B: expected 409, got %d: %s", respB.StatusCode, body)
	}
	var conflictResp apperror.ErrorResponse
	decodeJSON(t, respB, &conflictResp)
	if conflictResp.Error.Code != apperror.CodeConflict {
		t.Errorf("Registration B: expected code %q, got %q", apperror.CodeConflict, conflictResp.Error.Code)
	}

	// Now unblock Registration A's SendOTP so it fails and runs cleanup.
	close(sender.gate)
	regADone.Wait()

	if regAErr != nil {
		t.Fatalf("Registration A goroutine failed: %v", regAErr)
	}

	// Registration A should have returned 500 (SMTP failure → internal error).
	if regAStatus != http.StatusInternalServerError {
		t.Errorf("Registration A: expected 500, got %d", regAStatus)
	}

	// --- Verify that cleanup removed A's temporary rows ---

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var userCount int
	err := pool.QueryRow(ctx,
		"SELECT count(*) FROM users WHERE lower(email) = $1", email,
	).Scan(&userCount)
	if err != nil {
		t.Fatalf("counting users after race: %v", err)
	}
	if userCount != 0 {
		t.Fatalf("user count after failed registration cleanup = %d, want 0", userCount)
	}

	var otpCount int
	err = pool.QueryRow(ctx,
		"SELECT count(*) FROM otp_codes oc JOIN users u ON u.id = oc.user_id WHERE lower(u.email) = $1", email,
	).Scan(&otpCount)
	if err != nil {
		t.Fatalf("counting OTP rows after race: %v", err)
	}
	if otpCount != 0 {
		t.Fatalf("otp count after failed registration cleanup = %d, want 0", otpCount)
	}

	// A fresh follow-up registration after cleanup should now succeed.
	respC := postJSON(t, ts.URL+"/api/v1/auth/register", map[string]string{
		"email": email, "password": password,
	})
	if respC.StatusCode != http.StatusOK {
		body := readBody(t, respC)
		t.Fatalf("Registration C: expected 200, got %d: %s", respC.StatusCode, body)
	}
	discardBody(t, respC)

	otp, err := sender.lastOTP(email, mail.PurposeEmailVerification)
	if err != nil {
		t.Fatalf("no OTP captured for Registration C: %v", err)
	}

	verifyResp := postJSON(t, ts.URL+"/api/v1/auth/verify-otp", map[string]string{
		"email": email, "otp": otp,
	})
	if verifyResp.StatusCode != http.StatusOK {
		body := readBody(t, verifyResp)
		t.Fatalf("verify-otp after cleanup: expected 200, got %d: %s", verifyResp.StatusCode, body)
	}
	var ar authResponse
	decodeJSON(t, verifyResp, &ar)
	if ar.AccessToken == "" {
		t.Error("verify-otp: expected non-empty access_token")
	}
	if ar.User.Email != email {
		t.Errorf("verify-otp: expected email %q, got %q", email, ar.User.Email)
	}
}
