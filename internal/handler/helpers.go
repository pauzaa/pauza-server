package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/middleware"
	"github.com/IsorilovA/pauza-server/internal/service"
)

func parseUUIDParam(w http.ResponseWriter, r *http.Request, param string) (string, bool) {
	raw := chi.URLParam(r, param)
	if _, err := uuid.Parse(raw); err != nil {
		apperror.ValidationFieldErrors(w, "Invalid request", apperror.FieldErrors{
			param: param + " must be a valid UUID",
		})
		return "", false
	}
	return raw, true
}

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

func writeEmptyJSON(w http.ResponseWriter, logger *slog.Logger, status int, op string) {
	writeJSON(w, logger, status, struct{}{}, op)
}

func toUnixMsPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	ms := t.UnixMilli()
	return &ms
}

func writeServiceError(w http.ResponseWriter, err error) {
	var apiErr *service.APIError
	if errors.As(err, &apiErr) {
		if apiErr.RetryAfter > 0 {
			seconds := int(apiErr.RetryAfter / time.Second)
			if apiErr.RetryAfter%time.Second != 0 {
				seconds++
			}
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
		apperror.WriteError(w, apiErr.Code, apiErr.Message, apiErr.Details)
		return
	}

	switch {
	case errors.Is(err, service.ErrConflict):
		apperror.Conflict(w, "Conflict")
	case errors.Is(err, service.ErrNotFound):
		apperror.NotFound(w, "Not found")
	case errors.Is(err, service.ErrSubscriptionRequired):
		apperror.SubscriptionRequired(w, "Subscription required")
	case errors.Is(err, service.ErrUnauthorized):
		apperror.Unauthorized(w, "Unauthorized")
	case errors.Is(err, service.ErrRateLimited):
		apperror.RateLimited(w, "Too many requests")
	default:
		apperror.InternalError(w)
	}
}
