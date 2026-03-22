//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/testdb"
)

func testAdminPool(t *testing.T) DBTX {
	t.Helper()
	pool, _ := testdb.New(t)
	return pool
}

func insertUser(t *testing.T, pool DBTX, id, email, username string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, username)
		VALUES ($1, $2, $3)
	`, id, email, username); err != nil {
		t.Fatalf("inserting user %s: %v", username, err)
	}
}

func TestAdminEntitlementOverride_ConsistentResolution(t *testing.T) {
	pool := testAdminPool(t)
	adminRepo := NewAdminRepository()
	socialRepo := NewSocialRepository()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	userID := "00000000-0000-0000-0000-000000000001"
	insertUser(t, pool, userID, "alice@example.com", "alice")

	// Helper to check all queries agree on premium status.
	assertAllConsistent := func(t *testing.T, label string, wantPremium bool) {
		t.Helper()

		// ListUsers
		users, _, err := adminRepo.ListUsers(ctx, pool, ListUsersParams{Limit: 10, Offset: 0})
		if err != nil {
			t.Fatalf("[%s] ListUsers error: %v", label, err)
		}
		if len(users) != 1 {
			t.Fatalf("[%s] ListUsers: got %d users, want 1", label, len(users))
		}
		if users[0].IsPremium != wantPremium {
			t.Errorf("[%s] ListUsers.IsPremium = %v, want %v", label, users[0].IsPremium, wantPremium)
		}

		// GetUserDetail
		detail, err := adminRepo.GetUserDetail(ctx, pool, userID)
		if err != nil {
			t.Fatalf("[%s] GetUserDetail error: %v", label, err)
		}
		if detail.IsPremium != wantPremium {
			t.Errorf("[%s] GetUserDetail.IsPremium = %v, want %v", label, detail.IsPremium, wantPremium)
		}

		// ListEntitlements — only returns rows if there's a snapshot or override.
		ents, _, err := adminRepo.ListEntitlements(ctx, pool, ListEntitlementsParams{
			Entitlement: EntitlementPremium,
			Limit:       10,
			Offset:      0,
		})
		if err != nil {
			t.Fatalf("[%s] ListEntitlements error: %v", label, err)
		}
		if wantPremium {
			if len(ents) != 1 {
				t.Fatalf("[%s] ListEntitlements: got %d rows, want 1", label, len(ents))
			}
			if ents[0].IsActive != wantPremium {
				t.Errorf("[%s] ListEntitlements.IsActive = %v, want %v", label, ents[0].IsActive, wantPremium)
			}
		}

		// EffectivePremiumActive
		active, err := socialRepo.EffectivePremiumActive(ctx, pool, userID)
		if err != nil {
			t.Fatalf("[%s] EffectivePremiumActive error: %v", label, err)
		}
		if active != wantPremium {
			t.Errorf("[%s] EffectivePremiumActive = %v, want %v", label, active, wantPremium)
		}
	}

	// 1. No override, no snapshot → not premium.
	assertAllConsistent(t, "baseline", false)

	// 2. Grant with future expiry → premium.
	futureExpiry := time.Now().Add(24 * time.Hour)
	err := adminRepo.UpsertEntitlementOverride(ctx, pool, UpsertOverrideParams{
		UserID:      userID,
		Entitlement: EntitlementPremium,
		Action:      AdminOverrideGrant,
		ExpiresAt:   &futureExpiry,
	})
	if err != nil {
		t.Fatalf("UpsertEntitlementOverride(grant): %v", err)
	}
	assertAllConsistent(t, "grant-future", true)

	// 3. Grant with past expiry → not premium.
	pastExpiry := time.Now().Add(-1 * time.Hour)
	err = adminRepo.UpsertEntitlementOverride(ctx, pool, UpsertOverrideParams{
		UserID:      userID,
		Entitlement: EntitlementPremium,
		Action:      AdminOverrideGrant,
		ExpiresAt:   &pastExpiry,
	})
	if err != nil {
		t.Fatalf("UpsertEntitlementOverride(grant-past): %v", err)
	}
	assertAllConsistent(t, "grant-past-expiry", false)

	// 4. Grant then revoke → not premium.
	err = adminRepo.UpsertEntitlementOverride(ctx, pool, UpsertOverrideParams{
		UserID:      userID,
		Entitlement: EntitlementPremium,
		Action:      AdminOverrideGrant,
		ExpiresAt:   &futureExpiry,
	})
	if err != nil {
		t.Fatalf("UpsertEntitlementOverride(re-grant): %v", err)
	}
	err = adminRepo.UpsertEntitlementOverride(ctx, pool, UpsertOverrideParams{
		UserID:      userID,
		Entitlement: EntitlementPremium,
		Action:      AdminOverrideRevoke,
		ExpiresAt:   &futureExpiry,
	})
	if err != nil {
		t.Fatalf("UpsertEntitlementOverride(revoke): %v", err)
	}
	assertAllConsistent(t, "grant-then-revoke", false)

	// 5. Grant override-only (no user_entitlements row) → premium.
	err = adminRepo.UpsertEntitlementOverride(ctx, pool, UpsertOverrideParams{
		UserID:      userID,
		Entitlement: EntitlementPremium,
		Action:      AdminOverrideGrant,
		ExpiresAt:   &futureExpiry,
	})
	if err != nil {
		t.Fatalf("UpsertEntitlementOverride(override-only): %v", err)
	}
	assertAllConsistent(t, "override-only-grant", true)
}
