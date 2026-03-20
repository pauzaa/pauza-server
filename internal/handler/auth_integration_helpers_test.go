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
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/server"
	"github.com/IsorilovA/pauza-server/internal/testdb"
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
	Purpose mail.Purpose
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
		PushEnabled        bool    `json:"push_enabled"`
		LeaderboardVisible bool    `json:"leaderboard_visible"`
		CreatedAt          int64   `json:"created_at"`
		Subscription       any     `json:"subscription"`
	} `json:"user"`
}

type authStartRequest struct {
	Email string `json:"email"`
}

type authVerifyRequest struct {
	Email string `json:"email"`
	OTP   string `json:"otp"`
}

type authRefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (s *captureSender) Probe(context.Context) error { return nil }

func (s *captureSender) SendOTP(_ context.Context, to, otp string, purpose mail.Purpose) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, capturedOTP{To: to, OTP: otp, Purpose: purpose})
	return nil
}

func (s *captureSender) lastOTP(email string, purpose mail.Purpose) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.calls) - 1; i >= 0; i-- {
		if s.calls[i].To == email && s.calls[i].Purpose == purpose {
			return s.calls[i].OTP
		}
	}
	return ""
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

func setupTestServer(t *testing.T) (*httptest.Server, *pgxpool.Pool, *captureSender, string) {
	t.Helper()

	pool, _ := testdb.New(t)
	photoDir := t.TempDir()

	sender := &captureSender{}
	cfg := testConfig()
	cfg.PhotoStorageDir = photoDir
	cfg.PhotoPublicBaseURL = "https://api.test/photos"

	srv, cleanup, err := server.New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), pool, sender, nil, nil)
	if err != nil {
		t.Fatalf("creating server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(func() {
		ts.Close()
		cleanup()
	})

	return ts, pool, sender, photoDir
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

	resp := postJSON(t, baseURL+"/api/v1/auth/start", authStartRequest{Email: email})
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

	resp := postJSON(t, baseURL+"/api/v1/auth/verify", authVerifyRequest{
		Email: email,
		OTP:   otp,
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
	if got.User.CreatedAt == 0 {
		t.Fatal("expected user.created_at")
	}
}
