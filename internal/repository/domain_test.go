package repository_test

import (
	"encoding/json"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/repository"
)

func TestEntitlement_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	var got repository.Entitlement
	if err := json.Unmarshal([]byte(`"premium"`), &got); err != nil {
		t.Fatalf("unmarshal valid entitlement: %v", err)
	}
	if got != repository.EntitlementPremium {
		t.Fatalf("entitlement = %q, want %q", got, repository.EntitlementPremium)
	}

	if err := json.Unmarshal([]byte(`"invalid"`), &got); err == nil {
		t.Fatal("expected invalid entitlement to fail")
	}
	if err := json.Unmarshal([]byte(`""`), &got); err == nil {
		t.Fatal("expected empty entitlement to fail")
	}
}

func TestAdminOverrideAction_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	var got repository.AdminOverrideAction
	if err := json.Unmarshal([]byte(`"grant"`), &got); err != nil {
		t.Fatalf("unmarshal valid action: %v", err)
	}
	if got != repository.AdminOverrideGrant {
		t.Fatalf("action = %q, want %q", got, repository.AdminOverrideGrant)
	}

	if err := json.Unmarshal([]byte(`"suspend"`), &got); err == nil {
		t.Fatal("expected invalid action to fail")
	}
	if err := json.Unmarshal([]byte(`""`), &got); err == nil {
		t.Fatal("expected empty action to fail")
	}
}

func TestDevicePlatform_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	var got repository.DevicePlatform
	if err := json.Unmarshal([]byte(`"android"`), &got); err != nil {
		t.Fatalf("unmarshal valid platform: %v", err)
	}
	if got != repository.PlatformAndroid {
		t.Fatalf("platform = %q, want %q", got, repository.PlatformAndroid)
	}

	if err := json.Unmarshal([]byte(`"web"`), &got); err == nil {
		t.Fatal("expected invalid platform to fail")
	}
	if err := json.Unmarshal([]byte(`""`), &got); err == nil {
		t.Fatal("expected empty platform to fail")
	}
}
