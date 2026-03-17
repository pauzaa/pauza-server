//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/syncmodel"
	"github.com/IsorilovA/pauza-server/internal/testdb"
)

func testSocialRepoPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := testdb.New(t)
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
	if streakRows[0].User.Username != "carol" || streakRows[0].Rank != 1 || streakRows[0].CurrentStreakDays != 2 {
		t.Fatalf("unexpected first streak row: %#v", streakRows[0])
	}
	if streakRows[1].User.Username != "dave" || streakRows[1].Rank != 2 || streakRows[1].CurrentStreakDays != 1 {
		t.Fatalf("unexpected second streak row: %#v", streakRows[1])
	}
	if streakRows[2].User.Username != "alice" || streakRows[2].Rank != 3 || streakRows[2].CurrentStreakDays != 0 {
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
	if focusRows[1].User.Username != "carol" || focusRows[1].Rank != 2 || focusRows[1].TotalFocusTimeMS != 300 {
		t.Fatalf("unexpected second focus row: %#v", focusRows[1])
	}
	if focusRows[2].User.Username != "alice" || focusRows[2].Rank != 3 || focusRows[2].TotalFocusTimeMS != 0 {
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

func TestPgxSyncRepository_RecomputeStreakAggregates_RefreshesLeaderboardMetrics(t *testing.T) {

	pool := testSocialRepoPool(t)
	repo := NewPgxSyncRepository(NewSocialRepository())

	userID := "00000000-0000-0000-0000-000000000010"
	insertLeaderboardUser(t, pool, userID, "sync@example.com", "syncer", true)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Insert prerequisite mode and restriction sessions (FK targets for rollups)
	if _, err := pool.Exec(ctx, `INSERT INTO modes (user_id, id, title, text_on_screen, allowed_pauses_count, ending_pausing_scenario, icon_token, created_at, updated_at) VALUES ($1, 'mode1', 'M', 'T', 0, 'manual', 'icon', 1000, 1000)`, userID); err != nil {
		t.Fatalf("inserting mode: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO restriction_sessions (user_id, session_id, mode_id, source, started_at, pause_count, total_paused_ms, integrity_status, last_event_id, created_at, updated_at) VALUES ($1, 'sess-1', 'mode1', 'manual', 1000, 0, 0, 'ok', 'ev1', 1000, 1000)`, userID); err != nil {
		t.Fatalf("inserting restriction session 1: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO restriction_sessions (user_id, session_id, mode_id, source, started_at, pause_count, total_paused_ms, integrity_status, last_event_id, created_at, updated_at) VALUES ($1, 'sess-2', 'mode1', 'manual', 2000, 0, 0, 'ok', 'ev2', 2000, 2000)`, userID); err != nil {
		t.Fatalf("inserting restriction session 2: %v", err)
	}

	// Insert rollup data that RecomputeStreakAggregates will aggregate
	_, err := repo.SyncStreakSessionDailyRollups(ctx, pool, userID, syncmodel.TableSync[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{
		Cursor: 0,
		Upserts: []syncmodel.StreakSessionDailyRollup{
			{SessionID: "sess-1", LocalDay: "2026-03-03", EffectiveMS: 100, UpdatedAt: 103},
			{SessionID: "sess-2", LocalDay: "2026-03-02", EffectiveMS: 200, UpdatedAt: 102},
		},
	})
	if err != nil {
		t.Fatalf("SyncStreakSessionDailyRollups(upsert): %v", err)
	}

	if err := repo.RecomputeStreakAggregates(ctx, pool, userID, []string{"2026-03-03", "2026-03-02"}); err != nil {
		t.Fatalf("RecomputeStreakAggregates: %v", err)
	}

	// Verify aggregates were created
	result, err := repo.ListStreakDailyAggregateChanges(ctx, pool, userID, 0)
	if err != nil {
		t.Fatalf("ListStreakDailyAggregateChanges: %v", err)
	}
	if len(result.Upserts) != 2 {
		t.Fatalf("aggregate upserts = %d, want 2", len(result.Upserts))
	}

	// Delete rollup data and recompute to verify orphans are cleaned up
	_, err = repo.SyncStreakSessionDailyRollups(ctx, pool, userID, syncmodel.TableSync[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{
		Cursor:    0,
		Deletions: []syncmodel.StreakSessionDailyRollupKey{
			{SessionID: "sess-1", LocalDay: "2026-03-03"},
			{SessionID: "sess-2", LocalDay: "2026-03-02"},
		},
	})
	if err != nil {
		t.Fatalf("SyncStreakSessionDailyRollups(delete): %v", err)
	}

	if err := repo.RecomputeStreakAggregates(ctx, pool, userID, []string{"2026-03-03", "2026-03-02"}); err != nil {
		t.Fatalf("RecomputeStreakAggregates after delete: %v", err)
	}
}
