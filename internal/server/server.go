package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/handler"
	"github.com/IsorilovA/pauza-server/internal/mail"
	authmw "github.com/IsorilovA/pauza-server/internal/middleware"
	"github.com/IsorilovA/pauza-server/internal/ratelimit"
)

// respondRequestID copies the request ID from the context to the X-Request-Id
// response header so that callers can correlate responses with log entries.
func respondRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reqID := middleware.GetReqID(r.Context()); reqID != "" {
			w.Header().Set(middleware.RequestIDHeader, reqID)
		}
		next.ServeHTTP(w, r)
	})
}

// maxBodySize is the defense-in-depth limit for request bodies (1 MiB).
const maxBodySize = 1 << 20

// limitBody returns a middleware that caps request bodies to maxBodySize bytes.
// Handlers that read past the limit will receive an io error from the reader.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
		next.ServeHTTP(w, r)
	})
}

// requestLogger returns a middleware that logs every HTTP request using the
// provided slog.Logger with structured fields: method, path, status, duration,
// bytes written, request_id, and remote_addr.
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			status := ww.Status()
			// chi's WrapResponseWriter returns 0 when the handler never
			// calls WriteHeader explicitly; the net/http default is 200.
			if status == 0 {
				status = http.StatusOK
			}
			level := slog.LevelInfo
			if status >= 500 {
				level = slog.LevelError
			} else if status >= 400 {
				level = slog.LevelWarn
			}

			logger.LogAttrs(r.Context(), level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int("bytes", ww.BytesWritten()),
				slog.Duration("duration", time.Since(start)),
				slog.String("request_id", middleware.GetReqID(r.Context())),
				slog.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}

// authRateLimit is the maximum number of requests per minute allowed from a
// single IP to public /api/v1/auth endpoints.
const authRateLimit = 5

// authRateWindow is the fixed-window duration for the auth rate limiter.
const authRateWindow = time.Minute

// verifyOTPRateLimit is the maximum number of requests per minute allowed
// for /api/v1/auth/verify-otp per email address (BACKEND_SPEC §10).
const verifyOTPRateLimit = 3

// verifyOTPRateWindow is the fixed-window duration for the verify-otp
// per-email rate limiter.
const verifyOTPRateWindow = time.Minute

// New creates and configures the HTTP server with all routes and middleware.
// The returned cleanup function stops background goroutines (e.g. rate-limiter
// eviction loops) and must be called during graceful shutdown.
func New(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool) (*http.Server, func()) {
	r := chi.NewRouter()

	// Base middleware stack.
	r.Use(middleware.RequestID)  // generate/forward X-Request-Id in context
	r.Use(respondRequestID)      // echo request ID back in response header
	r.Use(middleware.RealIP)     // extract real client IP from proxy headers
	r.Use(requestLogger(logger)) // log each request with structured fields
	r.Use(middleware.Recoverer)  // recover from panics, return 500
	r.Use(limitBody)             // defense-in-depth: cap request bodies

	// Health check (not under /api/v1; used by container orchestration probes).
	r.Get("/health", handler.Health(pool))

	// Construct shared dependencies for handler wiring.
	emailSender := mail.NewSMTPSender(
		cfg.SMTPHost, cfg.SMTPPort,
		cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPFrom,
		int(auth.OTPExpiry.Minutes()),
		logger,
	)
	authHandler := handler.NewAuthHandler(
		pool, emailSender, cfg.JWTSecret,
		cfg.JWTAccessTokenTTL, cfg.JWTRefreshTokenTTL, logger,
	)

	// Per-IP rate limiter for public auth endpoints. The limiter is shared
	// across all auth routes so that an attacker cannot bypass the budget
	// by rotating across different endpoints.
	authLimiter := ratelimit.New(authRateLimit, authRateWindow)

	// Per-email rate limiter for /auth/verify-otp (BACKEND_SPEC §10:
	// 3 requests/minute scoped by normalized email address).
	verifyOTPLimiter := ratelimit.New(verifyOTPRateLimit, verifyOTPRateWindow)

	// --- /api/v1 routes -------------------------------------------------
	// Public and protected routes are mounted in separate chi.Route groups
	// so that the JWT middleware applies only to protected endpoints. This
	// avoids relying on chi route registration order for correctness.
	r.Route("/api/v1", func(r chi.Router) {
		// Public auth routes (no JWT required).
		r.Route("/auth", func(r chi.Router) {
			r.Use(authmw.RateLimit(authLimiter, authRateLimit, authmw.IPKey))
			r.Post("/register", authHandler.Register)
			r.With(authmw.RateLimit(verifyOTPLimiter, verifyOTPRateLimit, authmw.EmailKey)).
				Post("/verify-otp", authHandler.VerifyOTP)
			r.Post("/login", authHandler.Login)
			r.Post("/refresh", authHandler.Refresh)
			r.Post("/forgot-password", authHandler.ForgotPassword)
			r.Post("/reset-password", authHandler.ResetPassword)
		})

		// Protected routes (JWT required).
		r.Group(func(r chi.Router) {
			r.Use(authmw.JWTAuth(cfg.JWTSecret))

			// Placeholder: GET /api/v1/me will return the authenticated
			// user profile once the user-profile handler is implemented.
			r.Get("/me", func(w http.ResponseWriter, _ *http.Request) {
				apperror.NotFound(w, "user profile endpoint not yet implemented")
			})
		})
	})

	cleanup := func() {
		authLimiter.Stop()
		verifyOTPLimiter.Stop()
	}

	return &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}, cleanup
}
