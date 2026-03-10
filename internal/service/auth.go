package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/redact"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

var ErrConflict = errors.New("conflict")
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

type GetMeInput struct {
	UserID string
}

type UpdateMeInput struct {
	UserID             string
	Name               *string
	Username           *string
	LeaderboardVisible *bool
}

type UpdateProfilePhotoInput struct {
	UserID            string
	ProfilePictureURL string
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

type AuthService struct {
	pool               repository.Pool
	repo               repository.AuthRepository
	mailer             mail.Sender
	jwtSecret          string
	jwtAccessTokenTTL  time.Duration
	jwtRefreshTokenTTL time.Duration
	logger             *slog.Logger
}

const cleanupTimeout = 5 * time.Second

func NewAuthService(
	pool repository.Pool,
	repo repository.AuthRepository,
	mailer mail.Sender,
	jwtSecret string,
	jwtAccessTokenTTL time.Duration,
	jwtRefreshTokenTTL time.Duration,
	logger *slog.Logger,
) *AuthService {
	return &AuthService{
		pool:               pool,
		repo:               repo,
		mailer:             mailer,
		jwtSecret:          jwtSecret,
		jwtAccessTokenTTL:  jwtAccessTokenTTL,
		jwtRefreshTokenTTL: jwtRefreshTokenTTL,
		logger:             logger,
	}
}

// Start begins a passwordless sign-in challenge for the supplied email.
func (s *AuthService) Start(ctx context.Context, in StartAuthInput) (StartAuthOutput, error) {
	email := normalizeEmail(in.Email)

	otp, err := auth.GenerateOTP()
	if err != nil {
		s.logger.Error("generating otp", "err", err)
		return StartAuthOutput{}, ErrInternal
	}

	otpHash, err := auth.HashOTP(otp)
	if err != nil {
		s.logger.Error("hashing otp", "err", err)
		return StartAuthOutput{}, ErrInternal
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning start-auth transaction", "err", err)
		return StartAuthOutput{}, ErrInternal
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := s.repo.DeleteOTPsByEmailAndPurpose(ctx, tx, email, mail.PurposeAuthLogin); err != nil {
		s.logger.Error("deleting existing auth otp", "email", redact.Email(email), "err", err)
		return StartAuthOutput{}, ErrInternal
	}

	expiresAt := time.Now().UTC().Add(auth.OTPExpiry)
	otpID, err := s.repo.InsertOTP(ctx, tx, email, nil, otpHash, mail.PurposeAuthLogin, expiresAt)
	if err != nil {
		s.logger.Error("inserting auth otp", "email", redact.Email(email), "err", err)
		return StartAuthOutput{}, ErrInternal
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("committing start-auth transaction", "err", err)
		return StartAuthOutput{}, ErrInternal
	}

	if err := s.mailer.SendOTP(ctx, email, otp, mail.PurposeAuthLogin); err != nil {
		s.logger.Error("sending auth otp email", "email", redact.Email(email), "err", err)
		s.cleanupFailedOTP(otpID)
		return StartAuthOutput{}, ErrInternal
	}

	return StartAuthOutput{OTPRequired: true}, nil
}

// VerifyOTP validates the login OTP, creates a new user if needed, and
// returns a signed-in session.
func (s *AuthService) VerifyOTP(ctx context.Context, in VerifyOTPInput) (AuthOutput, error) {
	email := normalizeEmail(in.Email)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning verify-otp transaction", "err", err)
		return AuthOutput{}, ErrInternal
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	otpRow, err := s.repo.GetActiveOTPForUpdate(ctx, tx, email, mail.PurposeAuthLogin)
	if errors.Is(err, repository.ErrNotFound) {
		return AuthOutput{}, fmt.Errorf("%w: invalid or expired OTP", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("querying auth otp", "email", redact.Email(email), "err", err)
		return AuthOutput{}, ErrInternal
	}

	attemptWindowStart := time.Now().UTC().Add(-auth.OTPExpiry)
	attemptsUsed, err := s.repo.CountFailedOTPAttemptsSinceForUpdate(ctx, tx, email, mail.PurposeAuthLogin, attemptWindowStart)
	if err != nil {
		s.logger.Error("counting failed auth otp attempts", "email", redact.Email(email), "err", err)
		return AuthOutput{}, ErrInternal
	}
	if attemptsUsed >= auth.MaxOTPAttempts {
		rateLimitedErr := s.newOTPAttemptsRateLimitedError(ctx, tx, email, mail.PurposeAuthLogin, attemptWindowStart)
		if errors.Is(rateLimitedErr, ErrInternal) {
			return AuthOutput{}, ErrInternal
		}
		return AuthOutput{}, rateLimitedErr
	}

	otpMatch, err := auth.VerifyOTP(otpRow.CodeHash, in.OTP)
	if err != nil {
		s.logger.Error("verifying otp hash", "err", err)
		return AuthOutput{}, ErrInternal
	}
	if !otpMatch {
		failedAttemptAt := time.Now().UTC()
		if err := s.repo.InsertFailedOTPAttempt(ctx, tx, email, nil, mail.PurposeAuthLogin, failedAttemptAt); err != nil {
			s.logger.Error("recording failed verify-otp attempt", "email", redact.Email(email), "err", err)
			return AuthOutput{}, ErrInternal
		}
		if err := tx.Commit(ctx); err != nil {
			s.logger.Error("committing failed verify-otp attempt", "err", err)
			return AuthOutput{}, ErrInternal
		}
		return AuthOutput{}, fmt.Errorf("%w: invalid or expired OTP", ErrUnauthorized)
	}

	if err := s.repo.MarkOTPUsed(ctx, tx, otpRow.ID); err != nil {
		s.logger.Error("marking otp used", "err", err)
		return AuthOutput{}, ErrInternal
	}

	user, err := s.ensureUserForEmail(ctx, tx, email)
	if err != nil {
		return AuthOutput{}, err
	}

	subscription, err := s.lookupEntitlementSnapshot(ctx, user.ID)
	if err != nil {
		s.logger.Error("querying user entitlement", "user_id", user.ID, "err", err)
		return AuthOutput{}, ErrInternal
	}

	out, err := s.issueAuthOutput(ctx, tx, user, subscription)
	if err != nil {
		return AuthOutput{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("committing verify-otp transaction", "err", err)
		return AuthOutput{}, ErrInternal
	}

	return out, nil
}

// Refresh rotates a refresh token and issues a new access token pair.
func (s *AuthService) Refresh(ctx context.Context, in RefreshInput) (RefreshOutput, error) {
	tokenHash := auth.HashRefreshToken(in.RefreshToken)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning refresh transaction", "err", err)
		return RefreshOutput{}, ErrInternal
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tok, err := s.repo.GetRefreshTokenByHashForUpdate(ctx, tx, tokenHash)
	if errors.Is(err, repository.ErrNotFound) {
		return RefreshOutput{}, fmt.Errorf("%w: invalid refresh token", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("querying refresh token", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	if tok.Revoked {
		if err := s.repo.RevokeAllRefreshTokens(ctx, tx, tok.UserID); err != nil {
			s.logger.Error("revoking all refresh tokens after reuse", "err", err)
			return RefreshOutput{}, ErrInternal
		}
		if err := tx.Commit(ctx); err != nil {
			s.logger.Error("committing reuse-detection revoke-all", "err", err)
			return RefreshOutput{}, ErrInternal
		}
		return RefreshOutput{}, fmt.Errorf("%w: invalid refresh token", ErrUnauthorized)
	}

	if time.Now().UTC().After(tok.ExpiresAt) {
		return RefreshOutput{}, fmt.Errorf("%w: invalid refresh token", ErrUnauthorized)
	}

	if err := s.repo.RevokeRefreshToken(ctx, tx, tok.ID); err != nil {
		s.logger.Error("revoking current refresh token", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	email, err := s.repo.GetUserEmailByID(ctx, tx, tok.UserID)
	if err != nil {
		s.logger.Error("querying user email for refresh", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	accessToken, err := auth.IssueAccessToken(tok.UserID, email, s.jwtSecret, s.jwtAccessTokenTTL)
	if err != nil {
		s.logger.Error("issuing access token", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	rawRefresh, hashRefresh, err := auth.GenerateRefreshToken()
	if err != nil {
		s.logger.Error("generating refresh token", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	refreshExpiresAt := time.Now().UTC().Add(s.jwtRefreshTokenTTL)
	if err := s.repo.InsertRefreshToken(ctx, tx, tok.UserID, hashRefresh, refreshExpiresAt); err != nil {
		s.logger.Error("inserting new refresh token", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("committing refresh transaction", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	return RefreshOutput{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
	}, nil
}

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
	updated, err := s.repo.UpdateUser(ctx, s.pool, in.UserID, in.Name, in.Username, in.LeaderboardVisible, nil)
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

func (s *AuthService) RequestAccountDeletion(ctx context.Context, in DeleteAccountRequestInput) (MessageOutput, error) {
	user, err := s.repo.GetUserByID(ctx, s.pool, in.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return MessageOutput{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("loading user for deletion request", "err", err)
		return MessageOutput{}, ErrInternal
	}

	otp, err := auth.GenerateOTP()
	if err != nil {
		s.logger.Error("generating deletion otp", "err", err)
		return MessageOutput{}, ErrInternal
	}
	otpHash, err := auth.HashOTP(otp)
	if err != nil {
		s.logger.Error("hashing deletion otp", "err", err)
		return MessageOutput{}, ErrInternal
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning delete-request transaction", "err", err)
		return MessageOutput{}, ErrInternal
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := s.repo.GetUserByIDForUpdate(ctx, tx, in.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return MessageOutput{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
		}
		s.logger.Error("locking user for deletion request", "err", err)
		return MessageOutput{}, ErrInternal
	}
	if err := s.repo.DeleteOTPsByEmailAndPurpose(ctx, tx, user.Email, mail.PurposeAccountDeletion); err != nil {
		s.logger.Error("deleting existing deletion otp", "user_id", in.UserID, "err", err)
		return MessageOutput{}, ErrInternal
	}

	userID := in.UserID
	otpID, err := s.repo.InsertOTP(ctx, tx, user.Email, &userID, otpHash, mail.PurposeAccountDeletion, time.Now().UTC().Add(auth.OTPExpiry))
	if err != nil {
		s.logger.Error("inserting deletion otp", "user_id", in.UserID, "err", err)
		return MessageOutput{}, ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("committing deletion request", "err", err)
		return MessageOutput{}, ErrInternal
	}
	if err := s.mailer.SendOTP(ctx, user.Email, otp, mail.PurposeAccountDeletion); err != nil {
		s.logger.Error("sending deletion otp", "user_id", in.UserID, "err", err)
		s.cleanupFailedOTP(otpID)
		return MessageOutput{}, ErrInternal
	}

	return MessageOutput{Message: "If the account is eligible for deletion, a confirmation code has been sent."}, nil
}

func (s *AuthService) ConfirmAccountDeletion(ctx context.Context, in DeleteAccountConfirmInput) (MessageOutput, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning delete-confirm transaction", "err", err)
		return MessageOutput{}, ErrInternal
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	user, err := s.repo.GetUserByIDForUpdate(ctx, tx, in.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return MessageOutput{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("locking user for deletion confirm", "err", err)
		return MessageOutput{}, ErrInternal
	}

	attemptWindowStart := time.Now().UTC().Add(-auth.OTPExpiry)
	attemptsUsed, err := s.repo.CountFailedOTPAttemptsSinceForUpdate(ctx, tx, user.Email, mail.PurposeAccountDeletion, attemptWindowStart)
	if err != nil {
		s.logger.Error("counting failed deletion otp attempts", "user_id", in.UserID, "err", err)
		return MessageOutput{}, ErrInternal
	}
	if attemptsUsed >= auth.MaxOTPAttempts {
		rateLimitedErr := s.newOTPAttemptsRateLimitedError(ctx, tx, user.Email, mail.PurposeAccountDeletion, attemptWindowStart)
		if errors.Is(rateLimitedErr, ErrInternal) {
			return MessageOutput{}, ErrInternal
		}
		return MessageOutput{}, rateLimitedErr
	}

	otpRow, err := s.repo.GetActiveOTPForUpdate(ctx, tx, user.Email, mail.PurposeAccountDeletion)
	if errors.Is(err, repository.ErrNotFound) {
		return MessageOutput{}, fmt.Errorf("%w: invalid or expired OTP", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("loading deletion otp", "user_id", in.UserID, "err", err)
		return MessageOutput{}, ErrInternal
	}

	match, err := auth.VerifyOTP(otpRow.CodeHash, in.OTP)
	if err != nil {
		s.logger.Error("verifying deletion otp", "err", err)
		return MessageOutput{}, ErrInternal
	}
	if !match {
		userID := in.UserID
		if err := s.repo.InsertFailedOTPAttempt(ctx, tx, user.Email, &userID, mail.PurposeAccountDeletion, time.Now().UTC()); err != nil {
			s.logger.Error("recording failed deletion otp attempt", "user_id", in.UserID, "err", err)
			return MessageOutput{}, ErrInternal
		}
		if err := tx.Commit(ctx); err != nil {
			s.logger.Error("committing failed deletion otp attempt", "err", err)
			return MessageOutput{}, ErrInternal
		}
		return MessageOutput{}, fmt.Errorf("%w: invalid or expired OTP", ErrUnauthorized)
	}

	if err := s.repo.MarkOTPUsed(ctx, tx, otpRow.ID); err != nil {
		s.logger.Error("marking deletion otp used", "err", err)
		return MessageOutput{}, ErrInternal
	}
	if err := s.repo.DeleteUser(ctx, tx, in.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return MessageOutput{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
		}
		s.logger.Error("deleting user account", "user_id", in.UserID, "err", err)
		return MessageOutput{}, ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("committing account deletion", "err", err)
		return MessageOutput{}, ErrInternal
	}

	return MessageOutput{Message: "Account deleted"}, nil
}

func normalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

type retryAfterError struct {
	err        error
	retryAfter time.Duration
}

func (e retryAfterError) Error() string { return e.err.Error() }
func (e retryAfterError) Unwrap() error { return e.err }
func (e retryAfterError) RetryAfter() time.Duration {
	return e.retryAfter
}

func RetryAfter(err error) (time.Duration, bool) {
	var withRetryAfter interface {
		RetryAfter() time.Duration
	}
	if !errors.As(err, &withRetryAfter) {
		return 0, false
	}
	return withRetryAfter.RetryAfter(), true
}

func (s *AuthService) newOTPAttemptsRateLimitedError(ctx context.Context, db repository.DBTX, email, purpose string, since time.Time) error {
	retryAfter := auth.OTPExpiry
	oldestAttemptAt, err := s.repo.GetOldestFailedOTPAttemptSinceForUpdate(ctx, db, email, purpose, since)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		s.logger.Error("querying oldest failed otp attempt for retry-after", "purpose", purpose, "err", err)
		return ErrInternal
	}
	if err == nil {
		retryAfter = time.Until(oldestAttemptAt.Add(auth.OTPExpiry))
	}
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	return retryAfterError{
		err:        fmt.Errorf("%w: too many verification attempts", ErrRateLimited),
		retryAfter: retryAfter,
	}
}

func (s *AuthService) ensureUserForEmail(ctx context.Context, tx repository.DBTX, email string) (repository.UserRow, error) {
	user, err := s.repo.GetUserByEmailForUpdate(ctx, tx, email)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		s.logger.Error("querying user by email for verify", "email", redact.Email(email), "err", err)
		return repository.UserRow{}, ErrInternal
	}

	userID, err := s.insertUserWithRetries(ctx, tx, email)
	if err != nil {
		return repository.UserRow{}, err
	}

	user, err = s.repo.GetUserByID(ctx, tx, userID)
	if err != nil {
		s.logger.Error("querying new user profile", "user_id", userID, "err", err)
		return repository.UserRow{}, ErrInternal
	}
	return user, nil
}

func (s *AuthService) insertUserWithRetries(ctx context.Context, tx repository.DBTX, email string) (string, error) {
	txWithSavepoint, ok := tx.(repository.Tx)
	if !ok {
		s.logger.Error("user insert requires transaction with savepoints")
		return "", ErrInternal
	}

	const maxUsernameRetries = 3
	for attempt := 0; attempt < maxUsernameRetries; attempt++ {
		username, err := generateUsername()
		if err != nil {
			s.logger.Error("generating username", "err", err)
			return "", ErrInternal
		}

		sp, err := txWithSavepoint.Begin(ctx)
		if err != nil {
			s.logger.Error("creating savepoint for user insert", "err", err)
			return "", ErrInternal
		}

		userID, insertErr := s.repo.InsertUser(ctx, sp, email, username)
		if insertErr == nil {
			if err := sp.Commit(ctx); err != nil {
				s.logger.Error("releasing savepoint after user insert", "err", err)
				return "", ErrInternal
			}
			return userID, nil
		}

		if err := sp.Rollback(ctx); err != nil {
			s.logger.Error("rolling back savepoint after user insert failure", "err", err)
			return "", ErrInternal
		}
		if isUniqueViolation(insertErr, "users_username_key") || isUniqueViolation(insertErr, "idx_users_username") {
			continue
		}
		if isUniqueViolation(insertErr, "idx_users_email") {
			user, err := s.repo.GetUserByEmailForUpdate(ctx, tx, email)
			if err != nil {
				s.logger.Error("reloading user after concurrent email insert", "email", redact.Email(email), "err", err)
				return "", ErrInternal
			}
			return user.ID, nil
		}
		s.logger.Error("inserting user", "err", insertErr)
		return "", ErrInternal
	}
	s.logger.Error("failed to generate unique username after retries")
	return "", ErrInternal
}

func (s *AuthService) issueAuthOutput(ctx context.Context, db repository.DBTX, user repository.UserRow, subscription *EntitlementInfo) (AuthOutput, error) {
	accessToken, err := auth.IssueAccessToken(user.ID, user.Email, s.jwtSecret, s.jwtAccessTokenTTL)
	if err != nil {
		s.logger.Error("issuing access token", "err", err)
		return AuthOutput{}, ErrInternal
	}

	rawRefresh, hashRefresh, err := auth.GenerateRefreshToken()
	if err != nil {
		s.logger.Error("generating refresh token", "err", err)
		return AuthOutput{}, ErrInternal
	}

	refreshExpiresAt := time.Now().UTC().Add(s.jwtRefreshTokenTTL)
	if err := s.repo.InsertRefreshToken(ctx, db, user.ID, hashRefresh, refreshExpiresAt); err != nil {
		s.logger.Error("inserting refresh token", "err", err)
		return AuthOutput{}, ErrInternal
	}

	return AuthOutput{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		User:         userProfileFromRow(user, subscription),
	}, nil
}

func userProfileFromRow(user repository.UserRow, subscription *EntitlementInfo) UserProfile {
	return UserProfile{
		ID:                 user.ID,
		Email:              user.Email,
		Name:               user.Name,
		Username:           user.Username,
		ProfilePictureURL:  user.ProfilePictureURL,
		LeaderboardVisible: user.LeaderboardVisible,
		CreatedAt:          user.CreatedAt,
		Subscription:       subscription,
	}
}

func (s *AuthService) cleanupFailedOTP(otpID string) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()

	cleanupTx, err := s.pool.Begin(cleanupCtx)
	if err != nil {
		s.logger.Error("beginning otp cleanup transaction", "err", err)
		return
	}

	if err := s.repo.DeleteOTPByID(cleanupCtx, cleanupTx, otpID); err != nil {
		s.logger.Error("cleaning up otp after email failure", "err", err)
		if rbErr := cleanupTx.Rollback(cleanupCtx); rbErr != nil {
			s.logger.Error("rolling back otp cleanup transaction", "err", rbErr)
		}
		return
	}

	if err := cleanupTx.Commit(cleanupCtx); err != nil {
		s.logger.Error("committing otp cleanup transaction", "err", err)
	}
}

func generateUsername() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random username: %w", err)
	}
	return "user_" + hex.EncodeToString(b), nil
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	if pgErr.Code != pgerrcode.UniqueViolation {
		return false
	}
	if constraint != "" && pgErr.ConstraintName != constraint {
		return false
	}
	return true
}

func (s *AuthService) lookupEntitlementSnapshot(ctx context.Context, userID string) (*EntitlementInfo, error) {
	row, err := s.repo.GetEntitlementSnapshot(ctx, s.pool, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &EntitlementInfo{
		Entitlement:      row.Entitlement,
		IsActive:         row.IsActive,
		CurrentPeriodEnd: row.CurrentPeriodEnd,
	}, nil
}
