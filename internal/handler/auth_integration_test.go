//go:build integration

package handler_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/mail"
)

func TestIntegration_AuthStartReturnsOTPRequiredAndSendsLoginOTP(t *testing.T) {
	ts, _, sender := setupTestServer(t)

	email := "user@example.com"
	startAuthChallenge(t, ts.URL, email)

	if otp := sender.lastOTP(email, mail.PurposeAuthLogin); otp == "" {
		t.Fatal("expected auth login OTP to be sent")
	}
}

func TestIntegration_AuthVerifyCreatesUserAndReturnsProfile(t *testing.T) {
	ts, pool, sender := setupTestServer(t)

	email := "user@example.com"
	auth := startAndVerifyAuth(t, ts.URL, sender, email)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email) = lower($1)`, email).Scan(&count); err != nil {
		t.Fatalf("counting users: %v", err)
	}
	if count != 1 {
		t.Fatalf("users for %q = %d, want 1", email, count)
	}

	assertAuthEnvelope(t, auth, email)
}

func TestIntegration_AuthVerifyExistingUserSignsIntoSameAccount(t *testing.T) {
	ts, pool, sender := setupTestServer(t)

	email := "repeat@example.com"
	first := startAndVerifyAuth(t, ts.URL, sender, email)
	second := startAndVerifyAuth(t, ts.URL, sender, email)

	if first.User.ID != second.User.ID {
		t.Fatalf("user id changed across passwordless sign-ins: first=%q second=%q", first.User.ID, second.User.ID)
	}
	if first.RefreshToken == second.RefreshToken {
		t.Fatal("expected a fresh refresh token on second sign-in")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email) = lower($1)`, email).Scan(&count); err != nil {
		t.Fatalf("counting users: %v", err)
	}
	if count != 1 {
		t.Fatalf("users for %q = %d, want 1", email, count)
	}
}

func TestIntegration_AuthVerifyInvalidOTPReturnsUnauthorized(t *testing.T) {
	ts, _, sender := setupTestServer(t)

	email := "invalid@example.com"
	startAuthChallenge(t, ts.URL, email)
	if otp := sender.lastOTP(email, mail.PurposeAuthLogin); otp == "" {
		t.Fatal("expected auth login OTP to be sent")
	}

	resp := postJSON(t, ts.URL+"/api/v1/auth/verify", map[string]string{
		"email": email,
		"otp":   "000000",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("verify status = %d, want 401", resp.StatusCode)
	}

	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeUnauthorized {
		t.Fatalf("error.code = %q, want %q", errResp.Error.Code, apperror.CodeUnauthorized)
	}
	if errResp.Error.Message != "Invalid or expired OTP" {
		t.Fatalf("error.message = %q, want %q", errResp.Error.Message, "Invalid or expired OTP")
	}
}

func TestIntegration_AuthRefreshRotatesTokensAndDetectsReuse(t *testing.T) {
	ts, _, sender := setupTestServer(t)

	auth := startAndVerifyAuth(t, ts.URL, sender, "refresh@example.com")

	refreshResp := postJSON(t, ts.URL+"/api/v1/auth/refresh", map[string]string{
		"refresh_token": auth.RefreshToken,
	})
	if refreshResp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d: %s", refreshResp.StatusCode, string(readBody(t, refreshResp)))
	}

	var rotated struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	decodeJSON(t, refreshResp, &rotated)
	if rotated.AccessToken == "" || rotated.RefreshToken == "" {
		t.Fatal("expected rotated tokens")
	}
	if rotated.RefreshToken == auth.RefreshToken {
		t.Fatal("expected refresh token rotation")
	}

	reuseResp := postJSON(t, ts.URL+"/api/v1/auth/refresh", map[string]string{
		"refresh_token": auth.RefreshToken,
	})
	if reuseResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reuse refresh status = %d, want 401", reuseResp.StatusCode)
	}
	discardBody(t, reuseResp)

	revokedResp := postJSON(t, ts.URL+"/api/v1/auth/refresh", map[string]string{
		"refresh_token": rotated.RefreshToken,
	})
	if revokedResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("rotated token after reuse status = %d, want 401", revokedResp.StatusCode)
	}

	var errResp apperror.ErrorResponse
	decodeJSON(t, revokedResp, &errResp)
	if errResp.Error.Code != apperror.CodeUnauthorized {
		t.Fatalf("error.code = %q, want %q", errResp.Error.Code, apperror.CodeUnauthorized)
	}
}
