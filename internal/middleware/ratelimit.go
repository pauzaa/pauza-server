package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/ratelimit"
)

// RateLimit returns chi-compatible middleware that enforces a per-key request
// rate limit using the provided ratelimit.Limiter. The key for each request
// is extracted by keyFunc. When a request exceeds the budget the middleware
// writes a 429 RATE_LIMITED response with standard rate-limit headers and
// does not call the next handler.
//
// Headers emitted on every response:
//   - X-RateLimit-Limit:     the maximum requests allowed per window.
//   - X-RateLimit-Remaining: requests remaining in the current window.
//   - X-RateLimit-Reset:     UTC Unix timestamp when the window resets.
//
// Additional header on 429 responses:
//   - Retry-After:           seconds until the next request may succeed.
func RateLimit(lim ratelimit.Limiter, limit int, keyFunc func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)
			res, err := lim.Allow(r.Context(), key)
			if err != nil {
				// Backend error with no fail-open wrapper; allow the
				// request but skip rate-limit headers.
				next.ServeHTTP(w, r)
				return
			}

			// Emit rate-limit informational headers only when the limiter
			// returned a real budget. Fail-open limiters use a negative
			// Remaining sentinel for degraded-but-allowed responses so we do
			// not advertise fabricated enforcement data.
			if res.Remaining >= 0 {
				w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
				w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(res.ResetAt.Unix(), 10))
			}

			if !res.Allowed {
				retryAfter := int(time.Until(res.ResetAt).Seconds()) + 1
				if retryAfter < 1 {
					retryAfter = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				apperror.RateLimited(w, "Too many requests, please try again later")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// emailBody is a minimal struct for extracting only the "email" field from a
// JSON request body without binding the full request schema.
type emailBody struct {
	Email string `json:"email"`
}

type authEmailKeyContextKey struct{}

// ExtractAuthEmailKey reads and rewinds the request body once, storing a
// normalized auth email key in context for later rate-limit key lookup.
// Invalid or missing emails are ignored so callers can fall back to IP keys.
func ExtractAuthEmailKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		var parsed emailBody
		if err := json.Unmarshal(body, &parsed); err == nil {
			email := strings.ToLower(strings.TrimSpace(parsed.Email))
			if email != "" {
				r = r.WithContext(contextWithAuthEmailKey(r.Context(), "email:"+email))
			}
		}

		next.ServeHTTP(w, r)
	})
}

func contextWithAuthEmailKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, authEmailKeyContextKey{}, key)
}

func authEmailKeyFromContext(ctx context.Context) (string, bool) {
	key, ok := ctx.Value(authEmailKeyContextKey{}).(string)
	return key, ok && key != ""
}

// AuthEmailKey returns the previously extracted auth email key. If no email
// key was extracted it falls back to the caller IP so the request is still
// throttled.
func AuthEmailKey(r *http.Request) string {
	if key, ok := authEmailKeyFromContext(r.Context()); ok {
		return key
	}
	return IPKey(r)
}

// IPKey returns the remote IP address without port when possible.
func IPKey(r *http.Request) string {
	addr := strings.TrimSpace(r.RemoteAddr)
	if addr == "" {
		return ""
	}
	if parsed, err := netip.ParseAddrPort(addr); err == nil {
		return parsed.Addr().String()
	}
	if parsed, err := netip.ParseAddr(addr); err == nil {
		return parsed.String()
	}
	return addr
}

// UserIDKey builds a per-user key from auth context, falling back to IP.
func UserIDKey(r *http.Request) string {
	user, ok := UserFromContext(r.Context())
	if !ok || strings.TrimSpace(user.UserID) == "" {
		return IPKey(r)
	}
	return "user:" + user.UserID
}
