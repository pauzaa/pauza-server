package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealth_StatusOK(t *testing.T) {
	h := Health()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got: %d", rec.Code)
	}
}

func TestHealth_ContentType(t *testing.T) {
	h := Health()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got: %q", ct)
	}
}

func TestHealth_ResponseBody(t *testing.T) {
	h := Health()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	var resp healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got: %q", resp.Status)
	}

	if resp.Timestamp == "" {
		t.Fatal("expected timestamp to be set, got empty string")
	}
	if _, err := time.Parse(time.RFC3339, resp.Timestamp); err != nil {
		t.Fatalf("expected RFC3339 timestamp, got %q: %v", resp.Timestamp, err)
	}
	if !strings.HasSuffix(resp.Timestamp, "Z") {
		t.Errorf("expected UTC timestamp ending in 'Z', got: %q", resp.Timestamp)
	}
}

func TestHealth_ExactResponseShape(t *testing.T) {
	h := Health()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	var raw map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Response must contain exactly two keys: "status" and "timestamp"
	if len(raw) != 2 {
		t.Errorf("expected exactly 2 keys in response, got %d: %v", len(raw), raw)
	}

	if raw["status"] != "ok" {
		t.Errorf("expected status 'ok', got: %v", raw["status"])
	}
	if _, ok := raw["timestamp"]; !ok {
		t.Errorf("expected timestamp key to be present, got: %v", raw)
	}
}

func TestHealth_TimestampIsRecent(t *testing.T) {
	h := Health()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	before := time.Now().UTC()
	h.ServeHTTP(rec, req)
	after := time.Now().UTC()

	var resp healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	ts, err := time.Parse(time.RFC3339, resp.Timestamp)
	if err != nil {
		t.Fatalf("expected RFC3339 timestamp, got %q: %v", resp.Timestamp, err)
	}
	if !strings.HasSuffix(resp.Timestamp, "Z") {
		t.Errorf("expected UTC timestamp ending in 'Z', got: %q", resp.Timestamp)
	}

	if ts.Before(before.Add(-2*time.Second)) || ts.After(after.Add(2*time.Second)) {
		t.Fatalf("timestamp not recent: before=%s ts=%s after=%s", before.Format(time.RFC3339), ts.Format(time.RFC3339), after.Format(time.RFC3339))
	}
}
