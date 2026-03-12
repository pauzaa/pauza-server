package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/service"
	"github.com/IsorilovA/pauza-server/internal/syncmodel"
)

type SyncService interface {
	Sync(ctx context.Context, in service.SyncInput) (service.SyncOutput, error)
}

var _ SyncService = (*service.SyncService)(nil)

type SyncHandler struct {
	svc    SyncService
	logger *slog.Logger
}

func NewSyncHandler(svc SyncService) *SyncHandler {
	return &SyncHandler{svc: svc, logger: slog.Default()}
}

func NewSyncHandlerWithLogger(svc SyncService, logger *slog.Logger) *SyncHandler {
	return &SyncHandler{svc: svc, logger: logger}
}

func (h *SyncHandler) Sync(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req syncmodel.Request
	if !decodeJSONBody(w, r, &req) {
		return
	}

	tables, fields := req.ValidateAndConvert()
	if len(fields) > 0 {
		apperror.ValidationFieldErrors(w, "Invalid request body", fields)
		return
	}

	res, err := h.svc.Sync(r.Context(), service.SyncInput{UserID: userID, Tables: tables})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, res, "sync")
}
