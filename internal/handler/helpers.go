package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/middleware"
)

func requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok || user.UserID == "" {
		apperror.Unauthorized(w, "Missing or invalid authentication")
		return "", false
	}
	return user.UserID, true
}

func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, body any, op string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil && logger != nil {
		logger.Error("encoding "+op+" response", "err", err)
	}
}

func writeMessageResponse(w http.ResponseWriter, logger *slog.Logger, status int, message string, op string) {
	writeJSON(w, logger, status, messageResponse{Message: message}, op)
}
