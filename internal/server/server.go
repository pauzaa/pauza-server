package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/handler"
	"github.com/IsorilovA/pauza-server/internal/mail"
	authmw "github.com/IsorilovA/pauza-server/internal/middleware"
	"github.com/IsorilovA/pauza-server/internal/ratelimit"
	"github.com/IsorilovA/pauza-server/internal/repository"
	"github.com/IsorilovA/pauza-server/internal/service"
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

// defaultAuthRateLimit is the fallback maximum number of requests per window
// allowed from a single IP to public /api/v1/auth endpoints.
const defaultAuthRateLimit = 5

// defaultAuthRateWindow is the fallback fixed-window duration for the auth
// rate limiter.
const defaultAuthRateWindow = time.Minute

// defaultVerifyOTPRateLimit is the fallback maximum number of requests per
// window for /api/v1/auth/verify-otp per email address (BACKEND_SPEC §10).
const defaultVerifyOTPRateLimit = 3

// defaultVerifyOTPRateWindow is the fallback fixed-window duration for the
// verify-otp per-email rate limiter.
const defaultVerifyOTPRateWindow = time.Minute

// New creates and configures the HTTP server with all routes and middleware.
// The mailer parameter allows callers to inject any mail.Sender implementation
// (e.g. a real SMTP sender in production, or an in-memory stub in tests).
// The returned cleanup function stops background goroutines (e.g. rate-limiter
// eviction loops) and must be called during graceful shutdown.
func New(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool, mailer mail.Sender) (*http.Server, func()) {
	r := chi.NewRouter()

	// Base middleware stack.
	r.Use(middleware.RequestID)                            // generate/forward X-Request-Id in context
	r.Use(respondRequestID)                                // echo request ID back in response header
	r.Use(authmw.TrustedRealIP(cfg.ParseTrustedProxies())) // extract real client IP only from trusted proxies
	r.Use(requestLogger(logger))                           // log each request with structured fields
	r.Use(authmw.Recoverer(logger))                        // recover from panics with structured logging
	r.Use(limitBody)                                       // defense-in-depth: cap request bodies

	// Liveness and readiness probes (not under /api/v1; used by container
	// orchestration). /live is dependency-free; /ready pings the database.
	r.Get("/live", handler.Live(logger))
	r.Get("/ready", handler.Ready(pool, logger))

	// Construct shared dependencies for handler wiring.
	authRepo := repository.NewPgxAuthRepository()
	authService := service.NewAuthService(
		pool, authRepo, mailer, cfg.JWTSecret,
		cfg.JWTAccessTokenTTL, cfg.JWTRefreshTokenTTL, logger,
	)
	authHandler := handler.NewAuthHandler(authService, logger)

	// Resolve rate-limit values from config, falling back to defaults when
	// the config values are zero (e.g. in unit tests with a minimal config).
	authRL := cfg.AuthRateLimit
	if authRL <= 0 {
		authRL = defaultAuthRateLimit
	}
	authRW := cfg.AuthRateWindow
	if authRW <= 0 {
		authRW = defaultAuthRateWindow
	}
	votpRL := cfg.VerifyOTPRateLimit
	if votpRL <= 0 {
		votpRL = defaultVerifyOTPRateLimit
	}
	votpRW := cfg.VerifyOTPRateWindow
	if votpRW <= 0 {
		votpRW = defaultVerifyOTPRateWindow
	}

	// Per-IP rate limiter for public auth endpoints. The limiter is shared
	// across all auth routes so that an attacker cannot bypass the budget
	// by rotating across different endpoints.
	authLimiter := ratelimit.New(authRL, authRW)

	// Per-email rate limiter for /auth/verify-otp (BACKEND_SPEC §10:
	// 3 requests/minute scoped by normalized email address).
	verifyOTPLimiter := ratelimit.New(votpRL, votpRW)

	// --- /api/v1 routes -------------------------------------------------
	// Public and protected routes are mounted in separate chi.Route groups
	// so that the JWT middleware applies only to protected endpoints. This
	// avoids relying on chi route registration order for correctness.
	r.Route("/api/v1", func(r chi.Router) {
		// Public auth routes (no JWT required).
		r.Route("/auth", func(r chi.Router) {
			r.Use(authmw.RateLimit(authLimiter, authRL, authmw.IPKey))
			r.Post("/register", authHandler.Register)
			r.With(authmw.RateLimit(verifyOTPLimiter, votpRL, authmw.EmailKey)).
				Post("/verify-otp", authHandler.VerifyOTP)
			r.Post("/login", authHandler.Login)
			r.Post("/refresh", authHandler.Refresh)
			r.Post("/forgot-password", authHandler.ForgotPassword)
			r.Post("/reset-password", authHandler.ResetPassword)
		})

		// Protected routes — JWT required. All endpoints in this group
		// require a valid access token; the middleware stores the
		// authenticated user in the request context for handlers to use
		// via middleware.UserFromContext.
		r.Group(func(r chi.Router) {
			r.Use(authmw.JWTAuth(cfg.JWTSecret, logger))

			r.Get("/me", authHandler.GetMe)
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
