package service

import (
	"context"
	"errors"
	"time"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

func (s *AuthService) RequestAccountDeletion(ctx context.Context, in DeleteAccountRequestInput) (MessageOutput, error) {
	user, err := s.repo.GetUserByID(ctx, s.pool, in.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return MessageOutput{}, UnauthorizedError("Missing or invalid authentication")
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
			return MessageOutput{}, UnauthorizedError("Missing or invalid authentication")
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
		return MessageOutput{}, UnauthorizedError("Missing or invalid authentication")
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
		return MessageOutput{}, UnauthorizedError("Invalid or expired OTP")
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
		return MessageOutput{}, UnauthorizedError("Invalid or expired OTP")
	}

	if err := s.repo.MarkOTPUsed(ctx, tx, otpRow.ID); err != nil {
		s.logger.Error("marking deletion otp used", "err", err)
		return MessageOutput{}, ErrInternal
	}
	if err := s.repo.DeleteUser(ctx, tx, in.UserID); err != nil {
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
