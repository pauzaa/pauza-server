package revenuecat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// APIError is returned when the RevenueCat API responds with a non-200
// status code. Callers can inspect StatusCode to distinguish not-found
// (404) from auth failures (401/403) or transient server errors (5xx).
type APIError struct {
	StatusCode int
	AppUserID  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("revenuecat: unexpected status %d for subscriber %s", e.StatusCode, e.AppUserID)
}

const defaultBaseURL = "https://api.revenuecat.com/v1"

// Client calls the RevenueCat REST API (v1).
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// Option configures the Client for testing or non-default deployments.
type Option func(*Client)

// WithBaseURL overrides the default RevenueCat API base URL.
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = u }
}

// WithHTTPClient injects a custom *http.Client (useful for tests).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.http = hc }
}

// NewClient returns a Client configured with the given secret API key.
func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// GetSubscriber fetches the current subscriber record for appUserID.
// It returns a non-nil error for transport failures and non-200 responses.
func (c *Client) GetSubscriber(ctx context.Context, appUserID string) (*SubscriberResponse, error) {
	url := c.baseURL + "/subscribers/" + url.PathEscape(appUserID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("revenuecat: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("revenuecat: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{StatusCode: resp.StatusCode, AppUserID: appUserID}
	}

	var sub SubscriberResponse
	if err := json.NewDecoder(resp.Body).Decode(&sub); err != nil {
		return nil, fmt.Errorf("revenuecat: decoding response: %w", err)
	}
	return &sub, nil
}

// DeriveEntitlement inspects the subscriber's entitlements for the given
// entitlement identifier and returns the reconciled state. The entitlement
// is considered active when:
//   - it exists in the subscriber record, AND
//   - its ExpiresDate is nil (lifetime) or in the future, OR
//   - its GracePeriodExpiresDate is non-nil and in the future.
//
// now is accepted as a parameter so callers can control the clock in tests.
func DeriveEntitlement(sub *Subscriber, entitlementID string, now time.Time) ReconciledEntitlement {
	result := ReconciledEntitlement{
		Entitlement: entitlementID,
	}

	if sub == nil {
		return result
	}

	ent, ok := sub.Entitlements[entitlementID]
	if !ok {
		return result
	}

	result.CurrentPeriodEnd = ent.ExpiresDate

	switch {
	case ent.ExpiresDate == nil:
		// Lifetime / non-expiring entitlement.
		result.IsActive = true
	case ent.ExpiresDate.After(now):
		// Current period has not ended yet.
		result.IsActive = true
	case ent.GracePeriodExpiresDate != nil && ent.GracePeriodExpiresDate.After(now):
		// Billing issue, but grace period still valid.
		result.IsActive = true
	}

	return result
}
