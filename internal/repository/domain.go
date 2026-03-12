package repository

import (
	"encoding/json"
	"fmt"
)

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

func (e Entitlement) String() string {
	return string(e)
}

func (e Entitlement) Valid() bool {
	_, err := ParseEntitlement(e.String())
	return err == nil
}

func (e Entitlement) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.String())
}

func (e *Entitlement) UnmarshalJSON(data []byte) error {
	value, err := unmarshalEnumJSON(data, ParseEntitlement)
	if err != nil {
		return err
	}
	if value == EntitlementUnknown {
		return fmt.Errorf("invalid entitlement %q", value)
	}
	*e = value
	return nil
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

func (a AdminOverrideAction) String() string {
	return string(a)
}

func (a AdminOverrideAction) Valid() bool {
	_, err := ParseAdminOverrideAction(a.String())
	return err == nil
}

func (a AdminOverrideAction) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.String())
}

func (a *AdminOverrideAction) UnmarshalJSON(data []byte) error {
	value, err := unmarshalEnumJSON(data, ParseAdminOverrideAction)
	if err != nil {
		return err
	}
	if value == AdminOverrideActionUnknown {
		return fmt.Errorf("invalid admin override action %q", value)
	}
	*a = value
	return nil
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

func (p DevicePlatform) String() string {
	return string(p)
}

func (p DevicePlatform) Valid() bool {
	_, err := ParseDevicePlatform(p.String())
	return err == nil
}

func (p DevicePlatform) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.String())
}

func (p *DevicePlatform) UnmarshalJSON(data []byte) error {
	value, err := unmarshalEnumJSON(data, ParseDevicePlatform)
	if err != nil {
		return err
	}
	if value == DevicePlatformUnknown {
		return fmt.Errorf("invalid device platform %q", value)
	}
	*p = value
	return nil
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

func unmarshalEnumJSON[T ~string](data []byte, parse func(string) (T, error)) (T, error) {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		var zero T
		return zero, err
	}
	return parse(raw)
}
