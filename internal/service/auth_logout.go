package service

import "context"

// Logout revokes all refresh tokens for the given user, ending every active session.
func (s *AuthService) Logout(ctx context.Context, in LogoutInput) error {
	if err := s.refreshTokens.RevokeAllRefreshTokens(ctx, s.pool, in.UserID); err != nil {
		s.logger.Error("revoking all refresh tokens on logout", "err", err)
		return ErrInternal
	}
	return nil
}
