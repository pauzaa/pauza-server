package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/IsorilovA/pauza-server/internal/config"
	"github.com/IsorilovA/pauza-server/internal/handler"
)

// New creates and configures the HTTP server with all routes and middleware.
func New(cfg *config.Config, logger *slog.Logger) *http.Server {
	r := chi.NewRouter()

	// Base middleware
	r.Use(middleware.RequestID) // Generate/forward X-Request-Id header
	r.Use(middleware.RealIP)    // Extract real client IP from proxy headers
	r.Use(middleware.Logger)    // Log each request: method, path, status, duration
	r.Use(middleware.Recoverer) // Recover from panics, return 500

	// Health check (not under /api/v1, used for container health checks)
	r.Get("/health", handler.Health())

	return &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: r,
	}
}
