package repository

import "fmt"

type Entitlement string

const (
	EntitlementUnknown Entitlement = ""
	EntitlementPremium Entitlement = "premium"
)

func ParseEntitlement(raw string) (Entitlement, error) {
	switch Entitlement(raw) {
	case EntitlementUnknown, EntitlementPremium:
		return Entitlement(raw), nil
	default:
		return EntitlementUnknown, fmt.Errorf("invalid entitlement %q", raw)
	}
}

func MustParseEntitlement(raw string) Entitlement {
	value, err := ParseEntitlement(raw)
	if err != nil {
		panic(err)
	}
	return value
}

func (e Entitlement) Valid() bool {
	_, err := ParseEntitlement(string(e))
	return err == nil
}

type AdminOverrideAction string

const (
	AdminOverrideActionUnknown AdminOverrideAction = ""
	AdminOverrideGrant         AdminOverrideAction = "grant"
	AdminOverrideRevoke        AdminOverrideAction = "revoke"
)

func ParseAdminOverrideAction(raw string) (AdminOverrideAction, error) {
	switch AdminOverrideAction(raw) {
	case AdminOverrideActionUnknown, AdminOverrideGrant, AdminOverrideRevoke:
		return AdminOverrideAction(raw), nil
	default:
		return AdminOverrideActionUnknown, fmt.Errorf("invalid admin override action %q", raw)
	}
}

func (a AdminOverrideAction) Valid() bool {
	_, err := ParseAdminOverrideAction(string(a))
	return err == nil
}

type DevicePlatform string

const (
	DevicePlatformUnknown DevicePlatform = ""
	PlatformAndroid       DevicePlatform = "android"
	PlatformIOS           DevicePlatform = "ios"
)

func ParseDevicePlatform(raw string) (DevicePlatform, error) {
	switch DevicePlatform(raw) {
	case PlatformAndroid, PlatformIOS:
		return DevicePlatform(raw), nil
	default:
		return DevicePlatformUnknown, fmt.Errorf("invalid device platform %q", raw)
	}
}

func (p DevicePlatform) Valid() bool {
	_, err := ParseDevicePlatform(string(p))
	return err == nil
}

type FriendRequestDirection string

const (
	FriendRequestDirectionUnknown FriendRequestDirection = ""
	FriendRequestIncoming         FriendRequestDirection = "incoming"
	FriendRequestOutgoing         FriendRequestDirection = "outgoing"
)

func ParseFriendRequestDirection(raw string) (FriendRequestDirection, error) {
	switch FriendRequestDirection(raw) {
	case FriendRequestIncoming, FriendRequestOutgoing:
		return FriendRequestDirection(raw), nil
	default:
		return FriendRequestDirectionUnknown, fmt.Errorf("invalid friend request direction %q", raw)
	}
}

type FriendshipStatus string

const (
	FriendshipStatusUnknown  FriendshipStatus = ""
	FriendshipStatusPending  FriendshipStatus = "pending"
	FriendshipStatusAccepted FriendshipStatus = "accepted"
)

func ParseFriendshipStatus(raw string) (FriendshipStatus, error) {
	switch FriendshipStatus(raw) {
	case FriendshipStatusPending, FriendshipStatusAccepted:
		return FriendshipStatus(raw), nil
	default:
		return FriendshipStatusUnknown, fmt.Errorf("invalid friendship status %q", raw)
	}
}
