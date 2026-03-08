package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
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

			// Always emit rate-limit informational headers.
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(res.ResetAt.Unix(), 10))

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

// IPKey returns the bare IP address from r.RemoteAddr, stripping any port
// suffix. When chi's middleware.RealIP has run earlier in the chain,
// RemoteAddr contains the real client IP. The port is stripped so that
// rate-limit keys are stable regardless of the client's ephemeral source
// port. If RemoteAddr cannot be parsed (e.g. a Unix socket path), it is
// returned verbatim as a safe fallback.
func IPKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr may already be a bare IP (no port). Use it as-is.
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

// emailBody is a minimal struct for extracting only the "email" field from a
// JSON request body without binding the full request schema.
type emailBody struct {
	Email string `json:"email"`
}

// EmailKey extracts the normalized email address from a JSON request body
// for use as a rate-limit key. The body is read, buffered, and rewound
// so the downstream handler still sees the original payload. If the body
// cannot be read or decoded, the client IP (via IPKey) is returned as a
// fallback so the request is still rate-limited.
func EmailKey(r *http.Request) string {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return IPKey(r)
	}
	// Rewind the body for the downstream handler.
	r.Body = io.NopCloser(bytes.NewReader(body))

	var parsed emailBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return IPKey(r)
	}

	email := strings.ToLower(strings.TrimSpace(parsed.Email))
	if email == "" {
		return IPKey(r)
	}
	return "email:" + email
}

func UserIDKey(r *http.Request) string {
	user, ok := UserFromContext(r.Context())
	if !ok || strings.TrimSpace(user.UserID) == "" {
		return IPKey(r)
	}
	return "user:" + user.UserID
}
