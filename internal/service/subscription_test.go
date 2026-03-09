package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/repository"
)

func newTestSubscriptionService(repo *fakeSubscriptionRepo) *SubscriptionService {
	return NewSubscriptionService(
		&fakePool{},
		repo,
		slog.New(slog.NewTextHandler(
			devNull{},
			&slog.HandlerOptions{Level: slog.LevelError},
		)),
	)
}

func TestSubscriptionService_ListPlans_HappyPath(t *testing.T) {
	t.Parallel()

	discountPercent := 25
	discountEndsAt := time.Date(2026, time.March, 31, 12, 0, 0, 0, time.UTC)
	discountDescription := "Spring sale"

	repo := &fakeSubscriptionRepo{
		listActivePlansFn: func(context.Context, repository.DBTX) ([]repository.PlanRow, error) {
			return []repository.PlanRow{
				{
					ID:                     "plan-monthly",
					Name:                   "Monthly",
					DurationType:           "monthly",
					PriceCents:             999,
					Currency:               "USD",
					StudentDiscountPercent: 15,
					FeaturesJSON:           []byte(`{"downloads":10,"offline":true}`),
					DiscountPercent:        &discountPercent,
					DiscountEndsAt:         &discountEndsAt,
					DiscountDescription:    &discountDescription,
				},
				{
					ID:                     "plan-yearly",
					Name:                   "Yearly",
					DurationType:           "yearly",
					PriceCents:             9999,
					Currency:               "USD",
					StudentDiscountPercent: 20,
					FeaturesJSON:           []byte(`{"support":"priority"}`),
				},
			}, nil
		},
	}

	svc := newTestSubscriptionService(repo)

	out, err := svc.ListPlans(context.Background())
	if err != nil {
		t.Fatalf("ListPlans() error = %v", err)
	}

	if len(out.Plans) != 2 {
		t.Fatalf("ListPlans() plans len = %d, want 2", len(out.Plans))
	}

	monthly := out.Plans[0]
	if monthly.ID != "plan-monthly" || monthly.Name != "Monthly" || monthly.DurationType != "monthly" {
		t.Fatalf("ListPlans() first plan core fields = %#v", monthly)
	}
	if monthly.Features["downloads"] != float64(10) {
		t.Fatalf("ListPlans() first plan downloads = %#v, want 10", monthly.Features["downloads"])
	}
	if monthly.Features["offline"] != true {
		t.Fatalf("ListPlans() first plan offline = %#v, want true", monthly.Features["offline"])
	}
	if monthly.ActiveDiscount == nil {
		t.Fatal("ListPlans() first plan ActiveDiscount = nil, want value")
	}
	if monthly.ActiveDiscount.DiscountPercent != discountPercent ||
		!monthly.ActiveDiscount.EndsAt.Equal(discountEndsAt) ||
		monthly.ActiveDiscount.Description != discountDescription {
		t.Fatalf("ListPlans() first plan ActiveDiscount = %#v, want populated discount", monthly.ActiveDiscount)
	}

	yearly := out.Plans[1]
	if yearly.ActiveDiscount != nil {
		t.Fatalf("ListPlans() second plan ActiveDiscount = %#v, want nil", yearly.ActiveDiscount)
	}
	if yearly.Features["support"] != "priority" {
		t.Fatalf("ListPlans() second plan support = %#v, want priority", yearly.Features["support"])
	}
}

func TestSubscriptionService_ListPlans_EmptyResultReturnsEmptySlice(t *testing.T) {
	t.Parallel()

	repo := &fakeSubscriptionRepo{
		listActivePlansFn: func(context.Context, repository.DBTX) ([]repository.PlanRow, error) {
			return []repository.PlanRow{}, nil
		},
	}

	svc := newTestSubscriptionService(repo)

	out, err := svc.ListPlans(context.Background())
	if err != nil {
		t.Fatalf("ListPlans() error = %v", err)
	}
	if out.Plans == nil {
		t.Fatal("ListPlans() plans = nil, want empty slice")
	}
	if len(out.Plans) != 0 {
		t.Fatalf("ListPlans() plans len = %d, want 0", len(out.Plans))
	}
}

func TestSubscriptionService_ListPlans_RepositoryErrorReturnsInternal(t *testing.T) {
	t.Parallel()

	repo := &fakeSubscriptionRepo{
		listActivePlansFn: func(context.Context, repository.DBTX) ([]repository.PlanRow, error) {
			return nil, errBoom
		},
	}

	svc := newTestSubscriptionService(repo)

	_, err := svc.ListPlans(context.Background())
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("ListPlans() error = %v, want ErrInternal", err)
	}
}

func TestSubscriptionService_ListPlans_FeaturesJSONNullYieldsEmptyMapWithoutWarning(t *testing.T) {
	t.Parallel()

	var logs strings.Builder

	repo := &fakeSubscriptionRepo{
		listActivePlansFn: func(context.Context, repository.DBTX) ([]repository.PlanRow, error) {
			return []repository.PlanRow{
				{
					ID:           "null-json",
					Name:         "Null JSON",
					DurationType: "monthly",
					PriceCents:   500,
					Currency:     "USD",
					FeaturesJSON: []byte(`null`),
				},
			}, nil
		},
	}

	svc := NewSubscriptionService(
		&fakePool{},
		repo,
		slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
	)

	out, err := svc.ListPlans(context.Background())
	if err != nil {
		t.Fatalf("ListPlans() error = %v, want nil", err)
	}
	if len(out.Plans) != 1 {
		t.Fatalf("ListPlans() plans len = %d, want 1", len(out.Plans))
	}

	plan := out.Plans[0]
	if plan.Features == nil {
		t.Fatalf("ListPlans() plan %q Features = nil, want empty map", plan.ID)
	}
	if len(plan.Features) != 0 {
		t.Fatalf("ListPlans() plan %q Features len = %d, want 0", plan.ID, len(plan.Features))
	}
	if logged := logs.String(); logged != "" {
		t.Fatalf("ListPlans() logs = %q, want no warning for JSON null", logged)
	}

	plan.Features["normalized"] = true
	if got := out.Plans[0].Features["normalized"]; got != true {
		t.Fatalf("ListPlans() plan %q Features map should be writable after normalization, got %#v", plan.ID, got)
	}
}

func TestSubscriptionService_ListPlans_InvalidFeaturesJSONWarnsAndYieldsEmptyMap(t *testing.T) {
	t.Parallel()

	var logs strings.Builder

	repo := &fakeSubscriptionRepo{
		listActivePlansFn: func(context.Context, repository.DBTX) ([]repository.PlanRow, error) {
			return []repository.PlanRow{
				{
					ID:           "invalid-json",
					Name:         "Invalid JSON",
					DurationType: "yearly",
					PriceCents:   5000,
					Currency:     "USD",
					FeaturesJSON: []byte(`{"broken":`),
				},
			}, nil
		},
	}

	svc := NewSubscriptionService(
		&fakePool{},
		repo,
		slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
	)

	out, err := svc.ListPlans(context.Background())
	if err != nil {
		t.Fatalf("ListPlans() error = %v, want nil", err)
	}
	if len(out.Plans) != 1 {
		t.Fatalf("ListPlans() plans len = %d, want 1", len(out.Plans))
	}

	plan := out.Plans[0]
	if plan.Features == nil {
		t.Fatalf("ListPlans() plan %q Features = nil, want empty map", plan.ID)
	}
	if len(plan.Features) != 0 {
		t.Fatalf("ListPlans() plan %q Features len = %d, want 0", plan.ID, len(plan.Features))
	}

	logged := logs.String()
	if !strings.Contains(logged, "unmarshalling plan features") {
		t.Fatalf("ListPlans() logs = %q, want warning about invalid features_json", logged)
	}
	if !strings.Contains(logged, "plan_id=invalid-json") {
		t.Fatalf("ListPlans() logs = %q, want invalid plan id in warning", logged)
	}
}
