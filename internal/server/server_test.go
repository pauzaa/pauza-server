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

	"github.com/IsorilovA/pauza-server/internal/auth"
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
		JWTSecret:          "test-secret-for-unit-tests-32b!!",
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

func TestNew_LiveEndpoint(t *testing.T) {
	srv, cleanup := New(testConfig(), testLogger(), nil)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/live", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got: %d", rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got: %q", resp["status"])
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

func TestNew_ReadyEndpoint(t *testing.T) {
	srv, cleanup := New(testConfig(), testLogger(), nil)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
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

func TestNew_LiveMethodNotAllowed(t *testing.T) {
	srv, cleanup := New(testConfig(), testLogger(), nil)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/live", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got: %d", rec.Code)
	}
}

func TestNew_ReadyMethodNotAllowed(t *testing.T) {
	srv, cleanup := New(testConfig(), testLogger(), nil)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/ready", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got: %d", rec.Code)
	}
}

func TestNew_RequestIDHeader(t *testing.T) {
	srv, cleanup := New(testConfig(), testLogger(), nil)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/live", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/live", nil)
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

// TestNew_MeRouteWithValidJWT verifies that GET /api/v1/me with a valid JWT
// passes the auth middleware and reaches the handler. The nil DB pool causes a
// panic inside the handler, which the Recoverer middleware converts to 500.
// Asserting 500 specifically (rather than merely "not 404") proves both the
// route wiring and the auth middleware pass-through are working.
func TestNew_MeRouteWithValidJWT(t *testing.T) {
	cfg := testConfig()
	srv, cleanup := New(cfg, testLogger(), nil)
	defer cleanup()

	token, err := auth.IssueAccessToken("test-user-id", "test@example.com", cfg.JWTSecret, cfg.JWTAccessTokenTTL)
	if err != nil {
		t.Fatalf("issuing test JWT: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for GET /api/v1/me with nil pool (panic → Recoverer), got: %d", rec.Code)
	}
}

// TestNew_RecovererLogsJSONOnPanic is a full-stack integration test that wires
// the server via New(), triggers a panic through the /api/v1/me route (nil DB
// pool), and verifies that the structured recoverer produces a JSON log entry
// with all expected fields: panic, stack, request_id, method, and path.
func TestNew_RecovererLogsJSONOnPanic(t *testing.T) {
	cfg := testConfig()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	srv, cleanup := New(cfg, logger, nil)
	defer cleanup()

	token, err := auth.IssueAccessToken("test-user-id", "test@example.com", cfg.JWTSecret, cfg.JWTAccessTokenTTL)
	if err != nil {
		t.Fatalf("issuing test JWT: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got: %d", rec.Code)
	}

	// The log buffer may contain multiple JSON lines (request logger + recoverer).
	// Find the "panic recovered" entry.
	var panicEntry map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if msg, _ := entry["msg"].(string); msg == "panic recovered" {
			panicEntry = entry
			break
		}
	}
	if panicEntry == nil {
		t.Fatalf("expected 'panic recovered' JSON log entry, got log output:\n%s", buf.String())
	}

	// Verify level is ERROR.
	if lvl, _ := panicEntry["level"].(string); lvl != "ERROR" {
		t.Errorf("level = %q, want ERROR", lvl)
	}

	// Verify structured fields are present.
	if _, ok := panicEntry["panic"]; !ok {
		t.Error("expected 'panic' field in log entry")
	}
	stack, _ := panicEntry["stack"].(string)
	if stack == "" {
		t.Error("expected non-empty 'stack' field in log entry")
	}
	if !strings.Contains(stack, "goroutine") {
		t.Errorf("stack does not look like a Go stack trace: %.200s", stack)
	}
	if method, _ := panicEntry["method"].(string); method != "GET" {
		t.Errorf("method = %q, want GET", method)
	}
	if path, _ := panicEntry["path"].(string); path != "/api/v1/me" {
		t.Errorf("path = %q, want /api/v1/me", path)
	}

	// Verify request_id is present and non-empty, proving that the RequestID
	// middleware ran before the Recoverer in the middleware stack.
	reqID, _ := panicEntry["request_id"].(string)
	if reqID == "" {
		t.Error("expected non-empty 'request_id' in recoverer log (proves RequestID middleware runs first)")
	}
}

// TestNew_MiddlewareOrdering_RequestLoggerSees500 is an integration test that
// confirms the requestLogger middleware is positioned above the Recoverer in
// the middleware stack. When a panic occurs, the Recoverer writes a 500 status,
// and the requestLogger — which wraps the ResponseWriter — should observe and
// log status=500. This also verifies the request_id appears in the request
// logger output, confirming RequestID runs before both.
func TestNew_MiddlewareOrdering_RequestLoggerSees500(t *testing.T) {
	cfg := testConfig()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	srv, cleanup := New(cfg, logger, nil)
	defer cleanup()

	token, err := auth.IssueAccessToken("test-user-id", "test@example.com", cfg.JWTSecret, cfg.JWTAccessTokenTTL)
	if err != nil {
		t.Fatalf("issuing test JWT: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got: %d", rec.Code)
	}

	// Find the "http request" log entry (emitted by requestLogger).
	var requestLogEntry map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if msg, _ := entry["msg"].(string); msg == "http request" {
			requestLogEntry = entry
			break
		}
	}
	if requestLogEntry == nil {
		t.Fatalf("expected 'http request' JSON log entry, got log output:\n%s", buf.String())
	}

	// The requestLogger should have observed the 500 written by the Recoverer.
	if status, ok := requestLogEntry["status"].(float64); !ok || int(status) != http.StatusInternalServerError {
		t.Errorf("request logger status = %v, want 500 (proves requestLogger wraps Recoverer)", requestLogEntry["status"])
	}

	// The request logger should include request_id (proving RequestID runs first).
	if rid, _ := requestLogEntry["request_id"].(string); rid == "" {
		t.Error("expected non-empty request_id in request logger output")
	}

	// Verify both a "panic recovered" entry and an "http request" entry exist,
	// confirming the Recoverer and requestLogger both ran for the same request.
	var hasPanicEntry bool
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if msg, _ := entry["msg"].(string); msg == "panic recovered" {
			hasPanicEntry = true
			break
		}
	}
	if !hasPanicEntry {
		t.Error("expected 'panic recovered' log entry alongside 'http request' entry")
	}
}

// TestNew_MiddlewareOrdering_RequestIDInResponse verifies that the
// respondRequestID middleware echoes the X-Request-Id even when the Recoverer
// catches a panic. This proves respondRequestID runs before Recoverer in the
// middleware chain and that recovery does not suppress response headers.
func TestNew_MiddlewareOrdering_RequestIDInResponse(t *testing.T) {
	cfg := testConfig()
	srv, cleanup := New(cfg, testLogger(), nil)
	defer cleanup()

	token, err := auth.IssueAccessToken("test-user-id", "test@example.com", cfg.JWTSecret, cfg.JWTAccessTokenTTL)
	if err != nil {
		t.Fatalf("issuing test JWT: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Request-Id", "ordering-test-id")
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got: %d", rec.Code)
	}

	// X-Request-Id should be echoed even when the handler panicked,
	// because respondRequestID writes the header before calling next.
	got := rec.Header().Get("X-Request-Id")
	if got != "ordering-test-id" {
		t.Errorf("X-Request-Id = %q, want %q (proves respondRequestID runs before Recoverer)", got, "ordering-test-id")
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
