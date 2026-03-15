package middleware

import "context"

// AuthUser holds the authenticated user information extracted from a JWT.
type AuthUser struct {
	UserID    string
	Email     string
	SessionID string
}

// contextKey is an unexported type used for context keys in this package,
// preventing collisions with keys defined in other packages.
// The string field provides a descriptive label in debug/trace output.
type contextKey string

const authUserKey contextKey = "pauza.auth.user"

// WithUser returns a new context with the given AuthUser stored in it.
func WithUser(ctx context.Context, user AuthUser) context.Context {
	return context.WithValue(ctx, authUserKey, user)
}

// UserFromContext extracts the AuthUser from the context. The second return
// value is false if no user was stored.
func UserFromContext(ctx context.Context) (AuthUser, bool) {
	user, ok := ctx.Value(authUserKey).(AuthUser)
	return user, ok
}
