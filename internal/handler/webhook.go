package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/revenuecat"
)

// WebhookServicer is the subset of service.WebhookService the handler depends on.
type WebhookServicer interface {
	HandleWebhook(ctx context.Context, event revenuecat.WebhookEvent) error
}

// WebhookHandler handles incoming RevenueCat webhook requests.
type WebhookHandler struct {
	svc           WebhookServicer
	webhookSecret string
	logger        *slog.Logger
}

// NewWebhookHandler creates a WebhookHandler with the given service, webhook
// secret, and logger.
func NewWebhookHandler(svc WebhookServicer, webhookSecret string, logger *slog.Logger) *WebhookHandler {
	return &WebhookHandler{
		svc:           svc,
		webhookSecret: webhookSecret,
		logger:        logger,
	}
}

// HandleRevenueCat handles POST requests from RevenueCat webhooks. It
// authenticates the request using a Bearer token, decodes the webhook payload,
// and delegates processing to the service layer.
//
// Successfully processed webhooks return 200. Transient service failures return
// 500 so that RevenueCat retries the delivery.
func (h *WebhookHandler) HandleRevenueCat(w http.ResponseWriter, r *http.Request) {
	// Authenticate: require a valid Bearer token matching the webhook secret.
	if !h.authenticateWebhook(w, r) {
		return
	}

	// Decode the JSON payload. Unknown fields are tolerated because
	// RevenueCat may add new fields at any time.
	var payload revenuecat.WebhookPayload
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&payload); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			apperror.ValidationError(w, "Request body too large", nil)
			return
		}
		apperror.ValidationError(w, "Invalid request body", nil)
		return
	}

	// Reject trailing JSON documents or non-whitespace after the first object.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		apperror.ValidationError(w, "Invalid request body", nil)
		return
	}

	// Delegate to the service layer. Transient failures surface as 500 so
	// RevenueCat retries the webhook delivery.
	if err := h.svc.HandleWebhook(r.Context(), payload.Event); err != nil {
		h.logger.Error("webhook service error",
			"event_id", payload.Event.ID,
			"event_type", payload.Event.Type,
			"err", err,
		)
		apperror.InternalError(w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(struct{}{}); err != nil {
		h.logger.Error("encoding webhook response", "err", err)
	}
}

// authenticateWebhook checks the Authorization header for a Bearer token that
// matches the configured webhook secret using constant-time comparison. Returns
// true if authentication succeeds. On failure it writes an error response and
// returns false.
func (h *WebhookHandler) authenticateWebhook(w http.ResponseWriter, r *http.Request) bool {
	authHeader := r.Header.Get("Authorization")
	token, found := strings.CutPrefix(authHeader, "Bearer ")
	if !found || token == "" {
		apperror.Unauthorized(w, "Missing or invalid authentication")
		return false
	}

	if subtle.ConstantTimeCompare([]byte(token), []byte(h.webhookSecret)) != 1 {
		apperror.Unauthorized(w, "Missing or invalid authentication")
		return false
	}

	return true
}
