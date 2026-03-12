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
	if err := json.Unmarshal([]byte(`"BOGUS_EVENT"`), &got); err == nil {
		t.Fatal("expected invalid event type to fail")
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
	if err := json.Unmarshal([]byte(`"LOCAL"`), &got); err == nil {
		t.Fatal("expected invalid environment to fail")
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
	if err := json.Unmarshal([]byte(`"YEARLY"`), &got); err == nil {
		t.Fatal("expected invalid period type to fail")
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
	if err := json.Unmarshal([]byte(`"STEAM"`), &got); err == nil {
		t.Fatal("expected invalid store to fail")
	}
}
