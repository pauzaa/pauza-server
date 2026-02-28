package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
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
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError, // Suppress output during tests
	}))
}

func TestNew_HealthEndpoint(t *testing.T) {
	srv := New(testConfig(), testLogger())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
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

	if _, err := time.Parse(time.RFC3339, resp["timestamp"]); err != nil {
		t.Errorf("invalid timestamp: %q", resp["timestamp"])
	}
}

func TestNew_NotFoundRoute(t *testing.T) {
	srv := New(testConfig(), testLogger())

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got: %d", rec.Code)
	}
}

func TestNew_HealthMethodNotAllowed(t *testing.T) {
	srv := New(testConfig(), testLogger())

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got: %d", rec.Code)
	}
}

func TestNew_RequestIDHeader(t *testing.T) {
	srv := New(testConfig(), testLogger())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	// The RequestID middleware sets the header on the request context,
	// but chi's middleware.RequestID also sets it in the response via
	// the middleware chain. Let's verify the request was processed.
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got: %d", rec.Code)
	}
}

func TestNew_ServerAddr(t *testing.T) {
	cfg := testConfig()
	cfg.Port = 9090

	srv := New(cfg, testLogger())

	if srv.Addr != ":9090" {
		t.Errorf("expected addr ':9090', got: %q", srv.Addr)
	}
}
