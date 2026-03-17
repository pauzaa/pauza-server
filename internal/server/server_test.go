package server

import (
	"bytes"
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
		// RevenueCat — deterministic test values for webhook auth.
		RevenueCatWebhookSecret: "test-webhook-secret",
		// Consolidated rate limits — use generous values so tests are not
		// throttled. The windows must remain positive for limiter semantics.
		AuthRateLimit:          10000,
		AuthRateWindow:         time.Minute,
		VerifyOTPRateLimit:     10000,
		VerifyOTPRateWindow:    time.Minute,
		GeneralAPIRateLimit:    10000,
		GeneralAPIRateWindow:   time.Minute,
		SyncRateLimit:          10000,
		SyncRateWindow:         time.Minute,
		WebhookRateLimit:       10000,
		WebhookRateWindow:      time.Minute,
		AdminJWTAccessTokenTTL: time.Hour,
		AdminRateLimit:         10000,
		AdminRateWindow:        time.Minute,
		PhotoStorageDir:        "/tmp/pauza-server-test-photos",
		PhotoPublicBaseURL:     "https://api.test/photos",
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func testNew(t *testing.T, cfg *config.Config, logger *slog.Logger) (*http.Server, func()) {
	t.Helper()
	srv, cleanup, err := New(context.Background(), cfg, logger, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("creating server: %v", err)
	}
	return srv, cleanup
}

func TestNew_LiveEndpoint(t *testing.T) {
	srv, cleanup := testNew(t, testConfig(), testLogger())
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
	srv, cleanup := testNew(t, testConfig(), testLogger())
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
	srv, cleanup := testNew(t, testConfig(), testLogger())
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got: %d", rec.Code)
	}
}

func TestNew_LiveMethodNotAllowed(t *testing.T) {
	srv, cleanup := testNew(t, testConfig(), testLogger())
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/live", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got: %d", rec.Code)
	}
}

func TestNew_ReadyMethodNotAllowed(t *testing.T) {
	srv, cleanup := testNew(t, testConfig(), testLogger())
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/ready", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got: %d", rec.Code)
	}
}

func TestNew_RequestIDHeader(t *testing.T) {
	srv, cleanup := testNew(t, testConfig(), testLogger())
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
	srv, cleanup := testNew(t, testConfig(), testLogger())
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

	srv, cleanup := testNew(t, cfg, testLogger())
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
		{"start", "/api/v1/auth/start", `{"email":"bad"}`},
		{"verify", "/api/v1/auth/verify", `{"email":"a@b.com","otp":"abc"}`},
		{"refresh", "/api/v1/auth/refresh", `{"refresh_token":""}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, cleanup := testNew(t, testConfig(), testLogger())
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

// TestNew_ProtectedMeRoutesExist verifies that the protected /me endpoints
// are wired (non-404). Without a valid JWT the auth middleware returns 401,
// which proves the route exists and the middleware runs. A 404 would mean
// the route was never registered.
func TestNew_ProtectedMeRoutesExist(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"get_me", http.MethodGet, "/api/v1/me", ""},
		{"patch_me", http.MethodPatch, "/api/v1/me", `{"name":"Alice"}`},
		{"get_notification_preferences", http.MethodGet, "/api/v1/me/notification-preferences", ""},
		{"patch_notification_preferences", http.MethodPatch, "/api/v1/me/notification-preferences", `{"push_enabled":false}`},
		{"get_privacy", http.MethodGet, "/api/v1/me/privacy", ""},
		{"patch_privacy", http.MethodPatch, "/api/v1/me/privacy", `{"leaderboard_visible":false}`},
		{"username_available", http.MethodGet, "/api/v1/me/username-available?username=alice", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, cleanup := testNew(t, testConfig(), testLogger())
			defer cleanup()

			var bodyReader *strings.Reader
			if tt.body != "" {
				bodyReader = strings.NewReader(tt.body)
			}
			var req *http.Request
			if bodyReader != nil {
				req = httptest.NewRequest(tt.method, tt.path, bodyReader)
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(tt.method, tt.path, nil)
			}
			rec := httptest.NewRecorder()

			srv.Handler.ServeHTTP(rec, req)

			// Without a JWT, the auth middleware should return 401.
			// A 404 means the route was never registered.
			if rec.Code == http.StatusNotFound {
				t.Errorf("expected %s %s to be wired (non-404), got 404", tt.method, tt.path)
			}
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected %s %s without JWT to return 401, got %d", tt.method, tt.path, rec.Code)
			}
		})
	}
}

func TestNew_SyncRouteProtected(t *testing.T) {
	srv, cleanup := testNew(t, testConfig(), testLogger())
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(`{"tables":{}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("expected /api/v1/sync to be wired, got 404")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for /api/v1/sync without JWT, got %d", rec.Code)
	}
}

func TestNew_SyncRouteWithValidJWTPassesAuth(t *testing.T) {
	cfg := testConfig()
	srv, cleanup := testNew(t, cfg, testLogger())
	defer cleanup()

	token, err := auth.IssueAccessToken("test-user-id", "test@example.com", "test-session-id", cfg.JWTSecret, cfg.JWTAccessTokenTTL)
	if err != nil {
		t.Fatalf("issuing test JWT: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(`{"tables":{"modes":{"cursor":0,"upserts":[],"deletions":[]},"mode_blocked_apps":{"cursor":0,"upserts":[],"deletions":[]},"schedules":{"cursor":0,"upserts":[],"deletions":[]},"restriction_sessions":{"cursor":0,"upserts":[],"deletions":[]},"restriction_lifecycle_events":{"cursor":0,"upserts":[],"deletions":[]},"nfc_linked_chips":{"cursor":0,"upserts":[],"deletions":[]},"qr_linked_codes":{"cursor":0,"upserts":[],"deletions":[]},"streak_session_daily_rollups":{"cursor":0,"upserts":[],"deletions":[]},"streak_daily_aggregates":{"cursor":0,"upserts":[],"deletions":[]}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from nil pool after auth pass-through, got %d", rec.Code)
	}
}

func TestNew_SyncRateLimitPerUser(t *testing.T) {
	t.Skip("Redis-backed rate limiting requires a configured Redis test backend")
	cfg := testConfig()
	cfg.SyncRateLimit = 1
	cfg.SyncRateWindow = time.Minute
	srv, cleanup := testNew(t, cfg, testLogger())
	defer cleanup()

	token1, err := auth.IssueAccessToken("user-a", "a@example.com", "test-session-a", cfg.JWTSecret, cfg.JWTAccessTokenTTL)
	if err != nil {
		t.Fatalf("issuing token1: %v", err)
	}
	token2, err := auth.IssueAccessToken("user-b", "b@example.com", "test-session-b", cfg.JWTSecret, cfg.JWTAccessTokenTTL)
	if err != nil {
		t.Fatalf("issuing token2: %v", err)
	}
	body := `{"tables":{"modes":{"cursor":0,"upserts":[],"deletions":[]},"mode_blocked_apps":{"cursor":0,"upserts":[],"deletions":[]},"schedules":{"cursor":0,"upserts":[],"deletions":[]},"restriction_sessions":{"cursor":0,"upserts":[],"deletions":[]},"restriction_lifecycle_events":{"cursor":0,"upserts":[],"deletions":[]},"nfc_linked_chips":{"cursor":0,"upserts":[],"deletions":[]},"qr_linked_codes":{"cursor":0,"upserts":[],"deletions":[]},"streak_session_daily_rollups":{"cursor":0,"upserts":[],"deletions":[]},"streak_daily_aggregates":{"cursor":0,"upserts":[],"deletions":[]}}}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token1)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+token1)
	rec2 := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second sync for same user to be 429, got %d", rec2.Code)
	}

	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(body))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Authorization", "Bearer "+token2)
	rec3 := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec3, req3)

	if rec3.Code == http.StatusTooManyRequests {
		t.Fatalf("expected different user to have separate sync budget")
	}
}

func TestNew_SyncAndGeneralAPILimitsAreIndependent(t *testing.T) {
	t.Skip("Redis-backed rate limiting requires a configured Redis test backend")
	cfg := testConfig()
	cfg.GeneralAPIRateLimit = 1
	cfg.GeneralAPIRateWindow = time.Minute
	cfg.SyncRateLimit = 1
	cfg.SyncRateWindow = time.Minute
	srv, cleanup := testNew(t, cfg, testLogger())
	defer cleanup()

	token, err := auth.IssueAccessToken("user-a", "a@example.com", "test-session-a", cfg.JWTSecret, cfg.JWTAccessTokenTTL)
	if err != nil {
		t.Fatalf("issuing token: %v", err)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+token)
	meRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusInternalServerError {
		t.Fatalf("first GET /me: expected 500 after auth pass-through, got %d", meRec.Code)
	}

	meReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	meReq2.Header.Set("Authorization", "Bearer "+token)
	meRec2 := httptest.NewRecorder()
	srv.Handler.ServeHTTP(meRec2, meReq2)
	if meRec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second GET /me: expected 429 after general API budget exhausted, got %d", meRec2.Code)
	}

	body := `{"tables":{"modes":{"cursor":0,"upserts":[],"deletions":[]},"mode_blocked_apps":{"cursor":0,"upserts":[],"deletions":[]},"schedules":{"cursor":0,"upserts":[],"deletions":[]},"restriction_sessions":{"cursor":0,"upserts":[],"deletions":[]},"restriction_lifecycle_events":{"cursor":0,"upserts":[],"deletions":[]},"nfc_linked_chips":{"cursor":0,"upserts":[],"deletions":[]},"qr_linked_codes":{"cursor":0,"upserts":[],"deletions":[]},"streak_session_daily_rollups":{"cursor":0,"upserts":[],"deletions":[]},"streak_daily_aggregates":{"cursor":0,"upserts":[],"deletions":[]}}}`
	syncReq := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(body))
	syncReq.Header.Set("Content-Type", "application/json")
	syncReq.Header.Set("Authorization", "Bearer "+token)
	syncRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(syncRec, syncReq)
	if syncRec.Code != http.StatusInternalServerError {
		t.Fatalf("sync after exhausting general API limit: expected 500 from nil pool, got %d", syncRec.Code)
	}

	syncReq2 := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(body))
	syncReq2.Header.Set("Content-Type", "application/json")
	syncReq2.Header.Set("Authorization", "Bearer "+token)
	syncRec2 := httptest.NewRecorder()
	srv.Handler.ServeHTTP(syncRec2, syncReq2)
	if syncRec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second sync request: expected 429 after sync budget exhausted, got %d", syncRec2.Code)
	}

	meReq3 := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	meReq3.Header.Set("Authorization", "Bearer "+token)
	meRec3 := httptest.NewRecorder()
	srv.Handler.ServeHTTP(meRec3, meReq3)
	if meRec3.Code != http.StatusTooManyRequests {
		t.Fatalf("GET /me after exhausting sync limit: expected existing 429 from general API budget, got %d", meRec3.Code)
	}
}

func TestNew_ProtectedRouteUnauthorized(t *testing.T) {
	srv, cleanup := testNew(t, testConfig(), testLogger())
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
	srv, cleanup := testNew(t, cfg, testLogger())
	defer cleanup()

	token, err := auth.IssueAccessToken("test-user-id", "test@example.com", "test-session-id", cfg.JWTSecret, cfg.JWTAccessTokenTTL)
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

	// Verify the response follows the JSON error contract end to end.
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var errResp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode JSON error body: %v", err)
	}
	if errResp.Error.Code != apperror.CodeInternalError {
		t.Errorf("error.code = %q, want %q", errResp.Error.Code, apperror.CodeInternalError)
	}
	if errResp.Error.Message == "" {
		t.Error("error.message is empty, want non-empty safe message")
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

	srv, cleanup := testNew(t, cfg, logger)
	defer cleanup()

	token, err := auth.IssueAccessToken("test-user-id", "test@example.com", "test-session-id", cfg.JWTSecret, cfg.JWTAccessTokenTTL)
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

	// Verify the response follows the JSON error contract end to end.
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var errResp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode JSON error body: %v", err)
	}
	if errResp.Error.Code != apperror.CodeInternalError {
		t.Errorf("error.code = %q, want %q", errResp.Error.Code, apperror.CodeInternalError)
	}
	if errResp.Error.Message == "" {
		t.Error("error.message is empty, want non-empty safe message")
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

	srv, cleanup := testNew(t, cfg, logger)
	defer cleanup()

	token, err := auth.IssueAccessToken("test-user-id", "test@example.com", "test-session-id", cfg.JWTSecret, cfg.JWTAccessTokenTTL)
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

	// Verify the response follows the JSON error contract end to end.
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var errResp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode JSON error body: %v", err)
	}
	if errResp.Error.Code != apperror.CodeInternalError {
		t.Errorf("error.code = %q, want %q", errResp.Error.Code, apperror.CodeInternalError)
	}
	if errResp.Error.Message == "" {
		t.Error("error.message is empty, want non-empty safe message")
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
	srv, cleanup := testNew(t, cfg, testLogger())
	defer cleanup()

	token, err := auth.IssueAccessToken("test-user-id", "test@example.com", "test-session-id", cfg.JWTSecret, cfg.JWTAccessTokenTTL)
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

	// Verify the response follows the JSON error contract end to end.
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var errResp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode JSON error body: %v", err)
	}
	if errResp.Error.Code != apperror.CodeInternalError {
		t.Errorf("error.code = %q, want %q", errResp.Error.Code, apperror.CodeInternalError)
	}
	if errResp.Error.Message == "" {
		t.Error("error.message is empty, want non-empty safe message")
	}
}

// TestLimitBody_RejectsOversizedBody verifies the limitBody middleware caps
// request bodies at defaultMaxBodySize. A handler reading past the limit should
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

	// defaultMaxBodySize is 1 MiB; send 1 MiB + 1 byte.
	oversized := make([]byte, defaultMaxBodySize+1)
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

// TestNew_AuthEndpointsShareRateLimitBudget proves that passwordless auth start
// and verify share the public auth rate-limit group. Exhausting the start
// budget must also throttle verify requests from the same client.
func TestNew_AuthEndpointsShareRateLimitBudget(t *testing.T) {
	t.Skip("Redis-backed rate limiting requires a configured Redis test backend")
	cfg := testConfig()
	// Tight budget for the shared auth group; generous for protected routes.
	cfg.AuthRateLimit = 1
	cfg.AuthRateWindow = time.Minute
	cfg.GeneralAPIRateLimit = 10000
	cfg.GeneralAPIRateWindow = time.Minute

	srv, cleanup := testNew(t, cfg, testLogger())
	defer cleanup()

	startBody := `{"email":"bad"}`
	verifyBody := `{"email":"a@b.com","otp":"abc"}`

	// First start request — should be allowed (uses the 1-request budget).
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/start", strings.NewReader(startBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusTooManyRequests {
		t.Fatal("first start request should not be rate-limited")
	}

	// Second start request — should be rate-limited (budget exhausted).
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/start", strings.NewReader(startBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:12346"
	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("second start request: expected 429, got %d", rec.Code)
	}

	// Verify request from the same IP should also be rate-limited because
	// it shares the auth budget with start.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify", strings.NewReader(verifyBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.1:12347"
	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("verify request: expected 429 after shared auth budget exhausted, got %d", rec.Code)
	}
}

// TestNew_VerifyOTPUsesSharedAuthIPRateLimit proves that verify is part of
// the shared auth IP budget, independent of the per-email limiter.
func TestNew_VerifyOTPUsesSharedAuthIPRateLimit(t *testing.T) {
	t.Skip("Redis-backed rate limiting requires a configured Redis test backend")
	cfg := testConfig()
	cfg.AuthRateLimit = 1
	cfg.AuthRateWindow = time.Minute
	cfg.VerifyOTPRateLimit = 10000
	cfg.VerifyOTPRateWindow = time.Minute

	srv, cleanup := testNew(t, cfg, testLogger())
	defer cleanup()

	firstReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify", strings.NewReader(`{"email":"first@example.com","otp":"abc"}`))
	firstReq.Header.Set("Content-Type", "application/json")
	firstReq.RemoteAddr = "10.0.0.1:12345"
	firstRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(firstRec, firstReq)
	if firstRec.Code == http.StatusTooManyRequests {
		t.Fatal("first verify request should not be rate-limited")
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify", strings.NewReader(`{"email":"second@example.com","otp":"abc"}`))
	secondReq.Header.Set("Content-Type", "application/json")
	secondReq.RemoteAddr = "10.0.0.1:54321"
	secondRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(secondRec, secondReq)

	if secondRec.Code != http.StatusTooManyRequests {
		t.Fatalf("second verify request from same IP: expected 429, got %d", secondRec.Code)
	}
}

// TestNew_WebhookRevenueCatRouteExists verifies that POST /api/v1/webhooks/revenuecat
// is registered and protected by Bearer-secret auth (not JWT). Missing or invalid
// tokens must be rejected with 401; a valid Bearer secret must pass through to the
// handler (which will fail downstream due to nil deps, returning 500).
func TestNew_WebhookRevenueCatRouteExists(t *testing.T) {
	cfg := testConfig()
	srv, cleanup := testNew(t, cfg, testLogger())
	defer cleanup()

	t.Run("missing_auth_returns_401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/revenuecat",
			strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		srv.Handler.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Fatal("expected POST /api/v1/webhooks/revenuecat to be wired (non-404), got 404")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 without Authorization header, got %d", rec.Code)
		}

		var errResp apperror.ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
			t.Fatalf("failed to decode JSON error body: %v", err)
		}
		if errResp.Error.Code != apperror.CodeUnauthorized {
			t.Errorf("error.code = %q, want %q", errResp.Error.Code, apperror.CodeUnauthorized)
		}
	})

	t.Run("invalid_bearer_returns_401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/revenuecat",
			strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer wrong-secret")
		rec := httptest.NewRecorder()

		srv.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 with invalid Bearer token, got %d", rec.Code)
		}

		var errResp apperror.ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
			t.Fatalf("failed to decode JSON error body: %v", err)
		}
		if errResp.Error.Code != apperror.CodeUnauthorized {
			t.Errorf("error.code = %q, want %q", errResp.Error.Code, apperror.CodeUnauthorized)
		}
	})

	t.Run("valid_bearer_reaches_handler", func(t *testing.T) {
		// A valid webhook event payload with the correct Bearer secret should
		// pass auth and reach the handler. The nil DB pool causes a 500, which
		// proves routing + auth middleware are working as intended.
		body := `{"event":{"id":"evt_123","type":"INITIAL_PURCHASE","app_user_id":"user-1"}}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/revenuecat",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+cfg.RevenueCatWebhookSecret)
		rec := httptest.NewRecorder()

		srv.Handler.ServeHTTP(rec, req)

		if rec.Code == http.StatusUnauthorized {
			t.Fatal("expected valid Bearer secret to pass auth, got 401")
		}
		if rec.Code == http.StatusNotFound {
			t.Fatal("expected route to be wired, got 404")
		}
		// The handler will fail due to nil DB pool; 500 confirms we reached it.
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 from nil pool after auth pass-through, got %d", rec.Code)
		}
	})
}

// =========================================================================
// Admin route wiring tests
// =========================================================================

// TestNew_AdminLoginRouteExists verifies that POST /api/v1/admin/login is
// reachable (non-404) and public (no JWT required). An empty body should get
// a 422 validation error, not 401 or 404.
func TestNew_AdminLoginRouteExists(t *testing.T) {
	srv, cleanup := testNew(t, testConfig(), testLogger())
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login",
		strings.NewReader(`{"username":"","password":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatal("expected POST /api/v1/admin/login to be wired (non-404), got 404")
	}
	// Login is public — empty fields should result in a 422 validation error,
	// not 401 (auth middleware would produce 401).
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for empty credentials, got %d", rec.Code)
	}
}

// TestNew_AdminProtectedRoutesRequireAuth verifies that protected admin
// endpoints return 401 without any JWT.
func TestNew_AdminProtectedRoutesRequireAuth(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"list_users", http.MethodGet, "/api/v1/admin/users", ""},
		{"get_user_detail", http.MethodGet, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001", ""},
		{"get_stats", http.MethodGet, "/api/v1/admin/stats", ""},
		{"manage_entitlement", http.MethodPost, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001/entitlements",
			`{"action":"grant","entitlement":"premium"}`},
		{"list_entitlements", http.MethodGet, "/api/v1/admin/entitlements", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, cleanup := testNew(t, testConfig(), testLogger())
			defer cleanup()

			var bodyReader io.Reader
			if tt.body != "" {
				bodyReader = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.path, bodyReader)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()

			srv.Handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusNotFound {
				t.Errorf("expected %s %s to be wired (non-404), got 404", tt.method, tt.path)
			}
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected %s %s without JWT to return 401, got %d", tt.method, tt.path, rec.Code)
			}
		})
	}
}

// TestNew_AdminProtectedRoutesRejectUserJWT verifies that a valid user JWT
// (no role) is rejected by the AdminJWTAuth middleware on protected admin routes.
func TestNew_AdminProtectedRoutesRejectUserJWT(t *testing.T) {
	cfg := testConfig()
	srv, cleanup := testNew(t, cfg, testLogger())
	defer cleanup()

	// Issue a regular user JWT — no admin role.
	userToken, err := auth.IssueAccessToken("test-user-id", "user@example.com", "test-session-id", cfg.JWTSecret, cfg.JWTAccessTokenTTL)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"list_users", http.MethodGet, "/api/v1/admin/users", ""},
		{"get_user_detail", http.MethodGet, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001", ""},
		{"get_stats", http.MethodGet, "/api/v1/admin/stats", ""},
		{"manage_entitlement", http.MethodPost, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001/entitlements",
			`{"action":"grant","entitlement":"premium"}`},
		{"list_entitlements", http.MethodGet, "/api/v1/admin/entitlements", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bodyReader io.Reader
			if tt.body != "" {
				bodyReader = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.path, bodyReader)
			req.Header.Set("Authorization", "Bearer "+userToken)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()

			srv.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected %s %s with user JWT to return 401, got %d", tt.method, tt.path, rec.Code)
			}
		})
	}
}

// TestNew_AdminProtectedRoutesAcceptAdminJWT verifies that a valid admin JWT
// passes through AdminJWTAuth and reaches the handler. The nil DB pool causes
// a 500 (panic → Recoverer), which proves route + auth middleware work.
func TestNew_AdminProtectedRoutesAcceptAdminJWT(t *testing.T) {
	cfg := testConfig()
	srv, cleanup := testNew(t, cfg, testLogger())
	defer cleanup()

	adminToken, err := auth.IssueAdminToken("admin-001", cfg.JWTSecret, cfg.AdminJWTAccessTokenTTL)
	if err != nil {
		t.Fatalf("IssueAdminToken: %v", err)
	}

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"list_users", http.MethodGet, "/api/v1/admin/users", ""},
		{"get_user_detail", http.MethodGet, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001", ""},
		{"get_stats", http.MethodGet, "/api/v1/admin/stats", ""},
		{"manage_entitlement", http.MethodPost, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001/entitlements",
			`{"action":"grant","entitlement":"premium"}`},
		{"list_entitlements", http.MethodGet, "/api/v1/admin/entitlements", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bodyReader io.Reader
			if tt.body != "" {
				bodyReader = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.path, bodyReader)
			req.Header.Set("Authorization", "Bearer "+adminToken)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()

			srv.Handler.ServeHTTP(rec, req)

			// With admin JWT passing auth, the handler will fail because
			// there is no DB pool. A 500 proves the route + auth work.
			if rec.Code == http.StatusUnauthorized {
				t.Errorf("expected %s %s with admin JWT to pass auth, got 401", tt.method, tt.path)
			}
			if rec.Code == http.StatusNotFound {
				t.Errorf("expected %s %s to be wired, got 404", tt.method, tt.path)
			}
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("expected %s %s to return 500 from nil pool, got %d", tt.method, tt.path, rec.Code)
			}
		})
	}
}

// TestNew_VerifyOTPUsesPerEmailRateLimitAcrossIPs proves that verify also
// has its dedicated email-based limiter, independent of the shared auth IP
// limiter.
func TestNew_VerifyOTPUsesPerEmailRateLimitAcrossIPs(t *testing.T) {
	t.Skip("Redis-backed rate limiting requires a configured Redis test backend")
	cfg := testConfig()
	cfg.AuthRateLimit = 10000
	cfg.AuthRateWindow = time.Minute
	cfg.VerifyOTPRateLimit = 1
	cfg.VerifyOTPRateWindow = time.Minute

	srv, cleanup := testNew(t, cfg, testLogger())
	defer cleanup()

	firstReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify", strings.NewReader(`{"email":"shared@example.com","otp":"abc"}`))
	firstReq.Header.Set("Content-Type", "application/json")
	firstReq.RemoteAddr = "10.0.0.1:12345"
	firstRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(firstRec, firstReq)
	if firstRec.Code == http.StatusTooManyRequests {
		t.Fatal("first verify request should not be rate-limited")
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify", strings.NewReader(`{"email":"shared@example.com","otp":"abc"}`))
	secondReq.Header.Set("Content-Type", "application/json")
	secondReq.RemoteAddr = "10.0.0.2:12345"
	secondRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(secondRec, secondReq)

	if secondRec.Code != http.StatusTooManyRequests {
		t.Fatalf("second verify request for same email across IPs: expected 429, got %d", secondRec.Code)
	}

	thirdReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify", strings.NewReader(`{"email":"other@example.com","otp":"abc"}`))
	thirdReq.Header.Set("Content-Type", "application/json")
	thirdReq.RemoteAddr = "10.0.0.3:12345"
	thirdRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(thirdRec, thirdReq)

	if thirdRec.Code == http.StatusTooManyRequests {
		t.Fatal("different verify email should not share the exhausted per-email budget")
	}
}
