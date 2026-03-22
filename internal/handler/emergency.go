package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/IsorilovA/pauza-server/internal/service"
)

type EmergencyStopServiceInterface interface {
	UseEmergencyStop(ctx context.Context, userID string) (service.EmergencyStopOutput, error)
	GetRemainingStops(ctx context.Context, userID string) (service.EmergencyStopOutput, error)
}

type EmergencyHandler struct {
	svc    EmergencyStopServiceInterface
	logger *slog.Logger
}

func NewEmergencyHandler(svc EmergencyStopServiceInterface, logger *slog.Logger) *EmergencyHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &EmergencyHandler{svc: svc, logger: logger}
}

func (h *EmergencyHandler) UseEmergencyStop(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	out, err := h.svc.UseEmergencyStop(r.Context(), userID)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, out, "emergency-stop")
}

func (h *EmergencyHandler) GetRemainingStops(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	out, err := h.svc.GetRemainingStops(r.Context(), userID)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, out, "emergency-stops-remaining")
}
