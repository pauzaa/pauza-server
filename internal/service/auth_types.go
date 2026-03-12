package service

import (
	"errors"
	"time"
)

var ErrConflict = errors.New("conflict")
var ErrNotFound = errors.New("not found")
var ErrUnauthorized = errors.New("unauthorized")
var ErrRateLimited = errors.New("rate limited")
var ErrSubscriptionRequired = errors.New("subscription required")
var ErrInternal = errors.New("internal error")

type StartAuthInput struct {
	Email string
}

type StartAuthOutput struct {
	OTPRequired bool
}

type VerifyOTPInput struct {
	Email string
	OTP   string
}

type AuthOutput struct {
	AccessToken  string
	RefreshToken string
	User         UserProfile
}

type UserProfile struct {
	ID                 string
	Email              string
	Name               string
	Username           string
	ProfilePictureURL  *string
	PushEnabled        bool
	LeaderboardVisible bool
	CreatedAt          time.Time
	Subscription       *EntitlementInfo
}

type EntitlementInfo struct {
	Entitlement      string
	IsActive         bool
	CurrentPeriodEnd *time.Time
}

type RefreshInput struct {
	RefreshToken string
}

type RefreshOutput struct {
	AccessToken  string
	RefreshToken string
}

type MessageOutput struct {
	Message string
}

type NotificationPreferences struct {
	PushEnabled bool `json:"push_enabled"`
}

type PrivacyPreferences struct {
	LeaderboardVisible bool `json:"leaderboard_visible"`
}

type GetMeInput struct {
	UserID string
}

type UpdateMeInput struct {
	UserID   string
	Name     *string
	Username *string
}

type UpdateProfilePhotoInput struct {
	UserID            string
	ProfilePictureURL string
}

type GetNotificationPreferencesInput struct {
	UserID string
}

type UpdateNotificationPreferencesInput struct {
	UserID      string
	PushEnabled *bool
}

type GetPrivacyPreferencesInput struct {
	UserID string
}

type UpdatePrivacyPreferencesInput struct {
	UserID             string
	LeaderboardVisible *bool
}

type UsernameAvailableInput struct {
	UserID   string
	Username string
}

type UsernameAvailableOutput struct {
	Available bool
}

type DeleteAccountRequestInput struct {
	UserID string
}

type DeleteAccountConfirmInput struct {
	UserID string
	OTP    string
}
