package revenuecat

import (
	"encoding/json"
	"fmt"
	"time"
)

type WebhookEventType string

const (
	WebhookEventTypeInitialPurchase      WebhookEventType = "INITIAL_PURCHASE"
	WebhookEventTypeRenewal              WebhookEventType = "RENEWAL"
	WebhookEventTypeCancellation         WebhookEventType = "CANCELLATION"
	WebhookEventTypeUncancellation       WebhookEventType = "UNCANCELLATION"
	WebhookEventTypeNonRenewingPurchase  WebhookEventType = "NON_RENEWING_PURCHASE"
	WebhookEventTypeProductChange        WebhookEventType = "PRODUCT_CHANGE"
	WebhookEventTypeBillingIssue         WebhookEventType = "BILLING_ISSUE"
	WebhookEventTypeSubscriberAlias      WebhookEventType = "SUBSCRIBER_ALIAS"
	WebhookEventTypeSubscriptionPaused   WebhookEventType = "SUBSCRIPTION_PAUSED"
	WebhookEventTypeExpiration           WebhookEventType = "EXPIRATION"
	WebhookEventTypeTransfer             WebhookEventType = "TRANSFER"
	WebhookEventTypeTest                 WebhookEventType = "TEST"
	WebhookEventTypeTemporaryEntitlement WebhookEventType = "TEMPORARY_ENTITLEMENT_GRANT"
	WebhookEventTypeInvoiceIssuance      WebhookEventType = "INVOICE_ISSUANCE"
	WebhookEventTypeSubscriptionExtended WebhookEventType = "SUBSCRIPTION_EXTENDED"
	WebhookEventTypeRefundReversed       WebhookEventType = "REFUND_REVERSED"
	WebhookEventTypeVirtualCurrency      WebhookEventType = "VIRTUAL_CURRENCY_TRANSACTION"
	WebhookEventTypeExperimentEnrollment WebhookEventType = "EXPERIMENT_ENROLLMENT"
)

func parseWebhookEventType(raw string) (WebhookEventType, error) {
	if raw == "" {
		return "", fmt.Errorf("empty webhook event type")
	}
	return WebhookEventType(raw), nil
}

func (t WebhookEventType) String() string { return string(t) }

func (t WebhookEventType) MarshalJSON() ([]byte, error) { return json.Marshal(t.String()) }

func (t *WebhookEventType) UnmarshalJSON(data []byte) error {
	value, err := unmarshalEnumJSON(data, parseWebhookEventType)
	if err != nil {
		return err
	}
	*t = value
	return nil
}

type WebhookEnvironment string

const (
	WebhookEnvironmentSandbox    WebhookEnvironment = "SANDBOX"
	WebhookEnvironmentProduction WebhookEnvironment = "PRODUCTION"
)

func parseWebhookEnvironment(raw string) (WebhookEnvironment, error) {
	if raw == "" {
		return "", fmt.Errorf("empty webhook environment")
	}
	return WebhookEnvironment(raw), nil
}

func (e WebhookEnvironment) String() string { return string(e) }

func (e WebhookEnvironment) MarshalJSON() ([]byte, error) { return json.Marshal(e.String()) }

func (e *WebhookEnvironment) UnmarshalJSON(data []byte) error {
	value, err := unmarshalEnumJSON(data, parseWebhookEnvironment)
	if err != nil {
		return err
	}
	*e = value
	return nil
}

type WebhookPeriodType string

const (
	WebhookPeriodTypeNormal  WebhookPeriodType = "NORMAL"
	WebhookPeriodTypeIntro   WebhookPeriodType = "INTRO"
	WebhookPeriodTypeTrial   WebhookPeriodType = "TRIAL"
	WebhookPeriodTypePrepaid      WebhookPeriodType = "PREPAID"
	WebhookPeriodTypePromotional  WebhookPeriodType = "PROMOTIONAL"
)

func parseWebhookPeriodType(raw string) (WebhookPeriodType, error) {
	return WebhookPeriodType(raw), nil
}

func (p WebhookPeriodType) String() string { return string(p) }

func (p WebhookPeriodType) MarshalJSON() ([]byte, error) { return json.Marshal(p.String()) }

func (p *WebhookPeriodType) UnmarshalJSON(data []byte) error {
	value, err := unmarshalEnumJSON(data, parseWebhookPeriodType)
	if err != nil {
		return err
	}
	*p = value
	return nil
}

type WebhookStore string

const (
	WebhookStoreAppStore    WebhookStore = "APP_STORE"
	WebhookStoreMacAppStore WebhookStore = "MAC_APP_STORE"
	WebhookStorePlayStore   WebhookStore = "PLAY_STORE"
	WebhookStoreStripe      WebhookStore = "STRIPE"
	WebhookStorePromotional WebhookStore = "PROMOTIONAL"
	WebhookStoreAmazon      WebhookStore = "AMAZON"
	WebhookStoreRCBilling   WebhookStore = "RC_BILLING"
	WebhookStoreExternal    WebhookStore = "EXTERNAL"
	WebhookStorePaddle      WebhookStore = "PADDLE"
	WebhookStoreRoku        WebhookStore = "ROKU"
	WebhookStoreTestStore   WebhookStore = "TEST_STORE"
)

func parseWebhookStore(raw string) (WebhookStore, error) {
	return WebhookStore(raw), nil
}

func (s WebhookStore) String() string { return string(s) }

func (s WebhookStore) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

func (s *WebhookStore) UnmarshalJSON(data []byte) error {
	value, err := unmarshalEnumJSON(data, parseWebhookStore)
	if err != nil {
		return err
	}
	*s = value
	return nil
}

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
	Type              WebhookEventType   `json:"type"`
	ID                string             `json:"id"`
	AppUserID         string             `json:"app_user_id"`
	OriginalAppUserID string             `json:"original_app_user_id"`
	ProductID         string             `json:"product_id"`
	EntitlementIDs    []string           `json:"entitlement_ids"`
	EventTimestampMs  int64              `json:"event_timestamp_ms"`
	ExpirationAtMs    *int64             `json:"expiration_at_ms"`
	Environment       WebhookEnvironment `json:"environment"`
	PeriodType        WebhookPeriodType  `json:"period_type"`
	Store             WebhookStore       `json:"store"`
	TransferredFrom   []string           `json:"transferred_from"`
	TransferredTo     []string           `json:"transferred_to"`
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

func unmarshalEnumJSON[T ~string](data []byte, parse func(string) (T, error)) (T, error) {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		var zero T
		return zero, err
	}
	return parse(raw)
}
