package repository

import (
	"context"
	"fmt"
	"time"
)

// PlanRow holds the columns returned by subscription plan lookups.
type PlanRow struct {
	ID                     string
	Name                   string
	DurationType           string
	PriceCents             int
	Currency               string
	StudentDiscountPercent int
	FeaturesJSON           []byte
	DiscountPercent        *int
	DiscountEndsAt         *time.Time
	DiscountDescription    *string
}

// SubscriptionRepository defines the data-access operations used by
// subscription services.
type SubscriptionRepository interface {
	ListActivePlans(ctx context.Context, db DBTX) ([]PlanRow, error)
}

// PgxSubscriptionRepository implements SubscriptionRepository using pgx queries.
type PgxSubscriptionRepository struct{}

// NewPgxSubscriptionRepository returns a PgxSubscriptionRepository.
func NewPgxSubscriptionRepository() *PgxSubscriptionRepository {
	return &PgxSubscriptionRepository{}
}

// Compile-time check: PgxSubscriptionRepository satisfies SubscriptionRepository.
var _ SubscriptionRepository = (*PgxSubscriptionRepository)(nil)

func (r *PgxSubscriptionRepository) ListActivePlans(ctx context.Context, db DBTX) ([]PlanRow, error) {
	rows, err := db.Query(ctx,
		`SELECT sp.id, sp.name, sp.duration_type, sp.price_cents, sp.currency, sp.student_discount_percent, sp.features_json, spd.discount_percent, spd.ends_at, spd.description
		 FROM subscription_plans sp
		 LEFT JOIN LATERAL (
		 	SELECT discount_percent, ends_at, description
		 	FROM subscription_plan_discounts
		 	WHERE plan_id = sp.id
		 	  AND starts_at <= now()
		 	  AND ends_at > now()
		 	ORDER BY created_at DESC, id DESC
		 	LIMIT 1
		 ) spd ON true
		 WHERE sp.is_active = true
		 ORDER BY sp.created_at ASC, sp.id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing active plans: %w", err)
	}
	defer rows.Close()

	var plans []PlanRow
	for rows.Next() {
		var plan PlanRow
		if err := rows.Scan(
			&plan.ID,
			&plan.Name,
			&plan.DurationType,
			&plan.PriceCents,
			&plan.Currency,
			&plan.StudentDiscountPercent,
			&plan.FeaturesJSON,
			&plan.DiscountPercent,
			&plan.DiscountEndsAt,
			&plan.DiscountDescription,
		); err != nil {
			return nil, fmt.Errorf("scanning active plan: %w", err)
		}
		plans = append(plans, plan)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating active plans: %w", err)
	}

	return plans, nil
}
