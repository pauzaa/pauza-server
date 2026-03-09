//go:build integration

package repository

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/database"
	"github.com/IsorilovA/pauza-server/migrations"
)

// Integration tests in this package reset the shared test database, so they
// must not call t.Parallel().

func TestIntegrationListActivePlans_UsesPostgresTieBreakers(t *testing.T) {
	dbURL := testDatabaseURL(t)
	pool := testPool(t, dbURL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	createdAt := time.Date(2026, time.January, 15, 9, 0, 0, 0, time.UTC)
	startsAt := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Second)
	endsAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)

	const (
		planAID             = "11111111-1111-1111-1111-111111111111"
		planBID             = "22222222-2222-2222-2222-222222222222"
		discountLowerID     = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
		discountChosenID    = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
		chosenDescription   = "Chosen discount"
		rejectedDescription = "Earlier discount by id"
	)

	_, err := pool.Exec(ctx, `
		INSERT INTO subscription_plans (id, name, duration_type, price_cents, currency, features_json, is_active, student_discount_percent, created_at, updated_at)
		VALUES
			($1, 'Alpha Monthly', 'monthly', 499, 'USD', '{"friendships":true}', true, 10, $3, $3),
			($2, 'Beta Yearly', 'yearly', 4999, 'USD', '{"offline":true}', true, 15, $3, $3)
	`, planAID, planBID, createdAt)
	if err != nil {
		t.Fatalf("inserting subscription plans: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO subscription_plan_discounts (id, plan_id, discount_percent, starts_at, ends_at, description, created_at)
		VALUES
			($1, $3, 15, $4, $5, $6, $7),
			($2, $3, 25, $4, $5, $8, $7)
	`, discountLowerID, discountChosenID, planAID, startsAt, endsAt, rejectedDescription, createdAt, chosenDescription)
	if err != nil {
		t.Fatalf("inserting overlapping plan discounts: %v", err)
	}

	plans, err := NewPgxSubscriptionRepository().ListActivePlans(ctx, pool)
	if err != nil {
		t.Fatalf("ListActivePlans() error = %v", err)
	}

	if len(plans) != 2 {
		t.Fatalf("ListActivePlans() len = %d, want 2", len(plans))
	}

	if plans[0].ID != planAID || plans[1].ID != planBID {
		t.Fatalf("ListActivePlans() order = [%q %q], want [%q %q]", plans[0].ID, plans[1].ID, planAID, planBID)
	}

	if plans[0].DiscountPercent == nil || *plans[0].DiscountPercent != 25 {
		t.Fatalf("plans[0].DiscountPercent = %#v, want 25", plans[0].DiscountPercent)
	}
	if plans[0].DiscountDescription == nil || *plans[0].DiscountDescription != chosenDescription {
		t.Fatalf("plans[0].DiscountDescription = %#v, want %q", plans[0].DiscountDescription, chosenDescription)
	}
	if plans[0].DiscountEndsAt == nil || !plans[0].DiscountEndsAt.Equal(endsAt) {
		t.Fatalf("plans[0].DiscountEndsAt = %#v, want %v", plans[0].DiscountEndsAt, endsAt)
	}

	if plans[1].DiscountPercent != nil || plans[1].DiscountEndsAt != nil || plans[1].DiscountDescription != nil {
		t.Fatalf("plans[1] discount = (%#v, %#v, %#v), want all nil", plans[1].DiscountPercent, plans[1].DiscountEndsAt, plans[1].DiscountDescription)
	}
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping integration test")
	}

	return url
}

func testPool(t *testing.T, dbURL string) *pgxpool.Pool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("creating test pool: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("pinging test database: %v", err)
	}

	resetDatabase(t, pool)

	if err := database.RunMigrations(slog.New(slog.NewTextHandler(io.Discard, nil)), dbURL, migrations.FS); err != nil {
		pool.Close()
		t.Fatalf("applying migrations: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
	})

	return pool
}

func resetDatabase(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for _, q := range []string{
		"DROP SCHEMA public CASCADE",
		"CREATE SCHEMA public",
		"GRANT ALL ON SCHEMA public TO current_user",
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			pool.Close()
			t.Fatalf("resetting database (%s): %v", q, err)
		}
	}
}
