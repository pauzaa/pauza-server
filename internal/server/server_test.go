package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/config"
)

// testConfig returns a minimal config.Config suitable for unit tests that
// exercise server wiring without a real database or SMTP backend. Values
// are intentionally hardcoded so tests stay deterministic and never depend
// on environment variables.
func testConfig() *config.Config {
	return &config.Config{
		Port:               8080,
		LogLevel:           "info",
		JWTSecret:          "test-secret-for-unit-tests-only",
		JWTAccessTokenTTL:  15 * time.Minute,
		JWTRefreshTokenTTL: 7 * 24 * time.Hour,
		SMTPHost:           "localhost",
		SMTPPort:           1025,
		SMTPUsername:       "test",
		SMTPPassword:       "test",
		SMTPFrom:           "test@example.com",
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestNew_HealthEndpoint(t *testing.T) {
	srv, cleanup := New(testConfig(), testLogger(), nil)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got: %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "degraded" {
		t.Errorf("expected status 'degraded', got: %q", resp["status"])
	}
	if resp["timestamp"] == "" {
		t.Fatal("expected timestamp to be set, got empty string")
	}
	if _, err := time.Parse(time.RFC3339, resp["timestamp"]); err != nil {
		t.Fatalf("expected RFC3339 timestamp, got %q: %v", resp["timestamp"], err)
	}
	if !strings.HasSuffix(resp["timestamp"], "Z") {
		t.Errorf("expected UTC timestamp ending in 'Z', got: %q", resp["timestamp"])
	}
}

func TestNew_NotFoundRoute(t *testing.T) {
	srv, cleanup := New(testConfig(), testLogger(), nil)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got: %d", rec.Code)
	}
}

func TestNew_HealthMethodNotAllowed(t *testing.T) {
	srv, cleanup := New(testConfig(), testLogger(), nil)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got: %d", rec.Code)
	}
}

func TestNew_RequestIDHeader(t *testing.T) {
	srv, cleanup := New(testConfig(), testLogger(), nil)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	reqID := rec.Header().Get("X-Request-Id")
	if reqID == "" {
		t.Error("expected X-Request-Id header to be set in response, got empty string")
	}
}

func TestNew_RequestIDEchoesClientValue(t *testing.T) {
	srv, cleanup := New(testConfig(), testLogger(), nil)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-Id", "test-client-id-123")
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	got := rec.Header().Get("X-Request-Id")
	if got != "test-client-id-123" {
		t.Errorf("expected X-Request-Id 'test-client-id-123', got: %q", got)
	}
}

func TestNew_ServerAddr(t *testing.T) {
	cfg := testConfig()
	cfg.Port = 9090

	srv, cleanup := New(cfg, testLogger(), nil)
	defer cleanup()

	if srv.Addr != ":9090" {
		t.Errorf("expected addr ':9090', got: %q", srv.Addr)
	}
}

// TestRequestLogger_EmitsStructuredFields tests the requestLogger middleware
// directly with a simple inner handler, avoiding coupling to any specific
// route or handler behavior.
func TestRequestLogger_EmitsStructuredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	h := requestLogger(logger)(inner)

	req := httptest.NewRequest(http.MethodGet, "/some/path", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	logOutput := buf.String()
	if logOutput == "" {
		t.Fatal("expected log output, got empty string")
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(logOutput), &entry); err != nil {
		t.Fatalf("expected valid JSON log entry, got: %q: %v", logOutput, err)
	}

	// Verify message.
	if msg, ok := entry["msg"].(string); !ok || msg != "http request" {
		t.Errorf("expected msg 'http request', got: %v", entry["msg"])
	}

	// Verify structured fields are present and correct.
	if method, ok := entry["method"].(string); !ok || method != "GET" {
		t.Errorf("expected method 'GET', got: %v", entry["method"])
	}
	if path, ok := entry["path"].(string); !ok || path != "/some/path" {
		t.Errorf("expected path '/some/path', got: %v", entry["path"])
	}
	if status, ok := entry["status"].(float64); !ok || int(status) != http.StatusOK {
		t.Errorf("expected status %d, got: %v", http.StatusOK, entry["status"])
	}
	if _, ok := entry["duration"]; !ok {
		t.Error("expected 'duration' field in log entry")
	}
	if _, ok := entry["bytes"]; !ok {
		t.Error("expected 'bytes' field in log entry")
	}
	if _, ok := entry["remote_addr"]; !ok {
		t.Error("expected 'remote_addr' field in log entry")
	}
}

// TestRequestLogger_LevelByStatus tests the requestLogger middleware
// directly with inner handlers that return specific status codes, avoiding
// coupling to any particular route's behavior.
func TestRequestLogger_LevelByStatus(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		expectedLevel string
	}{
		{"5xx logs at ERROR", http.StatusInternalServerError, "ERROR"},
		{"4xx logs at WARN", http.StatusNotFound, "WARN"},
		{"2xx logs at INFO", http.StatusOK, "INFO"},
		{"3xx logs at INFO", http.StatusMovedPermanently, "INFO"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			})
			h := requestLogger(logger)(inner)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			var entry map[string]any
			if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
				t.Fatalf("expected valid JSON log entry, got: %q: %v", buf.String(), err)
			}

			if level, ok := entry["level"].(string); !ok || level != tt.expectedLevel {
				t.Errorf("expected level %q, got: %v", tt.expectedLevel, entry["level"])
			}
		})
	}
}

func TestRequestLogger_ImplicitStatus200(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Handler that writes a body but never calls WriteHeader explicitly.
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	h := requestLogger(logger)(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("expected valid JSON log entry, got: %q: %v", buf.String(), err)
	}

	if status, ok := entry["status"].(float64); !ok || int(status) != http.StatusOK {
		t.Errorf("expected status 200 for implicit write, got: %v", entry["status"])
	}
	if level, ok := entry["level"].(string); !ok || level != "INFO" {
		t.Errorf("expected level INFO, got: %v", entry["level"])
	}
}

// TestNew_AuthRoutesExist verifies that every public auth endpoint is
// reachable (non-404). The handlers will fail because there is no DB pool,
// but a 404 specifically means the route was never wired.
func TestNew_AuthRoutesExist(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{"register", "/api/v1/auth/register", `{"email":"a@b.com","password":"Test1234!"}`},
		{"login", "/api/v1/auth/login", `{"email":"a@b.com","password":"Test1234!"}`},
		{"verify-otp", "/api/v1/auth/verify-otp", `{"email":"a@b.com","otp":"123456"}`},
		{"refresh", "/api/v1/auth/refresh", `{"refresh_token":"dummy"}`},
		{"forgot-password", "/api/v1/auth/forgot-password", `{"email":"a@b.com"}`},
		{"reset-password", "/api/v1/auth/reset-password", `{"token":"dummy","new_password":"Test1234!"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, cleanup := New(testConfig(), testLogger(), nil)
			defer cleanup()

			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			srv.Handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusNotFound {
				t.Errorf("expected POST %s to be wired (non-404), got 404", tt.path)
			}
		})
	}
}

func TestNew_ProtectedRouteUnauthorized(t *testing.T) {
	srv, cleanup := New(testConfig(), testLogger(), nil)
	defer cleanup()

	// GET /api/v1/me is a protected route. Without an Authorization header,
	// the JWT middleware must reject the request with 401.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for GET /api/v1/me without Authorization, got: %d", rec.Code)
	}
}

// TestLimitBody_RejectsOversizedBody verifies the limitBody middleware caps
// request bodies at maxBodySize. A handler reading past the limit should
// receive an error from the reader.
func TestLimitBody_RejectsOversizedBody(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	h := limitBody(inner)

	// maxBodySize is 1 MiB; send 1 MiB + 1 byte.
	oversized := make([]byte, maxBodySize+1)
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(oversized))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 for oversized body, got: %d", rec.Code)
	}
}

// TestLimitBody_AllowsNormalBody verifies the limitBody middleware allows
// request bodies within the size limit to pass through normally.
func TestLimitBody_AllowsNormalBody(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	h := limitBody(inner)

	normal := make([]byte, 1024) // 1 KiB, well within limit
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(normal))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for normal-sized body, got: %d", rec.Code)
	}
}
