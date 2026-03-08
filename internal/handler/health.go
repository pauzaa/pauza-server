package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// healthResponse represents the JSON response from the liveness and readiness
// endpoints.
type healthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// Live returns an HTTP handler for the GET /live endpoint.
// It always responds with 200 and status "ok"; no external dependencies are
// checked. Container orchestrators use this to know the process is running.
func Live(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		resp := healthResponse{
			Status:    "ok",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			logger.Error("failed to encode live response", "err", err)
		}
	}
}

// Ready returns an HTTP handler for the GET /ready endpoint.
// It pings the database via pool to verify connectivity.
// Returns 200 with status "ok" when the DB is reachable, or
// 503 with status "degraded" when the pool is nil or the ping fails.
func Ready(pool *pgxpool.Pool, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := "ok"
		code := http.StatusOK

		if pool == nil {
			status = "degraded"
			code = http.StatusServiceUnavailable
		} else if err := pool.Ping(r.Context()); err != nil {
			logger.Warn("readiness check database ping failed", "err", err)
			status = "degraded"
			code = http.StatusServiceUnavailable
		}

		resp := healthResponse{
			Status:    status,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			// Response might already be partially written; just log.
			logger.Error("failed to encode ready response", "err", err)
		}
	}
}
