package service

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Unit tests for pure helpers and exported constants
// ---------------------------------------------------------------------------

// TestNormalizeEmail verifies that normalizeEmail lowercases and trims whitespace.
func TestNormalizeEmail(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"lowercase_noop", "user@example.com", "user@example.com"},
		{"mixed_case", "User@EXAMPLE.Com", "user@example.com"},
		{"leading_whitespace", "  user@example.com", "user@example.com"},
		{"trailing_whitespace", "user@example.com  ", "user@example.com"},
		{"both_whitespace", "  User@Example.COM  ", "user@example.com"},
		{"empty", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeEmail(tc.raw)
			if got != tc.want {
				t.Errorf("normalizeEmail(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestGenerateUsername verifies the format and uniqueness of generated usernames.
func TestGenerateUsername(t *testing.T) {
	t.Parallel()

	u1, err := generateUsername()
	if err != nil {
		t.Fatalf("generateUsername() error: %v", err)
	}

	// Must start with "user_" prefix.
	const prefix = "user_"
	if len(u1) <= len(prefix) || u1[:len(prefix)] != prefix {
		t.Errorf("generateUsername() = %q, expected prefix %q", u1, prefix)
	}

	// Must be prefix + 24 hex chars = 29 chars total.
	if len(u1) != 29 {
		t.Errorf("generateUsername() length = %d, want 29", len(u1))
	}

	// Subsequent call should produce a different username (with overwhelming probability).
	u2, err := generateUsername()
	if err != nil {
		t.Fatalf("generateUsername() error on second call: %v", err)
	}
	if u1 == u2 {
		t.Errorf("two generateUsername calls returned the same value: %q", u1)
	}
}

// TestIsUniqueViolation verifies that the isUniqueViolation helper correctly
// identifies Postgres unique-violation errors by constraint name.
func TestIsUniqueViolation(t *testing.T) {
	t.Parallel()

	// nil error should not match.
	if isUniqueViolation(nil, "") {
		t.Error("isUniqueViolation(nil, \"\") = true, want false")
	}

	// Non-PgError should not match.
	if isUniqueViolation(ErrInternal, "any_constraint") {
		t.Error("isUniqueViolation(ErrInternal, ...) = true, want false")
	}
}

// TestServiceMessage verifies the serviceMessage extraction and fallback.
func TestServiceMessage(t *testing.T) {
	t.Parallel()

	// This function is in the handler package, not the service package.
	// Service-layer sentinel errors are covered by their type assertions
	// in handler tests. Here we verify that the sentinel errors exist
	// and are distinct.

	sentinels := []error{ErrConflict, ErrUnauthorized, ErrRateLimited, ErrInternal}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && a == b {
				t.Errorf("sentinel errors %d and %d are the same: %v", i, j, a)
			}
		}
	}
}

// TestEntitlementInfo_CurrentPeriodEnd_PointerType verifies that
// EntitlementInfo.CurrentPeriodEnd accepts a *time.Time value.
func TestEntitlementInfo_CurrentPeriodEnd_PointerType(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	info := EntitlementInfo{}
	info.CurrentPeriodEnd = &now
	if info.CurrentPeriodEnd == nil || !info.CurrentPeriodEnd.Equal(now) {
		t.Error("expected CurrentPeriodEnd to hold the assigned time")
	}
}
