package middleware_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/middleware"
	"github.com/IsorilovA/pauza-server/internal/ratelimit"
)

// okHandler writes a 200 OK response. It is used as the inner handler in
// rate-limit middleware tests to confirm that allowed requests reach the
// next handler.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestRateLimit_AllowsUnderLimit(t *testing.T) {
	const rate = 3
	lim := ratelimit.New(rate, time.Minute)
	defer lim.Stop()

	handler := middleware.RateLimit(lim, rate, middleware.IPKey)(okHandler)

	for i := range rate {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}

		// Verify informational headers are present.
		if got := rec.Header().Get("X-RateLimit-Limit"); got != strconv.Itoa(rate) {
			t.Errorf("request %d: X-RateLimit-Limit = %q, want %q", i+1, got, strconv.Itoa(rate))
		}
		remaining := rec.Header().Get("X-RateLimit-Remaining")
		wantRemaining := strconv.Itoa(rate - (i + 1))
		if remaining != wantRemaining {
			t.Errorf("request %d: X-RateLimit-Remaining = %q, want %q", i+1, remaining, wantRemaining)
		}
		if rec.Header().Get("X-RateLimit-Reset") == "" {
			t.Errorf("request %d: X-RateLimit-Reset header missing", i+1)
		}
	}
}

func TestRateLimit_DeniesOverLimit(t *testing.T) {
	const rate = 2
	lim := ratelimit.New(rate, time.Minute)
	defer lim.Stop()

	handler := middleware.RateLimit(lim, rate, middleware.IPKey)(okHandler)

	// Exhaust the budget.
	for range rate {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// The next request should be denied.
	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}

	// Verify the RATE_LIMITED error envelope.
	var errResp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Error.Code != apperror.CodeRateLimited {
		t.Errorf("error code = %q, want %q", errResp.Error.Code, apperror.CodeRateLimited)
	}

	// Verify rate-limit headers.
	if got := rec.Header().Get("X-RateLimit-Remaining"); got != "0" {
		t.Errorf("X-RateLimit-Remaining = %q, want %q", got, "0")
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header missing on 429 response")
	}
	retryAfter, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil {
		t.Fatalf("Retry-After is not a valid integer: %v", err)
	}
	if retryAfter < 1 {
		t.Errorf("Retry-After = %d, want >= 1", retryAfter)
	}
}

func TestRateLimit_RetryAfterHeaderIsAbsent_WhenAllowed(t *testing.T) {
	lim := ratelimit.New(5, time.Minute)
	defer lim.Stop()

	handler := middleware.RateLimit(lim, 5, middleware.IPKey)(okHandler)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "" {
		t.Errorf("Retry-After should not be set on allowed requests, got %q", got)
	}
}

func TestRateLimit_DifferentKeysAreIndependent(t *testing.T) {
	const rate = 1
	lim := ratelimit.New(rate, time.Minute)
	defer lim.Stop()

	handler := middleware.RateLimit(lim, rate, middleware.IPKey)(okHandler)

	// First IP exhausts its budget.
	req1 := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req1.RemoteAddr = "10.0.0.1:1234"
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("IP1 first request: expected 200, got %d", rec1.Code)
	}

	// First IP's second request is denied.
	req2 := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req2.RemoteAddr = "10.0.0.1:1234"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("IP1 second request: expected 429, got %d", rec2.Code)
	}

	// Second IP's first request should still be allowed.
	req3 := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req3.RemoteAddr = "10.0.0.2:5678"
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusOK {
		t.Fatalf("IP2 first request: expected 200, got %d", rec3.Code)
	}
}

func TestRateLimit_ResetHeaderIsValidUnixTimestamp(t *testing.T) {
	lim := ratelimit.New(5, time.Minute)
	defer lim.Stop()

	handler := middleware.RateLimit(lim, 5, middleware.IPKey)(okHandler)

	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resetStr := rec.Header().Get("X-RateLimit-Reset")
	resetUnix, err := strconv.ParseInt(resetStr, 10, 64)
	if err != nil {
		t.Fatalf("X-RateLimit-Reset %q is not a valid unix timestamp: %v", resetStr, err)
	}

	resetTime := time.Unix(resetUnix, 0)
	now := time.Now()
	if resetTime.Before(now) {
		t.Errorf("X-RateLimit-Reset %v should be in the future, now is %v", resetTime, now)
	}
	if resetTime.After(now.Add(2 * time.Minute)) {
		t.Errorf("X-RateLimit-Reset %v is too far in the future", resetTime)
	}
}

func TestRateLimit_CustomKeyFunc(t *testing.T) {
	const rate = 1
	lim := ratelimit.New(rate, time.Minute)
	defer lim.Stop()

	// Key by a custom header instead of IP.
	customKey := func(r *http.Request) string {
		return r.Header.Get("X-Custom-Key")
	}

	handler := middleware.RateLimit(lim, rate, customKey)(okHandler)

	// Key "A" uses its one allowed request.
	req1 := httptest.NewRequest(http.MethodPost, "/test", nil)
	req1.Header.Set("X-Custom-Key", "A")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("key A first: expected 200, got %d", rec1.Code)
	}

	// Key "A" second request is denied.
	req2 := httptest.NewRequest(http.MethodPost, "/test", nil)
	req2.Header.Set("X-Custom-Key", "A")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("key A second: expected 429, got %d", rec2.Code)
	}

	// Key "B" is still allowed.
	req3 := httptest.NewRequest(http.MethodPost, "/test", nil)
	req3.Header.Set("X-Custom-Key", "B")
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusOK {
		t.Fatalf("key B first: expected 200, got %d", rec3.Code)
	}
}

func TestIPKey_StripsPort(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{"IPv4 with port", "192.168.1.1:12345", "192.168.1.1"},
		{"IPv4 without port", "192.168.1.1", "192.168.1.1"},
		{"IPv6 with port", "[::1]:8080", "::1"},
		{"IPv6 without port", "::1", "::1"},
		{"loopback with port", "127.0.0.1:54321", "127.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr

			got := middleware.IPKey(req)
			if got != tt.want {
				t.Errorf("IPKey(%q) = %q, want %q", tt.remoteAddr, got, tt.want)
			}
		})
	}
}

func TestEmailKey_ExtractsNormalizedEmail(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"lowercase email", `{"email":"user@example.com","otp":"123456"}`, "email:user@example.com"},
		{"mixed case email", `{"email":"User@Example.COM","otp":"123456"}`, "email:user@example.com"},
		{"whitespace trimmed", `{"email":"  user@example.com  ","otp":"123456"}`, "email:user@example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/auth/verify-otp",
				strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "10.0.0.1:9999"

			got := middleware.EmailKey(req)
			if got != tt.want {
				t.Errorf("EmailKey = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEmailKey_FallsBackToIP_WhenNoEmail(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"empty email field", `{"email":"","otp":"123456"}`},
		{"no email field", `{"otp":"123456"}`},
		{"invalid json", `not json`},
		{"empty body", ``},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/auth/verify-otp",
				strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "10.0.0.1:9999"

			got := middleware.EmailKey(req)
			// Should fall back to the IP key (stripped of port).
			if got != "10.0.0.1" {
				t.Errorf("EmailKey = %q, want fallback IP %q", got, "10.0.0.1")
			}
		})
	}
}

func TestEmailKey_RewoundsBody(t *testing.T) {
	body := `{"email":"user@example.com","otp":"123456"}`
	req := httptest.NewRequest(http.MethodPost, "/auth/verify-otp",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Call EmailKey — it should read and rewind the body.
	_ = middleware.EmailKey(req)

	// The downstream handler should still be able to read the full body.
	remaining, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("reading body after EmailKey: %v", err)
	}
	if string(remaining) != body {
		t.Errorf("body after EmailKey = %q, want %q", remaining, body)
	}
}

func TestRateLimit_PerEmailThrottling(t *testing.T) {
	const rate = 3
	lim := ratelimit.New(rate, time.Minute)
	defer lim.Stop()

	// echoBodyHandler reads and echoes the request body to verify it was
	// properly rewound after EmailKey consumed it.
	echoBodyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write(body) //nolint:errcheck
	})

	handler := middleware.RateLimit(lim, rate, middleware.EmailKey)(echoBodyHandler)

	makeReq := func(email string) *httptest.ResponseRecorder {
		body := `{"email":"` + email + `","otp":"123456"}`
		req := httptest.NewRequest(http.MethodPost, "/auth/verify-otp",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.1:9999"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// Same email: exhaust budget.
	for i := range rate {
		rec := makeReq("target@example.com")
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d for target: expected 200, got %d", i+1, rec.Code)
		}
		// Verify the body was rewound and available to the handler.
		respBody := rec.Body.String()
		if !strings.Contains(respBody, "target@example.com") {
			t.Errorf("request %d: handler did not see original body, got %q", i+1, respBody)
		}
	}

	// Same email: 4th request should be denied.
	rec := makeReq("target@example.com")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("4th request for same email: expected 429, got %d", rec.Code)
	}

	// Different email: should still be allowed.
	rec2 := makeReq("other@example.com")
	if rec2.Code != http.StatusOK {
		t.Fatalf("first request for other email: expected 200, got %d", rec2.Code)
	}
}
