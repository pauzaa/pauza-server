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

func TestNew_RequestLoggerEmitsStructuredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	srv := New(testConfig(), logger, nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-Id", "log-test-id-456")
	rec := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rec, req)

	logOutput := buf.String()
	if logOutput == "" {
		t.Fatal("expected log output, got empty string")
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(logOutput), &entry); err != nil {
		t.Fatalf("expected valid JSON log entry, got: %q: %v", logOutput, err)
	}

	// Verify message
	if msg, ok := entry["msg"].(string); !ok || msg != "http request" {
		t.Errorf("expected msg 'http request', got: %v", entry["msg"])
	}

	// Verify structured fields are present and correct
	if method, ok := entry["method"].(string); !ok || method != "GET" {
		t.Errorf("expected method 'GET', got: %v", entry["method"])
	}
	if path, ok := entry["path"].(string); !ok || path != "/health" {
		t.Errorf("expected path '/health', got: %v", entry["path"])
	}
	if status, ok := entry["status"].(float64); !ok || int(status) != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got: %v", http.StatusServiceUnavailable, entry["status"])
	}
	if reqID, ok := entry["request_id"].(string); !ok || reqID != "log-test-id-456" {
		t.Errorf("expected request_id 'log-test-id-456', got: %v", entry["request_id"])
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

func TestNew_RequestLoggerLevelByStatus(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		path          string
		expectedLevel string
	}{
		{
			name:          "5xx logs at ERROR",
			method:        http.MethodGet,
			path:          "/health",
			expectedLevel: "ERROR",
		},
		{
			name:          "4xx logs at WARN",
			method:        http.MethodGet,
			path:          "/nonexistent",
			expectedLevel: "WARN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			srv := New(testConfig(), logger, nil)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			srv.Handler.ServeHTTP(rec, req)

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
