//go:build integration

package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/syncmodel"
)

func syncPayload() map[string]any {
	table := func() map[string]any {
		return map[string]any{"last_synced_at": int64(0), "upserts": []any{}, "deletions": []any{}}
	}
	return map[string]any{
		"tables": map[string]any{
			"modes":                        table(),
			"mode_blocked_apps":            table(),
			"schedules":                    table(),
			"restriction_sessions":         table(),
			"restriction_lifecycle_events": table(),
			"nfc_linked_chips":             table(),
			"qr_linked_codes":              table(),
			"streak_session_daily_rollups": table(),
			"streak_daily_aggregates":      table(),
		},
	}
}

func postSync(t *testing.T, baseURL, token string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal sync body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/sync", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post sync: %v", err)
	}
	return resp
}

func TestIntegration_Sync_FirstSync_AllTables(t *testing.T) {
	ts, pool, mailer := setupTestServer(t)
	auth := registerAndVerify(t, ts.URL, mailer, "sync-first@example.com", "StrongPass123!")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := pool.Exec(ctx, `INSERT INTO modes (user_id, id, title, text_on_screen, description, allowed_pauses_count, minimum_duration_ms, ending_pausing_scenario, icon_token, created_at, updated_at)
		VALUES ($1,'mode1','Focus','Stay',NULL,1,1000,'manual','ms:v1:work',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert mode: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO mode_blocked_apps (user_id, mode_id, platform, app_identifier, created_at, updated_at)
		VALUES ($1,'mode1','android','com.app',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert mode_blocked_apps: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO schedules (user_id, id, mode_id, days, start_minute, end_minute, enabled, created_at, updated_at)
		VALUES ($1,'sch1','mode1','1111100',60,120,1,1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert schedule: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO restriction_sessions (user_id, session_id, mode_id, source, started_at, ended_at, pause_count, total_paused_ms, last_paused_at, integrity_status, last_anomaly_reason, last_event_id, created_at, updated_at)
		VALUES ($1,'sess1','mode1','manual',1000,NULL,0,0,NULL,'ok',NULL,'ev1',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert restriction_session: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO restriction_lifecycle_events (user_id, id, session_id, mode_id, action, source, reason, occurred_at, created_at)
		VALUES ($1,'ev1','sess1','mode1','START','manual','x',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert restriction_lifecycle_event: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO nfc_linked_chips (user_id, id, chip_identifier, name, created_at, updated_at)
		VALUES ($1,'nfc1','chip1','Chip',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert nfc: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO qr_linked_codes (user_id, id, scan_value, name, created_at, updated_at)
		VALUES ($1,'qr1','scan1','QR',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert qr: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO streak_session_daily_rollups (user_id, session_id, local_day, effective_ms, updated_at)
		VALUES ($1,'sess1','2026-03-01',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert rollup: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO streak_daily_aggregates (user_id, local_day, effective_ms, qualified, source_session_count, updated_at)
		VALUES ($1,'2026-03-01',1000,1,1,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert aggregate: %v", err)
	}

	resp := postSync(t, ts.URL, auth.AccessToken, syncPayload())
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("sync status %d: %s", resp.StatusCode, body)
	}
	var out syncmodel.Response
	decodeJSON(t, resp, &out)

	if out.Tables.Modes == nil || len(out.Tables.Modes.Upserts) != 1 || out.Tables.ModeBlockedApps == nil || len(out.Tables.ModeBlockedApps.Upserts) != 1 || out.Tables.Schedules == nil || len(out.Tables.Schedules.Upserts) != 1 || out.Tables.RestrictionSessions == nil || len(out.Tables.RestrictionSessions.Upserts) != 1 || out.Tables.RestrictionLifecycleEvents == nil || len(out.Tables.RestrictionLifecycleEvents.Upserts) != 1 || out.Tables.NFCLinkedChips == nil || len(out.Tables.NFCLinkedChips.Upserts) != 1 || out.Tables.QRLinkedCodes == nil || len(out.Tables.QRLinkedCodes.Upserts) != 1 || out.Tables.StreakSessionDailyRollups == nil || len(out.Tables.StreakSessionDailyRollups.Upserts) != 1 || out.Tables.StreakDailyAggregates == nil || len(out.Tables.StreakDailyAggregates.Upserts) != 1 {
		t.Fatalf("unexpected first sync upserts counts: %+v", out.Tables)
	}
}

func TestIntegration_Sync_OnlyRequestedTablesAreProcessed(t *testing.T) {
	ts, pool, mailer := setupTestServer(t)
	auth := registerAndVerify(t, ts.URL, mailer, "sync-partial@example.com", "StrongPass123!")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := pool.Exec(ctx, `INSERT INTO modes (user_id, id, title, text_on_screen, description, allowed_pauses_count, minimum_duration_ms, ending_pausing_scenario, icon_token, created_at, updated_at)
		VALUES ($1,'mode1','Focus','Stay',NULL,1,1000,'manual','ms:v1:work',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert mode: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO nfc_linked_chips (user_id, id, chip_identifier, name, created_at, updated_at)
		VALUES ($1,'nfc1','chip1','Chip',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert nfc: %v", err)
	}

	body := map[string]any{
		"tables": map[string]any{
			"modes": map[string]any{"last_synced_at": int64(0), "upserts": []any{}, "deletions": []any{}},
		},
	}
	resp := postSync(t, ts.URL, auth.AccessToken, body)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("sync status %d: %s", resp.StatusCode, body)
	}
	var out syncmodel.Response
	decodeJSON(t, resp, &out)

	if out.Tables.Modes == nil || len(out.Tables.Modes.Upserts) != 1 || out.Tables.Modes.Upserts[0].ID != "mode1" {
		t.Fatalf("expected requested modes table in response")
	}
	if out.Tables.NFCLinkedChips != nil || out.Tables.ModeBlockedApps != nil || out.Tables.Schedules != nil || out.Tables.RestrictionSessions != nil || out.Tables.RestrictionLifecycleEvents != nil || out.Tables.QRLinkedCodes != nil || out.Tables.StreakSessionDailyRollups != nil || out.Tables.StreakDailyAggregates != nil {
		t.Fatalf("expected omitted tables to stay absent in response: %+v", out.Tables)
	}
}

func TestIntegration_Sync_EmptyRequestedTableSerializesArrays(t *testing.T) {
	ts, _, mailer := setupTestServer(t)
	auth := registerAndVerify(t, ts.URL, mailer, "sync-empty@example.com", "StrongPass123!")

	body := map[string]any{
		"tables": map[string]any{
			"modes": map[string]any{"last_synced_at": int64(0), "upserts": []any{}, "deletions": []any{}},
		},
	}
	resp := postSync(t, ts.URL, auth.AccessToken, body)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("sync status %d: %s", resp.StatusCode, body)
	}
	raw := string(readBody(t, resp))

	if !strings.Contains(raw, `"modes":{"upserts":[],"deletions":[]}`) {
		t.Fatalf("expected empty arrays in raw response, got %s", raw)
	}
	if strings.Contains(raw, `"upserts":null`) || strings.Contains(raw, `"deletions":null`) {
		t.Fatalf("expected no null arrays in raw response, got %s", raw)
	}
}

func TestIntegration_Sync_LastWriteWinsAndEchoSuppression(t *testing.T) {
	ts, pool, mailer := setupTestServer(t)
	auth := registerAndVerify(t, ts.URL, mailer, "sync-lww@example.com", "StrongPass123!")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, `INSERT INTO modes (user_id, id, title, text_on_screen, description, allowed_pauses_count, minimum_duration_ms, ending_pausing_scenario, icon_token, created_at, updated_at)
		VALUES ($1,'mode1','Server','S',NULL,1,1000,'manual','ms:v1:work',1000,200)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert mode: %v", err)
	}

	oldPayload := syncPayload()
	oldPayload["tables"].(map[string]any)["modes"] = map[string]any{
		"last_synced_at": int64(150),
		"upserts": []any{map[string]any{
			"id": "mode1", "title": "OldClient", "text_on_screen": "S", "description": nil, "allowed_pauses_count": 1,
			"minimum_duration_ms": 1000, "ending_pausing_scenario": "manual", "icon_token": "ms:v1:work", "created_at": int64(1000), "updated_at": int64(100),
		}},
		"deletions": []any{},
	}
	resp := postSync(t, ts.URL, auth.AccessToken, oldPayload)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("old sync status %d: %s", resp.StatusCode, body)
	}
	var out1 syncmodel.Response
	decodeJSON(t, resp, &out1)
	if out1.Tables.Modes == nil || len(out1.Tables.Modes.Upserts) != 1 || out1.Tables.Modes.Upserts[0].Title != "Server" {
		t.Fatalf("expected server record returned on older client write")
	}

	newPayload := syncPayload()
	newPayload["tables"].(map[string]any)["modes"] = map[string]any{
		"last_synced_at": int64(150),
		"upserts": []any{map[string]any{
			"id": "mode1", "title": "ClientNew", "text_on_screen": "S", "description": nil, "allowed_pauses_count": 1,
			"minimum_duration_ms": 1000, "ending_pausing_scenario": "manual", "icon_token": "ms:v1:work", "created_at": int64(1000), "updated_at": int64(300),
		}},
		"deletions": []any{},
	}
	resp = postSync(t, ts.URL, auth.AccessToken, newPayload)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("new sync status %d: %s", resp.StatusCode, body)
	}
	var out2 syncmodel.Response
	decodeJSON(t, resp, &out2)
	if out2.Tables.Modes == nil || len(out2.Tables.Modes.Upserts) != 0 {
		t.Fatalf("expected no echo for freshly written mode")
	}

	var title string
	err = pool.QueryRow(ctx, `SELECT title FROM modes WHERE user_id = $1 AND id = 'mode1'`, auth.User.ID).Scan(&title)
	if err != nil {
		t.Fatalf("query mode title: %v", err)
	}
	if title != "ClientNew" {
		t.Fatalf("title = %q, want ClientNew", title)
	}
}

func TestIntegration_Sync_RestrictionLifecycleCursorUsesCreatedAt(t *testing.T) {
	ts, pool, mailer := setupTestServer(t)
	auth := registerAndVerify(t, ts.URL, mailer, "sync-events@example.com", "StrongPass123!")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, `INSERT INTO modes (user_id, id, title, text_on_screen, description, allowed_pauses_count, minimum_duration_ms, ending_pausing_scenario, icon_token, created_at, updated_at)
		VALUES ($1,'mode1','M','T',NULL,1,1000,'manual','ms:v1:work',1000,1000)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert mode: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO restriction_sessions (user_id, session_id, mode_id, source, started_at, ended_at, pause_count, total_paused_ms, last_paused_at, integrity_status, last_anomaly_reason, last_event_id, created_at, updated_at)
		VALUES ($1,'sess1','mode1','manual',1000,NULL,0,0,NULL,'ok',NULL,'ev1',1000,1000)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO restriction_lifecycle_events (user_id, id, session_id, mode_id, action, source, reason, occurred_at, created_at)
		VALUES ($1,'ev1','sess1','mode1','START','manual','x',900,1000)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	p := syncPayload()
	p["tables"].(map[string]any)["restriction_lifecycle_events"] = map[string]any{"last_synced_at": int64(900), "upserts": []any{}, "deletions": []any{}}
	resp := postSync(t, ts.URL, auth.AccessToken, p)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("sync status %d: %s", resp.StatusCode, body)
	}
	var out1 syncmodel.Response
	decodeJSON(t, resp, &out1)
	if out1.Tables.RestrictionLifecycleEvents == nil || len(out1.Tables.RestrictionLifecycleEvents.Upserts) != 1 {
		t.Fatalf("expected event for created_at cursor")
	}

	p2 := syncPayload()
	p2["tables"].(map[string]any)["restriction_lifecycle_events"] = map[string]any{"last_synced_at": int64(1000), "upserts": []any{}, "deletions": []any{}}
	resp = postSync(t, ts.URL, auth.AccessToken, p2)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("sync status %d: %s", resp.StatusCode, body)
	}
	var out2 syncmodel.Response
	decodeJSON(t, resp, &out2)
	if out2.Tables.RestrictionLifecycleEvents == nil || len(out2.Tables.RestrictionLifecycleEvents.Upserts) != 0 {
		t.Fatalf("expected no event when last_synced_at equals created_at")
	}
}

func TestIntegration_Sync_CascadeTombstonesReturnedOnFullRestore(t *testing.T) {
	ts, pool, mailer := setupTestServer(t)
	auth := registerAndVerify(t, ts.URL, mailer, "sync-cascade@example.com", "StrongPass123!")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, `INSERT INTO modes (user_id, id, title, text_on_screen, description, allowed_pauses_count, minimum_duration_ms, ending_pausing_scenario, icon_token, created_at, updated_at)
		VALUES ($1,'mode1','M','T',NULL,1,1000,'manual','ms:v1:work',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert mode: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO mode_blocked_apps (user_id, mode_id, platform, app_identifier, created_at, updated_at)
		VALUES ($1,'mode1','android','com.app',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert mode_blocked_apps: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO schedules (user_id, id, mode_id, days, start_minute, end_minute, enabled, created_at, updated_at)
		VALUES ($1,'sch1','mode1','1111100',60,120,1,1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert schedule: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO restriction_sessions (user_id, session_id, mode_id, source, started_at, ended_at, pause_count, total_paused_ms, last_paused_at, integrity_status, last_anomaly_reason, last_event_id, created_at, updated_at)
		VALUES ($1,'sess1','mode1','manual',1000,NULL,0,0,NULL,'ok',NULL,'ev1',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO restriction_lifecycle_events (user_id, id, session_id, mode_id, action, source, reason, occurred_at, created_at)
		VALUES ($1,'ev1','sess1','mode1','START','manual','x',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO streak_session_daily_rollups (user_id, session_id, local_day, effective_ms, updated_at)
		VALUES ($1,'sess1','2026-03-01',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert rollup: %v", err)
	}

	deletePayload := syncPayload()
	deletePayload["tables"].(map[string]any)["modes"] = map[string]any{"last_synced_at": int64(0), "upserts": []any{}, "deletions": []any{"mode1"}}
	resp := postSync(t, ts.URL, auth.AccessToken, deletePayload)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("delete sync status %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	restorePayload := syncPayload()
	resp = postSync(t, ts.URL, auth.AccessToken, restorePayload)
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("restore sync status %d: %s", resp.StatusCode, body)
	}
	var out syncmodel.Response
	decodeJSON(t, resp, &out)

	if out.Tables.Modes == nil || len(out.Tables.Modes.Deletions) == 0 || out.Tables.Modes.Deletions[0] != "mode1" {
		t.Fatalf("expected mode tombstone in full restore response")
	}
	if out.Tables.ModeBlockedApps == nil || len(out.Tables.ModeBlockedApps.Deletions) == 0 || out.Tables.ModeBlockedApps.Deletions[0].ModeID != "mode1" {
		t.Fatalf("expected mode_blocked_apps tombstone in full restore response")
	}
	if out.Tables.Schedules == nil || len(out.Tables.Schedules.Deletions) == 0 || out.Tables.Schedules.Deletions[0] != "sch1" {
		t.Fatalf("expected schedules tombstone in full restore response")
	}
	if out.Tables.RestrictionSessions == nil || len(out.Tables.RestrictionSessions.Deletions) == 0 || out.Tables.RestrictionSessions.Deletions[0] != "sess1" {
		t.Fatalf("expected restriction_sessions tombstone in full restore response")
	}
	if out.Tables.RestrictionLifecycleEvents == nil || len(out.Tables.RestrictionLifecycleEvents.Deletions) == 0 || out.Tables.RestrictionLifecycleEvents.Deletions[0] != "ev1" {
		t.Fatalf("expected restriction_lifecycle_events tombstone in full restore response")
	}
	if out.Tables.StreakSessionDailyRollups == nil || len(out.Tables.StreakSessionDailyRollups.Deletions) == 0 || out.Tables.StreakSessionDailyRollups.Deletions[0].SessionID != "sess1" {
		t.Fatalf("expected streak_session_daily_rollups tombstone in full restore response")
	}
}
