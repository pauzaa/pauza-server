package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/IsorilovA/pauza-server/internal/repository"
)

func (s *AuthService) GetMe(ctx context.Context, in GetMeInput) (UserProfile, error) {
	user, err := s.repo.GetUserByID(ctx, s.pool, in.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return UserProfile{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("querying user profile", "err", err)
		return UserProfile{}, ErrInternal
	}

	subscription, err := s.lookupEntitlementSnapshot(ctx, user.ID)
	if err != nil {
		s.logger.Error("querying user entitlement", "user_id", user.ID, "err", err)
		return UserProfile{}, ErrInternal
	}

	return userProfileFromRow(user, subscription), nil
}

func (s *AuthService) UpdateMe(ctx context.Context, in UpdateMeInput) (UserProfile, error) {
	updated, err := s.repo.UpdateUser(ctx, s.pool, in.UserID, in.Name, in.Username, nil, nil)
	if errors.Is(err, repository.ErrNotFound) {
		return UserProfile{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
	}
	if err != nil {
		if isUniqueViolation(err, "users_username_key") || isUniqueViolation(err, "idx_users_username") {
			return UserProfile{}, fmt.Errorf("%w: username already taken", ErrConflict)
		}
		s.logger.Error("updating user profile", "err", err)
		return UserProfile{}, ErrInternal
	}

	subscription, err := s.lookupEntitlementSnapshot(ctx, updated.ID)
	if err != nil {
		s.logger.Error("querying user entitlement", "user_id", updated.ID, "err", err)
		return UserProfile{}, ErrInternal
	}

	return userProfileFromRow(updated, subscription), nil
}

func (s *AuthService) CheckUsernameAvailable(ctx context.Context, in UsernameAvailableInput) (UsernameAvailableOutput, error) {
	taken, err := s.repo.IsUsernameTaken(ctx, s.pool, in.Username, in.UserID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return UsernameAvailableOutput{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
		}
		s.logger.Error("checking username availability", "err", err)
		return UsernameAvailableOutput{}, ErrInternal
	}
	return UsernameAvailableOutput{Available: !taken}, nil
}

func (s *AuthService) UpdateProfilePhoto(ctx context.Context, in UpdateProfilePhotoInput) (UserProfile, error) {
	updated, err := s.repo.UpdateUser(ctx, s.pool, in.UserID, nil, nil, nil, &in.ProfilePictureURL)
	if errors.Is(err, repository.ErrNotFound) {
		return UserProfile{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("updating profile photo", "err", err)
		return UserProfile{}, ErrInternal
	}

	subscription, err := s.lookupEntitlementSnapshot(ctx, updated.ID)
	if err != nil {
		s.logger.Error("querying user entitlement", "user_id", updated.ID, "err", err)
		return UserProfile{}, ErrInternal
	}

	return userProfileFromRow(updated, subscription), nil
}

func (s *AuthService) GetNotificationPreferences(ctx context.Context, in GetNotificationPreferencesInput) (NotificationPreferences, error) {
	pushEnabled, err := s.repo.GetPushEnabled(ctx, s.pool, in.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return NotificationPreferences{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("querying notification preferences", "err", err)
		return NotificationPreferences{}, ErrInternal
	}
	return NotificationPreferences{PushEnabled: pushEnabled}, nil
}

func (s *AuthService) UpdateNotificationPreferences(ctx context.Context, in UpdateNotificationPreferencesInput) (NotificationPreferences, error) {
	if in.PushEnabled == nil {
		return s.GetNotificationPreferences(ctx, GetNotificationPreferencesInput{UserID: in.UserID})
	}

	pushEnabled, err := s.repo.UpdatePushEnabled(ctx, s.pool, in.UserID, *in.PushEnabled)
	if errors.Is(err, repository.ErrNotFound) {
		return NotificationPreferences{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("updating notification preferences", "err", err)
		return NotificationPreferences{}, ErrInternal
	}
	return NotificationPreferences{PushEnabled: pushEnabled}, nil
}

func (s *AuthService) GetPrivacyPreferences(ctx context.Context, in GetPrivacyPreferencesInput) (PrivacyPreferences, error) {
	user, err := s.repo.GetUserByID(ctx, s.pool, in.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return PrivacyPreferences{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("querying privacy preferences", "err", err)
		return PrivacyPreferences{}, ErrInternal
	}
	return PrivacyPreferences{LeaderboardVisible: user.LeaderboardVisible}, nil
}

func (s *AuthService) UpdatePrivacyPreferences(ctx context.Context, in UpdatePrivacyPreferencesInput) (PrivacyPreferences, error) {
	if in.LeaderboardVisible == nil {
		return s.GetPrivacyPreferences(ctx, GetPrivacyPreferencesInput{UserID: in.UserID})
	}

	updated, err := s.repo.UpdateUser(ctx, s.pool, in.UserID, nil, nil, in.LeaderboardVisible, nil)
	if errors.Is(err, repository.ErrNotFound) {
		return PrivacyPreferences{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("updating privacy preferences", "err", err)
		return PrivacyPreferences{}, ErrInternal
	}
	return PrivacyPreferences{LeaderboardVisible: updated.LeaderboardVisible}, nil
}
