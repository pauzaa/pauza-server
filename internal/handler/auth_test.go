package handler

// This file contains unit tests for auth handler validation paths. The
// handler is constructed with a nil database pool, so any test that reaches
// a database call will panic — this is intentional. DB-backed flows
// (credential checks, OTP verification, token persistence, anti-enumeration
// at the DB layer, etc.) are covered by integration tests in
// auth_integration_test.go which run against a real Postgres instance.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
)

// mockEmailSender is a no-op stub of mail.EmailSender for validation-only
// handler tests. None of the unit tests here reach the mailer, so the
// implementation simply returns nil.
//
// No explicit compile-time interface check (var _ mail.EmailSender = ...) is
// needed: NewAuthHandler's mailer parameter is typed mail.EmailSender, so the
// compiler already verifies satisfaction at the call site in newTestAuthHandler.
type mockEmailSender struct{}

func (m *mockEmailSender) SendOTP(_ context.Context, _, _, _ string) error {
	return nil
}

// newTestAuthHandler builds an AuthHandler with nil pool (DB calls will panic
// if reached) and the given mock email sender. This is suitable for tests that
// exercise only request validation, which returns before any DB interaction.
func newTestAuthHandler(mailer *mockEmailSender) *AuthHandler {
	return NewAuthHandler(
		nil, // pool – nil is fine for validation-only tests
		mailer,
		"test-secret",
		0,
		0,
		noopLogger(),
	)
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
	if elapsed >= forgotPasswordMinDuration/2 {
		t.Errorf("validation path took %v, expected well under %v", elapsed, forgotPasswordMinDuration)
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

// ---------- generateUsername – format and uniqueness ----------

// TestGenerateUsername_Format verifies that generateUsername returns a string
// with the "user_" prefix followed by exactly 24 lowercase hex characters
// (12 random bytes = 96 bits of entropy).
func TestGenerateUsername_Format(t *testing.T) {
	t.Parallel()

	u, err := generateUsername()
	if err != nil {
		t.Fatalf("generateUsername() error: %v", err)
	}

	// Total length: "user_" (5) + 24 hex chars = 29.
	if len(u) != 29 {
		t.Fatalf("len = %d, want 29; got %q", len(u), u)
	}
	if !strings.HasPrefix(u, "user_") {
		t.Errorf("prefix = %q, want %q", u[:5], "user_")
	}

	// The hex suffix must be exactly 24 lowercase hex characters.
	hexPart := u[5:]
	for i, c := range hexPart {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("hexPart[%d] = %q, not a lowercase hex digit", i, string(c))
		}
	}
}

// TestGenerateUsername_Uniqueness calls generateUsername 100 times and asserts
// all results are distinct. With 96 bits of entropy the probability of any
// collision in 100 draws is negligible (~2^-79); a failure here would indicate
// a broken random source rather than a birthday collision.
func TestGenerateUsername_Uniqueness(t *testing.T) {
	t.Parallel()

	const n = 100
	seen := make(map[string]struct{}, n)
	for range n {
		u, err := generateUsername()
		if err != nil {
			t.Fatalf("generateUsername() error: %v", err)
		}
		if _, dup := seen[u]; dup {
			t.Fatalf("duplicate username after %d calls: %q", len(seen)+1, u)
		}
		seen[u] = struct{}{}
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
