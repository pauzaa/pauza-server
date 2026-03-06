package apperror_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/apperror"
)

// ---------- StatusCode mapping ----------

func TestStatusCode_KnownCodes(t *testing.T) {
	cases := []struct {
		name string
		code string
		want int
	}{
		{"ValidationError", apperror.CodeValidationError, http.StatusUnprocessableEntity},
		{"Unauthorized", apperror.CodeUnauthorized, http.StatusUnauthorized},
		{"Forbidden", apperror.CodeForbidden, http.StatusForbidden},
		{"NotFound", apperror.CodeNotFound, http.StatusNotFound},
		{"Conflict", apperror.CodeConflict, http.StatusConflict},
		{"RateLimited", apperror.CodeRateLimited, http.StatusTooManyRequests},
		{"SubscriptionRequired", apperror.CodeSubscriptionRequired, http.StatusForbidden},
		{"InternalError", apperror.CodeInternalError, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := apperror.StatusCode(tc.code); got != tc.want {
				t.Errorf("StatusCode(%q) = %d, want %d", tc.code, got, tc.want)
			}
		})
	}
}

func TestStatusCode_UnknownDefaultsTo500(t *testing.T) {
	if got := apperror.StatusCode("BOGUS_CODE"); got != http.StatusInternalServerError {
		t.Errorf("StatusCode(unknown) = %d, want 500", got)
	}
}

// ---------- WriteError – response shape ----------

func TestWriteError_ResponseShape(t *testing.T) {
	rec := httptest.NewRecorder()
	apperror.WriteError(rec, apperror.CodeNotFound, "resource not found", nil)

	// Status code
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	// Content-Type
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	// Decode into generic map to verify exact envelope shape
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	// Top level must have exactly one key: "error"
	if len(raw) != 1 {
		t.Fatalf("expected 1 top-level key, got %d: %v", len(raw), raw)
	}
	if _, ok := raw["error"]; !ok {
		t.Fatal("expected top-level 'error' key")
	}

	// Inner object: must contain code + message, no details
	var inner map[string]json.RawMessage
	if err := json.Unmarshal(raw["error"], &inner); err != nil {
		t.Fatalf("failed to decode inner error: %v", err)
	}

	// code
	var code string
	if err := json.Unmarshal(inner["code"], &code); err != nil {
		t.Fatalf("failed to decode code: %v", err)
	}
	if code != apperror.CodeNotFound {
		t.Errorf("code = %q, want %q", code, apperror.CodeNotFound)
	}

	// message
	var msg string
	if err := json.Unmarshal(inner["message"], &msg); err != nil {
		t.Fatalf("failed to decode message: %v", err)
	}
	if msg != "resource not found" {
		t.Errorf("message = %q, want %q", msg, "resource not found")
	}
}

// ---------- details: omitted when nil ----------

func TestWriteError_NilDetailsOmitted(t *testing.T) {
	rec := httptest.NewRecorder()
	apperror.WriteError(rec, apperror.CodeUnauthorized, "bad token", nil)

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var inner map[string]json.RawMessage
	if err := json.Unmarshal(raw["error"], &inner); err != nil {
		t.Fatalf("decode inner: %v", err)
	}
	if _, ok := inner["details"]; ok {
		t.Error("details should be omitted when nil")
	}
	// Exactly two keys: code and message
	if len(inner) != 2 {
		t.Errorf("expected 2 inner keys, got %d: %v", len(inner), inner)
	}
}

// ---------- details: present when non-nil ----------

func TestWriteError_DetailsPresent(t *testing.T) {
	rec := httptest.NewRecorder()
	details := map[string]string{"hint": "try again"}
	apperror.WriteError(rec, apperror.CodeConflict, "duplicate", details)

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Error.Details == nil {
		t.Fatal("expected details to be present")
	}

	// details should be a map with key "hint"
	detailsMap, ok := resp.Error.Details.(map[string]interface{})
	if !ok {
		t.Fatalf("expected details to be map, got %T", resp.Error.Details)
	}
	if detailsMap["hint"] != "try again" {
		t.Errorf("details.hint = %v, want %q", detailsMap["hint"], "try again")
	}
}

// ---------- ValidationFieldErrors – details shape ----------

func TestValidationFieldErrors_DetailsShape(t *testing.T) {
	rec := httptest.NewRecorder()
	fields := apperror.FieldErrors{
		"email":    "email is required",
		"password": "password must be at least 8 characters",
	}
	apperror.ValidationFieldErrors(rec, "invalid input", fields)

	// Status
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	// Decode full envelope
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var inner struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Fields map[string]string `json:"fields"`
		} `json:"details"`
	}
	if err := json.Unmarshal(raw["error"], &inner); err != nil {
		t.Fatalf("decode inner: %v", err)
	}

	if inner.Code != apperror.CodeValidationError {
		t.Errorf("code = %q, want %q", inner.Code, apperror.CodeValidationError)
	}
	if inner.Message != "invalid input" {
		t.Errorf("message = %q, want %q", inner.Message, "invalid input")
	}
	if len(inner.Details.Fields) != 2 {
		t.Fatalf("expected 2 field errors, got %d", len(inner.Details.Fields))
	}
	if inner.Details.Fields["email"] != "email is required" {
		t.Errorf("fields.email = %q", inner.Details.Fields["email"])
	}
	if inner.Details.Fields["password"] != "password must be at least 8 characters" {
		t.Errorf("fields.password = %q", inner.Details.Fields["password"])
	}
}

// ---------- NewValidationDetails – public constructor ----------

func TestNewValidationDetails_DetailsShape(t *testing.T) {
	rec := httptest.NewRecorder()
	fields := apperror.FieldErrors{"name": "name is required"}
	details := apperror.NewValidationDetails(fields)
	apperror.WriteError(rec, apperror.CodeValidationError, "invalid input", details)

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var inner struct {
		Details struct {
			Fields map[string]string `json:"fields"`
		} `json:"details"`
	}
	if err := json.Unmarshal(raw["error"], &inner); err != nil {
		t.Fatalf("decode inner: %v", err)
	}
	if inner.Details.Fields["name"] != "name is required" {
		t.Errorf("fields.name = %q, want %q", inner.Details.Fields["name"], "name is required")
	}
}

// ---------- ValidationError convenience ----------

func TestValidationError_StatusAndCode(t *testing.T) {
	rec := httptest.NewRecorder()
	apperror.ValidationError(rec, "bad request", nil)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeValidationError {
		t.Errorf("code = %q, want %q", resp.Error.Code, apperror.CodeValidationError)
	}
}

// ---------- InternalError – fixed safe message ----------

func TestInternalError_FixedMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	apperror.InternalError(rec)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Error.Code != apperror.CodeInternalError {
		t.Errorf("code = %q, want %q", resp.Error.Code, apperror.CodeInternalError)
	}
	if resp.Error.Message != "an unexpected error occurred" {
		t.Errorf("message = %q, want %q", resp.Error.Message, "an unexpected error occurred")
	}
	if resp.Error.Details != nil {
		t.Errorf("expected nil details for internal error, got %v", resp.Error.Details)
	}
}

// ---------- Convenience helpers – status & code ----------

func TestConvenienceHelpers_StatusAndCode(t *testing.T) {
	cases := []struct {
		name   string
		call   func(w http.ResponseWriter)
		status int
		code   string
	}{
		{
			name:   "Unauthorized",
			call:   func(w http.ResponseWriter) { apperror.Unauthorized(w, "invalid token") },
			status: http.StatusUnauthorized,
			code:   apperror.CodeUnauthorized,
		},
		{
			name:   "Forbidden",
			call:   func(w http.ResponseWriter) { apperror.Forbidden(w, "access denied") },
			status: http.StatusForbidden,
			code:   apperror.CodeForbidden,
		},
		{
			name:   "NotFound",
			call:   func(w http.ResponseWriter) { apperror.NotFound(w, "not found") },
			status: http.StatusNotFound,
			code:   apperror.CodeNotFound,
		},
		{
			name:   "Conflict",
			call:   func(w http.ResponseWriter) { apperror.Conflict(w, "already exists") },
			status: http.StatusConflict,
			code:   apperror.CodeConflict,
		},
		{
			name:   "RateLimited",
			call:   func(w http.ResponseWriter) { apperror.RateLimited(w, "slow down") },
			status: http.StatusTooManyRequests,
			code:   apperror.CodeRateLimited,
		},
		{
			name:   "SubscriptionRequired",
			call:   func(w http.ResponseWriter) { apperror.SubscriptionRequired(w, "upgrade needed") },
			status: http.StatusForbidden,
			code:   apperror.CodeSubscriptionRequired,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.call(rec)

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}

			ct := rec.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/json")
			}

			var resp apperror.ErrorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Error.Code != tc.code {
				t.Errorf("code = %q, want %q", resp.Error.Code, tc.code)
			}
			if resp.Error.Details != nil {
				t.Errorf("expected nil details, got %v", resp.Error.Details)
			}
		})
	}
}

// ---------- Convenience helpers – message passthrough ----------

func TestConvenienceHelpers_MessagePassthrough(t *testing.T) {
	rec := httptest.NewRecorder()
	apperror.Unauthorized(rec, "token expired")

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Message != "token expired" {
		t.Errorf("message = %q, want %q", resp.Error.Message, "token expired")
	}
}
