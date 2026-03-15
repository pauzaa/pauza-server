package service

import (
	"context"

	"github.com/IsorilovA/pauza-server/internal/mail"
)

// Start begins a passwordless sign-in challenge for the supplied email.
func (s *AuthService) Start(ctx context.Context, in StartAuthInput) (StartAuthOutput, error) {
	email := normalizeEmail(in.Email)
	if _, err := s.createChallenge(
		ctx,
		email,
		nil,
		mail.PurposeAuthLogin,
		"deleting existing auth otp",
		"inserting auth otp",
		"sending auth otp email",
	); err != nil {
		return StartAuthOutput{}, err
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

	challenge, needsCommit, err := s.verifyChallenge(
		ctx,
		tx,
		email,
		nil,
		mail.PurposeAuthLogin,
		in.OTP,
		"querying auth otp",
		"counting failed auth otp attempts",
		"recording failed verify-otp attempt",
	)
	if err != nil {
		if needsCommit {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				s.logger.Error("committing failed otp attempt", "err", commitErr)
			}
		}
		return AuthOutput{}, err
	}

	if err := s.otps.MarkOTPUsed(ctx, tx, challenge.OTPRow.ID); err != nil {
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

	if err := s.sessions.RevokeAllUserSessions(ctx, tx, user.ID); err != nil {
		s.logger.Error("revoking all user sessions on verify", "err", err)
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
