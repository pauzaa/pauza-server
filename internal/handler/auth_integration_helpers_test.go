//go:build integration

package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/database"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/server"
	"github.com/IsorilovA/pauza-server/migrations"
)

const (
	testJWTSecret          = "integration-test-secret-key-32b!"
	testJWTAccessTokenTTL  = 15 * time.Minute
	testJWTRefreshTokenTTL = 7 * 24 * time.Hour
)

type captureSender struct {
	mu    sync.Mutex
	calls []capturedOTP
}

type capturedOTP struct {
	To      string
	OTP     string
	Purpose string
}

type authEnvelope struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	User         struct {
		ID                 string  `json:"id"`
		Email              string  `json:"email"`
		Name               string  `json:"name"`
		Username           string  `json:"username"`
		ProfilePictureURL  *string `json:"profile_picture_url"`
		LeaderboardVisible bool    `json:"leaderboard_visible"`
		CreatedAt          string  `json:"created_at"`
		Subscription       any     `json:"subscription"`
	} `json:"user"`
}

func (s *captureSender) Probe(context.Context) error { return nil }

func (s *captureSender) SendOTP(_ context.Context, to, otp, purpose string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, capturedOTP{To: to, OTP: otp, Purpose: purpose})
	return nil
}

func (s *captureSender) lastOTP(email, purpose string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.calls) - 1; i >= 0; i-- {
		if s.calls[i].To == email && s.calls[i].Purpose == purpose {
			return s.calls[i].OTP
		}
	}
	return ""
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	return url
}

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
		WebhookRateLimit:     10000,
		WebhookRateWindow:    time.Minute,
	}
}

func setupTestServer(t *testing.T) (*httptest.Server, *pgxpool.Pool, *captureSender) {
	t.Helper()

	dbURL := testDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("creating test pool: %v", err)
	}

	for _, q := range []string{
		"DROP SCHEMA public CASCADE",
		"CREATE SCHEMA public",
		"GRANT ALL ON SCHEMA public TO current_user",
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("resetting database (%s): %v", q, err)
		}
	}

	if err := database.RunMigrations(slog.New(slog.NewTextHandler(io.Discard, nil)), dbURL, migrations.FS); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}

	sender := &captureSender{}
	srv, cleanup := server.New(testConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)), pool, sender, nil)
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(func() {
		ts.Close()
		cleanup()
		pool.Close()
	})

	return ts, pool, sender
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, target any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return b
}

func discardBody(_ *testing.T, resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()              //nolint:errcheck
}

func startAuthChallenge(t *testing.T, baseURL, email string) {
	t.Helper()

	resp := postJSON(t, baseURL+"/api/v1/auth/start", map[string]string{"email": email})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d: %s", resp.StatusCode, string(readBody(t, resp)))
	}

	var body struct {
		OTPRequired bool `json:"otp_required"`
	}
	decodeJSON(t, resp, &body)
	if !body.OTPRequired {
		t.Fatal("expected otp_required = true")
	}
}

func verifyAuthOTP(t *testing.T, baseURL, email, otp string) authEnvelope {
	t.Helper()

	resp := postJSON(t, baseURL+"/api/v1/auth/verify", map[string]string{
		"email": email,
		"otp":   otp,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify status = %d: %s", resp.StatusCode, string(readBody(t, resp)))
	}

	var out authEnvelope
	decodeJSON(t, resp, &out)
	assertAuthEnvelope(t, out, email)
	return out
}

func startAndVerifyAuth(t *testing.T, baseURL string, sender *captureSender, email string) authEnvelope {
	t.Helper()

	startAuthChallenge(t, baseURL, email)

	otp := sender.lastOTP(email, mail.PurposeAuthLogin)
	if otp == "" {
		t.Fatal("expected auth OTP to be sent")
	}

	return verifyAuthOTP(t, baseURL, email, otp)
}

func assertAuthEnvelope(t *testing.T, got authEnvelope, email string) {
	t.Helper()

	if got.AccessToken == "" {
		t.Fatal("expected access_token")
	}
	if got.RefreshToken == "" {
		t.Fatal("expected refresh_token")
	}
	if got.User.ID == "" {
		t.Fatal("expected user.id")
	}
	if got.User.Email != email {
		t.Fatalf("user.email = %q, want %q", got.User.Email, email)
	}
	if got.User.Username == "" {
		t.Fatal("expected user.username")
	}
	if got.User.CreatedAt == "" {
		t.Fatal("expected user.created_at")
	}
	if _, err := time.Parse(time.RFC3339, got.User.CreatedAt); err != nil {
		t.Fatalf("created_at parse: %v", err)
	}
}
