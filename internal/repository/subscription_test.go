package repository

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestListActivePlans_ReturnsMappedPlans(t *testing.T) {
	t.Parallel()

	discountPercent := 25
	discountEndsAt := time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC)
	discountDescription := "Spring sale"
	var issuedSQL string

	rows := &fakePlanRows{
		data: [][]any{
			{
				"plan-monthly",
				"Monthly",
				"monthly",
				999,
				"USD",
				15,
				[]byte(`{"focus_sessions":true}`),
				&discountPercent,
				&discountEndsAt,
				&discountDescription,
			},
			{
				"plan-yearly",
				"Yearly",
				"yearly",
				9999,
				"USD",
				0,
				[]byte(`{"focus_sessions":true,"coach":false}`),
				nil,
				nil,
				nil,
			},
		},
	}

	db := fakeSubscriptionDBTX{
		queryFn: func(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
			issuedSQL = sql
			return rows, nil
		},
	}

	repo := NewPgxSubscriptionRepository()
	plans, err := repo.ListActivePlans(context.Background(), db)
	if err != nil {
		t.Fatalf("ListActivePlans() error = %v", err)
	}

	want := []PlanRow{
		{
			ID:                     "plan-monthly",
			Name:                   "Monthly",
			DurationType:           "monthly",
			PriceCents:             999,
			Currency:               "USD",
			StudentDiscountPercent: 15,
			FeaturesJSON:           []byte(`{"focus_sessions":true}`),
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
			StudentDiscountPercent: 0,
			FeaturesJSON:           []byte(`{"focus_sessions":true,"coach":false}`),
		},
	}

	if !reflect.DeepEqual(plans, want) {
		t.Fatalf("ListActivePlans() = %#v, want %#v", plans, want)
	}

	if !rows.exhausted {
		t.Fatal("expected rows to reach EOF")
	}

	if !rows.closed {
		t.Fatal("expected rows to be closed")
	}

	for _, want := range []string{
		"LEFT JOIN LATERAL",
		"WHERE plan_id = sp.id",
		"AND starts_at <= now()",
		"AND ends_at > now()",
		"ORDER BY created_at DESC, id DESC",
		"WHERE sp.is_active = true",
		"ORDER BY sp.created_at ASC, sp.id ASC",
	} {
		if !strings.Contains(issuedSQL, want) {
			t.Fatalf("ListActivePlans() query = %q, want substring %q", issuedSQL, want)
		}
	}
}

func TestListActivePlans_QueryError(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	db := fakeSubscriptionDBTX{
		queryFn: func(context.Context, string, ...any) (pgx.Rows, error) {
			return nil, boom
		},
	}

	_, err := NewPgxSubscriptionRepository().ListActivePlans(context.Background(), db)
	if !errors.Is(err, boom) {
		t.Fatalf("ListActivePlans() error = %v, want wrapped boom", err)
	}
	if !strings.Contains(err.Error(), "listing active plans") {
		t.Fatalf("ListActivePlans() error = %q, want context prefix", err)
	}
}

func TestListActivePlans_ScanError(t *testing.T) {
	t.Parallel()

	boom := errors.New("bad row")
	rows := &fakePlanRows{
		data:    [][]any{{"plan-monthly", "Monthly", "monthly", 999, "USD", 15, []byte(`{}`), nil, nil, nil}},
		scanErr: boom,
	}
	db := fakeSubscriptionDBTX{
		queryFn: func(context.Context, string, ...any) (pgx.Rows, error) {
			return rows, nil
		},
	}

	_, err := NewPgxSubscriptionRepository().ListActivePlans(context.Background(), db)
	if !errors.Is(err, boom) {
		t.Fatalf("ListActivePlans() error = %v, want wrapped bad row", err)
	}
	if !strings.Contains(err.Error(), "scanning active plan") {
		t.Fatalf("ListActivePlans() error = %q, want scan context", err)
	}
}

func TestListActivePlans_IterationError(t *testing.T) {
	t.Parallel()

	boom := errors.New("cursor failed")
	rows := &fakePlanRows{err: boom}
	db := fakeSubscriptionDBTX{
		queryFn: func(context.Context, string, ...any) (pgx.Rows, error) {
			return rows, nil
		},
	}

	_, err := NewPgxSubscriptionRepository().ListActivePlans(context.Background(), db)
	if !errors.Is(err, boom) {
		t.Fatalf("ListActivePlans() error = %v, want wrapped cursor failed", err)
	}
	if !strings.Contains(err.Error(), "iterating active plans") {
		t.Fatalf("ListActivePlans() error = %q, want iteration context", err)
	}
}

type fakeSubscriptionDBTX struct {
	queryFn func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (f fakeSubscriptionDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("fakeSubscriptionDBTX.Exec: unexpected call")
}

func (f fakeSubscriptionDBTX) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if f.queryFn == nil {
		panic("fakeSubscriptionDBTX.Query: unexpected call")
	}
	return f.queryFn(ctx, sql, args...)
}

func (f fakeSubscriptionDBTX) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("fakeSubscriptionDBTX.QueryRow: unexpected call")
}

type fakePlanRows struct {
	data      [][]any
	idx       int
	exhausted bool
	closed    bool
	err       error
	scanErr   error
	current   []any
}

func (r *fakePlanRows) Close() {
	r.closed = true
}

func (r *fakePlanRows) Err() error {
	return r.err
}

func (r *fakePlanRows) CommandTag() pgconn.CommandTag {
	return pgconn.CommandTag{}
}

func (r *fakePlanRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}

func (r *fakePlanRows) Next() bool {
	if r.idx >= len(r.data) {
		r.exhausted = true
		return false
	}
	r.current = r.data[r.idx]
	r.idx++
	return true
}

func (r *fakePlanRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if len(dest) != len(r.current) {
		return fmt.Errorf("scan dest count = %d, want %d", len(dest), len(r.current))
	}
	for i := range dest {
		if err := assignScanValue(dest[i], r.current[i]); err != nil {
			return err
		}
	}
	return nil
}

func (r *fakePlanRows) Values() ([]any, error) {
	return r.current, nil
}

func (r *fakePlanRows) RawValues() [][]byte {
	return nil
}

func (r *fakePlanRows) Conn() *pgx.Conn {
	return nil
}

func assignScanValue(dest any, src any) error {
	dv := reflect.ValueOf(dest)
	if !dv.IsValid() || dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("destination must be a non-nil pointer")
	}

	if src == nil {
		dv.Elem().Set(reflect.Zero(dv.Elem().Type()))
		return nil
	}

	sv := reflect.ValueOf(src)
	if sv.Type().AssignableTo(dv.Elem().Type()) {
		dv.Elem().Set(sv)
		return nil
	}
	if sv.Type().ConvertibleTo(dv.Elem().Type()) {
		dv.Elem().Set(sv.Convert(dv.Elem().Type()))
		return nil
	}

	return fmt.Errorf("cannot assign %T to %T", src, dest)
}
