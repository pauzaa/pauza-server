package service

import "context"

// Logout revokes the current session, ending the user's active session.
func (s *AuthService) Logout(ctx context.Context, in LogoutInput) error {
	if err := s.sessions.RevokeSession(ctx, s.pool, in.SessionID); err != nil {
		s.logger.Error("revoking session on logout", "err", err)
		return ErrInternal
	}
	return nil
}
