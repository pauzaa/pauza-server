package middleware_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/middleware"
)

const testSecret = "test-secret-key-at-least-32-bytes!"

// discardLogger returns a logger that silently drops all output, suitable for
// tests that do not need to inspect log messages.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// handlerSpy records whether ServeHTTP was called. The atomic.Bool is
// safe for concurrent use, which future-proofs the spy against parallel
// sub-tests or race-detector runs.
type handlerSpy struct {
	called atomic.Bool
}

func (s *handlerSpy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.called.Store(true)
	w.WriteHeader(http.StatusOK)
}

func assertUnauthorized(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error.Code != apperror.CodeUnauthorized {
		t.Errorf("error code = %q, want %q", resp.Error.Code, apperror.CodeUnauthorized)
	}
	if resp.Error.Message != "missing or invalid authentication" {
		t.Errorf("error message = %q, want %q", resp.Error.Message, "missing or invalid authentication")
	}
}

func TestJWTAuth_MissingHeader(t *testing.T) {
	spy := &handlerSpy{}
	handler := middleware.JWTAuth(testSecret, discardLogger())(spy)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertUnauthorized(t, rec)
	if spy.called.Load() {
		t.Error("next handler should not have been called")
	}
}

func TestJWTAuth_MalformedHeader(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"no_bearer_prefix", "Token abc123"},
		{"missing_token_value", "Bearer"},
		{"empty_token_value", "Bearer "},
		{"basic_scheme", "Basic dXNlcjpwYXNz"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &handlerSpy{}
			handler := middleware.JWTAuth(testSecret, discardLogger())(spy)

			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.Header.Set("Authorization", tc.header)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assertUnauthorized(t, rec)
			if spy.called.Load() {
				t.Error("next handler should not have been called")
			}
		})
	}
}

func TestJWTAuth_BearerCaseInsensitive(t *testing.T) {
	userID := "550e8400-e29b-41d4-a716-446655440000"
	email := "test@example.com"

	tokenStr, err := auth.IssueAccessToken(userID, email, testSecret, 15*time.Minute)
	if err != nil {
		t.Fatalf("IssueAccessToken() error = %v", err)
	}

	cases := []struct {
		name   string
		prefix string
	}{
		{"lowercase", "bearer"},
		{"uppercase", "BEARER"},
		{"mixed_case", "bEaReR"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotUser middleware.AuthUser
			var gotOK bool
			called := false

			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				gotUser, gotOK = middleware.UserFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})

			handler := middleware.JWTAuth(testSecret, discardLogger())(inner)

			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.Header.Set("Authorization", tc.prefix+" "+tokenStr)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if !called {
				t.Fatal("next handler should have been called")
			}
			if !gotOK {
				t.Fatal("UserFromContext returned ok = false, want true")
			}
			if gotUser.UserID != userID {
				t.Errorf("UserID = %q, want %q", gotUser.UserID, userID)
			}
			if gotUser.Email != email {
				t.Errorf("Email = %q, want %q", gotUser.Email, email)
			}
		})
	}
}

func TestJWTAuth_ExpiredToken(t *testing.T) {
	tokenStr, err := auth.IssueAccessToken(
		"550e8400-e29b-41d4-a716-446655440000",
		"test@example.com",
		testSecret,
		-1*time.Second,
	)
	if err != nil {
		t.Fatalf("IssueAccessToken() error = %v", err)
	}

	spy := &handlerSpy{}
	handler := middleware.JWTAuth(testSecret, discardLogger())(spy)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertUnauthorized(t, rec)
	if spy.called.Load() {
		t.Error("next handler should not have been called")
	}
}

func TestJWTAuth_WrongSecret(t *testing.T) {
	const wrongSecret = "wrong-secret-at-least-32-bytes!!"

	tokenStr, err := auth.IssueAccessToken(
		"550e8400-e29b-41d4-a716-446655440000",
		"test@example.com",
		testSecret,
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("IssueAccessToken() error = %v", err)
	}

	spy := &handlerSpy{}
	handler := middleware.JWTAuth(wrongSecret, discardLogger())(spy)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertUnauthorized(t, rec)
	if spy.called.Load() {
		t.Error("next handler should not have been called")
	}
}

func TestJWTAuth_ValidToken(t *testing.T) {
	userID := "550e8400-e29b-41d4-a716-446655440000"
	email := "test@example.com"

	tokenStr, err := auth.IssueAccessToken(userID, email, testSecret, 15*time.Minute)
	if err != nil {
		t.Fatalf("IssueAccessToken() error = %v", err)
	}

	var gotUser middleware.AuthUser
	var gotOK bool
	called := false

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		gotUser, gotOK = middleware.UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware.JWTAuth(testSecret, discardLogger())(inner)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !called {
		t.Fatal("next handler should have been called")
	}
	if !gotOK {
		t.Fatal("UserFromContext returned ok = false, want true")
	}
	if gotUser.UserID != userID {
		t.Errorf("UserID = %q, want %q", gotUser.UserID, userID)
	}
	if gotUser.Email != email {
		t.Errorf("Email = %q, want %q", gotUser.Email, email)
	}
}

// =========================================================================
// AdminJWTAuth tests
// =========================================================================

func TestAdminJWTAuth_ValidAdminToken(t *testing.T) {
	adminID := "admin-001"
	tokenStr, err := auth.IssueAdminToken(adminID, testSecret, 30*time.Minute)
	if err != nil {
		t.Fatalf("IssueAdminToken() error = %v", err)
	}

	var gotUser middleware.AuthUser
	var gotOK bool
	called := false

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		gotUser, gotOK = middleware.UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware.AdminJWTAuth(testSecret, discardLogger())(inner)

	req := httptest.NewRequest(http.MethodGet, "/admin/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !called {
		t.Fatal("next handler should have been called")
	}
	if !gotOK {
		t.Fatal("UserFromContext returned ok = false, want true")
	}
	if gotUser.UserID != adminID {
		t.Errorf("UserID = %q, want %q", gotUser.UserID, adminID)
	}
}

func TestAdminJWTAuth_RejectsUserToken(t *testing.T) {
	// A valid user JWT (no role) should be rejected by AdminJWTAuth.
	tokenStr, err := auth.IssueAccessToken(
		"550e8400-e29b-41d4-a716-446655440000",
		"user@example.com",
		testSecret,
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("IssueAccessToken() error = %v", err)
	}

	spy := &handlerSpy{}
	handler := middleware.AdminJWTAuth(testSecret, discardLogger())(spy)

	req := httptest.NewRequest(http.MethodGet, "/admin/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertUnauthorized(t, rec)
	if spy.called.Load() {
		t.Error("next handler should not have been called")
	}
}

func TestAdminJWTAuth_MissingHeader(t *testing.T) {
	spy := &handlerSpy{}
	handler := middleware.AdminJWTAuth(testSecret, discardLogger())(spy)

	req := httptest.NewRequest(http.MethodGet, "/admin/protected", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertUnauthorized(t, rec)
	if spy.called.Load() {
		t.Error("next handler should not have been called")
	}
}

func TestAdminJWTAuth_ExpiredToken(t *testing.T) {
	tokenStr, err := auth.IssueAdminToken(
		"admin-001",
		testSecret,
		-1*time.Second,
	)
	if err != nil {
		t.Fatalf("IssueAdminToken() error = %v", err)
	}

	spy := &handlerSpy{}
	handler := middleware.AdminJWTAuth(testSecret, discardLogger())(spy)

	req := httptest.NewRequest(http.MethodGet, "/admin/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertUnauthorized(t, rec)
	if spy.called.Load() {
		t.Error("next handler should not have been called")
	}
}

func TestAdminJWTAuth_WrongSecret(t *testing.T) {
	const wrongSecret = "wrong-secret-at-least-32-bytes!!"

	tokenStr, err := auth.IssueAdminToken("admin-001", testSecret, 30*time.Minute)
	if err != nil {
		t.Fatalf("IssueAdminToken() error = %v", err)
	}

	spy := &handlerSpy{}
	handler := middleware.AdminJWTAuth(wrongSecret, discardLogger())(spy)

	req := httptest.NewRequest(http.MethodGet, "/admin/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertUnauthorized(t, rec)
	if spy.called.Load() {
		t.Error("next handler should not have been called")
	}
}

// =========================================================================
// JWTAuth logging tests
// =========================================================================

func TestJWTAuth_MalformedInput_LogsWarning(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	spy := &handlerSpy{}
	handler := middleware.JWTAuth(testSecret, logger)(spy)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Token abc123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertUnauthorized(t, rec)
	if spy.called.Load() {
		t.Error("next handler should not have been called")
	}

	logged := buf.String()
	if !strings.Contains(logged, "jwt auth: malformed authorization header") {
		t.Errorf("expected warning message in log output, got: %s", logged)
	}
	if !strings.Contains(logged, `"level":"WARN"`) {
		t.Errorf("expected WARN level in log output, got: %s", logged)
	}
	if !strings.Contains(logged, `"path":"/protected"`) {
		t.Errorf("expected path field in log output, got: %s", logged)
	}
	// Ensure the raw Authorization header value is NOT logged (no secret leak).
	if strings.Contains(logged, "abc123") {
		t.Errorf("log output must not contain the raw token value, got: %s", logged)
	}
}
