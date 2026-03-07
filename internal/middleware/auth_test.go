package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/middleware"
)

const testSecret = "test-secret-key-at-least-32-bytes!"

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
	handler := middleware.JWTAuth(testSecret)(spy)

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
			handler := middleware.JWTAuth(testSecret)(spy)

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

			handler := middleware.JWTAuth(testSecret)(inner)

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
	handler := middleware.JWTAuth(testSecret)(spy)

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
	handler := middleware.JWTAuth(wrongSecret)(spy)

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

	handler := middleware.JWTAuth(testSecret)(inner)

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
