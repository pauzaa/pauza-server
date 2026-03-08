package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/middleware"
)

// panicHandler always panics with the given value.
func panicHandler(v any) http.Handler {
	return http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(v)
	})
}

func TestRecoverer_Returns500JSONOnPanic(t *testing.T) {
	handler := middleware.Recoverer(discardLogger())(panicHandler("boom"))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode JSON body: %v", err)
	}
	if resp.Error.Code != apperror.CodeInternalError {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, apperror.CodeInternalError)
	}
	if resp.Error.Message == "" {
		t.Error("error.message is empty, want non-empty safe message")
	}
}

func TestRecoverer_NoPanic_PassesThrough(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware.Recoverer(discardLogger())(inner)

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRecoverer_LogsStructuredFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))

	handler := middleware.Recoverer(logger)(panicHandler("test-panic-value"))

	req := httptest.NewRequest(http.MethodPost, "/explode", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	logged := buf.String()

	// Verify the log line is valid JSON with expected fields.
	var entry map[string]any
	if err := json.Unmarshal([]byte(logged), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v\noutput: %s", err, logged)
	}

	if msg, ok := entry["msg"].(string); !ok || msg != "panic recovered" {
		t.Errorf("msg = %v, want %q", entry["msg"], "panic recovered")
	}
	if lvl, ok := entry["level"].(string); !ok || lvl != "ERROR" {
		t.Errorf("level = %v, want %q", entry["level"], "ERROR")
	}
	if v, ok := entry["panic"].(string); !ok || v != "test-panic-value" {
		t.Errorf("panic = %v, want %q", entry["panic"], "test-panic-value")
	}
	if v, ok := entry["method"].(string); !ok || v != "POST" {
		t.Errorf("method = %v, want %q", entry["method"], "POST")
	}
	if v, ok := entry["path"].(string); !ok || v != "/explode" {
		t.Errorf("path = %v, want %q", entry["path"], "/explode")
	}

	// Stack trace must be present and non-empty.
	stack, ok := entry["stack"].(string)
	if !ok || stack == "" {
		t.Error("stack field missing or empty in log output")
	}
	if !strings.Contains(stack, "goroutine") {
		t.Errorf("stack does not look like a Go stack trace: %s", stack[:min(200, len(stack))])
	}

	// request_id should be present (empty string when no chi RequestID middleware).
	if _, ok := entry["request_id"]; !ok {
		t.Error("request_id field missing in log output")
	}
}

func TestRecoverer_RepanicksAbortHandler(t *testing.T) {
	handler := middleware.Recoverer(discardLogger())(panicHandler(http.ErrAbortHandler))

	req := httptest.NewRequest(http.MethodGet, "/abort", nil)
	rec := httptest.NewRecorder()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected http.ErrAbortHandler to be re-panicked, but no panic occurred")
		}
		if r != http.ErrAbortHandler {
			t.Errorf("re-panicked value = %v, want http.ErrAbortHandler", r)
		}
	}()

	handler.ServeHTTP(rec, req)

	// Should not reach here — the panic should propagate.
	t.Fatal("ServeHTTP should have panicked with http.ErrAbortHandler")
}

func TestRecoverer_HandlesNonStringPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))

	// Panic with an integer value.
	handler := middleware.Recoverer(logger)(panicHandler(42))

	req := httptest.NewRequest(http.MethodGet, "/int-panic", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode JSON body: %v", err)
	}
	if resp.Error.Code != apperror.CodeInternalError {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, apperror.CodeInternalError)
	}

	logged := buf.String()
	var entry map[string]any
	if err := json.Unmarshal([]byte(logged), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v\noutput: %s", err, logged)
	}

	// slog.Any encodes an int as a JSON number.
	if v, ok := entry["panic"].(float64); !ok || v != 42 {
		t.Errorf("panic = %v (%T), want 42", entry["panic"], entry["panic"])
	}
}

func TestRecoverer_SkipsBodyWhenHeadersAlreadyWritten(t *testing.T) {
	// Handler that writes a partial response before panicking.
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("late panic")
	})

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))

	handler := middleware.Recoverer(logger)(inner)

	req := httptest.NewRequest(http.MethodGet, "/partial", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// The status should be the original 200 (headers were already flushed).
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (headers already written)", rec.Code, http.StatusOK)
	}

	// Body should contain only the partial write, not a JSON error envelope.
	body := rec.Body.String()
	if body != "partial" {
		t.Errorf("body = %q, want %q", body, "partial")
	}

	// The panic should still have been logged.
	if !strings.Contains(buf.String(), "panic recovered") {
		t.Error("expected 'panic recovered' in log output")
	}
}
