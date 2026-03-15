package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/IsorilovA/pauza-server/internal/syncmodel"
)

const (
	syncTableModes                      = "modes"
	syncTableModeBlockedApps            = "mode_blocked_apps"
	syncTableSchedules                  = "schedules"
	syncTableRestrictionSessions        = "restriction_sessions"
	syncTableRestrictionLifecycleEvents = "restriction_lifecycle_events"
	syncTableNFCLinkedChips             = "nfc_linked_chips"
	syncTableQRLinkedCodes              = "qr_linked_codes"
	syncTableStreakSessionDailyRollups  = "streak_session_daily_rollups"
	syncTableStreakDailyAggregates      = "streak_daily_aggregates"
)

type SyncRepository interface {
	SyncModes(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.Mode, string]) (syncmodel.TableResult[syncmodel.Mode, string], error)
	SyncModeBlockedApps(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]) (syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey], error)
	SyncSchedules(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.Schedule, string]) (syncmodel.TableResult[syncmodel.Schedule, string], error)
	SyncRestrictionSessions(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.RestrictionSession, string]) (syncmodel.TableResult[syncmodel.RestrictionSession, string], error)
	SyncRestrictionLifecycleEvents(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.RestrictionLifecycleEvent, string]) (syncmodel.TableResult[syncmodel.RestrictionLifecycleEvent, string], error)
	SyncNFCLinkedChips(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.NFCLinkedChip, string]) (syncmodel.TableResult[syncmodel.NFCLinkedChip, string], error)
	SyncQRLinkedCodes(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.QRLinkedCode, string]) (syncmodel.TableResult[syncmodel.QRLinkedCode, string], error)
	SyncStreakSessionDailyRollups(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]) (syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey], error)
	ListStreakDailyAggregateChanges(ctx context.Context, db DBTX, userID string, cursor int64) (syncmodel.TableResult[syncmodel.StreakDailyAggregate, string], error)
	RecomputeStreakAggregates(ctx context.Context, db DBTX, userID string) error
}

// LeaderboardRefresher is satisfied by any type that can recompute leaderboard
// metrics for a single user. It allows sync operations to trigger a
// refresh without importing SocialRepository directly.
type LeaderboardRefresher interface {
	RefreshLeaderboardMetrics(ctx context.Context, db DBTX, userID string) error
}

type PgxSyncRepository struct {
	leaderboard LeaderboardRefresher
}

func NewPgxSyncRepository(leaderboard LeaderboardRefresher) *PgxSyncRepository {
	return &PgxSyncRepository{leaderboard: leaderboard}
}

var _ SyncRepository = (*PgxSyncRepository)(nil)

func computeNextCursor(inputCursor int64, versions []int64) int64 {
	maxV := inputCursor
	for _, v := range versions {
		if v > maxV {
			maxV = v
		}
	}
	return maxV
}

func (r *PgxSyncRepository) clearTombstones(ctx context.Context, db DBTX, userID, tableName string, recordIDs []string) error {
	if len(recordIDs) == 0 {
		return nil
	}
	args := []any{userID, tableName}
	placeholders := make([]string, len(recordIDs))
	for i, id := range recordIDs {
		args = append(args, id)
		placeholders[i] = fmt.Sprintf("$%d", i+3)
	}
	_, err := db.Exec(ctx,
		fmt.Sprintf(`DELETE FROM sync_tombstones WHERE user_id = $1 AND table_name = $2 AND record_id IN (%s)`,
			strings.Join(placeholders, ",")),
		args...)
	if err != nil {
		return fmt.Errorf("clearing tombstones for %s: %w", tableName, err)
	}
	return nil
}

func (r *PgxSyncRepository) SyncModes(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.Mode, string]) (syncmodel.TableResult[syncmodel.Mode, string], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}
	var versions []int64

	if len(in.Upserts) > 0 {
		var sb strings.Builder
		args := []any{userID}
		sb.WriteString(`INSERT INTO modes (user_id, id, title, text_on_screen, description, allowed_pauses_count, minimum_duration_ms, ending_pausing_scenario, icon_token, created_at, updated_at) VALUES `)
		for i, rec := range in.Upserts {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := len(args) + 1
			fmt.Fprintf(&sb, "($1, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				base, base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9)
			args = append(args, rec.ID, rec.Title, rec.TextOnScreen, rec.Description, rec.AllowedPausesCount, rec.MinimumDurationMS, rec.EndingPausingScenario, rec.IconToken, rec.CreatedAt, rec.UpdatedAt)
		}
		sb.WriteString(` ON CONFLICT (user_id, id) DO UPDATE SET
			title = EXCLUDED.title, text_on_screen = EXCLUDED.text_on_screen, description = EXCLUDED.description,
			allowed_pauses_count = EXCLUDED.allowed_pauses_count, minimum_duration_ms = EXCLUDED.minimum_duration_ms,
			ending_pausing_scenario = EXCLUDED.ending_pausing_scenario, icon_token = EXCLUDED.icon_token,
			created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at
			WHERE EXCLUDED.updated_at > modes.updated_at
			RETURNING id`)
		rows, err := db.Query(ctx, sb.String(), args...)
		if err != nil {
			return syncmodel.TableResult[syncmodel.Mode, string]{}, fmt.Errorf("upserting modes: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return syncmodel.TableResult[syncmodel.Mode, string]{}, fmt.Errorf("scanning upserted mode id: %w", err)
			}
			written[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableResult[syncmodel.Mode, string]{}, fmt.Errorf("iterating upserted mode ids: %w", err)
		}
	}

	if len(written) > 0 {
		keys := make([]string, 0, len(written))
		for k := range written {
			keys = append(keys, k)
		}
		if err := r.clearTombstones(ctx, db, userID, syncTableModes, keys); err != nil {
			return syncmodel.TableResult[syncmodel.Mode, string]{}, err
		}
	}

	var modeCascades map[string]map[string][]string
	if len(in.Deletions) > 0 {
		var err error
		modeCascades, err = r.collectModeCascade(ctx, db, userID, in.Deletions)
		if err != nil {
			return syncmodel.TableResult[syncmodel.Mode, string]{}, err
		}
	}
	for _, id := range in.Deletions {
		tag, err := db.Exec(ctx, "DELETE FROM modes WHERE user_id = $1 AND id = $2", userID, id)
		if err != nil {
			return syncmodel.TableResult[syncmodel.Mode, string]{}, fmt.Errorf("deleting mode: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableModes, id); err != nil {
			return syncmodel.TableResult[syncmodel.Mode, string]{}, err
		}
		deleted[id] = struct{}{}
		for tableName, recordIDs := range modeCascades[id] {
			for _, recordID := range recordIDs {
				if err := r.insertTombstone(ctx, db, userID, tableName, recordID); err != nil {
					return syncmodel.TableResult[syncmodel.Mode, string]{}, err
				}
			}
		}
	}

	rows, err := db.Query(ctx, `SELECT id, title, text_on_screen, description, allowed_pauses_count, minimum_duration_ms, ending_pausing_scenario, icon_token, created_at, updated_at, sync_version
		FROM modes
		WHERE user_id = $1 AND sync_version > $2
		ORDER BY sync_version`, userID, in.Cursor)
	if err != nil {
		return syncmodel.TableResult[syncmodel.Mode, string]{}, fmt.Errorf("listing changed modes: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.Mode
	for rows.Next() {
		var rec syncmodel.Mode
		var version int64
		if err := rows.Scan(&rec.ID, &rec.Title, &rec.TextOnScreen, &rec.Description, &rec.AllowedPausesCount, &rec.MinimumDurationMS, &rec.EndingPausingScenario, &rec.IconToken, &rec.CreatedAt, &rec.UpdatedAt, &version); err != nil {
			return syncmodel.TableResult[syncmodel.Mode, string]{}, fmt.Errorf("scanning changed mode: %w", err)
		}
		if _, ok := written[rec.ID]; ok {
			continue
		}
		upserts = append(upserts, rec)
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableResult[syncmodel.Mode, string]{}, fmt.Errorf("iterating changed modes: %w", err)
	}

	tombstoneIDs, tombMaxV, err := r.listTombstones(ctx, db, userID, syncTableModes, in.Cursor, deleted)
	if err != nil {
		return syncmodel.TableResult[syncmodel.Mode, string]{}, err
	}
	if tombMaxV > 0 {
		versions = append(versions, tombMaxV)
	}

	return syncmodel.TableResult[syncmodel.Mode, string]{
		NextCursor: computeNextCursor(in.Cursor, versions),
		Upserts:    upserts,
		Deletions:  tombstoneIDs,
	}, nil
}

func (r *PgxSyncRepository) SyncModeBlockedApps(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]) (syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}
	var versions []int64

	if len(in.Upserts) > 0 {
		var sb strings.Builder
		args := []any{userID}
		sb.WriteString(`INSERT INTO mode_blocked_apps (user_id, mode_id, platform, app_identifier, created_at, updated_at) VALUES `)
		for i, rec := range in.Upserts {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := len(args) + 1
			fmt.Fprintf(&sb, "($1, $%d, $%d, $%d, $%d, $%d)",
				base, base+1, base+2, base+3, base+4)
			args = append(args, rec.ModeID, rec.Platform, rec.AppIdentifier, rec.CreatedAt, rec.UpdatedAt)
		}
		sb.WriteString(` ON CONFLICT (user_id, mode_id, platform, app_identifier) DO UPDATE SET
			created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at
			WHERE EXCLUDED.updated_at > mode_blocked_apps.updated_at
			RETURNING mode_id, platform, app_identifier`)
		rows, err := db.Query(ctx, sb.String(), args...)
		if err != nil {
			return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("upserting mode_blocked_apps: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var modeID string
			var platform syncmodel.DevicePlatform
			var appIdentifier string
			if err := rows.Scan(&modeID, &platform, &appIdentifier); err != nil {
				return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("scanning upserted mode_blocked_app key: %w", err)
			}
			key, err := encodeModeBlockedAppKey(syncmodel.ModeBlockedAppKey{ModeID: modeID, Platform: platform, AppIdentifier: appIdentifier})
			if err != nil {
				return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, err
			}
			written[key] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("iterating upserted mode_blocked_app keys: %w", err)
		}
	}

	if len(written) > 0 {
		keys := make([]string, 0, len(written))
		for k := range written {
			keys = append(keys, k)
		}
		if err := r.clearTombstones(ctx, db, userID, syncTableModeBlockedApps, keys); err != nil {
			return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, err
		}
	}

	for _, keyObj := range in.Deletions {
		key, err := encodeModeBlockedAppKey(keyObj)
		if err != nil {
			return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, err
		}
		tag, err := db.Exec(ctx, "DELETE FROM mode_blocked_apps WHERE user_id = $1 AND mode_id = $2 AND platform = $3 AND app_identifier = $4", userID, keyObj.ModeID, keyObj.Platform, keyObj.AppIdentifier)
		if err != nil {
			return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("deleting mode_blocked_app: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableModeBlockedApps, key); err != nil {
			return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, err
		}
		deleted[key] = struct{}{}
	}

	rows, err := db.Query(ctx, `SELECT mode_id, platform, app_identifier, created_at, updated_at, sync_version
		FROM mode_blocked_apps
		WHERE user_id = $1 AND sync_version > $2
		ORDER BY sync_version`, userID, in.Cursor)
	if err != nil {
		return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("listing changed mode_blocked_apps: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.ModeBlockedApp
	for rows.Next() {
		var rec syncmodel.ModeBlockedApp
		var version int64
		if err := rows.Scan(&rec.ModeID, &rec.Platform, &rec.AppIdentifier, &rec.CreatedAt, &rec.UpdatedAt, &version); err != nil {
			return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("scanning changed mode_blocked_app: %w", err)
		}
		key, err := encodeModeBlockedAppKey(syncmodel.ModeBlockedAppKey{ModeID: rec.ModeID, Platform: rec.Platform, AppIdentifier: rec.AppIdentifier})
		if err != nil {
			return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, err
		}
		if _, ok := written[key]; ok {
			continue
		}
		upserts = append(upserts, rec)
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("iterating changed mode_blocked_apps: %w", err)
	}

	tombstones, tombMaxV, err := r.listTombstones(ctx, db, userID, syncTableModeBlockedApps, in.Cursor, deleted)
	if err != nil {
		return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, err
	}
	if tombMaxV > 0 {
		versions = append(versions, tombMaxV)
	}
	var outDeletions []syncmodel.ModeBlockedAppKey
	for _, encoded := range tombstones {
		key, err := decodeModeBlockedAppKey(encoded)
		if err != nil {
			return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, err
		}
		outDeletions = append(outDeletions, key)
	}

	return syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{
		NextCursor: computeNextCursor(in.Cursor, versions),
		Upserts:    upserts,
		Deletions:  outDeletions,
	}, nil
}

func (r *PgxSyncRepository) SyncSchedules(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.Schedule, string]) (syncmodel.TableResult[syncmodel.Schedule, string], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}
	var versions []int64

	if len(in.Upserts) > 0 {
		var sb strings.Builder
		args := []any{userID}
		sb.WriteString(`INSERT INTO schedules (user_id, id, mode_id, days, start_minute, end_minute, enabled, created_at, updated_at) VALUES `)
		for i, rec := range in.Upserts {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := len(args) + 1
			fmt.Fprintf(&sb, "($1, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				base, base+1, base+2, base+3, base+4, base+5, base+6, base+7)
			args = append(args, rec.ID, rec.ModeID, rec.Days, rec.StartMinute, rec.EndMinute, rec.Enabled, rec.CreatedAt, rec.UpdatedAt)
		}
		sb.WriteString(` ON CONFLICT (user_id, id) DO UPDATE SET
			mode_id = EXCLUDED.mode_id, days = EXCLUDED.days, start_minute = EXCLUDED.start_minute,
			end_minute = EXCLUDED.end_minute, enabled = EXCLUDED.enabled,
			created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at
			WHERE EXCLUDED.updated_at > schedules.updated_at
			RETURNING id`)
		rows, err := db.Query(ctx, sb.String(), args...)
		if err != nil {
			return syncmodel.TableResult[syncmodel.Schedule, string]{}, fmt.Errorf("upserting schedules: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return syncmodel.TableResult[syncmodel.Schedule, string]{}, fmt.Errorf("scanning upserted schedule id: %w", err)
			}
			written[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableResult[syncmodel.Schedule, string]{}, fmt.Errorf("iterating upserted schedule ids: %w", err)
		}
	}

	if len(written) > 0 {
		keys := make([]string, 0, len(written))
		for k := range written {
			keys = append(keys, k)
		}
		if err := r.clearTombstones(ctx, db, userID, syncTableSchedules, keys); err != nil {
			return syncmodel.TableResult[syncmodel.Schedule, string]{}, err
		}
	}

	for _, id := range in.Deletions {
		tag, err := db.Exec(ctx, "DELETE FROM schedules WHERE user_id = $1 AND id = $2", userID, id)
		if err != nil {
			return syncmodel.TableResult[syncmodel.Schedule, string]{}, fmt.Errorf("deleting schedule: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableSchedules, id); err != nil {
			return syncmodel.TableResult[syncmodel.Schedule, string]{}, err
		}
		deleted[id] = struct{}{}
	}

	rows, err := db.Query(ctx, `SELECT id, mode_id, days, start_minute, end_minute, enabled, created_at, updated_at, sync_version
		FROM schedules
		WHERE user_id = $1 AND sync_version > $2
		ORDER BY sync_version`, userID, in.Cursor)
	if err != nil {
		return syncmodel.TableResult[syncmodel.Schedule, string]{}, fmt.Errorf("listing changed schedules: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.Schedule
	for rows.Next() {
		var rec syncmodel.Schedule
		var version int64
		if err := rows.Scan(&rec.ID, &rec.ModeID, &rec.Days, &rec.StartMinute, &rec.EndMinute, &rec.Enabled, &rec.CreatedAt, &rec.UpdatedAt, &version); err != nil {
			return syncmodel.TableResult[syncmodel.Schedule, string]{}, fmt.Errorf("scanning changed schedule: %w", err)
		}
		if _, ok := written[rec.ID]; ok {
			continue
		}
		upserts = append(upserts, rec)
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableResult[syncmodel.Schedule, string]{}, fmt.Errorf("iterating changed schedules: %w", err)
	}

	tombstoneIDs, tombMaxV, err := r.listTombstones(ctx, db, userID, syncTableSchedules, in.Cursor, deleted)
	if err != nil {
		return syncmodel.TableResult[syncmodel.Schedule, string]{}, err
	}
	if tombMaxV > 0 {
		versions = append(versions, tombMaxV)
	}

	return syncmodel.TableResult[syncmodel.Schedule, string]{
		NextCursor: computeNextCursor(in.Cursor, versions),
		Upserts:    upserts,
		Deletions:  tombstoneIDs,
	}, nil
}

func (r *PgxSyncRepository) SyncRestrictionSessions(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.RestrictionSession, string]) (syncmodel.TableResult[syncmodel.RestrictionSession, string], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}
	var versions []int64

	if len(in.Upserts) > 0 {
		var sb strings.Builder
		args := []any{userID}
		sb.WriteString(`INSERT INTO restriction_sessions (user_id, session_id, mode_id, source, started_at, ended_at, pause_count, total_paused_ms, last_paused_at, integrity_status, last_anomaly_reason, last_event_id, created_at, updated_at) VALUES `)
		for i, rec := range in.Upserts {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := len(args) + 1
			fmt.Fprintf(&sb, "($1, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				base, base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11, base+12)
			args = append(args, rec.SessionID, rec.ModeID, rec.Source, rec.StartedAt, rec.EndedAt, rec.PauseCount, rec.TotalPausedMS, rec.LastPausedAt, rec.IntegrityStatus, rec.LastAnomalyReason, rec.LastEventID, rec.CreatedAt, rec.UpdatedAt)
		}
		sb.WriteString(` ON CONFLICT (user_id, session_id) DO UPDATE SET
			mode_id = EXCLUDED.mode_id, source = EXCLUDED.source, started_at = EXCLUDED.started_at,
			ended_at = EXCLUDED.ended_at, pause_count = EXCLUDED.pause_count, total_paused_ms = EXCLUDED.total_paused_ms,
			last_paused_at = EXCLUDED.last_paused_at, integrity_status = EXCLUDED.integrity_status,
			last_anomaly_reason = EXCLUDED.last_anomaly_reason, last_event_id = EXCLUDED.last_event_id,
			created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at
			WHERE EXCLUDED.updated_at > restriction_sessions.updated_at
			RETURNING session_id`)
		rows, err := db.Query(ctx, sb.String(), args...)
		if err != nil {
			return syncmodel.TableResult[syncmodel.RestrictionSession, string]{}, fmt.Errorf("upserting restriction_sessions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sessionID string
			if err := rows.Scan(&sessionID); err != nil {
				return syncmodel.TableResult[syncmodel.RestrictionSession, string]{}, fmt.Errorf("scanning upserted restriction_session id: %w", err)
			}
			written[sessionID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableResult[syncmodel.RestrictionSession, string]{}, fmt.Errorf("iterating upserted restriction_session ids: %w", err)
		}
	}

	if len(written) > 0 {
		keys := make([]string, 0, len(written))
		for k := range written {
			keys = append(keys, k)
		}
		if err := r.clearTombstones(ctx, db, userID, syncTableRestrictionSessions, keys); err != nil {
			return syncmodel.TableResult[syncmodel.RestrictionSession, string]{}, err
		}
	}

	var sessionCascades map[string]map[string][]string
	if len(in.Deletions) > 0 {
		var err error
		sessionCascades, err = r.collectRestrictionSessionCascade(ctx, db, userID, in.Deletions)
		if err != nil {
			return syncmodel.TableResult[syncmodel.RestrictionSession, string]{}, err
		}
	}
	for _, sessionID := range in.Deletions {
		tag, err := db.Exec(ctx, "DELETE FROM restriction_sessions WHERE user_id = $1 AND session_id = $2", userID, sessionID)
		if err != nil {
			return syncmodel.TableResult[syncmodel.RestrictionSession, string]{}, fmt.Errorf("deleting restriction_session: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableRestrictionSessions, sessionID); err != nil {
			return syncmodel.TableResult[syncmodel.RestrictionSession, string]{}, err
		}
		deleted[sessionID] = struct{}{}
		for tableName, recordIDs := range sessionCascades[sessionID] {
			for _, recordID := range recordIDs {
				if err := r.insertTombstone(ctx, db, userID, tableName, recordID); err != nil {
					return syncmodel.TableResult[syncmodel.RestrictionSession, string]{}, err
				}
			}
		}
	}

	rows, err := db.Query(ctx, `SELECT session_id, mode_id, source, started_at, ended_at, pause_count, total_paused_ms, last_paused_at, integrity_status, last_anomaly_reason, last_event_id, created_at, updated_at, sync_version
		FROM restriction_sessions
		WHERE user_id = $1 AND sync_version > $2
		ORDER BY sync_version`, userID, in.Cursor)
	if err != nil {
		return syncmodel.TableResult[syncmodel.RestrictionSession, string]{}, fmt.Errorf("listing changed restriction_sessions: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.RestrictionSession
	for rows.Next() {
		var rec syncmodel.RestrictionSession
		var version int64
		if err := rows.Scan(&rec.SessionID, &rec.ModeID, &rec.Source, &rec.StartedAt, &rec.EndedAt, &rec.PauseCount, &rec.TotalPausedMS, &rec.LastPausedAt, &rec.IntegrityStatus, &rec.LastAnomalyReason, &rec.LastEventID, &rec.CreatedAt, &rec.UpdatedAt, &version); err != nil {
			return syncmodel.TableResult[syncmodel.RestrictionSession, string]{}, fmt.Errorf("scanning changed restriction_session: %w", err)
		}
		if _, ok := written[rec.SessionID]; ok {
			continue
		}
		upserts = append(upserts, rec)
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableResult[syncmodel.RestrictionSession, string]{}, fmt.Errorf("iterating changed restriction_sessions: %w", err)
	}

	tombstoneIDs, tombMaxV, err := r.listTombstones(ctx, db, userID, syncTableRestrictionSessions, in.Cursor, deleted)
	if err != nil {
		return syncmodel.TableResult[syncmodel.RestrictionSession, string]{}, err
	}
	if tombMaxV > 0 {
		versions = append(versions, tombMaxV)
	}

	return syncmodel.TableResult[syncmodel.RestrictionSession, string]{
		NextCursor: computeNextCursor(in.Cursor, versions),
		Upserts:    upserts,
		Deletions:  tombstoneIDs,
	}, nil
}

func (r *PgxSyncRepository) SyncRestrictionLifecycleEvents(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.RestrictionLifecycleEvent, string]) (syncmodel.TableResult[syncmodel.RestrictionLifecycleEvent, string], error) {
	written := map[string]struct{}{}
	var versions []int64

	if len(in.Upserts) > 0 {
		var sb strings.Builder
		args := []any{userID}
		sb.WriteString(`WITH inserted AS (INSERT INTO restriction_lifecycle_events (user_id, id, session_id, mode_id, action, source, reason, occurred_at, created_at) VALUES `)
		for i, rec := range in.Upserts {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := len(args) + 1
			fmt.Fprintf(&sb, "($1, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				base, base+1, base+2, base+3, base+4, base+5, base+6, base+7)
			args = append(args, rec.ID, rec.SessionID, rec.ModeID, rec.Action, rec.Source, rec.Reason, rec.OccurredAt, rec.CreatedAt)
		}
		sb.WriteString(` ON CONFLICT (user_id, id) DO NOTHING
			RETURNING id) SELECT id FROM inserted`)
		rows, err := db.Query(ctx, sb.String(), args...)
		if err != nil {
			return syncmodel.TableResult[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("upserting restriction_lifecycle_events: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return syncmodel.TableResult[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("scanning upserted restriction_lifecycle_event id: %w", err)
			}
			written[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableResult[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("iterating upserted restriction_lifecycle_event ids: %w", err)
		}
	}

	rows, err := db.Query(ctx, `SELECT id, session_id, mode_id, action, source, reason, occurred_at, created_at, sync_version
		FROM restriction_lifecycle_events
		WHERE user_id = $1 AND sync_version > $2
		ORDER BY sync_version`, userID, in.Cursor)
	if err != nil {
		return syncmodel.TableResult[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("listing changed restriction_lifecycle_events: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.RestrictionLifecycleEvent
	for rows.Next() {
		var rec syncmodel.RestrictionLifecycleEvent
		var version int64
		if err := rows.Scan(&rec.ID, &rec.SessionID, &rec.ModeID, &rec.Action, &rec.Source, &rec.Reason, &rec.OccurredAt, &rec.CreatedAt, &version); err != nil {
			return syncmodel.TableResult[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("scanning changed restriction_lifecycle_event: %w", err)
		}
		if _, ok := written[rec.ID]; ok {
			continue
		}
		upserts = append(upserts, rec)
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableResult[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("iterating changed restriction_lifecycle_events: %w", err)
	}

	tombstoneIDs, tombMaxV, err := r.listTombstones(ctx, db, userID, syncTableRestrictionLifecycleEvents, in.Cursor, nil)
	if err != nil {
		return syncmodel.TableResult[syncmodel.RestrictionLifecycleEvent, string]{}, err
	}
	if tombMaxV > 0 {
		versions = append(versions, tombMaxV)
	}

	return syncmodel.TableResult[syncmodel.RestrictionLifecycleEvent, string]{
		NextCursor: computeNextCursor(in.Cursor, versions),
		Upserts:    upserts,
		Deletions:  tombstoneIDs,
	}, nil
}

func (r *PgxSyncRepository) SyncNFCLinkedChips(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.NFCLinkedChip, string]) (syncmodel.TableResult[syncmodel.NFCLinkedChip, string], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}
	var versions []int64

	if len(in.Upserts) > 0 {
		var sb strings.Builder
		args := []any{userID}
		sb.WriteString(`INSERT INTO nfc_linked_chips (user_id, id, chip_identifier, name, created_at, updated_at) VALUES `)
		for i, rec := range in.Upserts {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := len(args) + 1
			fmt.Fprintf(&sb, "($1, $%d, $%d, $%d, $%d, $%d)",
				base, base+1, base+2, base+3, base+4)
			args = append(args, rec.ID, rec.ChipIdentifier, rec.Name, rec.CreatedAt, rec.UpdatedAt)
		}
		sb.WriteString(` ON CONFLICT (user_id, id) DO UPDATE SET
			chip_identifier = EXCLUDED.chip_identifier, name = EXCLUDED.name,
			created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at
			WHERE EXCLUDED.updated_at > nfc_linked_chips.updated_at
			RETURNING id`)
		rows, err := db.Query(ctx, sb.String(), args...)
		if err != nil {
			return syncmodel.TableResult[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("upserting nfc_linked_chips: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return syncmodel.TableResult[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("scanning upserted nfc_linked_chip id: %w", err)
			}
			written[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableResult[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("iterating upserted nfc_linked_chip ids: %w", err)
		}
	}

	if len(written) > 0 {
		keys := make([]string, 0, len(written))
		for k := range written {
			keys = append(keys, k)
		}
		if err := r.clearTombstones(ctx, db, userID, syncTableNFCLinkedChips, keys); err != nil {
			return syncmodel.TableResult[syncmodel.NFCLinkedChip, string]{}, err
		}
	}

	for _, id := range in.Deletions {
		tag, err := db.Exec(ctx, "DELETE FROM nfc_linked_chips WHERE user_id = $1 AND id = $2", userID, id)
		if err != nil {
			return syncmodel.TableResult[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("deleting nfc_linked_chip: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableNFCLinkedChips, id); err != nil {
			return syncmodel.TableResult[syncmodel.NFCLinkedChip, string]{}, err
		}
		deleted[id] = struct{}{}
	}

	rows, err := db.Query(ctx, `SELECT id, chip_identifier, name, created_at, updated_at, sync_version
		FROM nfc_linked_chips
		WHERE user_id = $1 AND sync_version > $2
		ORDER BY sync_version`, userID, in.Cursor)
	if err != nil {
		return syncmodel.TableResult[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("listing changed nfc_linked_chips: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.NFCLinkedChip
	for rows.Next() {
		var rec syncmodel.NFCLinkedChip
		var version int64
		if err := rows.Scan(&rec.ID, &rec.ChipIdentifier, &rec.Name, &rec.CreatedAt, &rec.UpdatedAt, &version); err != nil {
			return syncmodel.TableResult[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("scanning changed nfc_linked_chip: %w", err)
		}
		if _, ok := written[rec.ID]; ok {
			continue
		}
		upserts = append(upserts, rec)
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableResult[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("iterating changed nfc_linked_chips: %w", err)
	}

	tombstoneIDs, tombMaxV, err := r.listTombstones(ctx, db, userID, syncTableNFCLinkedChips, in.Cursor, deleted)
	if err != nil {
		return syncmodel.TableResult[syncmodel.NFCLinkedChip, string]{}, err
	}
	if tombMaxV > 0 {
		versions = append(versions, tombMaxV)
	}

	return syncmodel.TableResult[syncmodel.NFCLinkedChip, string]{
		NextCursor: computeNextCursor(in.Cursor, versions),
		Upserts:    upserts,
		Deletions:  tombstoneIDs,
	}, nil
}

func (r *PgxSyncRepository) SyncQRLinkedCodes(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.QRLinkedCode, string]) (syncmodel.TableResult[syncmodel.QRLinkedCode, string], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}
	var versions []int64

	if len(in.Upserts) > 0 {
		var sb strings.Builder
		args := []any{userID}
		sb.WriteString(`INSERT INTO qr_linked_codes (user_id, id, scan_value, name, created_at, updated_at) VALUES `)
		for i, rec := range in.Upserts {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := len(args) + 1
			fmt.Fprintf(&sb, "($1, $%d, $%d, $%d, $%d, $%d)",
				base, base+1, base+2, base+3, base+4)
			args = append(args, rec.ID, rec.ScanValue, rec.Name, rec.CreatedAt, rec.UpdatedAt)
		}
		sb.WriteString(` ON CONFLICT (user_id, id) DO UPDATE SET
			scan_value = EXCLUDED.scan_value, name = EXCLUDED.name,
			created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at
			WHERE EXCLUDED.updated_at > qr_linked_codes.updated_at
			RETURNING id`)
		rows, err := db.Query(ctx, sb.String(), args...)
		if err != nil {
			return syncmodel.TableResult[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("upserting qr_linked_codes: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return syncmodel.TableResult[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("scanning upserted qr_linked_code id: %w", err)
			}
			written[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableResult[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("iterating upserted qr_linked_code ids: %w", err)
		}
	}

	if len(written) > 0 {
		keys := make([]string, 0, len(written))
		for k := range written {
			keys = append(keys, k)
		}
		if err := r.clearTombstones(ctx, db, userID, syncTableQRLinkedCodes, keys); err != nil {
			return syncmodel.TableResult[syncmodel.QRLinkedCode, string]{}, err
		}
	}

	for _, id := range in.Deletions {
		tag, err := db.Exec(ctx, "DELETE FROM qr_linked_codes WHERE user_id = $1 AND id = $2", userID, id)
		if err != nil {
			return syncmodel.TableResult[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("deleting qr_linked_code: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableQRLinkedCodes, id); err != nil {
			return syncmodel.TableResult[syncmodel.QRLinkedCode, string]{}, err
		}
		deleted[id] = struct{}{}
	}

	rows, err := db.Query(ctx, `SELECT id, scan_value, name, created_at, updated_at, sync_version
		FROM qr_linked_codes
		WHERE user_id = $1 AND sync_version > $2
		ORDER BY sync_version`, userID, in.Cursor)
	if err != nil {
		return syncmodel.TableResult[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("listing changed qr_linked_codes: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.QRLinkedCode
	for rows.Next() {
		var rec syncmodel.QRLinkedCode
		var version int64
		if err := rows.Scan(&rec.ID, &rec.ScanValue, &rec.Name, &rec.CreatedAt, &rec.UpdatedAt, &version); err != nil {
			return syncmodel.TableResult[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("scanning changed qr_linked_code: %w", err)
		}
		if _, ok := written[rec.ID]; ok {
			continue
		}
		upserts = append(upserts, rec)
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableResult[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("iterating changed qr_linked_codes: %w", err)
	}

	tombstoneIDs, tombMaxV, err := r.listTombstones(ctx, db, userID, syncTableQRLinkedCodes, in.Cursor, deleted)
	if err != nil {
		return syncmodel.TableResult[syncmodel.QRLinkedCode, string]{}, err
	}
	if tombMaxV > 0 {
		versions = append(versions, tombMaxV)
	}

	return syncmodel.TableResult[syncmodel.QRLinkedCode, string]{
		NextCursor: computeNextCursor(in.Cursor, versions),
		Upserts:    upserts,
		Deletions:  tombstoneIDs,
	}, nil
}

func (r *PgxSyncRepository) SyncStreakSessionDailyRollups(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]) (syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}
	var versions []int64

	if len(in.Upserts) > 0 {
		var sb strings.Builder
		args := []any{userID}
		sb.WriteString(`INSERT INTO streak_session_daily_rollups (user_id, session_id, local_day, effective_ms, updated_at) VALUES `)
		for i, rec := range in.Upserts {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := len(args) + 1
			fmt.Fprintf(&sb, "($1, $%d, $%d, $%d, $%d)",
				base, base+1, base+2, base+3)
			args = append(args, rec.SessionID, rec.LocalDay, rec.EffectiveMS, rec.UpdatedAt)
		}
		sb.WriteString(` ON CONFLICT (user_id, session_id, local_day) DO UPDATE SET
			effective_ms = EXCLUDED.effective_ms, updated_at = EXCLUDED.updated_at
			WHERE EXCLUDED.updated_at > streak_session_daily_rollups.updated_at
			RETURNING session_id, local_day`)
		rows, err := db.Query(ctx, sb.String(), args...)
		if err != nil {
			return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("upserting streak_session_daily_rollups: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sessionID, localDay string
			if err := rows.Scan(&sessionID, &localDay); err != nil {
				return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("scanning upserted streak_session_daily_rollup key: %w", err)
			}
			key, err := encodeStreakSessionDailyRollupKey(syncmodel.StreakSessionDailyRollupKey{SessionID: sessionID, LocalDay: localDay})
			if err != nil {
				return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, err
			}
			written[key] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("iterating upserted streak_session_daily_rollup keys: %w", err)
		}
	}

	if len(written) > 0 {
		keys := make([]string, 0, len(written))
		for k := range written {
			keys = append(keys, k)
		}
		if err := r.clearTombstones(ctx, db, userID, syncTableStreakSessionDailyRollups, keys); err != nil {
			return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, err
		}
	}

	for _, keyObj := range in.Deletions {
		key, err := encodeStreakSessionDailyRollupKey(keyObj)
		if err != nil {
			return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, err
		}
		tag, err := db.Exec(ctx, "DELETE FROM streak_session_daily_rollups WHERE user_id = $1 AND session_id = $2 AND local_day = $3", userID, keyObj.SessionID, keyObj.LocalDay)
		if err != nil {
			return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("deleting streak_session_daily_rollup: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableStreakSessionDailyRollups, key); err != nil {
			return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, err
		}
		deleted[key] = struct{}{}
	}

	rows, err := db.Query(ctx, `SELECT session_id, local_day, effective_ms, updated_at, sync_version
		FROM streak_session_daily_rollups
		WHERE user_id = $1 AND sync_version > $2
		ORDER BY sync_version`, userID, in.Cursor)
	if err != nil {
		return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("listing changed streak_session_daily_rollups: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.StreakSessionDailyRollup
	for rows.Next() {
		var rec syncmodel.StreakSessionDailyRollup
		var version int64
		if err := rows.Scan(&rec.SessionID, &rec.LocalDay, &rec.EffectiveMS, &rec.UpdatedAt, &version); err != nil {
			return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("scanning changed streak_session_daily_rollup: %w", err)
		}
		key, err := encodeStreakSessionDailyRollupKey(syncmodel.StreakSessionDailyRollupKey{SessionID: rec.SessionID, LocalDay: rec.LocalDay})
		if err != nil {
			return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, err
		}
		if _, ok := written[key]; ok {
			continue
		}
		upserts = append(upserts, rec)
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("iterating changed streak_session_daily_rollups: %w", err)
	}

	tombstones, tombMaxV, err := r.listTombstones(ctx, db, userID, syncTableStreakSessionDailyRollups, in.Cursor, deleted)
	if err != nil {
		return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, err
	}
	if tombMaxV > 0 {
		versions = append(versions, tombMaxV)
	}
	var outDeletions []syncmodel.StreakSessionDailyRollupKey
	for _, encoded := range tombstones {
		key, err := decodeStreakSessionDailyRollupKey(encoded)
		if err != nil {
			return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, err
		}
		outDeletions = append(outDeletions, key)
	}

	return syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{
		NextCursor: computeNextCursor(in.Cursor, versions),
		Upserts:    upserts,
		Deletions:  outDeletions,
	}, nil
}

func (r *PgxSyncRepository) ListStreakDailyAggregateChanges(ctx context.Context, db DBTX, userID string, cursor int64) (syncmodel.TableResult[syncmodel.StreakDailyAggregate, string], error) {
	var versions []int64
	rows, err := db.Query(ctx,
		`SELECT local_day, effective_ms, qualified, source_session_count, updated_at, sync_version
		 FROM streak_daily_aggregates
		 WHERE user_id = $1 AND sync_version > $2
		 ORDER BY sync_version`, userID, cursor)
	if err != nil {
		return syncmodel.TableResult[syncmodel.StreakDailyAggregate, string]{}, fmt.Errorf("listing changed streak aggregates: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.StreakDailyAggregate
	for rows.Next() {
		var rec syncmodel.StreakDailyAggregate
		var version int64
		if err := rows.Scan(&rec.LocalDay, &rec.EffectiveMS, &rec.Qualified, &rec.SourceSessionCount, &rec.UpdatedAt, &version); err != nil {
			return syncmodel.TableResult[syncmodel.StreakDailyAggregate, string]{}, fmt.Errorf("scanning streak aggregate: %w", err)
		}
		upserts = append(upserts, rec)
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableResult[syncmodel.StreakDailyAggregate, string]{}, err
	}

	tombstoneIDs, tombMaxV, err := r.listTombstones(ctx, db, userID, syncTableStreakDailyAggregates, cursor, nil)
	if err != nil {
		return syncmodel.TableResult[syncmodel.StreakDailyAggregate, string]{}, err
	}
	if tombMaxV > 0 {
		versions = append(versions, tombMaxV)
	}

	return syncmodel.TableResult[syncmodel.StreakDailyAggregate, string]{
		NextCursor: computeNextCursor(cursor, versions),
		Upserts:    upserts,
		Deletions:  tombstoneIDs,
	}, nil
}

func (r *PgxSyncRepository) RecomputeStreakAggregates(ctx context.Context, db DBTX, userID string) error {
	_, err := db.Exec(ctx, `
		INSERT INTO streak_daily_aggregates (user_id, local_day, effective_ms, qualified, source_session_count, updated_at)
		SELECT $1, local_day, SUM(effective_ms),
			CASE WHEN SUM(effective_ms) >= 1800000 THEN 1 ELSE 0 END,
			COUNT(DISTINCT session_id),
			EXTRACT(EPOCH FROM now())::bigint * 1000
		FROM streak_session_daily_rollups WHERE user_id = $1
		GROUP BY local_day
		ON CONFLICT (user_id, local_day) DO UPDATE SET
			effective_ms = EXCLUDED.effective_ms,
			qualified = EXCLUDED.qualified,
			source_session_count = EXCLUDED.source_session_count,
			updated_at = EXCLUDED.updated_at`, userID)
	if err != nil {
		return fmt.Errorf("recomputing streak aggregates: %w", err)
	}

	rows, err := db.Query(ctx, `
		DELETE FROM streak_daily_aggregates
		WHERE user_id = $1
		  AND local_day NOT IN (SELECT DISTINCT local_day FROM streak_session_daily_rollups WHERE user_id = $1)
		RETURNING local_day`, userID)
	if err != nil {
		return fmt.Errorf("deleting orphaned streak aggregates: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var localDay string
		if err := rows.Scan(&localDay); err != nil {
			return fmt.Errorf("scanning deleted aggregate day: %w", err)
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableStreakDailyAggregates, localDay); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (r *PgxSyncRepository) insertTombstone(ctx context.Context, db DBTX, userID string, tableName string, recordID string) error {
	_, err := db.Exec(ctx,
		`INSERT INTO sync_tombstones (user_id, table_name, record_id)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, table_name, record_id) DO UPDATE SET
		   deleted_at = now(),
		   sync_version = DEFAULT`,
		userID, tableName, recordID,
	)
	if err != nil {
		return fmt.Errorf("inserting tombstone for %s: %w", tableName, err)
	}
	return nil
}

func (r *PgxSyncRepository) listTombstones(ctx context.Context, db DBTX, userID, tableName string, cursor int64, excludeIDs map[string]struct{}) ([]string, int64, error) {
	rows, err := db.Query(ctx,
		`SELECT record_id, sync_version FROM sync_tombstones
		 WHERE user_id = $1 AND table_name = $2 AND sync_version > $3
		 ORDER BY sync_version`,
		userID, tableName, cursor)
	if err != nil {
		return nil, 0, fmt.Errorf("listing tombstones for %s: %w", tableName, err)
	}
	defer rows.Close()

	var ids []string
	var maxVersion int64
	for rows.Next() {
		var id string
		var version int64
		if err := rows.Scan(&id, &version); err != nil {
			return nil, 0, fmt.Errorf("scanning tombstone for %s: %w", tableName, err)
		}
		if _, excluded := excludeIDs[id]; excluded {
			continue
		}
		ids = append(ids, id)
		if version > maxVersion {
			maxVersion = version
		}
	}
	return ids, maxVersion, rows.Err()
}

func encodeModeBlockedAppKey(in syncmodel.ModeBlockedAppKey) (string, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return "", fmt.Errorf("encoding mode_blocked_apps key: %w", err)
	}
	return string(b), nil
}

func decodeModeBlockedAppKey(encoded string) (syncmodel.ModeBlockedAppKey, error) {
	var out syncmodel.ModeBlockedAppKey
	if err := json.Unmarshal([]byte(encoded), &out); err != nil {
		return syncmodel.ModeBlockedAppKey{}, fmt.Errorf("decoding mode_blocked_apps key: %w", err)
	}
	return out, nil
}

func encodeStreakSessionDailyRollupKey(in syncmodel.StreakSessionDailyRollupKey) (string, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return "", fmt.Errorf("encoding streak_session_daily_rollups key: %w", err)
	}
	return string(b), nil
}

func decodeStreakSessionDailyRollupKey(encoded string) (syncmodel.StreakSessionDailyRollupKey, error) {
	var out syncmodel.StreakSessionDailyRollupKey
	if err := json.Unmarshal([]byte(encoded), &out); err != nil {
		return syncmodel.StreakSessionDailyRollupKey{}, fmt.Errorf("decoding streak_session_daily_rollups key: %w", err)
	}
	return out, nil
}

// collectModeCascade returns, for each modeID in the given slice, the set of
// dependent records that must be tombstoned when that mode is deleted.
// All sub-queries are batched with ANY($2) to avoid N+1 round-trips.
// Result shape: modeID -> tableName -> []recordID.
func (r *PgxSyncRepository) collectModeCascade(ctx context.Context, db DBTX, userID string, modeIDs []string) (map[string]map[string][]string, error) {
	out := make(map[string]map[string][]string, len(modeIDs))
	for _, id := range modeIDs {
		out[id] = map[string][]string{}
	}

	mbaRows, err := db.Query(ctx, `SELECT mode_id, platform, app_identifier FROM mode_blocked_apps WHERE user_id = $1 AND mode_id = ANY($2)`, userID, modeIDs)
	if err != nil {
		return nil, fmt.Errorf("querying mode_blocked_apps cascade: %w", err)
	}
	for mbaRows.Next() {
		var modeIDVal, platform, appIdentifier string
		if err := mbaRows.Scan(&modeIDVal, &platform, &appIdentifier); err != nil {
			mbaRows.Close()
			return nil, fmt.Errorf("scanning mode_blocked_apps cascade: %w", err)
		}
		key, err := encodeModeBlockedAppKey(syncmodel.ModeBlockedAppKey{ModeID: modeIDVal, Platform: syncmodel.DevicePlatform(platform), AppIdentifier: appIdentifier})
		if err != nil {
			mbaRows.Close()
			return nil, err
		}
		out[modeIDVal][syncTableModeBlockedApps] = append(out[modeIDVal][syncTableModeBlockedApps], key)
	}
	mbaRows.Close()
	if err := mbaRows.Err(); err != nil {
		return nil, fmt.Errorf("iterating mode_blocked_apps cascade: %w", err)
	}

	schedRows, err := db.Query(ctx, `SELECT id, mode_id FROM schedules WHERE user_id = $1 AND mode_id = ANY($2)`, userID, modeIDs)
	if err != nil {
		return nil, fmt.Errorf("querying schedules cascade: %w", err)
	}
	for schedRows.Next() {
		var id, modeIDVal string
		if err := schedRows.Scan(&id, &modeIDVal); err != nil {
			schedRows.Close()
			return nil, fmt.Errorf("scanning schedules cascade: %w", err)
		}
		out[modeIDVal][syncTableSchedules] = append(out[modeIDVal][syncTableSchedules], id)
	}
	schedRows.Close()
	if err := schedRows.Err(); err != nil {
		return nil, fmt.Errorf("iterating schedules cascade: %w", err)
	}

	sessRows, err := db.Query(ctx, `SELECT session_id, mode_id FROM restriction_sessions WHERE user_id = $1 AND mode_id = ANY($2)`, userID, modeIDs)
	if err != nil {
		return nil, fmt.Errorf("querying restriction_sessions cascade: %w", err)
	}
	// Collect session IDs per mode for the rollup query below.
	allSessions := make([]string, 0)
	for sessRows.Next() {
		var sessionID, modeIDVal string
		if err := sessRows.Scan(&sessionID, &modeIDVal); err != nil {
			sessRows.Close()
			return nil, fmt.Errorf("scanning restriction_sessions cascade: %w", err)
		}
		allSessions = append(allSessions, sessionID)
		out[modeIDVal][syncTableRestrictionSessions] = append(out[modeIDVal][syncTableRestrictionSessions], sessionID)
	}
	sessRows.Close()
	if err := sessRows.Err(); err != nil {
		return nil, fmt.Errorf("iterating restriction_sessions cascade: %w", err)
	}

	rleRows, err := db.Query(ctx, `SELECT id, mode_id FROM restriction_lifecycle_events WHERE user_id = $1 AND mode_id = ANY($2)`, userID, modeIDs)
	if err != nil {
		return nil, fmt.Errorf("querying restriction_lifecycle_events cascade: %w", err)
	}
	for rleRows.Next() {
		var id, modeIDVal string
		if err := rleRows.Scan(&id, &modeIDVal); err != nil {
			rleRows.Close()
			return nil, fmt.Errorf("scanning restriction_lifecycle_events cascade: %w", err)
		}
		out[modeIDVal][syncTableRestrictionLifecycleEvents] = append(out[modeIDVal][syncTableRestrictionLifecycleEvents], id)
	}
	rleRows.Close()
	if err := rleRows.Err(); err != nil {
		return nil, fmt.Errorf("iterating restriction_lifecycle_events cascade: %w", err)
	}

	if len(allSessions) > 0 {
		// Build session->modeID reverse map to attribute rollups back to their mode.
		sessionToMode := make(map[string]string, len(allSessions))
		for _, mid := range modeIDs {
			for _, sid := range out[mid][syncTableRestrictionSessions] {
				sessionToMode[sid] = mid
			}
		}
		rollupRows, err := db.Query(ctx, `SELECT session_id, local_day FROM streak_session_daily_rollups WHERE user_id = $1 AND session_id = ANY($2)`, userID, allSessions)
		if err != nil {
			return nil, fmt.Errorf("querying streak_session_daily_rollups cascade: %w", err)
		}
		for rollupRows.Next() {
			var sessionID, localDay string
			if err := rollupRows.Scan(&sessionID, &localDay); err != nil {
				rollupRows.Close()
				return nil, fmt.Errorf("scanning streak_session_daily_rollups cascade: %w", err)
			}
			key, err := encodeStreakSessionDailyRollupKey(syncmodel.StreakSessionDailyRollupKey{SessionID: sessionID, LocalDay: localDay})
			if err != nil {
				rollupRows.Close()
				return nil, err
			}
			modeIDVal := sessionToMode[sessionID]
			out[modeIDVal][syncTableStreakSessionDailyRollups] = append(out[modeIDVal][syncTableStreakSessionDailyRollups], key)
		}
		rollupRows.Close()
		if err := rollupRows.Err(); err != nil {
			return nil, fmt.Errorf("iterating streak_session_daily_rollups cascade: %w", err)
		}
	}

	return out, nil
}

// collectRestrictionSessionCascade returns, for each sessionID in the given
// slice, the set of dependent records that must be tombstoned on deletion.
// All sub-queries are batched with ANY($2) to avoid N+1 round-trips.
// Result shape: sessionID -> tableName -> []recordID.
func (r *PgxSyncRepository) collectRestrictionSessionCascade(ctx context.Context, db DBTX, userID string, sessionIDs []string) (map[string]map[string][]string, error) {
	out := make(map[string]map[string][]string, len(sessionIDs))
	for _, id := range sessionIDs {
		out[id] = map[string][]string{}
	}

	rleRows, err := db.Query(ctx, `SELECT id, session_id FROM restriction_lifecycle_events WHERE user_id = $1 AND session_id = ANY($2)`, userID, sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("querying restriction_lifecycle_events session cascade: %w", err)
	}
	for rleRows.Next() {
		var id, sessionIDVal string
		if err := rleRows.Scan(&id, &sessionIDVal); err != nil {
			rleRows.Close()
			return nil, fmt.Errorf("scanning restriction_lifecycle_events session cascade: %w", err)
		}
		out[sessionIDVal][syncTableRestrictionLifecycleEvents] = append(out[sessionIDVal][syncTableRestrictionLifecycleEvents], id)
	}
	rleRows.Close()
	if err := rleRows.Err(); err != nil {
		return nil, fmt.Errorf("iterating restriction_lifecycle_events session cascade: %w", err)
	}

	rollupRows, err := db.Query(ctx, `SELECT session_id, local_day FROM streak_session_daily_rollups WHERE user_id = $1 AND session_id = ANY($2)`, userID, sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("querying streak_session_daily_rollups session cascade: %w", err)
	}
	for rollupRows.Next() {
		var sessionIDVal, localDay string
		if err := rollupRows.Scan(&sessionIDVal, &localDay); err != nil {
			rollupRows.Close()
			return nil, fmt.Errorf("scanning streak_session_daily_rollups session cascade: %w", err)
		}
		key, err := encodeStreakSessionDailyRollupKey(syncmodel.StreakSessionDailyRollupKey{SessionID: sessionIDVal, LocalDay: localDay})
		if err != nil {
			rollupRows.Close()
			return nil, err
		}
		out[sessionIDVal][syncTableStreakSessionDailyRollups] = append(out[sessionIDVal][syncTableStreakSessionDailyRollups], key)
	}
	rollupRows.Close()
	if err := rollupRows.Err(); err != nil {
		return nil, fmt.Errorf("iterating streak_session_daily_rollups session cascade: %w", err)
	}

	return out, nil
}
