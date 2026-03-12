package service

import (
	"errors"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/domain"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

var ErrConflict = errors.New("conflict")
var ErrNotFound = errors.New("not found")
var ErrUnauthorized = errors.New("unauthorized")
var ErrRateLimited = errors.New("rate limited")
var ErrSubscriptionRequired = errors.New("subscription required")
var ErrInternal = errors.New("internal error")

type APIError struct {
	Code       string
	Message    string
	Details    *apperror.ValidationDetails
	RetryAfter time.Duration
	Cause      error
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Code
}

func (e *APIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func apiError(code string, cause error, message string) error {
	return &APIError{Code: code, Message: message, Cause: cause}
}

func UnauthorizedError(message string) error {
	return apiError(apperror.CodeUnauthorized, ErrUnauthorized, message)
}

func ConflictError(message string) error {
	return apiError(apperror.CodeConflict, ErrConflict, message)
}

func NotFoundError(message string) error {
	return apiError(apperror.CodeNotFound, ErrNotFound, message)
}

func SubscriptionRequiredError(message string) error {
	return apiError(apperror.CodeSubscriptionRequired, ErrSubscriptionRequired, message)
}

func ValidationError(message string, fields apperror.FieldErrors) error {
	var details *apperror.ValidationDetails
	if len(fields) > 0 {
		details = apperror.NewValidationDetails(fields)
	}
	return &APIError{
		Code:    apperror.CodeValidationError,
		Message: message,
		Details: details,
	}
}

func RateLimitedError(message string, retryAfter time.Duration) error {
	return &APIError{
		Code:       apperror.CodeRateLimited,
		Message:    message,
		RetryAfter: retryAfter,
		Cause:      ErrRateLimited,
	}
}

func invalidEntitlementError(entitlement repository.Entitlement) error {
	return ValidationError("Invalid request body", apperror.FieldErrors{
		"entitlement": "entitlement must be " + string(repository.EntitlementPremium),
	})
}

func invalidAdminActionError() error {
	return ValidationError("Invalid request body", apperror.FieldErrors{
		"action": "action must be grant or revoke",
	})
}

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
	Entitlement      domain.Entitlement
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
	PushEnabled bool
}

type PrivacyPreferences struct {
	LeaderboardVisible bool
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
