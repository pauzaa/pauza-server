package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/revenuecat"
)

// mockWebhookService records calls from the handler so tests can assert the
// event was passed through correctly or simulate a service error.
type mockWebhookService struct {
	handleFn func(ctx context.Context, event revenuecat.WebhookEvent) error
	called   bool
	lastEvt  revenuecat.WebhookEvent
}

func (m *mockWebhookService) HandleWebhook(ctx context.Context, event revenuecat.WebhookEvent) error {
	m.called = true
	m.lastEvt = event
	if m.handleFn != nil {
		return m.handleFn(ctx, event)
	}
	return nil
}

const testWebhookSecret = "test-webhook-secret-xyz"

func newTestWebhookHandler(svc WebhookServicer) *WebhookHandler {
	return NewWebhookHandler(svc, testWebhookSecret, noopLogger())
}

// ---------------------------------------------------------------------------
// Authentication tests
// ---------------------------------------------------------------------------

func TestWebhook_MissingAuthorizationHeader_Returns401(t *testing.T) {
	t.Parallel()

	svc := &mockWebhookService{}
	h := newTestWebhookHandler(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/revenuecat", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	h.HandleRevenueCat(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeUnauthorized {
		t.Fatalf("error.code = %q, want %q", resp.Error.Code, apperror.CodeUnauthorized)
	}
	if svc.called {
		t.Fatal("service should not have been called")
	}
}

func TestWebhook_WrongBearerToken_Returns401(t *testing.T) {
	t.Parallel()

	svc := &mockWebhookService{}
	h := newTestWebhookHandler(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/revenuecat", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()

	h.HandleRevenueCat(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeUnauthorized {
		t.Fatalf("error.code = %q, want %q", resp.Error.Code, apperror.CodeUnauthorized)
	}
	if svc.called {
		t.Fatal("service should not have been called")
	}
}

func TestWebhook_BearerPrefixMissing_Returns401(t *testing.T) {
	t.Parallel()

	svc := &mockWebhookService{}
	h := newTestWebhookHandler(svc)

	// Token value is correct but missing the "Bearer " prefix.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/revenuecat", strings.NewReader(`{}`))
	req.Header.Set("Authorization", testWebhookSecret)
	rec := httptest.NewRecorder()

	h.HandleRevenueCat(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if svc.called {
		t.Fatal("service should not have been called")
	}
}

// ---------------------------------------------------------------------------
// Valid auth + valid JSON
// ---------------------------------------------------------------------------

func TestWebhook_ValidAuthAndPayload_Returns200(t *testing.T) {
	t.Parallel()

	svc := &mockWebhookService{}
	h := newTestWebhookHandler(svc)

	payload := `{
		"api_version": "4",
		"event": {
			"type": "INITIAL_PURCHASE",
			"id": "evt-123",
			"app_user_id": "user-1",
			"product_id": "premium_monthly",
			"entitlement_ids": ["premium"],
			"event_timestamp_ms": 1700000000000,
			"environment": "PRODUCTION"
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/revenuecat", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+testWebhookSecret)
	rec := httptest.NewRecorder()

	h.HandleRevenueCat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	if !svc.called {
		t.Fatal("expected service to be called")
	}
	if svc.lastEvt.Type != "INITIAL_PURCHASE" {
		t.Fatalf("event type = %q, want INITIAL_PURCHASE", svc.lastEvt.Type)
	}
	if svc.lastEvt.ID != "evt-123" {
		t.Fatalf("event id = %q, want evt-123", svc.lastEvt.ID)
	}
	if svc.lastEvt.AppUserID != "user-1" {
		t.Fatalf("app_user_id = %q, want user-1", svc.lastEvt.AppUserID)
	}
	if svc.lastEvt.ProductID != "premium_monthly" {
		t.Fatalf("product_id = %q, want premium_monthly", svc.lastEvt.ProductID)
	}
	if len(svc.lastEvt.EntitlementIDs) != 1 || svc.lastEvt.EntitlementIDs[0] != "premium" {
		t.Fatalf("entitlement_ids = %v, want [premium]", svc.lastEvt.EntitlementIDs)
	}
	if svc.lastEvt.EventTimestampMs != 1700000000000 {
		t.Fatalf("event_timestamp_ms = %d, want 1700000000000", svc.lastEvt.EventTimestampMs)
	}
	if svc.lastEvt.Environment != "PRODUCTION" {
		t.Fatalf("environment = %q, want PRODUCTION", svc.lastEvt.Environment)
	}
}

// ---------------------------------------------------------------------------
// Valid auth + invalid JSON
// ---------------------------------------------------------------------------

func TestWebhook_InvalidJSON_ReturnsValidationError(t *testing.T) {
	t.Parallel()

	svc := &mockWebhookService{}
	h := newTestWebhookHandler(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/revenuecat", strings.NewReader("{not json"))
	req.Header.Set("Authorization", "Bearer "+testWebhookSecret)
	rec := httptest.NewRecorder()

	h.HandleRevenueCat(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeValidationError {
		t.Fatalf("error.code = %q, want %q", resp.Error.Code, apperror.CodeValidationError)
	}
	if svc.called {
		t.Fatal("service should not have been called")
	}
}

// ---------------------------------------------------------------------------
// Valid auth + service error -> 500 for retry
// ---------------------------------------------------------------------------

func TestWebhook_ServiceError_Returns500(t *testing.T) {
	t.Parallel()

	svc := &mockWebhookService{
		handleFn: func(_ context.Context, _ revenuecat.WebhookEvent) error {
			return errors.New("database timeout")
		},
	}
	h := newTestWebhookHandler(svc)

	payload := `{"event":{"type":"RENEWAL","id":"evt-456","app_user_id":"user-2"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/revenuecat", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+testWebhookSecret)
	rec := httptest.NewRecorder()

	h.HandleRevenueCat(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeInternalError {
		t.Fatalf("error.code = %q, want %q", resp.Error.Code, apperror.CodeInternalError)
	}
	if !svc.called {
		t.Fatal("expected service to be called")
	}
}

// ---------------------------------------------------------------------------
// Unknown fields in JSON payload tolerated
// ---------------------------------------------------------------------------

func TestWebhook_UnknownFieldsTolerated(t *testing.T) {
	t.Parallel()

	svc := &mockWebhookService{}
	h := newTestWebhookHandler(svc)

	// Payload includes extra fields not in WebhookPayload / WebhookEvent.
	payload := `{
		"api_version": "4",
		"extra_top_level_field": "should be ignored",
		"event": {
			"type": "EXPIRATION",
			"id": "evt-789",
			"app_user_id": "user-3",
			"unknown_nested": {"deeply": "nested"},
			"new_future_field": 42
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/revenuecat", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+testWebhookSecret)
	rec := httptest.NewRecorder()

	h.HandleRevenueCat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !svc.called {
		t.Fatal("expected service to be called")
	}
	if svc.lastEvt.Type != "EXPIRATION" {
		t.Fatalf("event type = %q, want EXPIRATION", svc.lastEvt.Type)
	}
	if svc.lastEvt.ID != "evt-789" {
		t.Fatalf("event id = %q, want evt-789", svc.lastEvt.ID)
	}
	if svc.lastEvt.AppUserID != "user-3" {
		t.Fatalf("app_user_id = %q, want user-3", svc.lastEvt.AppUserID)
	}
}

// ---------------------------------------------------------------------------
// Trailing data after first JSON document
// ---------------------------------------------------------------------------

func TestWebhook_TrailingJSONDocument_ReturnsValidationError(t *testing.T) {
	t.Parallel()

	svc := &mockWebhookService{}
	h := newTestWebhookHandler(svc)

	// Two concatenated JSON objects.
	body := `{"event":{"type":"TEST","id":"evt-1","app_user_id":"u1"}}{"event":{"type":"TEST","id":"evt-2"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/revenuecat", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testWebhookSecret)
	rec := httptest.NewRecorder()

	h.HandleRevenueCat(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeValidationError {
		t.Fatalf("error.code = %q, want %q", resp.Error.Code, apperror.CodeValidationError)
	}
	if svc.called {
		t.Fatal("service should not have been called")
	}
}

func TestWebhook_TrailingNonWhitespace_ReturnsValidationError(t *testing.T) {
	t.Parallel()

	svc := &mockWebhookService{}
	h := newTestWebhookHandler(svc)

	// Valid JSON followed by trailing garbage text.
	body := `{"event":{"type":"TEST","id":"evt-1","app_user_id":"u1"}} garbage`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/revenuecat", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testWebhookSecret)
	rec := httptest.NewRecorder()

	h.HandleRevenueCat(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeValidationError {
		t.Fatalf("error.code = %q, want %q", resp.Error.Code, apperror.CodeValidationError)
	}
	if svc.called {
		t.Fatal("service should not have been called")
	}
}

func TestWebhook_TrailingWhitespaceOnly_Returns200(t *testing.T) {
	t.Parallel()

	svc := &mockWebhookService{}
	h := newTestWebhookHandler(svc)

	// Valid JSON followed by trailing whitespace only (should be accepted).
	body := `{"event":{"type":"TEST","id":"evt-1","app_user_id":"u1"}}   ` + "\n\t\n"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/revenuecat", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testWebhookSecret)
	rec := httptest.NewRecorder()

	h.HandleRevenueCat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !svc.called {
		t.Fatal("expected service to be called")
	}
}

// ---------------------------------------------------------------------------
// Response body shape
// ---------------------------------------------------------------------------

func TestWebhook_ResponseIsEmptyJSONObject(t *testing.T) {
	t.Parallel()

	svc := &mockWebhookService{}
	h := newTestWebhookHandler(svc)

	payload := `{"event":{"type":"TEST","id":"evt-000"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/revenuecat", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+testWebhookSecret)
	rec := httptest.NewRecorder()

	h.HandleRevenueCat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("expected empty JSON object, got %v", raw)
	}
}
