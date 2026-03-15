package service

import (
	"context"
	"errors"
	"time"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

// Refresh rotates a refresh token and issues a new access token pair.
func (s *AuthService) Refresh(ctx context.Context, in RefreshInput) (RefreshOutput, error) {
	tokenHash := auth.HashRefreshToken(in.RefreshToken)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning refresh transaction", "err", err)
		return RefreshOutput{}, ErrInternal
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tok, err := s.refreshTokens.GetRefreshTokenByHashForUpdate(ctx, tx, tokenHash)
	if errors.Is(err, repository.ErrNotFound) {
		return RefreshOutput{}, UnauthorizedError("Invalid refresh token")
	}
	if err != nil {
		s.logger.Error("querying refresh token", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	if tok.Revoked {
		// Reuse detection: revoke the entire session
		if err := s.sessions.RevokeSession(ctx, tx, tok.SessionID); err != nil {
			s.logger.Error("revoking session after reuse", "err", err)
			return RefreshOutput{}, ErrInternal
		}
		if err := tx.Commit(ctx); err != nil {
			s.logger.Error("committing reuse-detection revoke", "err", err)
			return RefreshOutput{}, ErrInternal
		}
		return RefreshOutput{}, UnauthorizedError("Invalid refresh token")
	}

	if time.Now().UTC().After(tok.ExpiresAt) {
		return RefreshOutput{}, UnauthorizedError("Invalid refresh token")
	}

	// Validate the session is still active
	sess, err := s.sessions.GetSessionByID(ctx, tx, tok.SessionID)
	if errors.Is(err, repository.ErrNotFound) {
		return RefreshOutput{}, UnauthorizedError("Invalid refresh token")
	}
	if err != nil {
		s.logger.Error("querying session for refresh", "err", err)
		return RefreshOutput{}, ErrInternal
	}
	if sess.Revoked || time.Now().UTC().After(sess.ExpiresAt) {
		return RefreshOutput{}, UnauthorizedError("Invalid refresh token")
	}

	if err := s.refreshTokens.RevokeRefreshToken(ctx, tx, tok.ID); err != nil {
		s.logger.Error("revoking current refresh token", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	email, err := s.refreshTokens.GetUserEmailByID(ctx, tx, tok.UserID)
	if err != nil {
		s.logger.Error("querying user email for refresh", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	accessToken, err := auth.IssueAccessToken(tok.UserID, email, tok.SessionID, s.jwtSecret, s.jwtAccessTokenTTL)
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
	if err := s.refreshTokens.InsertRefreshToken(ctx, tx, tok.UserID, tok.SessionID, hashRefresh, refreshExpiresAt); err != nil {
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
