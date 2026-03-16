//go:build integration

package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/repository"
)

func TestDevicesRegisterAndUnregister(t *testing.T) {
	baseURL, pool, sender, _ := setupTestServer(t)

	auth := startAndVerifyAuth(t, baseURL.URL, sender, "devices@example.com")

	reqBody := `{"fcm_token":"token-1","platform":"ios"}`
	req, err := http.NewRequest(http.MethodPost, baseURL.URL+"/api/v1/devices", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("creating register request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/v1/devices: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d: %s", resp.StatusCode, string(readBody(t, resp)))
	}
	discardBody(t, resp)

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM device_tokens WHERE user_id = $1 AND fcm_token = $2`,
		auth.User.ID, "token-1",
	).Scan(&count); err != nil {
		t.Fatalf("counting registered device token: %v", err)
	}
	if count != 1 {
		t.Fatalf("registered device token count = %d, want 1", count)
	}

	unregisterReq, err := http.NewRequest(http.MethodPost, baseURL.URL+"/api/v1/devices/unregister", strings.NewReader(`{"fcm_token":"token-1"}`))
	if err != nil {
		t.Fatalf("creating unregister request: %v", err)
	}
	unregisterReq.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	unregisterReq.Header.Set("Content-Type", "application/json")

	unregisterResp, err := http.DefaultClient.Do(unregisterReq)
	if err != nil {
		t.Fatalf("POST /api/v1/devices/unregister: %v", err)
	}
	if unregisterResp.StatusCode != http.StatusOK {
		t.Fatalf("unregister status = %d: %s", unregisterResp.StatusCode, string(readBody(t, unregisterResp)))
	}
	discardBody(t, unregisterResp)

	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM device_tokens WHERE user_id = $1 AND fcm_token = $2`,
		auth.User.ID, "token-1",
	).Scan(&count); err != nil {
		t.Fatalf("counting device token after unregister: %v", err)
	}
	if count != 0 {
		t.Fatalf("device token count after unregister = %d, want 0", count)
	}
}

func TestLeaderboardEndpoints_UsePersistedMetricsAndPreserveVisibilityRules(t *testing.T) {
	ts, pool, sender, _ := setupTestServer(t)
	auth := startAndVerifyAuth(t, ts.URL, sender, "leaderboard@example.com")

	ctx := context.Background()
	repo := repository.NewSocialRepository()

	if _, err := pool.Exec(ctx, `UPDATE users SET leaderboard_visible = false WHERE id = $1`, auth.User.ID); err != nil {
		t.Fatalf("hide actor from leaderboard entries: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO streak_daily_aggregates (user_id, local_day, effective_ms, qualified, source_session_count, updated_at)
		VALUES
			($1, '2026-03-03', 100, 1, 1, 103),
			($1, '2026-03-02', 150, 1, 1, 102)
	`, auth.User.ID); err != nil {
		t.Fatalf("insert actor aggregates: %v", err)
	}

	hiddenID := "10000000-0000-0000-0000-000000000001"
	visibleStreakID := "10000000-0000-0000-0000-000000000002"
	visibleFocusID := "10000000-0000-0000-0000-000000000003"

	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, username, leaderboard_visible)
		VALUES
			($1, 'able@example.com', 'able', false),
			($2, 'bravo@example.com', 'bravo', true),
			($3, 'charlie@example.com', 'charlie', true)
	`, hiddenID, visibleStreakID, visibleFocusID); err != nil {
		t.Fatalf("insert leaderboard users: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO streak_daily_aggregates (user_id, local_day, effective_ms, qualified, source_session_count, updated_at)
		VALUES
			($1, '2026-03-03', 200, 1, 1, 103),
			($1, '2026-03-02', 200, 1, 1, 102),
			($1, '2026-03-01', 50, 1, 1, 101),
			($2, '2026-03-03', 180, 1, 1, 103),
			($2, '2026-03-02', 220, 1, 1, 102),
			($2, '2026-03-01', 10, 1, 1, 101),
			($3, '2026-03-03', 500, 1, 1, 103)
	`, hiddenID, visibleStreakID, visibleFocusID); err != nil {
		t.Fatalf("insert leaderboard aggregates: %v", err)
	}

	for _, userID := range []string{auth.User.ID, hiddenID, visibleStreakID, visibleFocusID} {
		if err := repo.RefreshLeaderboardMetrics(ctx, pool, userID); err != nil {
			t.Fatalf("RefreshLeaderboardMetrics(%s): %v", userID, err)
		}
	}

	type leaderboardResponse struct {
		Entries []struct {
			Rank int `json:"rank"`
			User struct {
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"user"`
			CurrentStreakDays int   `json:"current_streak_days"`
			TotalFocusTimeMS  int64 `json:"total_focus_time_ms"`
		} `json:"entries"`
		MyRank struct {
			Rank              int   `json:"rank"`
			CurrentStreakDays int   `json:"current_streak_days"`
			TotalFocusTimeMS  int64 `json:"total_focus_time_ms"`
		} `json:"my_rank"`
		Pagination struct {
			Page  int `json:"page"`
			Limit int `json:"limit"`
			Total int `json:"total"`
		} `json:"pagination"`
	}

	getLeaderboard := func(path string) leaderboardResponse {
		t.Helper()

		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatalf("create request %s: %v", path, err)
		}
		req.Header.Set("Authorization", "Bearer "+auth.AccessToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d: %s", path, resp.StatusCode, string(readBody(t, resp)))
		}
		defer resp.Body.Close()

		var out leaderboardResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return out
	}

	streakOut := getLeaderboard("/api/v1/leaderboard/streaks")
	if streakOut.Pagination.Total != 2 {
		t.Fatalf("streak total = %d, want 2", streakOut.Pagination.Total)
	}
	if streakOut.MyRank.Rank != 2 || streakOut.MyRank.CurrentStreakDays != 2 {
		t.Fatalf("unexpected streak my_rank: %#v", streakOut.MyRank)
	}
	if len(streakOut.Entries) != 2 {
		t.Fatalf("streak entries len = %d, want 2", len(streakOut.Entries))
	}
	if streakOut.Entries[0].User.Username != "bravo" || streakOut.Entries[0].Rank != 1 || streakOut.Entries[0].CurrentStreakDays != 3 {
		t.Fatalf("unexpected first streak entry: %#v", streakOut.Entries[0])
	}
	if streakOut.Entries[1].User.Username != "charlie" || streakOut.Entries[1].Rank != 2 || streakOut.Entries[1].CurrentStreakDays != 1 {
		t.Fatalf("unexpected second streak entry: %#v", streakOut.Entries[1])
	}

	focusOut := getLeaderboard("/api/v1/leaderboard/focus-time")
	if focusOut.Pagination.Total != 2 {
		t.Fatalf("focus total = %d, want 2", focusOut.Pagination.Total)
	}
	if focusOut.MyRank.Rank != 3 || focusOut.MyRank.TotalFocusTimeMS != 250 {
		t.Fatalf("unexpected focus my_rank: %#v", focusOut.MyRank)
	}
	if len(focusOut.Entries) != 2 {
		t.Fatalf("focus entries len = %d, want 2", len(focusOut.Entries))
	}
	if focusOut.Entries[0].User.Username != "charlie" || focusOut.Entries[0].Rank != 1 || focusOut.Entries[0].TotalFocusTimeMS != 500 {
		t.Fatalf("unexpected first focus entry: %#v", focusOut.Entries[0])
	}
	if focusOut.Entries[1].User.Username != "bravo" || focusOut.Entries[1].Rank != 2 || focusOut.Entries[1].TotalFocusTimeMS != 410 {
		t.Fatalf("unexpected second focus entry: %#v", focusOut.Entries[1])
	}
}
