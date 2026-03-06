package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// healthResponse represents the JSON response from the health endpoint.
type healthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// Health returns an HTTP handler for the GET /health endpoint.
// It pings the database via pool to verify connectivity.
// Returns 200 with status "ok" when the DB is reachable, or
// 503 with status "degraded" when the pool is nil or the ping fails.
func Health(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := "ok"
		code := http.StatusOK

		if pool == nil {
			status = "degraded"
			code = http.StatusServiceUnavailable
		} else if err := pool.Ping(r.Context()); err != nil {
			slog.Warn("health check database ping failed", "err", err)
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
			slog.Default().Error("failed to encode health response", "err", err)
		}
	}
}
