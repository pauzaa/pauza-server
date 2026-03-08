package handler

// This file contains unit tests for auth handler validation paths. The
// handler is constructed with a service backed by nil dependencies because
// these tests only exercise request validation, which returns before any
// service/DB interaction. No test in this file should reach — or assert
// that it reaches — a service or database call. DB-backed flows (credential
// checks, OTP verification, token persistence, anti-enumeration at the DB
// layer, etc.) are covered by integration tests in auth_integration_test.go
// which run against a real Postgres instance.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/service"
)

// mockEmailSender is a no-op stub of mail.Sender for validation-only
// handler tests. None of the unit tests here reach the mailer, so the
// implementation simply returns nil.
//
// No explicit compile-time interface check (var _ mail.Sender = ...) is
// needed: NewAuthService's mailer parameter is typed mail.Sender, so the
// compiler already verifies satisfaction at the call site in newTestAuthHandler.
type mockEmailSender struct{}

func (m *mockEmailSender) SendOTP(_ context.Context, _, _, _ string) error {
	return nil
}

// newTestAuthHandler builds an AuthHandler backed by an AuthService with nil
// pool/repo. This is suitable for tests that exercise only request validation,
// which returns before any service/DB interaction.
func newTestAuthHandler(mailer *mockEmailSender) *AuthHandler {
	svc := service.NewAuthService(
		nil, // pool – nil is fine for validation-only tests
		nil, // repo – nil is fine for validation-only tests
		mailer,
		"test-secret",
		0,
		0,
		noopLogger(),
	)
	return NewAuthHandler(svc, noopLogger())
}

// noopLogger returns a slog.Logger that discards all output.
func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// assertValidationEnvelope is a test helper that verifies a 422 response
// matches the BACKEND_SPEC error envelope. It checks status, Content-Type,
// the single top-level "error" key, the VALIDATION_ERROR code, the expected
// message, and that details.fields contains exactly the expectedFields (no
// extra, no missing).
func assertValidationEnvelope(t *testing.T, rec *httptest.ResponseRecorder, expectedFields []string) {
	t.Helper()

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("expected 1 top-level key, got %d: %v", len(raw), raw)
	}
	if _, ok := raw["error"]; !ok {
		t.Fatal("expected top-level 'error' key")
	}

	var inner struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Fields map[string]string `json:"fields"`
		} `json:"details"`
	}
	if err := json.Unmarshal(raw["error"], &inner); err != nil {
		t.Fatalf("decode inner: %v", err)
	}
	if inner.Code != apperror.CodeValidationError {
		t.Errorf("code = %q, want %q", inner.Code, apperror.CodeValidationError)
	}
	if inner.Message != "Invalid request body" {
		t.Errorf("message = %q, want %q", inner.Message, "Invalid request body")
	}
	for _, f := range expectedFields {
		if _, ok := inner.Details.Fields[f]; !ok {
			t.Errorf("missing expected field %q in details.fields (got %v)", f, inner.Details.Fields)
		}
	}

	// Report unexpected extra fields so mismatches are easy to diagnose.
	expected := make(map[string]struct{}, len(expectedFields))
	for _, f := range expectedFields {
		expected[f] = struct{}{}
	}
	for f := range inner.Details.Fields {
		if _, ok := expected[f]; !ok {
			t.Errorf("unexpected extra field %q in details.fields (got %v)", f, inner.Details.Fields)
		}
	}
}

// ---------- Register – validation tests ----------

// TestRegister_Validation groups simple input-validation cases that should all
// return 422 with VALIDATION_ERROR. Each subtest verifies status code and error
// code without inspecting field-level details.
func TestRegister_Validation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"missing_email", `{"password":"securepass1"}`},
		{"short_password", `{"email":"user@example.com","password":"short"}`},
		{"invalid_email", `{"email":"not-an-email","password":"securepass1"}`},
		{"invalid_json", `{invalid json`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newTestAuthHandler(&mockEmailSender{})

			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.Register(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
			}

			var resp apperror.ErrorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Error.Code != apperror.CodeValidationError {
				t.Errorf("code = %q, want %q", resp.Error.Code, apperror.CodeValidationError)
			}
		})
	}
}

// TestRegister_ValidationError_MatchesSpecEnvelope verifies the full JSON
// envelope structure including per-field error details, Content-Type header,
// and the spec-mandated message casing.
func TestRegister_ValidationError_MatchesSpecEnvelope(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	// Both fields invalid.
	body := `{"email":"","password":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Register(rec, req)

	assertValidationEnvelope(t, rec, []string{"email", "password"})
}

// TestRegister_MissingBothFields_ReturnsBothFieldErrors verifies that when
// both email and password are missing, the per-field details include errors
// for both fields.
func TestRegister_MissingBothFields_ReturnsBothFieldErrors(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Register(rec, req)

	assertValidationEnvelope(t, rec, []string{"email", "password"})
}

// ---------- VerifyOTP – validation tests ----------

// TestVerifyOTP_Validation groups input-validation cases that should all
// return 422 with VALIDATION_ERROR. Each subtest verifies status code and
// error code without reaching the database.
func TestVerifyOTP_Validation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"missing_email", `{"otp":"123456"}`},
		{"missing_otp", `{"email":"user@example.com"}`},
		{"invalid_otp_too_short", `{"email":"user@example.com","otp":"123"}`},
		{"invalid_otp_letters", `{"email":"user@example.com","otp":"abcdef"}`},
		{"invalid_otp_too_long", `{"email":"user@example.com","otp":"1234567"}`},
		{"invalid_email", `{"email":"not-an-email","otp":"123456"}`},
		{"invalid_json", `{invalid json`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newTestAuthHandler(&mockEmailSender{})

			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify-otp", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.VerifyOTP(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
			}

			var resp apperror.ErrorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Error.Code != apperror.CodeValidationError {
				t.Errorf("code = %q, want %q", resp.Error.Code, apperror.CodeValidationError)
			}
		})
	}
}

// TestVerifyOTP_ValidationError_MatchesSpecEnvelope verifies the full JSON
// envelope structure including per-field error details, Content-Type header,
// and the spec-mandated message casing for verify-otp validation errors.
func TestVerifyOTP_ValidationError_MatchesSpecEnvelope(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	// Both fields invalid.
	body := `{"email":"","otp":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify-otp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.VerifyOTP(rec, req)

	assertValidationEnvelope(t, rec, []string{"email", "otp"})
}

// TestVerifyOTP_MissingBothFields_ReturnsBothFieldErrors verifies that when
// both email and otp are missing, the per-field details include errors for
// both fields.
func TestVerifyOTP_MissingBothFields_ReturnsBothFieldErrors(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify-otp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.VerifyOTP(rec, req)

	assertValidationEnvelope(t, rec, []string{"email", "otp"})
}

// ---------- Login – validation tests ----------

// TestLogin_Validation groups input-validation cases that should all return
// 422 with VALIDATION_ERROR. Each subtest verifies status code and error code
// without reaching the database.
func TestLogin_Validation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"missing_email", `{"password":"securepass1"}`},
		{"missing_password", `{"email":"user@example.com"}`},
		{"short_password", `{"email":"user@example.com","password":"short"}`},
		{"invalid_email", `{"email":"not-an-email","password":"securepass1"}`},
		{"invalid_json", `{invalid json`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newTestAuthHandler(&mockEmailSender{})

			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.Login(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
			}

			var resp apperror.ErrorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Error.Code != apperror.CodeValidationError {
				t.Errorf("code = %q, want %q", resp.Error.Code, apperror.CodeValidationError)
			}
		})
	}
}

// TestLogin_ValidationError_MatchesSpecEnvelope verifies the full JSON
// envelope structure for login validation errors.
func TestLogin_ValidationError_MatchesSpecEnvelope(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"email":"","password":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Login(rec, req)

	assertValidationEnvelope(t, rec, []string{"email", "password"})
}

// TestLogin_MissingBothFields_ReturnsBothFieldErrors verifies that when
// both email and password are missing, the per-field details include errors
// for both fields.
func TestLogin_MissingBothFields_ReturnsBothFieldErrors(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Login(rec, req)

	assertValidationEnvelope(t, rec, []string{"email", "password"})
}

// ---------- Refresh – validation tests ----------

// TestRefresh_Validation groups input-validation cases that should all return
// 422 with VALIDATION_ERROR. Each subtest verifies status code and error code
// without reaching the database.
func TestRefresh_Validation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"missing_refresh_token", `{}`},
		{"empty_refresh_token", `{"refresh_token":""}`},
		{"whitespace_refresh_token", `{"refresh_token":"   "}`},
		{"invalid_json", `{invalid json`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newTestAuthHandler(&mockEmailSender{})

			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.Refresh(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
			}

			var resp apperror.ErrorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Error.Code != apperror.CodeValidationError {
				t.Errorf("code = %q, want %q", resp.Error.Code, apperror.CodeValidationError)
			}
		})
	}
}

// TestRefresh_ValidationError_MatchesSpecEnvelope verifies the JSON envelope
// structure for refresh validation errors.
func TestRefresh_ValidationError_MatchesSpecEnvelope(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"refresh_token":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Refresh(rec, req)

	assertValidationEnvelope(t, rec, []string{"refresh_token"})
}

// ---------- ForgotPassword – validation tests ----------

// TestForgotPassword_Validation groups input-validation cases that should all
// return 422 with VALIDATION_ERROR. Each subtest verifies status code and
// error code without reaching the database.
func TestForgotPassword_Validation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"invalid_email", `{"email":"not-an-email"}`},
		{"empty_email", `{"email":""}`},
		{"missing_email", `{}`},
		{"invalid_json", `{invalid`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newTestAuthHandler(&mockEmailSender{})

			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.ForgotPassword(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
			}

			var resp apperror.ErrorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Error.Code != apperror.CodeValidationError {
				t.Errorf("code = %q, want %q", resp.Error.Code, apperror.CodeValidationError)
			}
		})
	}
}

// TestForgotPassword_ValidationError_MatchesSpecEnvelope verifies the JSON
// envelope structure for forgot-password validation errors.
func TestForgotPassword_ValidationError_MatchesSpecEnvelope(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"email":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ForgotPassword(rec, req)

	assertValidationEnvelope(t, rec, []string{"email"})
}

// TestForgotPassword_ValidationPath_NotDelayed verifies that validation
// failures in ForgotPassword return immediately, without waiting for the
// timing normalization floor that protects account-specific code paths.
func TestForgotPassword_ValidationPath_NotDelayed(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"email":"not-an-email"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	start := time.Now()
	h.ForgotPassword(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	// The validation path should return well before the timing floor.
	// Using half the floor as the threshold gives a generous margin
	// without making the assertion timing-sensitive.
	if elapsed >= service.ForgotPasswordMinDuration/2 {
		t.Errorf("validation path took %v, expected well under %v", elapsed, service.ForgotPasswordMinDuration)
	}
}

// ---------- ResetPassword – validation tests ----------

// TestResetPassword_Validation groups input-validation cases that should all
// return 422 with VALIDATION_ERROR. Each subtest verifies status code and
// error code without reaching the database.
func TestResetPassword_Validation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"missing_email", `{"otp":"123456","new_password":"securepass1"}`},
		{"missing_otp", `{"email":"user@example.com","new_password":"securepass1"}`},
		{"missing_new_password", `{"email":"user@example.com","otp":"123456"}`},
		{"short_new_password", `{"email":"user@example.com","otp":"123456","new_password":"short"}`},
		{"invalid_email", `{"email":"not-an-email","otp":"123456","new_password":"securepass1"}`},
		{"invalid_otp", `{"email":"user@example.com","otp":"abc","new_password":"securepass1"}`},
		{"invalid_json", `{invalid json`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newTestAuthHandler(&mockEmailSender{})

			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.ResetPassword(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
			}

			var resp apperror.ErrorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Error.Code != apperror.CodeValidationError {
				t.Errorf("code = %q, want %q", resp.Error.Code, apperror.CodeValidationError)
			}
		})
	}
}

// TestResetPassword_ValidationError_MatchesSpecEnvelope verifies the full JSON
// envelope structure for reset-password validation errors.
func TestResetPassword_ValidationError_MatchesSpecEnvelope(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	// All fields invalid.
	body := `{"email":"","otp":"","new_password":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ResetPassword(rec, req)

	assertValidationEnvelope(t, rec, []string{"email", "otp", "new_password"})
}

// TestResetPassword_MissingAllFields_ReturnsAllFieldErrors verifies that when
// all fields are missing, the per-field details include errors for all three.
func TestResetPassword_MissingAllFields_ReturnsAllFieldErrors(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ResetPassword(rec, req)

	assertValidationEnvelope(t, rec, []string{"email", "otp", "new_password"})
}

// ---------- Strict JSON decoding – unknown fields ----------

// assertBodyValidationError is a test helper that verifies a 422 response
// matches the BACKEND_SPEC error envelope with VALIDATION_ERROR code, the
// "Invalid request body" message, and no per-field details. This is the
// shape produced by decodeJSONBody when it rejects unknown fields or trailing
// JSON documents.
func assertBodyValidationError(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeValidationError {
		t.Errorf("code = %q, want %q", resp.Error.Code, apperror.CodeValidationError)
	}
	if resp.Error.Message != "Invalid request body" {
		t.Errorf("message = %q, want %q", resp.Error.Message, "Invalid request body")
	}
}

// TestRegister_UnknownField_Rejected verifies that Register rejects a request
// containing an unknown JSON field and returns a VALIDATION_ERROR.
func TestRegister_UnknownField_Rejected(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"email":"user@example.com","password":"securepass1","unknown":"field"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Register(rec, req)

	assertBodyValidationError(t, rec)
}

// TestRegister_TrailingJSON_Rejected verifies that Register rejects a request
// with a valid JSON object followed by a trailing JSON document.
func TestRegister_TrailingJSON_Rejected(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"email":"user@example.com","password":"securepass1"}{"extra":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Register(rec, req)

	assertBodyValidationError(t, rec)
}

// TestVerifyOTP_UnknownField_Rejected verifies that VerifyOTP rejects a
// request containing an unknown JSON field.
func TestVerifyOTP_UnknownField_Rejected(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"email":"user@example.com","otp":"123456","extra":"value"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify-otp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.VerifyOTP(rec, req)

	assertBodyValidationError(t, rec)
}

// TestVerifyOTP_TrailingJSON_Rejected verifies that VerifyOTP rejects a
// request with a valid JSON object followed by a trailing JSON document.
func TestVerifyOTP_TrailingJSON_Rejected(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"email":"user@example.com","otp":"123456"}{"trailing":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify-otp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.VerifyOTP(rec, req)

	assertBodyValidationError(t, rec)
}

// TestLogin_UnknownField_Rejected verifies that Login rejects a request
// containing an unknown JSON field.
func TestLogin_UnknownField_Rejected(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"email":"user@example.com","password":"securepass1","remember":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Login(rec, req)

	assertBodyValidationError(t, rec)
}

// TestLogin_TrailingJSON_Rejected verifies that Login rejects a request with
// a valid JSON object followed by a trailing JSON document.
func TestLogin_TrailingJSON_Rejected(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"email":"user@example.com","password":"securepass1"}null`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Login(rec, req)

	assertBodyValidationError(t, rec)
}

// TestRefresh_UnknownField_Rejected verifies that Refresh rejects a request
// containing an unknown JSON field.
func TestRefresh_UnknownField_Rejected(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"refresh_token":"some-valid-token","device_id":"abc"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Refresh(rec, req)

	assertBodyValidationError(t, rec)
}

// TestRefresh_TrailingJSON_Rejected verifies that Refresh rejects a request
// with a valid JSON object followed by a trailing JSON document.
func TestRefresh_TrailingJSON_Rejected(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"refresh_token":"some-valid-token"}[]`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Refresh(rec, req)

	assertBodyValidationError(t, rec)
}

// TestForgotPassword_UnknownField_Rejected verifies that ForgotPassword
// rejects a request containing an unknown JSON field.
func TestForgotPassword_UnknownField_Rejected(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"email":"user@example.com","phone":"+1234567890"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ForgotPassword(rec, req)

	assertBodyValidationError(t, rec)
}

// TestForgotPassword_TrailingJSON_Rejected verifies that ForgotPassword
// rejects a request with a valid JSON object followed by trailing data.
func TestForgotPassword_TrailingJSON_Rejected(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"email":"user@example.com"}{"email":"other@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ForgotPassword(rec, req)

	assertBodyValidationError(t, rec)
}

// TestResetPassword_UnknownField_Rejected verifies that ResetPassword rejects
// a request containing an unknown JSON field.
func TestResetPassword_UnknownField_Rejected(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"email":"user@example.com","otp":"123456","new_password":"securepass1","confirm":"securepass1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ResetPassword(rec, req)

	assertBodyValidationError(t, rec)
}

// TestResetPassword_TrailingJSON_Rejected verifies that ResetPassword rejects
// a request with a valid JSON object followed by trailing data.
func TestResetPassword_TrailingJSON_Rejected(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	body := `{"email":"user@example.com","otp":"123456","new_password":"securepass1"}42`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ResetPassword(rec, req)

	assertBodyValidationError(t, rec)
}

// ---------- Empty body – rejected across all POST auth endpoints ----------

// TestEmptyBody_RejectedAcrossAllAuthEndpoints verifies that all 6 POST auth
// endpoints reject an empty request body with the VALIDATION_ERROR envelope and
// the "Invalid request body" message. This exercises the decodeJSONBody path
// that handles io.EOF (no JSON at all) rather than field-level validation.
func TestEmptyBody_RejectedAcrossAllAuthEndpoints(t *testing.T) {
	t.Parallel()

	h := newTestAuthHandler(&mockEmailSender{})

	cases := []struct {
		name    string
		path    string
		handler http.HandlerFunc
	}{
		{"register", "/api/v1/auth/register", h.Register},
		{"verify_otp", "/api/v1/auth/verify-otp", h.VerifyOTP},
		{"login", "/api/v1/auth/login", h.Login},
		{"refresh", "/api/v1/auth/refresh", h.Refresh},
		{"forgot_password", "/api/v1/auth/forgot-password", h.ForgotPassword},
		{"reset_password", "/api/v1/auth/reset-password", h.ResetPassword},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(""))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			tc.handler(rec, req)

			assertBodyValidationError(t, rec)
		})
	}
}

// ---------- MaxBytesError – body too large tests ----------

// TestRegister_OversizedBody_ReturnsBodyTooLarge verifies that a request body
// exceeding MaxBytesReader's limit returns 422 with the "Request body too
// large" message rather than the generic "Invalid request body".
//
// In production, MaxBytesReader is applied by middleware in server.go before
// the handler runs. This test wraps the body manually to exercise the
// decodeJSONBody branch that distinguishes MaxBytesError from other decode
// failures.
func TestRegister_OversizedBody_ReturnsBodyTooLarge(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	// Build a valid-looking JSON payload that exceeds the byte limit.
	bigBody := `{"email":"` + strings.Repeat("a", 256) + `@example.com","password":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	req.Body = http.MaxBytesReader(rec, req.Body, 10) // 10-byte limit

	h.Register(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeValidationError {
		t.Errorf("code = %q, want %q", resp.Error.Code, apperror.CodeValidationError)
	}
	if resp.Error.Message != "Request body too large" {
		t.Errorf("message = %q, want %q", resp.Error.Message, "Request body too large")
	}
}

// ---------- GetMe – unit tests ----------

// TestGetMe_MissingUser_ReturnsUnauthorized verifies that GetMe returns 401
// with the UNAUTHORIZED error code when the request context does not contain
// an authenticated user (i.e. middleware.UserFromContext returns false).
func TestGetMe_MissingUser_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()
	h := newTestAuthHandler(&mockEmailSender{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()

	h.GetMe(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeUnauthorized {
		t.Errorf("code = %q, want %q", resp.Error.Code, apperror.CodeUnauthorized)
	}
}

// ---------- Mock-service tests (exercises AuthServicer interface) ----------

// mockAuthService is a minimal AuthServicer stub that returns pre-configured
// results. It allows handler tests to exercise service-error mapping and
// success paths without a real service or database.
type mockAuthService struct {
	loginFn          func(ctx context.Context, in service.LoginInput) (service.AuthOutput, error)
	registerFn       func(ctx context.Context, in service.RegisterInput) (service.RegisterOutput, error)
	verifyOTPFn      func(ctx context.Context, in service.VerifyOTPInput) (service.AuthOutput, error)
	refreshFn        func(ctx context.Context, in service.RefreshInput) (service.RefreshOutput, error)
	forgotPasswordFn func(ctx context.Context, in service.ForgotPasswordInput) (service.MessageOutput, error)
	resetPasswordFn  func(ctx context.Context, in service.ResetPasswordInput) (service.MessageOutput, error)
	getMeFn          func(ctx context.Context, in service.GetMeInput) (service.UserProfile, error)
}

// Compile-time check: *mockAuthService satisfies AuthServicer.
var _ AuthServicer = (*mockAuthService)(nil)

func (m *mockAuthService) Register(ctx context.Context, in service.RegisterInput) (service.RegisterOutput, error) {
	if m.registerFn != nil {
		return m.registerFn(ctx, in)
	}
	return service.RegisterOutput{}, nil
}

func (m *mockAuthService) VerifyOTP(ctx context.Context, in service.VerifyOTPInput) (service.AuthOutput, error) {
	if m.verifyOTPFn != nil {
		return m.verifyOTPFn(ctx, in)
	}
	return service.AuthOutput{}, nil
}

func (m *mockAuthService) Login(ctx context.Context, in service.LoginInput) (service.AuthOutput, error) {
	if m.loginFn != nil {
		return m.loginFn(ctx, in)
	}
	return service.AuthOutput{}, nil
}

func (m *mockAuthService) Refresh(ctx context.Context, in service.RefreshInput) (service.RefreshOutput, error) {
	if m.refreshFn != nil {
		return m.refreshFn(ctx, in)
	}
	return service.RefreshOutput{}, nil
}

func (m *mockAuthService) ForgotPassword(ctx context.Context, in service.ForgotPasswordInput) (service.MessageOutput, error) {
	if m.forgotPasswordFn != nil {
		return m.forgotPasswordFn(ctx, in)
	}
	return service.MessageOutput{}, nil
}

func (m *mockAuthService) ResetPassword(ctx context.Context, in service.ResetPasswordInput) (service.MessageOutput, error) {
	if m.resetPasswordFn != nil {
		return m.resetPasswordFn(ctx, in)
	}
	return service.MessageOutput{}, nil
}

func (m *mockAuthService) GetMe(ctx context.Context, in service.GetMeInput) (service.UserProfile, error) {
	if m.getMeFn != nil {
		return m.getMeFn(ctx, in)
	}
	return service.UserProfile{}, nil
}

// TestLogin_ServiceConflict_Returns409 verifies that when the service returns
// ErrConflict the handler translates it to a 409 response.
func TestLogin_ServiceConflict_Returns409(t *testing.T) {
	t.Parallel()

	mock := &mockAuthService{
		loginFn: func(_ context.Context, _ service.LoginInput) (service.AuthOutput, error) {
			return service.AuthOutput{}, fmt.Errorf("%w: email already registered", service.ErrConflict)
		},
	}
	h := NewAuthHandler(mock, noopLogger())

	body := `{"email":"user@example.com","password":"securepass1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Login(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeConflict {
		t.Errorf("code = %q, want %q", resp.Error.Code, apperror.CodeConflict)
	}
}

// TestLogin_ServiceUnauthorized_Returns401 verifies that when the service returns
// ErrUnauthorized the handler translates it to a 401 response.
func TestLogin_ServiceUnauthorized_Returns401(t *testing.T) {
	t.Parallel()

	mock := &mockAuthService{
		loginFn: func(_ context.Context, _ service.LoginInput) (service.AuthOutput, error) {
			return service.AuthOutput{}, fmt.Errorf("%w: invalid email or password", service.ErrUnauthorized)
		},
	}
	h := NewAuthHandler(mock, noopLogger())

	body := `{"email":"user@example.com","password":"securepass1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Login(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeUnauthorized {
		t.Errorf("code = %q, want %q", resp.Error.Code, apperror.CodeUnauthorized)
	}
}

// TestLogin_ServiceInternal_Returns500 verifies that when the service returns
// ErrInternal the handler translates it to a 500 response.
func TestLogin_ServiceInternal_Returns500(t *testing.T) {
	t.Parallel()

	mock := &mockAuthService{
		loginFn: func(_ context.Context, _ service.LoginInput) (service.AuthOutput, error) {
			return service.AuthOutput{}, service.ErrInternal
		},
	}
	h := NewAuthHandler(mock, noopLogger())

	body := `{"email":"user@example.com","password":"securepass1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Login(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// TestRegister_ServiceSuccess_ReturnsOTPRequired verifies that when the service
// returns a successful RegisterOutput, the handler returns 200 with otp_required.
func TestRegister_ServiceSuccess_ReturnsOTPRequired(t *testing.T) {
	t.Parallel()

	mock := &mockAuthService{
		registerFn: func(_ context.Context, _ service.RegisterInput) (service.RegisterOutput, error) {
			return service.RegisterOutput{OTPRequired: true}, nil
		},
	}
	h := NewAuthHandler(mock, noopLogger())

	body := `{"email":"user@example.com","password":"securepass1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Register(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp struct {
		OTPRequired bool `json:"otp_required"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OTPRequired {
		t.Error("expected otp_required = true")
	}
}

// TestLogin_ServiceSuccess_ReturnsAuthResponse verifies that when the service
// returns a successful AuthOutput, the handler returns 200 with tokens and user.
func TestLogin_ServiceSuccess_ReturnsAuthResponse(t *testing.T) {
	t.Parallel()

	mock := &mockAuthService{
		loginFn: func(_ context.Context, _ service.LoginInput) (service.AuthOutput, error) {
			return service.AuthOutput{
				AccessToken:  "access-tok",
				RefreshToken: "refresh-tok",
				User: service.UserProfile{
					ID:        "uid-123",
					Email:     "user@example.com",
					Name:      "Test User",
					Username:  "user_abc",
					CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			}, nil
		},
	}
	h := NewAuthHandler(mock, noopLogger())

	body := `{"email":"user@example.com","password":"securepass1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Login(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		User         struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"user"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AccessToken != "access-tok" {
		t.Errorf("access_token = %q, want %q", resp.AccessToken, "access-tok")
	}
	if resp.RefreshToken != "refresh-tok" {
		t.Errorf("refresh_token = %q, want %q", resp.RefreshToken, "refresh-tok")
	}
	if resp.User.ID != "uid-123" {
		t.Errorf("user.id = %q, want %q", resp.User.ID, "uid-123")
	}
	if resp.User.Email != "user@example.com" {
		t.Errorf("user.email = %q, want %q", resp.User.Email, "user@example.com")
	}
}

// TestForgotPassword_ServiceRateLimited_Returns429 verifies that when the
// service returns ErrRateLimited the handler translates it to a 429 response.
func TestForgotPassword_ServiceRateLimited_Returns429(t *testing.T) {
	t.Parallel()

	mock := &mockAuthService{
		forgotPasswordFn: func(_ context.Context, _ service.ForgotPasswordInput) (service.MessageOutput, error) {
			return service.MessageOutput{}, fmt.Errorf("%w: too many requests", service.ErrRateLimited)
		},
	}
	h := NewAuthHandler(mock, noopLogger())

	body := `{"email":"user@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ForgotPassword(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
}
