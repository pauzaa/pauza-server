package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/IsorilovA/pauza-server/internal/repository"
)

// PlanDiscount is the service-layer representation of an active plan discount.
type PlanDiscount struct {
	DiscountPercent int
	EndsAt          time.Time
	Description     string
}

// Plan is the service-layer representation of a subscription plan.
type Plan struct {
	ID                     string
	Name                   string
	DurationType           string
	PriceCents             int
	Currency               string
	StudentDiscountPercent int
	Features               map[string]any
	ActiveDiscount         *PlanDiscount
}

// ListPlansOutput holds the result of listing active subscription plans.
type ListPlansOutput struct {
	Plans []Plan
}

// SubscriptionService encapsulates subscription plan business logic.
type SubscriptionService struct {
	pool   repository.Pool
	repo   repository.SubscriptionRepository
	logger *slog.Logger
}

// NewSubscriptionService creates a SubscriptionService with the given dependencies.
func NewSubscriptionService(pool repository.Pool, repo repository.SubscriptionRepository, logger *slog.Logger) *SubscriptionService {
	return &SubscriptionService{pool: pool, repo: repo, logger: logger}
}

// ListPlans returns all active subscription plans.
func (s *SubscriptionService) ListPlans(ctx context.Context) (ListPlansOutput, error) {
	rows, err := s.repo.ListActivePlans(ctx, s.pool)
	if err != nil {
		s.logger.Error("listing active plans", "err", err)
		return ListPlansOutput{}, ErrInternal
	}

	out := ListPlansOutput{Plans: make([]Plan, 0, len(rows))}
	for _, row := range rows {
		plan := Plan{
			ID:                     row.ID,
			Name:                   row.Name,
			DurationType:           row.DurationType,
			PriceCents:             row.PriceCents,
			Currency:               row.Currency,
			StudentDiscountPercent: row.StudentDiscountPercent,
			Features:               make(map[string]any),
		}

		if len(row.FeaturesJSON) > 0 {
			if err := json.Unmarshal(row.FeaturesJSON, &plan.Features); err != nil {
				s.logger.Warn("unmarshalling plan features", "plan_id", row.ID, "err", err)
				plan.Features = make(map[string]any)
			}
		}

		if plan.Features == nil {
			plan.Features = make(map[string]any)
		}

		if row.DiscountPercent != nil && row.DiscountEndsAt != nil && row.DiscountDescription != nil {
			plan.ActiveDiscount = &PlanDiscount{
				DiscountPercent: *row.DiscountPercent,
				EndsAt:          *row.DiscountEndsAt,
				Description:     *row.DiscountDescription,
			}
		}

		out.Plans = append(out.Plans, plan)
	}

	return out, nil
}
