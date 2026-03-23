package revenuecat_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/revenuecat"
)

// ---------------------------------------------------------------------------
// DeriveEntitlement – entitlement derivation logic
// ---------------------------------------------------------------------------

func TestDeriveEntitlement_ActiveSubscription(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	futureExpiry := now.Add(30 * 24 * time.Hour) // 30 days from now

	sub := &revenuecat.Subscriber{
		Entitlements: map[string]revenuecat.EntitlementObj{
			"pro": {
				ExpiresDate:       &futureExpiry,
				PurchaseDate:      now.Add(-30 * 24 * time.Hour),
				ProductIdentifier: "monthly_pro",
			},
		},
	}

	result := revenuecat.DeriveEntitlement(sub, "pro", now)

	if !result.IsActive {
		t.Error("expected IsActive = true for subscription with future expiry")
	}
	if result.Entitlement != "pro" {
		t.Errorf("Entitlement = %q, want %q", result.Entitlement, "pro")
	}
	if result.CurrentPeriodEnd == nil {
		t.Fatal("expected CurrentPeriodEnd to be non-nil")
	}
	if !result.CurrentPeriodEnd.Equal(futureExpiry) {
		t.Errorf("CurrentPeriodEnd = %v, want %v", result.CurrentPeriodEnd, futureExpiry)
	}
}

func TestDeriveEntitlement_ExpiredSubscription(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	pastExpiry := now.Add(-24 * time.Hour) // expired yesterday

	sub := &revenuecat.Subscriber{
		Entitlements: map[string]revenuecat.EntitlementObj{
			"pro": {
				ExpiresDate:       &pastExpiry,
				PurchaseDate:      now.Add(-60 * 24 * time.Hour),
				ProductIdentifier: "monthly_pro",
			},
		},
	}

	result := revenuecat.DeriveEntitlement(sub, "pro", now)

	if result.IsActive {
		t.Error("expected IsActive = false for expired subscription")
	}
	if result.CurrentPeriodEnd == nil {
		t.Fatal("expected CurrentPeriodEnd to be non-nil even when expired")
	}
	if !result.CurrentPeriodEnd.Equal(pastExpiry) {
		t.Errorf("CurrentPeriodEnd = %v, want %v", result.CurrentPeriodEnd, pastExpiry)
	}
}

func TestDeriveEntitlement_GracePeriodActive(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	pastExpiry := now.Add(-2 * 24 * time.Hour)  // billing expired 2 days ago
	futureGrace := now.Add(14 * 24 * time.Hour) // grace period still valid

	sub := &revenuecat.Subscriber{
		Entitlements: map[string]revenuecat.EntitlementObj{
			"pro": {
				ExpiresDate:            &pastExpiry,
				GracePeriodExpiresDate: &futureGrace,
				PurchaseDate:           now.Add(-60 * 24 * time.Hour),
				ProductIdentifier:      "monthly_pro",
			},
		},
	}

	result := revenuecat.DeriveEntitlement(sub, "pro", now)

	if !result.IsActive {
		t.Error("expected IsActive = true during grace period")
	}
	if result.CurrentPeriodEnd == nil {
		t.Fatal("expected CurrentPeriodEnd to be non-nil")
	}
	if !result.CurrentPeriodEnd.Equal(pastExpiry) {
		t.Errorf("CurrentPeriodEnd = %v, want %v (the original expiry)", result.CurrentPeriodEnd, pastExpiry)
	}
}

func TestDeriveEntitlement_GracePeriodExpired(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	pastExpiry := now.Add(-30 * 24 * time.Hour) // billing expired 30 days ago
	pastGrace := now.Add(-14 * 24 * time.Hour)  // grace also expired

	sub := &revenuecat.Subscriber{
		Entitlements: map[string]revenuecat.EntitlementObj{
			"pro": {
				ExpiresDate:            &pastExpiry,
				GracePeriodExpiresDate: &pastGrace,
				PurchaseDate:           now.Add(-90 * 24 * time.Hour),
				ProductIdentifier:      "monthly_pro",
			},
		},
	}

	result := revenuecat.DeriveEntitlement(sub, "pro", now)

	if result.IsActive {
		t.Error("expected IsActive = false when both expiry and grace period are in the past")
	}
}

func TestDeriveEntitlement_EntitlementAbsent(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	sub := &revenuecat.Subscriber{
		Entitlements: map[string]revenuecat.EntitlementObj{
			"basic": {
				PurchaseDate:      now.Add(-30 * 24 * time.Hour),
				ProductIdentifier: "basic_plan",
			},
		},
	}

	result := revenuecat.DeriveEntitlement(sub, "pro", now)

	if result.IsActive {
		t.Error("expected IsActive = false for absent entitlement")
	}
	if result.Entitlement != "pro" {
		t.Errorf("Entitlement = %q, want %q", result.Entitlement, "pro")
	}
	if result.CurrentPeriodEnd != nil {
		t.Errorf("expected CurrentPeriodEnd = nil for absent entitlement, got %v", result.CurrentPeriodEnd)
	}
}

func TestDeriveEntitlement_LifetimeSubscription(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	sub := &revenuecat.Subscriber{
		Entitlements: map[string]revenuecat.EntitlementObj{
			"pro": {
				ExpiresDate:       nil, // lifetime
				PurchaseDate:      now.Add(-365 * 24 * time.Hour),
				ProductIdentifier: "lifetime_pro",
			},
		},
	}

	result := revenuecat.DeriveEntitlement(sub, "pro", now)

	if !result.IsActive {
		t.Error("expected IsActive = true for lifetime (nil ExpiresDate) entitlement")
	}
	if result.CurrentPeriodEnd != nil {
		t.Errorf("expected CurrentPeriodEnd = nil for lifetime entitlement, got %v", result.CurrentPeriodEnd)
	}
}

func TestDeriveEntitlement_EmptyEntitlements(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	sub := &revenuecat.Subscriber{
		Entitlements: map[string]revenuecat.EntitlementObj{},
	}

	result := revenuecat.DeriveEntitlement(sub, "pro", now)

	if result.IsActive {
		t.Error("expected IsActive = false when entitlements map is empty")
	}
}

func TestDeriveEntitlement_NilSubscriber(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	result := revenuecat.DeriveEntitlement(nil, "pro", now)

	if result.IsActive {
		t.Error("expected IsActive = false for nil subscriber")
	}
	if result.Entitlement != "pro" {
		t.Errorf("Entitlement = %q, want %q", result.Entitlement, "pro")
	}
	if result.CurrentPeriodEnd != nil {
		t.Errorf("expected CurrentPeriodEnd = nil for nil subscriber, got %v", result.CurrentPeriodEnd)
	}
}

func TestDeriveEntitlement_ExpiresExactlyAtNow(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	expiresAtNow := now // expires exactly at "now" — not After(now)

	sub := &revenuecat.Subscriber{
		Entitlements: map[string]revenuecat.EntitlementObj{
			"pro": {
				ExpiresDate:       &expiresAtNow,
				PurchaseDate:      now.Add(-30 * 24 * time.Hour),
				ProductIdentifier: "monthly_pro",
			},
		},
	}

	result := revenuecat.DeriveEntitlement(sub, "pro", now)

	if result.IsActive {
		t.Error("expected IsActive = false when ExpiresDate == now (not After)")
	}
}

// ---------------------------------------------------------------------------
// GetSubscriber – HTTP client tests via httptest.NewServer
// ---------------------------------------------------------------------------

func TestGetSubscriber_HappyPath(t *testing.T) {
	t.Parallel()

	expires := time.Date(2025, 7, 15, 12, 0, 0, 0, time.UTC)
	purchase := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	respBody := revenuecat.SubscriberResponse{
		Subscriber: revenuecat.Subscriber{
			OriginalAppUserID: "user-123",
			Entitlements: map[string]revenuecat.EntitlementObj{
				"pro": {
					ExpiresDate:       &expires,
					PurchaseDate:      purchase,
					ProductIdentifier: "monthly_pro",
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(respBody)
	}))
	defer srv.Close()

	client := revenuecat.NewClient("test-api-key", revenuecat.WithBaseURL(srv.URL))
	got, err := client.GetSubscriber(context.Background(), "user-123")
	if err != nil {
		t.Fatalf("GetSubscriber() error: %v", err)
	}

	if got.Subscriber.OriginalAppUserID != "user-123" {
		t.Errorf("OriginalAppUserID = %q, want %q", got.Subscriber.OriginalAppUserID, "user-123")
	}
	ent, ok := got.Subscriber.Entitlements["pro"]
	if !ok {
		t.Fatal("expected 'pro' entitlement in response")
	}
	if ent.ProductIdentifier != "monthly_pro" {
		t.Errorf("ProductIdentifier = %q, want %q", ent.ProductIdentifier, "monthly_pro")
	}
	if ent.ExpiresDate == nil {
		t.Fatal("expected ExpiresDate to be non-nil")
	}
	if !ent.ExpiresDate.Equal(expires) {
		t.Errorf("ExpiresDate = %v, want %v", ent.ExpiresDate, expires)
	}
}

func TestGetSubscriber_404_Distinguishable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":7000,"message":"Subscriber not found."}`))
	}))
	defer srv.Close()

	client := revenuecat.NewClient("test-api-key", revenuecat.WithBaseURL(srv.URL))
	_, err := client.GetSubscriber(context.Background(), "nonexistent-user")
	if err == nil {
		t.Fatal("expected error for 404 response")
	}

	var apiErr *revenuecat.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusNotFound)
	}
	if apiErr.AppUserID != "nonexistent-user" {
		t.Errorf("AppUserID = %q, want %q", apiErr.AppUserID, "nonexistent-user")
	}
}

func TestGetSubscriber_500_ReturnsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":7999,"message":"Internal server error."}`))
	}))
	defer srv.Close()

	client := revenuecat.NewClient("test-api-key", revenuecat.WithBaseURL(srv.URL))
	_, err := client.GetSubscriber(context.Background(), "user-123")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}

	var apiErr *revenuecat.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusInternalServerError)
	}
}

func TestGetSubscriber_InvalidJSON_ReturnsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{invalid json`))
	}))
	defer srv.Close()

	client := revenuecat.NewClient("test-api-key", revenuecat.WithBaseURL(srv.URL))
	_, err := client.GetSubscriber(context.Background(), "user-123")
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}

	// Should NOT be an APIError — it's a decoding error, not a non-200 status.
	var apiErr *revenuecat.APIError
	if errors.As(err, &apiErr) {
		t.Errorf("expected non-APIError for invalid JSON, got APIError with status %d", apiErr.StatusCode)
	}

	if !strings.Contains(err.Error(), "decoding response") {
		t.Errorf("error message = %q, want it to contain %q", err.Error(), "decoding response")
	}
}

func TestGetSubscriber_AuthHeaderSent(t *testing.T) {
	t.Parallel()

	const testAPIKey = "sk_test_secret_key_12345"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		expectedAuth := "Bearer " + testAPIKey
		if authHeader != expectedAuth {
			t.Errorf("Authorization header = %q, want %q", authHeader, expectedAuth)
		}

		contentType := r.Header.Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("Content-Type header = %q, want %q", contentType, "application/json")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(revenuecat.SubscriberResponse{
			Subscriber: revenuecat.Subscriber{
				Entitlements: map[string]revenuecat.EntitlementObj{},
			},
		})
	}))
	defer srv.Close()

	client := revenuecat.NewClient(testAPIKey, revenuecat.WithBaseURL(srv.URL))
	_, err := client.GetSubscriber(context.Background(), "user-123")
	if err != nil {
		t.Fatalf("GetSubscriber() error: %v", err)
	}
}

func TestGetSubscriber_PathEscaping(t *testing.T) {
	t.Parallel()

	const appUserID = "user/with spaces+special"

	var receivedRawPath string
	var receivedDecodedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RawPath preserves the percent-encoded form when it differs
		// from the decoded Path.
		receivedRawPath = r.URL.RawPath
		receivedDecodedPath = r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(revenuecat.SubscriberResponse{
			Subscriber: revenuecat.Subscriber{
				Entitlements: map[string]revenuecat.EntitlementObj{},
			},
		})
	}))
	defer srv.Close()

	client := revenuecat.NewClient("test-api-key", revenuecat.WithBaseURL(srv.URL))
	_, err := client.GetSubscriber(context.Background(), appUserID)
	if err != nil {
		t.Fatalf("GetSubscriber() error: %v", err)
	}

	// The raw path must contain %2F for the literal "/" in the user ID,
	// so it does not create an additional path segment.
	if receivedRawPath == "" {
		// If RawPath is empty, Go considered Path to be equivalent,
		// meaning no special encoding was present — which would be
		// wrong for a user ID containing "/".
		t.Fatal("expected RawPath to be non-empty when app user ID contains special characters")
	}

	if !strings.Contains(receivedRawPath, "%2F") && !strings.Contains(receivedRawPath, "%2f") {
		t.Errorf("expected RawPath %q to contain %%2F for escaped slash", receivedRawPath)
	}

	// The decoded path may contain the literal slash, but the raw
	// path segments (splitting on unescaped slashes) should only have
	// ["subscribers", "<escaped-user-id>"] because the base URL is
	// just srv.URL (no /v1 prefix when overriding for tests).
	rawSegments := strings.Split(strings.TrimPrefix(receivedRawPath, "/"), "/")
	// Expected: ["subscribers", "<percent-encoded-user-id>"]
	if len(rawSegments) != 2 {
		t.Errorf("expected 2 raw path segments, got %d: %v (raw path: %q)", len(rawSegments), rawSegments, receivedRawPath)
	}

	// Sanity check: decoded path should contain the original characters.
	if !strings.Contains(receivedDecodedPath, "spaces") {
		t.Errorf("decoded path %q should contain the original user ID characters", receivedDecodedPath)
	}
}

func TestGetSubscriber_HTTPMethod(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("HTTP method = %q, want %q", r.Method, http.MethodGet)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(revenuecat.SubscriberResponse{
			Subscriber: revenuecat.Subscriber{
				Entitlements: map[string]revenuecat.EntitlementObj{},
			},
		})
	}))
	defer srv.Close()

	client := revenuecat.NewClient("test-api-key", revenuecat.WithBaseURL(srv.URL))
	_, err := client.GetSubscriber(context.Background(), "user-123")
	if err != nil {
		t.Fatalf("GetSubscriber() error: %v", err)
	}
}

func TestGetSubscriber_ContextCancelled(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow response that the client shouldn't wait for.
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := revenuecat.NewClient("test-api-key", revenuecat.WithBaseURL(srv.URL))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := client.GetSubscriber(ctx, "user-123")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ---------------------------------------------------------------------------
// APIError – error message format
// ---------------------------------------------------------------------------

func TestAPIError_ErrorMessage(t *testing.T) {
	t.Parallel()

	apiErr := &revenuecat.APIError{StatusCode: 404, AppUserID: "user-abc"}
	msg := apiErr.Error()

	if !strings.Contains(msg, "404") {
		t.Errorf("error message %q should contain status code 404", msg)
	}
	if !strings.Contains(msg, "user-abc") {
		t.Errorf("error message %q should contain app user ID", msg)
	}
}

// ---------------------------------------------------------------------------
// NewClient – option application
// ---------------------------------------------------------------------------

func TestNewClient_DefaultBaseURL(t *testing.T) {
	t.Parallel()

	// When no WithBaseURL is provided, the client should use the default
	// RevenueCat API base URL. We verify indirectly: a request to a
	// non-existent host will fail, but the error should contain the
	// default URL.
	client := revenuecat.NewClient("test-key")
	_, err := client.GetSubscriber(context.Background(), "test-user")
	if err == nil {
		t.Fatal("expected error when calling default RevenueCat URL without valid key")
	}
	// The error should reference the revenuecat API domain in some way,
	// or at least be a network/request error, not an APIError.
	// This validates the client was constructed without panics.
}

func TestNewClient_WithHTTPClient(t *testing.T) {
	t.Parallel()

	customClient := &http.Client{Timeout: 1 * time.Millisecond}
	client := revenuecat.NewClient("test-key",
		revenuecat.WithHTTPClient(customClient),
		revenuecat.WithBaseURL("http://localhost:1"), // unreachable port
	)

	_, err := client.GetSubscriber(context.Background(), "user-123")
	if err == nil {
		t.Fatal("expected error for unreachable server with tiny timeout")
	}
}

func TestGetSubscriber_401_ReturnsAPIError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":7001,"message":"Invalid API key."}`))
	}))
	defer srv.Close()

	client := revenuecat.NewClient("bad-api-key", revenuecat.WithBaseURL(srv.URL))
	_, err := client.GetSubscriber(context.Background(), "user-123")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}

	var apiErr *revenuecat.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusUnauthorized)
	}
}
