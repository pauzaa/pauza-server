package handler

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// healthResponse represents the JSON response from the liveness and readiness
// endpoints.
type healthResponse struct {
	Status    healthStatus `json:"status"`
	Timestamp string       `json:"timestamp"`
}

type healthStatus string

const (
	healthStatusOK       healthStatus = "ok"
	healthStatusDegraded healthStatus = "degraded"
)

// Live returns an HTTP handler for the GET /live endpoint.
// It always responds with 200 and status "ok"; no external dependencies are
// checked. Container orchestrators use this to know the process is running.
func Live(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		resp := healthResponse{
			Status:    healthStatusOK,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		writeJSON(w, logger, http.StatusOK, resp, "live")
	}
}

// Ready returns an HTTP handler for the GET /ready endpoint.
// It pings the database via pool to verify connectivity.
// Returns 200 with status "ok" when the DB is reachable, or
// 503 with status "degraded" when the pool is nil or the ping fails.
func Ready(pool *pgxpool.Pool, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := healthStatusOK
		code := http.StatusOK

		if pool == nil {
			status = healthStatusDegraded
			code = http.StatusServiceUnavailable
		} else if err := pool.Ping(r.Context()); err != nil {
			logger.Warn("readiness check database ping failed", "err", err)
			status = healthStatusDegraded
			code = http.StatusServiceUnavailable
		}

		resp := healthResponse{
			Status:    status,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		writeJSON(w, logger, code, resp, "ready")
	}
}
