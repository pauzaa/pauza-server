package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/IsorilovA/pauza-server/internal/service"
)

// SubscriptionServicer defines the behavior the subscription handler needs
// from the service layer.
type SubscriptionServicer interface {
	ListPlans(ctx context.Context) (service.ListPlansOutput, error)
}

// Compile-time check: *service.SubscriptionService satisfies SubscriptionServicer.
var _ SubscriptionServicer = (*service.SubscriptionService)(nil)

// SubscriptionHandler handles subscription-related HTTP requests.
type SubscriptionHandler struct {
	svc    SubscriptionServicer
	logger *slog.Logger
}

// NewSubscriptionHandler creates a SubscriptionHandler with the given dependencies.
func NewSubscriptionHandler(svc SubscriptionServicer, logger *slog.Logger) *SubscriptionHandler {
	return &SubscriptionHandler{
		svc:    svc,
		logger: logger,
	}
}

type listPlansResponse struct {
	Plans []planResponse `json:"plans"`
}

type planResponse struct {
	ID                     string                `json:"id"`
	Name                   string                `json:"name"`
	DurationType           string                `json:"duration_type"`
	PriceCents             int                   `json:"price_cents"`
	Currency               string                `json:"currency"`
	StudentDiscountPercent int                   `json:"student_discount_percent"`
	Features               map[string]any        `json:"features"`
	ActiveDiscount         *planDiscountResponse `json:"active_discount"`
}

type planDiscountResponse struct {
	DiscountPercent int    `json:"discount_percent"`
	EndsAt          string `json:"ends_at"`
	Description     string `json:"description"`
}

// ListPlans handles GET /api/v1/subscriptions/plans.
func (h *SubscriptionHandler) ListPlans(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ListPlans(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}

	resp := listPlansResponse{
		Plans: make([]planResponse, 0, len(out.Plans)),
	}
	for _, plan := range out.Plans {
		item := planResponse{
			ID:                     plan.ID,
			Name:                   plan.Name,
			DurationType:           plan.DurationType,
			PriceCents:             plan.PriceCents,
			Currency:               plan.Currency,
			StudentDiscountPercent: plan.StudentDiscountPercent,
			Features:               plan.Features,
		}
		if item.Features == nil {
			item.Features = make(map[string]any)
		}
		if plan.ActiveDiscount != nil {
			item.ActiveDiscount = &planDiscountResponse{
				DiscountPercent: plan.ActiveDiscount.DiscountPercent,
				EndsAt:          plan.ActiveDiscount.EndsAt.UTC().Format(time.RFC3339),
				Description:     plan.ActiveDiscount.Description,
			}
		}
		resp.Plans = append(resp.Plans, item)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("encoding list plans response", "err", err)
	}
}
