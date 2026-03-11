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
	"github.com/IsorilovA/pauza-server/internal/syncmodel"
	"github.com/IsorilovA/pauza-server/migrations"
)

func testSocialRepoPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("creating test pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("pinging test pool: %v", err)
	}

	resetQueries := []string{
		"DROP SCHEMA public CASCADE",
		"CREATE SCHEMA public",
		"GRANT ALL ON SCHEMA public TO current_user",
	}
	for _, q := range resetQueries {
		if _, err := pool.Exec(ctx, q); err != nil {
			pool.Close()
			t.Fatalf("resetting database (%s): %v", q, err)
		}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := database.RunMigrations(logger, dbURL, migrations.FS); err != nil {
		pool.Close()
		t.Fatalf("applying migrations: %v", err)
	}

	t.Cleanup(func() { pool.Close() })
	return pool
}

func insertLeaderboardUser(t *testing.T, pool DBTX, id, email, username string, visible bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, username, leaderboard_visible)
		VALUES ($1, $2, $3, $4)
	`, id, email, username, visible); err != nil {
		t.Fatalf("inserting user %s: %v", username, err)
	}
}

func insertAggregateRow(t *testing.T, pool DBTX, userID, localDay string, effectiveMS, qualified, sessionCount int, updatedAt int64) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx, `
		INSERT INTO streak_daily_aggregates (user_id, local_day, effective_ms, qualified, source_session_count, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, userID, localDay, effectiveMS, qualified, sessionCount, updatedAt); err != nil {
		t.Fatalf("inserting aggregate row %s/%s: %v", userID, localDay, err)
	}
}

func TestSocialRepository_LeaderboardQueries(t *testing.T) {
	t.Parallel()

	pool := testSocialRepoPool(t)
	repo := NewSocialRepository()

	aliceID := "00000000-0000-0000-0000-000000000001"
	bobID := "00000000-0000-0000-0000-000000000002"
	carolID := "00000000-0000-0000-0000-000000000003"
	daveID := "00000000-0000-0000-0000-000000000004"

	insertLeaderboardUser(t, pool, aliceID, "alice@example.com", "alice", true)
	insertLeaderboardUser(t, pool, bobID, "bob@example.com", "bob", false)
	insertLeaderboardUser(t, pool, carolID, "carol@example.com", "carol", true)
	insertLeaderboardUser(t, pool, daveID, "dave@example.com", "dave", true)

	insertAggregateRow(t, pool, bobID, "2026-03-03", 200, 1, 1, 103)
	insertAggregateRow(t, pool, bobID, "2026-03-02", 300, 1, 1, 102)
	insertAggregateRow(t, pool, bobID, "2026-03-01", 0, 1, 0, 101)
	insertAggregateRow(t, pool, carolID, "2026-03-03", 100, 1, 1, 103)
	insertAggregateRow(t, pool, carolID, "2026-03-02", 200, 1, 1, 102)
	insertAggregateRow(t, pool, daveID, "2026-03-03", 700, 1, 1, 103)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, userID := range []string{bobID, carolID, daveID} {
		if err := repo.RefreshLeaderboardMetrics(ctx, pool, userID); err != nil {
			t.Fatalf("RefreshLeaderboardMetrics(%s): %v", userID, err)
		}
	}

	streakRows, total, err := repo.ListLeaderboardEntries(ctx, pool, LeaderboardMetricStreak, 1, 10)
	if err != nil {
		t.Fatalf("ListLeaderboardEntries(streak): %v", err)
	}
	if total != 3 {
		t.Fatalf("visible total = %d, want 3", total)
	}
	if len(streakRows) != 3 {
		t.Fatalf("streak rows len = %d, want 3", len(streakRows))
	}
	if streakRows[0].User.Username != "carol" || streakRows[0].Rank != 2 || streakRows[0].CurrentStreakDays != 2 {
		t.Fatalf("unexpected first streak row: %#v", streakRows[0])
	}
	if streakRows[1].User.Username != "dave" || streakRows[1].Rank != 3 || streakRows[1].CurrentStreakDays != 1 {
		t.Fatalf("unexpected second streak row: %#v", streakRows[1])
	}
	if streakRows[2].User.Username != "alice" || streakRows[2].Rank != 4 || streakRows[2].CurrentStreakDays != 0 {
		t.Fatalf("unexpected third streak row: %#v", streakRows[2])
	}

	focusRows, _, err := repo.ListLeaderboardEntries(ctx, pool, LeaderboardMetricFocusTime, 1, 10)
	if err != nil {
		t.Fatalf("ListLeaderboardEntries(focus_time): %v", err)
	}
	if len(focusRows) != 3 {
		t.Fatalf("focus rows len = %d, want 3", len(focusRows))
	}
	if focusRows[0].User.Username != "dave" || focusRows[0].Rank != 1 || focusRows[0].TotalFocusTimeMS != 700 {
		t.Fatalf("unexpected first focus row: %#v", focusRows[0])
	}
	if focusRows[1].User.Username != "carol" || focusRows[1].Rank != 3 || focusRows[1].TotalFocusTimeMS != 300 {
		t.Fatalf("unexpected second focus row: %#v", focusRows[1])
	}
	if focusRows[2].User.Username != "alice" || focusRows[2].Rank != 4 || focusRows[2].TotalFocusTimeMS != 0 {
		t.Fatalf("unexpected third focus row: %#v", focusRows[2])
	}

	bobRank, err := repo.GetLeaderboardRank(ctx, pool, LeaderboardMetricStreak, bobID)
	if err != nil {
		t.Fatalf("GetLeaderboardRank(streak, bob): %v", err)
	}
	if bobRank.Rank != 1 || bobRank.User.LeaderboardVisible || bobRank.CurrentStreakDays != 3 {
		t.Fatalf("unexpected bob streak rank: %#v", bobRank)
	}

	outOfRangeRows, outOfRangeTotal, err := repo.ListLeaderboardEntries(ctx, pool, LeaderboardMetricStreak, 3, 2)
	if err != nil {
		t.Fatalf("ListLeaderboardEntries(out-of-range): %v", err)
	}
	if outOfRangeTotal != 3 {
		t.Fatalf("out-of-range total = %d, want 3", outOfRangeTotal)
	}
	if len(outOfRangeRows) != 0 {
		t.Fatalf("out-of-range rows len = %d, want 0", len(outOfRangeRows))
	}
}

func TestPgxSyncRepository_SyncStreakDailyAggregates_RefreshesLeaderboardMetrics(t *testing.T) {
	t.Parallel()

	pool := testSocialRepoPool(t)
	repo := NewPgxSyncRepository()

	userID := "00000000-0000-0000-0000-000000000010"
	insertLeaderboardUser(t, pool, userID, "sync@example.com", "syncer", true)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := repo.SyncStreakDailyAggregates(ctx, pool, userID, syncmodel.TableSync[syncmodel.StreakDailyAggregate, string]{
		LastSyncedAt: 0,
		Upserts: []syncmodel.StreakDailyAggregate{
			{LocalDay: "2026-03-03", EffectiveMS: 100, Qualified: 1, SourceSessionCount: 1, UpdatedAt: 103},
			{LocalDay: "2026-03-02", EffectiveMS: 200, Qualified: 1, SourceSessionCount: 1, UpdatedAt: 102},
		},
	})
	if err != nil {
		t.Fatalf("SyncStreakDailyAggregates(upsert): %v", err)
	}

	var currentStreak int
	var totalFocus int64
	if err := pool.QueryRow(ctx, `
		SELECT current_streak_days, total_focus_time_ms
		FROM leaderboard_metrics
		WHERE user_id = $1
	`, userID).Scan(&currentStreak, &totalFocus); err != nil {
		t.Fatalf("loading leaderboard metrics after upsert: %v", err)
	}
	if currentStreak != 2 || totalFocus != 300 {
		t.Fatalf("metrics after upsert = (%d, %d), want (2, 300)", currentStreak, totalFocus)
	}

	_, err = repo.SyncStreakDailyAggregates(ctx, pool, userID, syncmodel.TableSync[syncmodel.StreakDailyAggregate, string]{
		LastSyncedAt: 0,
		Deletions:    []string{"2026-03-03", "2026-03-02"},
	})
	if err != nil {
		t.Fatalf("SyncStreakDailyAggregates(delete): %v", err)
	}

	if err := pool.QueryRow(ctx, `
		SELECT current_streak_days, total_focus_time_ms
		FROM leaderboard_metrics
		WHERE user_id = $1
	`, userID).Scan(&currentStreak, &totalFocus); err != nil {
		t.Fatalf("loading leaderboard metrics after delete: %v", err)
	}
	if currentStreak != 0 || totalFocus != 0 {
		t.Fatalf("metrics after delete = (%d, %d), want (0, 0)", currentStreak, totalFocus)
	}
}
