package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	// Verify timestamp is valid RFC3339
	_, err := time.Parse(time.RFC3339, resp.Timestamp)
	if err != nil {
		t.Errorf("expected valid RFC3339 timestamp, got: %q (error: %v)", resp.Timestamp, err)
	}
}

func TestHealth_TimestampIsRecent(t *testing.T) {
	h := Health()

	before := time.Now().UTC().Add(-1 * time.Second)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	after := time.Now().UTC().Add(1 * time.Second)

	var resp healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	ts, err := time.Parse(time.RFC3339, resp.Timestamp)
	if err != nil {
		t.Fatalf("failed to parse timestamp: %v", err)
	}

	if ts.Before(before) || ts.After(after) {
		t.Errorf("timestamp %v is not between %v and %v", ts, before, after)
	}
}
