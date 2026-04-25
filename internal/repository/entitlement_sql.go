package repository

import "fmt"

// premiumJoinSQL returns LEFT JOIN clauses for resolving effective premium status.
// overrideAlias and entitlementAlias are the SQL aliases for the joined tables.
// userIDExpr is a trusted column reference (e.g., "u.id") — never user input.
func premiumJoinSQL(userIDExpr, overrideAlias, entitlementAlias string) string {
	return fmt.Sprintf(
		` LEFT JOIN user_entitlements %[3]s
		   ON %[3]s.user_id = %[1]s AND %[3]s.entitlement = 'premium'
		 LEFT JOIN admin_entitlement_overrides %[2]s
		   ON %[2]s.user_id = %[1]s AND %[2]s.entitlement = 'premium'
		   AND (%[2]s.expires_at IS NULL OR %[2]s.expires_at > now())`,
		userIDExpr, overrideAlias, entitlementAlias,
	)
}

// premiumCaseSQL returns a CASE expression that resolves effective premium status
// given the override and entitlement table aliases from premiumJoinSQL.
func premiumCaseSQL(overrideAlias, entitlementAlias string) string {
	return fmt.Sprintf(
		`CASE
		   WHEN %[1]s.action = 'grant'  THEN true
		   WHEN %[1]s.action = 'revoke' THEN false
		   ELSE COALESCE(%[2]s.is_active, false)
		 END`,
		overrideAlias, entitlementAlias,
	)
}

// premiumDisplayPeriodEndSQL returns the expiry timestamp shown in admin UIs:
// an active grant override uses override.expires_at; otherwise the RevenueCat snapshot.
func premiumDisplayPeriodEndSQL(overrideAlias, entitlementAlias string) string {
	return fmt.Sprintf(
		`CASE
		   WHEN %[1]s.action = 'grant' THEN %[1]s.expires_at
		   ELSE %[2]s.current_period_end
		 END`,
		overrideAlias, entitlementAlias,
	)
}

// premiumOverrideOnlyDisplayPeriodEndSQL is for ListEntitlements rows that have no
// user_entitlements snapshot (override-only branch).
func premiumOverrideOnlyDisplayPeriodEndSQL(overrideAlias string) string {
	return fmt.Sprintf(
		`CASE
		   WHEN %[1]s.action = 'grant' THEN %[1]s.expires_at
		   ELSE NULL
		 END`,
		overrideAlias,
	)
}
