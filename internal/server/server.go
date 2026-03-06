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
func New(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool) *http.Server {
	r := chi.NewRouter()

	// Base middleware
	r.Use(middleware.RequestID)  // Generate/forward X-Request-Id in context
	r.Use(respondRequestID)      // Echo request ID back in response header
	r.Use(middleware.RealIP)     // Extract real client IP from proxy headers
	r.Use(requestLogger(logger)) // Log each request: method, path, status, duration
	r.Use(middleware.Recoverer)  // Recover from panics, return 500

	// Health check (not under /api/v1, used for container health checks)
	r.Get("/health", handler.Health(pool))

	return &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}
