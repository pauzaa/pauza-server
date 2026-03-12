package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/redact"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

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
