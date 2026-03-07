package middleware

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/auth"
)

// JWTAuth returns a chi-compatible middleware that validates JWT access tokens.
// It extracts the token from the Authorization header (Bearer scheme), validates
// it using the provided secret, and stores the authenticated user in the request
// context. Requests without a valid token receive a 401 UNAUTHORIZED response.
func JWTAuth(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if header == "" {
				apperror.Unauthorized(w, "missing or invalid authentication")
				return
			}

			parts := strings.SplitN(header, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				slog.WarnContext(r.Context(), "jwt auth: malformed authorization header",
					"path", r.URL.Path)
				apperror.Unauthorized(w, "missing or invalid authentication")
				return
			}

			tokenString := parts[1]
			// Defense-in-depth: "Bearer " (trailing space) passes SplitN
			// but yields an empty token string.
			if tokenString == "" {
				slog.WarnContext(r.Context(), "jwt auth: empty bearer token",
					"path", r.URL.Path)
				apperror.Unauthorized(w, "missing or invalid authentication")
				return
			}

			claims, err := auth.ValidateAccessToken(tokenString, secret)
			if err != nil {
				slog.WarnContext(r.Context(), "jwt auth: token validation failed",
					"path", r.URL.Path, "err", err)
				apperror.Unauthorized(w, "missing or invalid authentication")
				return
			}

			user := AuthUser{
				UserID: claims.Subject,
				Email:  claims.Email,
			}

			ctx := WithUser(r.Context(), user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
