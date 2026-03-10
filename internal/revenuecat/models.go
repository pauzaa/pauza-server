package revenuecat

import "time"

// --- Webhook payload models ---

// WebhookPayload is the top-level object RevenueCat POSTs to the webhook
// endpoint. Unknown fields are tolerated by the JSON decoder.
type WebhookPayload struct {
	APIVersion string       `json:"api_version"`
	Event      WebhookEvent `json:"event"`
}

// WebhookEvent holds the fields from a RevenueCat webhook event that the
// reconciliation flow needs. Additional fields sent by RevenueCat are
// silently ignored so the handler tolerates forward-compatible changes.
type WebhookEvent struct {
	Type              string   `json:"type"`
	ID                string   `json:"id"`
	AppUserID         string   `json:"app_user_id"`
	OriginalAppUserID string   `json:"original_app_user_id"`
	ProductID         string   `json:"product_id"`
	EntitlementIDs    []string `json:"entitlement_ids"`
	EventTimestampMs  int64    `json:"event_timestamp_ms"`
	ExpirationAtMs    *int64   `json:"expiration_at_ms"`
	Environment       string   `json:"environment"`
	PeriodType        string   `json:"period_type"`
	Store             string   `json:"store"`
	TransferredFrom   []string `json:"transferred_from"`
	TransferredTo     []string `json:"transferred_to"`
}

// --- Subscriber API response models ---

// SubscriberResponse is the top-level JSON returned by
// GET /v1/subscribers/{app_user_id}.
type SubscriberResponse struct {
	Subscriber Subscriber `json:"subscriber"`
}

// Subscriber represents the customer record returned by the RevenueCat API.
type Subscriber struct {
	OriginalAppUserID string                    `json:"original_app_user_id"`
	Entitlements      map[string]EntitlementObj `json:"entitlements"`
}

// EntitlementObj represents a single entitlement inside the subscriber record.
type EntitlementObj struct {
	ExpiresDate            *time.Time `json:"expires_date"`
	GracePeriodExpiresDate *time.Time `json:"grace_period_expires_date"`
	PurchaseDate           time.Time  `json:"purchase_date"`
	ProductIdentifier      string     `json:"product_identifier"`
}

// --- Reconciled entitlement state ---

// ReconciledEntitlement holds the derived entitlement state produced by
// inspecting a subscriber's entitlements and comparing expiration timestamps
// against the current time.
type ReconciledEntitlement struct {
	Entitlement      string
	IsActive         bool
	CurrentPeriodEnd *time.Time
}
