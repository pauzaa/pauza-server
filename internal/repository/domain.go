package repository

import "github.com/IsorilovA/pauza-server/internal/domain"

// Type aliases — these are identical to the domain types so that existing
// repository consumers continue to compile without import changes.
type Entitlement = domain.Entitlement
type AdminOverrideAction = domain.AdminOverrideAction
type DevicePlatform = domain.DevicePlatform
type FriendRequestDirection = domain.FriendRequestDirection
type FriendshipStatus = domain.FriendshipStatus

// Re-export constants.
const (
	EntitlementUnknown = domain.EntitlementUnknown
	EntitlementPremium = domain.EntitlementPremium

	AdminOverrideActionUnknown = domain.AdminOverrideActionUnknown
	AdminOverrideGrant         = domain.AdminOverrideGrant
	AdminOverrideRevoke        = domain.AdminOverrideRevoke

	DevicePlatformUnknown  = domain.DevicePlatformUnknown
	DevicePlatformAndroid  = domain.DevicePlatformAndroid
	DevicePlatformIOS      = domain.DevicePlatformIOS
	PlatformAndroid        = domain.DevicePlatformAndroid // deprecated alias
	PlatformIOS            = domain.DevicePlatformIOS     // deprecated alias

	FriendRequestDirectionUnknown = domain.FriendRequestDirectionUnknown
	FriendRequestIncoming         = domain.FriendRequestIncoming
	FriendRequestOutgoing         = domain.FriendRequestOutgoing

	FriendshipStatusUnknown  = domain.FriendshipStatusUnknown
	FriendshipStatusPending  = domain.FriendshipStatusPending
	FriendshipStatusAccepted = domain.FriendshipStatusAccepted
)

// Re-export functions.
var (
	ParseEntitlement         = domain.ParseEntitlement
	MustParseEntitlement     = domain.MustParseEntitlement
	ParseAdminOverrideAction = domain.ParseAdminOverrideAction
	ParseDevicePlatform      = domain.ParseDevicePlatform
	ParseFriendRequestDirection = domain.ParseFriendRequestDirection
	ParseFriendshipStatus       = domain.ParseFriendshipStatus
)
