package revenuecat_test

import (
	"encoding/json"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/revenuecat"
)

func TestWebhookEventType_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	var got revenuecat.WebhookEventType
	if err := json.Unmarshal([]byte(`"RENEWAL"`), &got); err != nil {
		t.Fatalf("unmarshal valid event type: %v", err)
	}
	if got != revenuecat.WebhookEventTypeRenewal {
		t.Fatalf("event type = %q, want %q", got, revenuecat.WebhookEventTypeRenewal)
	}
}

func TestWebhookEventType_UnmarshalJSON_UnknownAccepted(t *testing.T) {
	t.Parallel()

	var got revenuecat.WebhookEventType
	if err := json.Unmarshal([]byte(`"BOGUS_EVENT"`), &got); err != nil {
		t.Fatalf("unknown event type should be accepted: %v", err)
	}
	if got != "BOGUS_EVENT" {
		t.Fatalf("event type = %q, want %q", got, "BOGUS_EVENT")
	}
}

func TestWebhookEventType_UnmarshalJSON_EmptyRejects(t *testing.T) {
	t.Parallel()

	var got revenuecat.WebhookEventType
	if err := json.Unmarshal([]byte(`""`), &got); err == nil {
		t.Fatal("expected empty event type to fail")
	}
}

func TestWebhookEventType_NewConstants(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		raw  string
		want revenuecat.WebhookEventType
	}{
		{"SUBSCRIPTION_EXTENDED", revenuecat.WebhookEventTypeSubscriptionExtended},
		{"REFUND_REVERSED", revenuecat.WebhookEventTypeRefundReversed},
		{"VIRTUAL_CURRENCY_TRANSACTION", revenuecat.WebhookEventTypeVirtualCurrency},
		{"EXPERIMENT_ENROLLMENT", revenuecat.WebhookEventTypeExperimentEnrollment},
	} {
		var got revenuecat.WebhookEventType
		if err := json.Unmarshal([]byte(`"`+tc.raw+`"`), &got); err != nil {
			t.Errorf("unmarshal %q: %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("event type = %q, want %q", got, tc.want)
		}
	}
}

func TestWebhookEnvironment_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	var got revenuecat.WebhookEnvironment
	if err := json.Unmarshal([]byte(`"PRODUCTION"`), &got); err != nil {
		t.Fatalf("unmarshal valid environment: %v", err)
	}
	if got != revenuecat.WebhookEnvironmentProduction {
		t.Fatalf("environment = %q, want %q", got, revenuecat.WebhookEnvironmentProduction)
	}
}

func TestWebhookEnvironment_UnmarshalJSON_UnknownAccepted(t *testing.T) {
	t.Parallel()

	var got revenuecat.WebhookEnvironment
	if err := json.Unmarshal([]byte(`"LOCAL"`), &got); err != nil {
		t.Fatalf("unknown environment should be accepted: %v", err)
	}
	if got != "LOCAL" {
		t.Fatalf("environment = %q, want %q", got, "LOCAL")
	}
}

func TestWebhookEnvironment_UnmarshalJSON_EmptyRejects(t *testing.T) {
	t.Parallel()

	var got revenuecat.WebhookEnvironment
	if err := json.Unmarshal([]byte(`""`), &got); err == nil {
		t.Fatal("expected empty environment to fail")
	}
}

func TestWebhookPeriodType_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	var got revenuecat.WebhookPeriodType
	if err := json.Unmarshal([]byte(`"TRIAL"`), &got); err != nil {
		t.Fatalf("unmarshal valid period type: %v", err)
	}
	if got != revenuecat.WebhookPeriodTypeTrial {
		t.Fatalf("period_type = %q, want %q", got, revenuecat.WebhookPeriodTypeTrial)
	}
}

func TestWebhookPeriodType_UnmarshalJSON_UnknownAccepted(t *testing.T) {
	t.Parallel()

	var got revenuecat.WebhookPeriodType
	if err := json.Unmarshal([]byte(`"YEARLY"`), &got); err != nil {
		t.Fatalf("unknown period type should be accepted: %v", err)
	}
	if got != "YEARLY" {
		t.Fatalf("period_type = %q, want %q", got, "YEARLY")
	}
}

func TestWebhookPeriodType_UnmarshalJSON_EmptyAccepted(t *testing.T) {
	t.Parallel()

	var got revenuecat.WebhookPeriodType
	if err := json.Unmarshal([]byte(`""`), &got); err != nil {
		t.Fatalf("empty period type should be accepted: %v", err)
	}
	if got != "" {
		t.Fatalf("period_type = %q, want empty", got)
	}
}

func TestWebhookPeriodType_Promotional(t *testing.T) {
	t.Parallel()

	var got revenuecat.WebhookPeriodType
	if err := json.Unmarshal([]byte(`"PROMOTIONAL"`), &got); err != nil {
		t.Fatalf("unmarshal PROMOTIONAL: %v", err)
	}
	if got != revenuecat.WebhookPeriodTypePromotional {
		t.Fatalf("period_type = %q, want %q", got, revenuecat.WebhookPeriodTypePromotional)
	}
}

func TestWebhookStore_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	var got revenuecat.WebhookStore
	if err := json.Unmarshal([]byte(`"PLAY_STORE"`), &got); err != nil {
		t.Fatalf("unmarshal valid store: %v", err)
	}
	if got != revenuecat.WebhookStorePlayStore {
		t.Fatalf("store = %q, want %q", got, revenuecat.WebhookStorePlayStore)
	}
}

func TestWebhookStore_UnmarshalJSON_UnknownAccepted(t *testing.T) {
	t.Parallel()

	var got revenuecat.WebhookStore
	if err := json.Unmarshal([]byte(`"STEAM"`), &got); err != nil {
		t.Fatalf("unknown store should be accepted: %v", err)
	}
	if got != "STEAM" {
		t.Fatalf("store = %q, want %q", got, "STEAM")
	}
}

func TestWebhookStore_UnmarshalJSON_EmptyAccepted(t *testing.T) {
	t.Parallel()

	var got revenuecat.WebhookStore
	if err := json.Unmarshal([]byte(`""`), &got); err != nil {
		t.Fatalf("empty store should be accepted: %v", err)
	}
	if got != "" {
		t.Fatalf("store = %q, want empty", got)
	}
}

func TestWebhookStore_NewConstants(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		raw  string
		want revenuecat.WebhookStore
	}{
		{"PADDLE", revenuecat.WebhookStorePaddle},
		{"ROKU", revenuecat.WebhookStoreRoku},
		{"TEST_STORE", revenuecat.WebhookStoreTestStore},
	} {
		var got revenuecat.WebhookStore
		if err := json.Unmarshal([]byte(`"`+tc.raw+`"`), &got); err != nil {
			t.Errorf("unmarshal %q: %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("store = %q, want %q", got, tc.want)
		}
	}
}
