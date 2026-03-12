package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/redact"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

type authChallenge struct {
	OTPID string
	OTP   string
}

type verifiedChallenge struct {
	OTPRow repository.OTPRow
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

func (s *AuthService) newOTPAttemptsRateLimitedError(ctx context.Context, db repository.DBTX, email string, purpose mail.Purpose, since time.Time) error {
	retryAfter := auth.OTPExpiry
	oldestAttemptAt, err := s.otps.GetOldestFailedOTPAttemptSinceForUpdate(ctx, db, email, purpose, since)
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
		err:        RateLimitedError("Too many verification attempts", retryAfter),
		retryAfter: retryAfter,
	}
}

func (s *AuthService) createChallenge(ctx context.Context, email string, userID *string, purpose mail.Purpose, deleteLogMsg, insertLogMsg, sendLogMsg string) (authChallenge, error) {
	otp, err := auth.GenerateOTP()
	if err != nil {
		s.logger.Error("generating otp", "err", err)
		return authChallenge{}, ErrInternal
	}

	otpHash, err := auth.HashOTP(otp)
	if err != nil {
		s.logger.Error("hashing otp", "err", err)
		return authChallenge{}, ErrInternal
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning otp challenge transaction", "err", err)
		return authChallenge{}, ErrInternal
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := s.otps.DeleteOTPsByEmailAndPurpose(ctx, tx, email, purpose); err != nil {
		s.logger.Error(deleteLogMsg, "email", redact.Email(email), "err", err)
		return authChallenge{}, ErrInternal
	}

	otpID, err := s.otps.InsertOTP(ctx, tx, email, userID, otpHash, purpose, time.Now().UTC().Add(auth.OTPExpiry))
	if err != nil {
		s.logger.Error(insertLogMsg, "email", redact.Email(email), "err", err)
		return authChallenge{}, ErrInternal
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("committing otp challenge transaction", "err", err)
		return authChallenge{}, ErrInternal
	}

	if err := s.mailer.SendOTP(ctx, email, otp, purpose); err != nil {
		s.logger.Error(sendLogMsg, "email", redact.Email(email), "err", err)
		s.cleanupFailedOTP(otpID)
		return authChallenge{}, ErrInternal
	}

	return authChallenge{OTPID: otpID, OTP: otp}, nil
}

func (s *AuthService) verifyChallenge(ctx context.Context, tx repository.Tx, email string, userID *string, purpose mail.Purpose, providedOTP, loadLogMsg, countLogMsg, failedLogMsg string) (verifiedChallenge, error) {
	otpRow, err := s.otps.GetActiveOTPForUpdate(ctx, tx, email, purpose)
	if errors.Is(err, repository.ErrNotFound) {
		return verifiedChallenge{}, UnauthorizedError("Invalid or expired OTP")
	}
	if err != nil {
		s.logger.Error(loadLogMsg, "email", redact.Email(email), "err", err)
		return verifiedChallenge{}, ErrInternal
	}

	attemptWindowStart := time.Now().UTC().Add(-auth.OTPExpiry)
	attemptsUsed, err := s.otps.CountFailedOTPAttemptsSinceForUpdate(ctx, tx, email, purpose, attemptWindowStart)
	if err != nil {
		s.logger.Error(countLogMsg, "email", redact.Email(email), "err", err)
		return verifiedChallenge{}, ErrInternal
	}
	if attemptsUsed >= auth.MaxOTPAttempts {
		rateLimitedErr := s.newOTPAttemptsRateLimitedError(ctx, tx, email, purpose, attemptWindowStart)
		if errors.Is(rateLimitedErr, ErrInternal) {
			return verifiedChallenge{}, ErrInternal
		}
		return verifiedChallenge{}, rateLimitedErr
	}

	otpMatch, err := auth.VerifyOTP(otpRow.CodeHash, providedOTP)
	if err != nil {
		s.logger.Error("verifying otp hash", "err", err)
		return verifiedChallenge{}, ErrInternal
	}
	if !otpMatch {
		if err := s.otps.InsertFailedOTPAttempt(ctx, tx, email, userID, purpose, time.Now().UTC()); err != nil {
			s.logger.Error(failedLogMsg, "email", redact.Email(email), "err", err)
			return verifiedChallenge{}, ErrInternal
		}
		if err := tx.Commit(ctx); err != nil {
			s.logger.Error("committing failed otp attempt", "err", err)
			return verifiedChallenge{}, ErrInternal
		}
		return verifiedChallenge{}, UnauthorizedError("Invalid or expired OTP")
	}

	return verifiedChallenge{OTPRow: otpRow}, nil
}

func (s *AuthService) ensureUserForEmail(ctx context.Context, tx repository.DBTX, email string) (repository.UserRow, error) {
	user, err := s.users.GetUserByEmailForUpdate(ctx, tx, email)
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

	user, err = s.users.GetUserByID(ctx, tx, userID)
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

		userID, insertErr := s.users.InsertUser(ctx, sp, email, username)
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
			user, err := s.users.GetUserByEmailForUpdate(ctx, tx, email)
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
	if err := s.refreshTokens.InsertRefreshToken(ctx, db, user.ID, hashRefresh, refreshExpiresAt); err != nil {
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
		PushEnabled:        user.PushEnabled,
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

	if err := s.otps.DeleteOTPByID(cleanupCtx, cleanupTx, otpID); err != nil {
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
	row, err := s.entitlements.GetEntitlementSnapshot(ctx, s.pool, userID)
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
