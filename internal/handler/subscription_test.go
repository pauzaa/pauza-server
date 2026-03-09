package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/service"
)

type stubSubscriptionService struct {
	listPlansFn func(ctx context.Context) (service.ListPlansOutput, error)
}

var _ SubscriptionServicer = (*stubSubscriptionService)(nil)

func (s *stubSubscriptionService) ListPlans(ctx context.Context) (service.ListPlansOutput, error) {
	if s.listPlansFn != nil {
		return s.listPlansFn(ctx)
	}
	return service.ListPlansOutput{}, nil
}

func TestSubscriptionHandler_ListPlans_HappyPath(t *testing.T) {
	t.Parallel()

	discountEndsAt := time.Date(2026, time.March, 10, 14, 30, 0, 0, time.FixedZone("UTC+3", 3*60*60))

	svc := &stubSubscriptionService{
		listPlansFn: func(_ context.Context) (service.ListPlansOutput, error) {
			return service.ListPlansOutput{
				Plans: []service.Plan{
					{
						ID:                     "plan-monthly",
						Name:                   "Premium Monthly",
						DurationType:           "monthly",
						PriceCents:             499,
						Currency:               "USD",
						StudentDiscountPercent: 50,
						Features: map[string]any{
							"friendships":     true,
							"advanced_stats": true,
							"support":        "priority",
						},
						ActiveDiscount: &service.PlanDiscount{
							DiscountPercent: 20,
							EndsAt:          discountEndsAt,
							Description:     "Spring Sale",
						},
					},
					{
						ID:                     "plan-yearly",
						Name:                   "Premium Yearly",
						DurationType:           "yearly",
						PriceCents:             4999,
						Currency:               "USD",
						StudentDiscountPercent: 15,
						Features: map[string]any{
							"friendships": true,
							"offline":     true,
						},
						ActiveDiscount: nil,
					},
				},
			}, nil
		},
	}

	h := NewSubscriptionHandler(svc, noopLogger())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions/plans", nil)
	rec := httptest.NewRecorder()

	h.ListPlans(rec, req)
	body := rec.Body.Bytes()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", ct, "application/json")
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("decode top-level response: %v", err)
	}

	if len(top) != 1 {
		t.Fatalf("top-level keys = %d, want 1", len(top))
	}

	plansRaw, ok := top["plans"]
	if !ok {
		t.Fatal("missing top-level key plans")
	}

	var plans []map[string]json.RawMessage
	if err := json.Unmarshal(plansRaw, &plans); err != nil {
		t.Fatalf("decode plans payload: %v", err)
	}

	if len(plans) != 2 {
		t.Fatalf("plans len = %d, want 2", len(plans))
	}

	for i, plan := range plans {
		if len(plan) != 8 {
			t.Fatalf("plan[%d] keys = %d, want 8", i, len(plan))
		}
		for _, key := range []string{"id", "name", "duration_type", "price_cents", "currency", "student_discount_percent", "features", "active_discount"} {
			if _, ok := plan[key]; !ok {
				t.Fatalf("plan[%d] missing key %q", i, key)
			}
		}
	}

	var resp listPlansResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode typed response: %v", err)
	}

	if len(resp.Plans) != 2 {
		t.Fatalf("typed plans len = %d, want 2", len(resp.Plans))
	}

	monthly := resp.Plans[0]
	if monthly.ID != "plan-monthly" || monthly.Name != "Premium Monthly" || monthly.DurationType != "monthly" || monthly.PriceCents != 499 || monthly.Currency != "USD" || monthly.StudentDiscountPercent != 50 {
		t.Fatalf("monthly core fields = %#v", monthly)
	}
	if got := monthly.Features["friendships"]; got != true {
		t.Fatalf("monthly features.friendships = %#v, want true", got)
	}
	if got := monthly.Features["advanced_stats"]; got != true {
		t.Fatalf("monthly features.advanced_stats = %#v, want true", got)
	}
	if got := monthly.Features["support"]; got != "priority" {
		t.Fatalf("monthly features.support = %#v, want priority", got)
	}
	if monthly.ActiveDiscount == nil {
		t.Fatal("monthly active_discount = nil, want value")
	}
	if monthly.ActiveDiscount.DiscountPercent != 20 {
		t.Fatalf("monthly active_discount.discount_percent = %d, want 20", monthly.ActiveDiscount.DiscountPercent)
	}
	if monthly.ActiveDiscount.Description != "Spring Sale" {
		t.Fatalf("monthly active_discount.description = %q, want %q", monthly.ActiveDiscount.Description, "Spring Sale")
	}
	if _, err := time.Parse(time.RFC3339, monthly.ActiveDiscount.EndsAt); err != nil {
		t.Fatalf("monthly active_discount.ends_at = %q, want RFC3339: %v", monthly.ActiveDiscount.EndsAt, err)
	}
	if monthly.ActiveDiscount.EndsAt != discountEndsAt.UTC().Format(time.RFC3339) {
		t.Fatalf("monthly active_discount.ends_at = %q, want %q", monthly.ActiveDiscount.EndsAt, discountEndsAt.UTC().Format(time.RFC3339))
	}

	yearly := resp.Plans[1]
	if yearly.ActiveDiscount != nil {
		t.Fatalf("yearly active_discount = %#v, want nil", yearly.ActiveDiscount)
	}
	if got := yearly.Features["offline"]; got != true {
		t.Fatalf("yearly features.offline = %#v, want true", got)
	}
	if string(plans[1]["active_discount"]) != "null" {
		t.Fatalf("yearly raw active_discount = %s, want null", string(plans[1]["active_discount"]))
	}

	var discount map[string]json.RawMessage
	if err := json.Unmarshal(plans[0]["active_discount"], &discount); err != nil {
		t.Fatalf("decode raw active_discount: %v", err)
	}
	if len(discount) != 3 {
		t.Fatalf("active_discount keys = %d, want 3", len(discount))
	}
	for _, key := range []string{"discount_percent", "ends_at", "description"} {
		if _, ok := discount[key]; !ok {
			t.Fatalf("active_discount missing key %q", key)
		}
	}
}

func TestSubscriptionHandler_ListPlans_ServiceInternalError_Returns500(t *testing.T) {
	t.Parallel()

	svc := &stubSubscriptionService{
		listPlansFn: func(_ context.Context) (service.ListPlansOutput, error) {
			return service.ListPlansOutput{}, service.ErrInternal
		},
	}

	h := NewSubscriptionHandler(svc, noopLogger())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions/plans", nil)
	rec := httptest.NewRecorder()

	h.ListPlans(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if resp.Error.Code != apperror.CodeInternalError {
		t.Fatalf("error code = %q, want %q", resp.Error.Code, apperror.CodeInternalError)
	}
}
