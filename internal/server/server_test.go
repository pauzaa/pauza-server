package server

import (
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

func testConfig() *config.Config {
	return &config.Config{
		Port:     8080,
		LogLevel: "info",
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestNew_HealthEndpoint(t *testing.T) {
	srv := New(testConfig(), testLogger(), nil)

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
	srv := New(testConfig(), testLogger(), nil)

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got: %d", rec.Code)
	}
}

func TestNew_HealthMethodNotAllowed(t *testing.T) {
	srv := New(testConfig(), testLogger(), nil)

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got: %d", rec.Code)
	}
}

func TestNew_RequestIDHeader(t *testing.T) {
	srv := New(testConfig(), testLogger(), nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	reqID := rec.Header().Get("X-Request-Id")
	if reqID == "" {
		t.Error("expected X-Request-Id header to be set in response, got empty string")
	}
}

func TestNew_RequestIDEchoesClientValue(t *testing.T) {
	srv := New(testConfig(), testLogger(), nil)

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

	srv := New(cfg, testLogger(), nil)

	if srv.Addr != ":9090" {
		t.Errorf("expected addr ':9090', got: %q", srv.Addr)
	}
}
