package service

import (
	"context"
	"errors"

	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

func (s *AuthService) RequestAccountDeletion(ctx context.Context, in DeleteAccountRequestInput) (MessageOutput, error) {
	user, err := s.users.GetUserByID(ctx, s.pool, in.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return MessageOutput{}, UnauthorizedError("Missing or invalid authentication")
	}
	if err != nil {
		s.logger.Error("loading user for deletion request", "err", err)
		return MessageOutput{}, ErrInternal
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning delete-request transaction", "err", err)
		return MessageOutput{}, ErrInternal
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := s.users.GetUserByIDForUpdate(ctx, tx, in.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return MessageOutput{}, UnauthorizedError("Missing or invalid authentication")
		}
		s.logger.Error("locking user for deletion request", "err", err)
		return MessageOutput{}, ErrInternal
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("committing deletion-request user lock", "err", err)
		return MessageOutput{}, ErrInternal
	}

	userID := in.UserID
	if _, err := s.createChallenge(
		ctx,
		user.Email,
		&userID,
		mail.PurposeAccountDeletion,
		"deleting existing deletion otp",
		"inserting deletion otp",
		"sending deletion otp",
	); err != nil {
		return MessageOutput{}, err
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

	user, err := s.users.GetUserByIDForUpdate(ctx, tx, in.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return MessageOutput{}, UnauthorizedError("Missing or invalid authentication")
	}
	if err != nil {
		s.logger.Error("locking user for deletion confirm", "err", err)
		return MessageOutput{}, ErrInternal
	}

	userID := in.UserID
	challenge, err := s.verifyChallenge(
		ctx,
		tx,
		user.Email,
		&userID,
		mail.PurposeAccountDeletion,
		in.OTP,
		"loading deletion otp",
		"counting failed deletion otp attempts",
		"recording failed deletion otp attempt",
	)
	if err != nil {
		return MessageOutput{}, err
	}

	if err := s.otps.MarkOTPUsed(ctx, tx, challenge.OTPRow.ID); err != nil {
		s.logger.Error("marking deletion otp used", "err", err)
		return MessageOutput{}, ErrInternal
	}
	if err := s.users.DeleteUser(ctx, tx, in.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return MessageOutput{}, UnauthorizedError("Missing or invalid authentication")
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
