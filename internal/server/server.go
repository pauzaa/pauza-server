package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/mail"
	authmw "github.com/IsorilovA/pauza-server/internal/middleware"
	"github.com/IsorilovA/pauza-server/internal/push"
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

// New creates and configures the HTTP server with all routes and middleware.
// The mailer parameter allows callers to inject any mail.Sender implementation
// (e.g. a real SMTP sender in production, or an in-memory stub in tests).
// The redisClient parameter is explicit: rate limiters use Redis as the shared
// backend wrapped in a fail-open layer. Production startup requires Redis; nil
// is tolerated here only so unit tests can exercise router wiring without a
// live backend.
// The returned cleanup function stops background goroutines (e.g. rate-limiter
// eviction loops) and must be called during graceful shutdown.
func New(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool, mailer mail.Sender, pushSender push.Sender, redisClient *redis.Client) (*http.Server, func()) {
	r := chi.NewRouter()

	// Base middleware stack.
	r.Use(middleware.RequestID)                            // generate/forward X-Request-Id in context
	r.Use(respondRequestID)                                // echo request ID back in response header
	r.Use(authmw.TrustedRealIP(cfg.ParseTrustedProxies())) // extract real client IP only from trusted proxies
	r.Use(requestLogger(logger))                           // log each request with structured fields
	r.Use(authmw.Recoverer(logger))                        // recover from panics with structured logging
	r.Use(limitBody)                                       // defense-in-depth: cap request bodies

	deps := buildDependencies(cfg, logger, pool, mailer, pushSender)
	limiters, cleanup := buildLimiters(cfg, logger, redisClient)
	mountRoutes(r, cfg, logger, pool, deps, limiters)

	return newHTTPServer(cfg, r), cleanup
}
