package repository

type Entitlement string

const (
	EntitlementPremium Entitlement = "premium"
)

type AdminOverrideAction string

const (
	AdminOverrideGrant  AdminOverrideAction = "grant"
	AdminOverrideRevoke AdminOverrideAction = "revoke"
)

type DevicePlatform string

const (
	PlatformAndroid DevicePlatform = "android"
	PlatformIOS     DevicePlatform = "ios"
)

type FriendRequestDirection string

const (
	FriendRequestIncoming FriendRequestDirection = "incoming"
	FriendRequestOutgoing FriendRequestDirection = "outgoing"
)
