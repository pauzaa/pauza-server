package handler

import (
	"encoding/json"
	"net/http"
	"time"
)

// healthResponse represents the JSON response from the health endpoint.
type healthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// Health returns an HTTP handler for the GET /health endpoint.
// In Phase 1 this always returns 200 with status "ok".
// Phase 2 will add a database connectivity check (503 if DB unreachable).
func Health() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := healthResponse{
			Status:    "ok",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}
}
