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

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/syncmodel"
)

type syncTableFixture[T any, D any] struct {
	LastSyncedAt *int64 `json:"last_synced_at,omitempty"`
	Upserts      []T    `json:"upserts"`
	Deletions    []D    `json:"deletions"`
}

type syncRequestFixture struct {
	Tables syncTablesFixture `json:"tables"`
}

type syncTablesFixture struct {
	Modes                      *syncTableFixture[syncModeFixture, string]                                              `json:"modes,omitempty"`
	ModeBlockedApps            *syncTableFixture[syncModeBlockedAppFixture, syncModeBlockedAppKey]                     `json:"mode_blocked_apps,omitempty"`
	Schedules                  *syncTableFixture[syncScheduleFixture, string]                                          `json:"schedules,omitempty"`
	RestrictionSessions        *syncTableFixture[syncRestrictionSessionFixture, string]                                `json:"restriction_sessions,omitempty"`
	RestrictionLifecycleEvents *syncTableFixture[syncRestrictionLifecycleEventFixture, string]                         `json:"restriction_lifecycle_events,omitempty"`
	NFCLinkedChips             *syncTableFixture[syncNFCLinkedChipFixture, string]                                     `json:"nfc_linked_chips,omitempty"`
	QRLinkedCodes              *syncTableFixture[syncQRLinkedCodeFixture, string]                                      `json:"qr_linked_codes,omitempty"`
	StreakSessionDailyRollups  *syncTableFixture[syncStreakSessionDailyRollupFixture, syncStreakSessionDailyRollupKey] `json:"streak_session_daily_rollups,omitempty"`
	StreakDailyAggregates      *syncTableFixture[syncStreakDailyAggregateFixture, string]                              `json:"streak_daily_aggregates,omitempty"`
}

type syncModeFixture struct {
	ID                    string  `json:"id"`
	Title                 string  `json:"title"`
	TextOnScreen          string  `json:"text_on_screen"`
	Description           *string `json:"description"`
	AllowedPausesCount    *int    `json:"allowed_pauses_count"`
	MinimumDurationMS     *int    `json:"minimum_duration_ms"`
	EndingPausingScenario string  `json:"ending_pausing_scenario"`
	IconToken             string  `json:"icon_token"`
	CreatedAt             *int64  `json:"created_at"`
	UpdatedAt             *int64  `json:"updated_at"`
}

type syncModeBlockedAppFixture struct {
	ModeID        string `json:"mode_id"`
	Platform      string `json:"platform"`
	AppIdentifier string `json:"app_identifier"`
	CreatedAt     *int64 `json:"created_at"`
	UpdatedAt     *int64 `json:"updated_at"`
}

type syncModeBlockedAppKey struct {
	ModeID        string `json:"mode_id"`
	Platform      string `json:"platform"`
	AppIdentifier string `json:"app_identifier"`
}

type syncScheduleFixture struct {
	ID          string `json:"id"`
	ModeID      string `json:"mode_id"`
	Days        string `json:"days"`
	StartMinute *int   `json:"start_minute"`
	EndMinute   *int   `json:"end_minute"`
	Enabled     *int   `json:"enabled"`
	CreatedAt   *int64 `json:"created_at"`
	UpdatedAt   *int64 `json:"updated_at"`
}

type syncRestrictionSessionFixture struct {
	SessionID         string  `json:"session_id"`
	ModeID            string  `json:"mode_id"`
	Source            string  `json:"source"`
	StartedAt         *int64  `json:"started_at"`
	EndedAt           *int64  `json:"ended_at"`
	PauseCount        *int    `json:"pause_count"`
	TotalPausedMS     *int    `json:"total_paused_ms"`
	LastPausedAt      *int64  `json:"last_paused_at"`
	IntegrityStatus   string  `json:"integrity_status"`
	LastAnomalyReason *string `json:"last_anomaly_reason"`
	LastEventID       string  `json:"last_event_id"`
	CreatedAt         *int64  `json:"created_at"`
	UpdatedAt         *int64  `json:"updated_at"`
}

type syncRestrictionLifecycleEventFixture struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	ModeID     string `json:"mode_id"`
	Action     string `json:"action"`
	Source     string `json:"source"`
	Reason     string `json:"reason"`
	OccurredAt *int64 `json:"occurred_at"`
	CreatedAt  *int64 `json:"created_at"`
}

type syncNFCLinkedChipFixture struct {
	ID             string `json:"id"`
	ChipIdentifier string `json:"chip_identifier"`
	Name           string `json:"name"`
	CreatedAt      *int64 `json:"created_at"`
	UpdatedAt      *int64 `json:"updated_at"`
}

type syncQRLinkedCodeFixture struct {
	ID        string `json:"id"`
	ScanValue string `json:"scan_value"`
	Name      string `json:"name"`
	CreatedAt *int64 `json:"created_at"`
	UpdatedAt *int64 `json:"updated_at"`
}

type syncStreakSessionDailyRollupFixture struct {
	SessionID   string `json:"session_id"`
	LocalDay    string `json:"local_day"`
	EffectiveMS *int   `json:"effective_ms"`
	UpdatedAt   *int64 `json:"updated_at"`
}

type syncStreakSessionDailyRollupKey struct {
	SessionID string `json:"session_id"`
	LocalDay  string `json:"local_day"`
}

type syncStreakDailyAggregateFixture struct {
	LocalDay           string `json:"local_day"`
	EffectiveMS        *int   `json:"effective_ms"`
	Qualified          *int   `json:"qualified"`
	SourceSessionCount *int   `json:"source_session_count"`
	UpdatedAt          *int64 `json:"updated_at"`
}

func syncPayload() syncRequestFixture {
	return syncRequestFixture{
		Tables: syncTablesFixture{
			Modes:                      syncEmptyTable[syncModeFixture, string](),
			ModeBlockedApps:            syncEmptyTable[syncModeBlockedAppFixture, syncModeBlockedAppKey](),
			Schedules:                  syncEmptyTable[syncScheduleFixture, string](),
			RestrictionSessions:        syncEmptyTable[syncRestrictionSessionFixture, string](),
			RestrictionLifecycleEvents: syncEmptyTable[syncRestrictionLifecycleEventFixture, string](),
			NFCLinkedChips:             syncEmptyTable[syncNFCLinkedChipFixture, string](),
			QRLinkedCodes:              syncEmptyTable[syncQRLinkedCodeFixture, string](),
			StreakSessionDailyRollups:  syncEmptyTable[syncStreakSessionDailyRollupFixture, syncStreakSessionDailyRollupKey](),
			StreakDailyAggregates:      syncEmptyTable[syncStreakDailyAggregateFixture, string](),
		},
	}
}

func syncEmptyTable[T any, D any]() *syncTableFixture[T, D] {
	return &syncTableFixture[T, D]{
		LastSyncedAt: int64Ptr(0),
		Upserts:      []T{},
		Deletions:    []D{},
	}
}

func int64Ptr(v int64) *int64 { return &v }
func intPtr(v int) *int       { return &v }

func syncModeUpsert(title string, updatedAt int64) syncModeFixture {
	return syncModeFixture{
		ID:                    "mode1",
		Title:                 title,
		TextOnScreen:          "S",
		Description:           nil,
		AllowedPausesCount:    intPtr(1),
		MinimumDurationMS:     intPtr(1000),
		EndingPausingScenario: "manual",
		IconToken:             "ms:v1:work",
		CreatedAt:             int64Ptr(1000),
		UpdatedAt:             int64Ptr(updatedAt),
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
	ts, pool, mailer, _ := setupTestServer(t)
	auth := startAndVerifyAuth(t, ts.URL, mailer, "sync-first@example.com")

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
		VALUES ($1,'ev1','sess1','mode1','START','manual','manual',1000,1100)`, auth.User.ID)
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
	ts, pool, mailer, _ := setupTestServer(t)
	auth := startAndVerifyAuth(t, ts.URL, mailer, "sync-partial@example.com")

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

	body := syncRequestFixture{Tables: syncTablesFixture{Modes: syncEmptyTable[syncModeFixture, string]()}}
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
	ts, _, mailer, _ := setupTestServer(t)
	auth := startAndVerifyAuth(t, ts.URL, mailer, "sync-empty@example.com")

	body := syncRequestFixture{Tables: syncTablesFixture{Modes: syncEmptyTable[syncModeFixture, string]()}}
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
	ts, pool, mailer, _ := setupTestServer(t)
	auth := startAndVerifyAuth(t, ts.URL, mailer, "sync-lww@example.com")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, `INSERT INTO modes (user_id, id, title, text_on_screen, description, allowed_pauses_count, minimum_duration_ms, ending_pausing_scenario, icon_token, created_at, updated_at)
		VALUES ($1,'mode1','Server','S',NULL,1,1000,'manual','ms:v1:work',1000,200)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert mode: %v", err)
	}

	oldPayload := syncPayload()
	oldPayload.Tables.Modes = &syncTableFixture[syncModeFixture, string]{
		LastSyncedAt: int64Ptr(150),
		Upserts:      []syncModeFixture{syncModeUpsert("OldClient", 100)},
		Deletions:    []string{},
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
	newPayload.Tables.Modes = &syncTableFixture[syncModeFixture, string]{
		LastSyncedAt: int64Ptr(150),
		Upserts:      []syncModeFixture{syncModeUpsert("ClientNew", 300)},
		Deletions:    []string{},
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
	ts, pool, mailer, _ := setupTestServer(t)
	auth := startAndVerifyAuth(t, ts.URL, mailer, "sync-events@example.com")

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
		VALUES ($1,'ev1','sess1','mode1','START','manual','manual',900,1000)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}

	p := syncPayload()
	p.Tables.RestrictionLifecycleEvents = &syncTableFixture[syncRestrictionLifecycleEventFixture, string]{
		LastSyncedAt: int64Ptr(900),
		Upserts:      []syncRestrictionLifecycleEventFixture{},
		Deletions:    []string{},
	}
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
	p2.Tables.RestrictionLifecycleEvents = &syncTableFixture[syncRestrictionLifecycleEventFixture, string]{
		LastSyncedAt: int64Ptr(1000),
		Upserts:      []syncRestrictionLifecycleEventFixture{},
		Deletions:    []string{},
	}
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
	ts, pool, mailer, _ := setupTestServer(t)
	auth := startAndVerifyAuth(t, ts.URL, mailer, "sync-cascade@example.com")

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
		VALUES ($1,'ev1','sess1','mode1','START','manual','manual',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO streak_session_daily_rollups (user_id, session_id, local_day, effective_ms, updated_at)
		VALUES ($1,'sess1','2026-03-01',1000,1100)`, auth.User.ID)
	if err != nil {
		t.Fatalf("insert rollup: %v", err)
	}

	deletePayload := syncPayload()
	deletePayload.Tables.Modes = &syncTableFixture[syncModeFixture, string]{
		LastSyncedAt: int64Ptr(0),
		Upserts:      []syncModeFixture{},
		Deletions:    []string{"mode1"},
	}
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

func TestIntegration_Sync_DeletedUserRejected(t *testing.T) {
	ts, pool, mailer, _ := setupTestServer(t)
	auth := startAndVerifyAuth(t, ts.URL, mailer, "sync-deleted-user@example.com")

	resp := postSync(t, ts.URL, auth.AccessToken, syncPayload())
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("sync before delete: expected 200, got %d: %s", resp.StatusCode, body)
	}
	discardBody(t, resp)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", auth.User.ID)
	if err != nil {
		t.Fatalf("deleting user row: %v", err)
	}

	resp = postSync(t, ts.URL, auth.AccessToken, syncPayload())
	if resp.StatusCode != http.StatusUnauthorized {
		body := readBody(t, resp)
		t.Fatalf("sync after delete: expected 401, got %d: %s", resp.StatusCode, body)
	}

	var errResp apperror.ErrorResponse
	decodeJSON(t, resp, &errResp)
	if errResp.Error.Code != apperror.CodeUnauthorized {
		t.Errorf("sync after delete: code = %q, want %q", errResp.Error.Code, apperror.CodeUnauthorized)
	}
}
