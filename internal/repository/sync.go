package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

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
	SyncModes(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.Mode, string]) (syncmodel.TableChanges[syncmodel.Mode, string], error)
	SyncModeBlockedApps(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]) (syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey], error)
	SyncSchedules(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.Schedule, string]) (syncmodel.TableChanges[syncmodel.Schedule, string], error)
	SyncRestrictionSessions(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.RestrictionSession, string]) (syncmodel.TableChanges[syncmodel.RestrictionSession, string], error)
	SyncRestrictionLifecycleEvents(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.RestrictionLifecycleEvent, string]) (syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string], error)
	SyncNFCLinkedChips(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.NFCLinkedChip, string]) (syncmodel.TableChanges[syncmodel.NFCLinkedChip, string], error)
	SyncQRLinkedCodes(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.QRLinkedCode, string]) (syncmodel.TableChanges[syncmodel.QRLinkedCode, string], error)
	SyncStreakSessionDailyRollups(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]) (syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey], error)
	SyncStreakDailyAggregates(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.StreakDailyAggregate, string]) (syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string], error)
}

type PgxSyncRepository struct{}

func NewPgxSyncRepository() *PgxSyncRepository {
	return &PgxSyncRepository{}
}

var _ SyncRepository = (*PgxSyncRepository)(nil)

func (r *PgxSyncRepository) SyncModes(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.Mode, string]) (syncmodel.TableChanges[syncmodel.Mode, string], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}

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
			return syncmodel.TableChanges[syncmodel.Mode, string]{}, fmt.Errorf("upserting modes: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return syncmodel.TableChanges[syncmodel.Mode, string]{}, fmt.Errorf("scanning upserted mode id: %w", err)
			}
			written[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableChanges[syncmodel.Mode, string]{}, fmt.Errorf("iterating upserted mode ids: %w", err)
		}
	}

	for _, id := range in.Deletions {
		cascade, err := r.collectModeCascade(ctx, db, userID, id)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.Mode, string]{}, err
		}
		tag, err := db.Exec(ctx, "DELETE FROM modes WHERE user_id = $1 AND id = $2", userID, id)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.Mode, string]{}, fmt.Errorf("deleting mode: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableModes, id); err != nil {
			return syncmodel.TableChanges[syncmodel.Mode, string]{}, err
		}
		deleted[id] = struct{}{}
		for tableName, recordIDs := range cascade {
			for _, recordID := range recordIDs {
				if err := r.insertTombstone(ctx, db, userID, tableName, recordID); err != nil {
					return syncmodel.TableChanges[syncmodel.Mode, string]{}, err
				}
			}
		}
	}

	rows, err := db.Query(ctx, `SELECT id, title, text_on_screen, description, allowed_pauses_count, minimum_duration_ms, ending_pausing_scenario, icon_token, created_at, updated_at
		FROM modes
		WHERE user_id = $1 AND ($2 = 0 OR server_updated_at > $2)
		ORDER BY id`, userID, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.Mode, string]{}, fmt.Errorf("listing changed modes: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.Mode
	for rows.Next() {
		var rec syncmodel.Mode
		if err := rows.Scan(&rec.ID, &rec.Title, &rec.TextOnScreen, &rec.Description, &rec.AllowedPausesCount, &rec.MinimumDurationMS, &rec.EndingPausingScenario, &rec.IconToken, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return syncmodel.TableChanges[syncmodel.Mode, string]{}, fmt.Errorf("scanning changed mode: %w", err)
		}
		if _, ok := written[rec.ID]; ok {
			continue
		}
		upserts = append(upserts, rec)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableChanges[syncmodel.Mode, string]{}, fmt.Errorf("iterating changed modes: %w", err)
	}

	tombstones, err := r.listTombstones(ctx, db, userID, syncTableModes, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.Mode, string]{}, err
	}
	outDeletions := make([]string, 0, len(tombstones))
	for _, id := range tombstones {
		if _, ok := deleted[id]; ok {
			continue
		}
		outDeletions = append(outDeletions, id)
	}

	return syncmodel.TableChanges[syncmodel.Mode, string]{Upserts: upserts, Deletions: outDeletions}, nil
}

func (r *PgxSyncRepository) SyncModeBlockedApps(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]) (syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}

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
			return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("upserting mode_blocked_apps: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var modeID string
			var platform syncmodel.DevicePlatform
			var appIdentifier string
			if err := rows.Scan(&modeID, &platform, &appIdentifier); err != nil {
				return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("scanning upserted mode_blocked_app key: %w", err)
			}
			key, err := encodeModeBlockedAppKey(syncmodel.ModeBlockedAppKey{ModeID: modeID, Platform: platform, AppIdentifier: appIdentifier})
			if err != nil {
				return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, err
			}
			written[key] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("iterating upserted mode_blocked_app keys: %w", err)
		}
	}

	for _, keyObj := range in.Deletions {
		key, err := encodeModeBlockedAppKey(keyObj)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, err
		}
		tag, err := db.Exec(ctx, "DELETE FROM mode_blocked_apps WHERE user_id = $1 AND mode_id = $2 AND platform = $3 AND app_identifier = $4", userID, keyObj.ModeID, keyObj.Platform, keyObj.AppIdentifier)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("deleting mode_blocked_app: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableModeBlockedApps, key); err != nil {
			return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, err
		}
		deleted[key] = struct{}{}
	}

	rows, err := db.Query(ctx, `SELECT mode_id, platform, app_identifier, created_at, updated_at
		FROM mode_blocked_apps
		WHERE user_id = $1 AND ($2 = 0 OR server_updated_at > $2)
		ORDER BY mode_id, platform, app_identifier`, userID, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("listing changed mode_blocked_apps: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.ModeBlockedApp
	for rows.Next() {
		var rec syncmodel.ModeBlockedApp
		if err := rows.Scan(&rec.ModeID, &rec.Platform, &rec.AppIdentifier, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("scanning changed mode_blocked_app: %w", err)
		}
		key, err := encodeModeBlockedAppKey(syncmodel.ModeBlockedAppKey{ModeID: rec.ModeID, Platform: rec.Platform, AppIdentifier: rec.AppIdentifier})
		if err != nil {
			return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, err
		}
		if _, ok := written[key]; ok {
			continue
		}
		upserts = append(upserts, rec)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("iterating changed mode_blocked_apps: %w", err)
	}

	tombstones, err := r.listTombstones(ctx, db, userID, syncTableModeBlockedApps, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, err
	}
	var outDeletions []syncmodel.ModeBlockedAppKey
	for _, encoded := range tombstones {
		if _, ok := deleted[encoded]; ok {
			continue
		}
		key, err := decodeModeBlockedAppKey(encoded)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, err
		}
		outDeletions = append(outDeletions, key)
	}

	return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{Upserts: upserts, Deletions: outDeletions}, nil
}

func (r *PgxSyncRepository) SyncSchedules(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.Schedule, string]) (syncmodel.TableChanges[syncmodel.Schedule, string], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}

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
			return syncmodel.TableChanges[syncmodel.Schedule, string]{}, fmt.Errorf("upserting schedules: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return syncmodel.TableChanges[syncmodel.Schedule, string]{}, fmt.Errorf("scanning upserted schedule id: %w", err)
			}
			written[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableChanges[syncmodel.Schedule, string]{}, fmt.Errorf("iterating upserted schedule ids: %w", err)
		}
	}

	for _, id := range in.Deletions {
		tag, err := db.Exec(ctx, "DELETE FROM schedules WHERE user_id = $1 AND id = $2", userID, id)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.Schedule, string]{}, fmt.Errorf("deleting schedule: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableSchedules, id); err != nil {
			return syncmodel.TableChanges[syncmodel.Schedule, string]{}, err
		}
		deleted[id] = struct{}{}
	}

	rows, err := db.Query(ctx, `SELECT id, mode_id, days, start_minute, end_minute, enabled, created_at, updated_at
		FROM schedules
		WHERE user_id = $1 AND ($2 = 0 OR server_updated_at > $2)
		ORDER BY id`, userID, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.Schedule, string]{}, fmt.Errorf("listing changed schedules: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.Schedule
	for rows.Next() {
		var rec syncmodel.Schedule
		if err := rows.Scan(&rec.ID, &rec.ModeID, &rec.Days, &rec.StartMinute, &rec.EndMinute, &rec.Enabled, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return syncmodel.TableChanges[syncmodel.Schedule, string]{}, fmt.Errorf("scanning changed schedule: %w", err)
		}
		if _, ok := written[rec.ID]; ok {
			continue
		}
		upserts = append(upserts, rec)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableChanges[syncmodel.Schedule, string]{}, fmt.Errorf("iterating changed schedules: %w", err)
	}

	tombstones, err := r.listTombstones(ctx, db, userID, syncTableSchedules, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.Schedule, string]{}, err
	}
	var outDeletions []string
	for _, id := range tombstones {
		if _, ok := deleted[id]; ok {
			continue
		}
		outDeletions = append(outDeletions, id)
	}

	return syncmodel.TableChanges[syncmodel.Schedule, string]{Upserts: upserts, Deletions: outDeletions}, nil
}

func (r *PgxSyncRepository) SyncRestrictionSessions(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.RestrictionSession, string]) (syncmodel.TableChanges[syncmodel.RestrictionSession, string], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}

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
			return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{}, fmt.Errorf("upserting restriction_sessions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sessionID string
			if err := rows.Scan(&sessionID); err != nil {
				return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{}, fmt.Errorf("scanning upserted restriction_session id: %w", err)
			}
			written[sessionID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{}, fmt.Errorf("iterating upserted restriction_session ids: %w", err)
		}
	}

	for _, sessionID := range in.Deletions {
		cascade, err := r.collectRestrictionSessionCascade(ctx, db, userID, sessionID)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{}, err
		}
		tag, err := db.Exec(ctx, "DELETE FROM restriction_sessions WHERE user_id = $1 AND session_id = $2", userID, sessionID)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{}, fmt.Errorf("deleting restriction_session: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableRestrictionSessions, sessionID); err != nil {
			return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{}, err
		}
		deleted[sessionID] = struct{}{}
		for tableName, recordIDs := range cascade {
			for _, recordID := range recordIDs {
				if err := r.insertTombstone(ctx, db, userID, tableName, recordID); err != nil {
					return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{}, err
				}
			}
		}
	}

	rows, err := db.Query(ctx, `SELECT session_id, mode_id, source, started_at, ended_at, pause_count, total_paused_ms, last_paused_at, integrity_status, last_anomaly_reason, last_event_id, created_at, updated_at
		FROM restriction_sessions
		WHERE user_id = $1 AND ($2 = 0 OR server_updated_at > $2)
		ORDER BY session_id`, userID, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{}, fmt.Errorf("listing changed restriction_sessions: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.RestrictionSession
	for rows.Next() {
		var rec syncmodel.RestrictionSession
		if err := rows.Scan(&rec.SessionID, &rec.ModeID, &rec.Source, &rec.StartedAt, &rec.EndedAt, &rec.PauseCount, &rec.TotalPausedMS, &rec.LastPausedAt, &rec.IntegrityStatus, &rec.LastAnomalyReason, &rec.LastEventID, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{}, fmt.Errorf("scanning changed restriction_session: %w", err)
		}
		if _, ok := written[rec.SessionID]; ok {
			continue
		}
		upserts = append(upserts, rec)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{}, fmt.Errorf("iterating changed restriction_sessions: %w", err)
	}

	tombstones, err := r.listTombstones(ctx, db, userID, syncTableRestrictionSessions, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{}, err
	}
	var outDeletions []string
	for _, id := range tombstones {
		if _, ok := deleted[id]; ok {
			continue
		}
		outDeletions = append(outDeletions, id)
	}

	return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{Upserts: upserts, Deletions: outDeletions}, nil
}

func (r *PgxSyncRepository) SyncRestrictionLifecycleEvents(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.RestrictionLifecycleEvent, string]) (syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}

	if len(in.Upserts) > 0 {
		var sb strings.Builder
		args := []any{userID}
		sb.WriteString(`INSERT INTO restriction_lifecycle_events (user_id, id, session_id, mode_id, action, source, reason, occurred_at, created_at) VALUES `)
		for i, rec := range in.Upserts {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := len(args) + 1
			fmt.Fprintf(&sb, "($1, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				base, base+1, base+2, base+3, base+4, base+5, base+6, base+7)
			args = append(args, rec.ID, rec.SessionID, rec.ModeID, rec.Action, rec.Source, rec.Reason, rec.OccurredAt, rec.CreatedAt)
		}
		sb.WriteString(` ON CONFLICT (user_id, id) DO UPDATE SET
			session_id = EXCLUDED.session_id, mode_id = EXCLUDED.mode_id, action = EXCLUDED.action,
			source = EXCLUDED.source, reason = EXCLUDED.reason, occurred_at = EXCLUDED.occurred_at,
			created_at = EXCLUDED.created_at
			WHERE EXCLUDED.created_at > restriction_lifecycle_events.created_at
			RETURNING id`)
		rows, err := db.Query(ctx, sb.String(), args...)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("upserting restriction_lifecycle_events: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("scanning upserted restriction_lifecycle_event id: %w", err)
			}
			written[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("iterating upserted restriction_lifecycle_event ids: %w", err)
		}
	}

	for _, id := range in.Deletions {
		tag, err := db.Exec(ctx, "DELETE FROM restriction_lifecycle_events WHERE user_id = $1 AND id = $2", userID, id)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("deleting restriction_lifecycle_event: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableRestrictionLifecycleEvents, id); err != nil {
			return syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string]{}, err
		}
		deleted[id] = struct{}{}
	}

	rows, err := db.Query(ctx, `SELECT id, session_id, mode_id, action, source, reason, occurred_at, created_at
		FROM restriction_lifecycle_events
		WHERE user_id = $1 AND ($2 = 0 OR created_at > $2)
		ORDER BY id`, userID, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("listing changed restriction_lifecycle_events: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.RestrictionLifecycleEvent
	for rows.Next() {
		var rec syncmodel.RestrictionLifecycleEvent
		if err := rows.Scan(&rec.ID, &rec.SessionID, &rec.ModeID, &rec.Action, &rec.Source, &rec.Reason, &rec.OccurredAt, &rec.CreatedAt); err != nil {
			return syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("scanning changed restriction_lifecycle_event: %w", err)
		}
		if _, ok := written[rec.ID]; ok {
			continue
		}
		upserts = append(upserts, rec)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("iterating changed restriction_lifecycle_events: %w", err)
	}

	tombstones, err := r.listTombstones(ctx, db, userID, syncTableRestrictionLifecycleEvents, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string]{}, err
	}
	var outDeletions []string
	for _, id := range tombstones {
		if _, ok := deleted[id]; ok {
			continue
		}
		outDeletions = append(outDeletions, id)
	}

	return syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string]{Upserts: upserts, Deletions: outDeletions}, nil
}

func (r *PgxSyncRepository) SyncNFCLinkedChips(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.NFCLinkedChip, string]) (syncmodel.TableChanges[syncmodel.NFCLinkedChip, string], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}

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
			return syncmodel.TableChanges[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("upserting nfc_linked_chips: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return syncmodel.TableChanges[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("scanning upserted nfc_linked_chip id: %w", err)
			}
			written[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableChanges[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("iterating upserted nfc_linked_chip ids: %w", err)
		}
	}

	for _, id := range in.Deletions {
		tag, err := db.Exec(ctx, "DELETE FROM nfc_linked_chips WHERE user_id = $1 AND id = $2", userID, id)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("deleting nfc_linked_chip: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableNFCLinkedChips, id); err != nil {
			return syncmodel.TableChanges[syncmodel.NFCLinkedChip, string]{}, err
		}
		deleted[id] = struct{}{}
	}

	rows, err := db.Query(ctx, `SELECT id, chip_identifier, name, created_at, updated_at
		FROM nfc_linked_chips
		WHERE user_id = $1 AND ($2 = 0 OR server_updated_at > $2)
		ORDER BY id`, userID, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("listing changed nfc_linked_chips: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.NFCLinkedChip
	for rows.Next() {
		var rec syncmodel.NFCLinkedChip
		if err := rows.Scan(&rec.ID, &rec.ChipIdentifier, &rec.Name, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return syncmodel.TableChanges[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("scanning changed nfc_linked_chip: %w", err)
		}
		if _, ok := written[rec.ID]; ok {
			continue
		}
		upserts = append(upserts, rec)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableChanges[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("iterating changed nfc_linked_chips: %w", err)
	}

	tombstones, err := r.listTombstones(ctx, db, userID, syncTableNFCLinkedChips, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.NFCLinkedChip, string]{}, err
	}
	var outDeletions []string
	for _, id := range tombstones {
		if _, ok := deleted[id]; ok {
			continue
		}
		outDeletions = append(outDeletions, id)
	}

	return syncmodel.TableChanges[syncmodel.NFCLinkedChip, string]{Upserts: upserts, Deletions: outDeletions}, nil
}

func (r *PgxSyncRepository) SyncQRLinkedCodes(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.QRLinkedCode, string]) (syncmodel.TableChanges[syncmodel.QRLinkedCode, string], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}

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
			return syncmodel.TableChanges[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("upserting qr_linked_codes: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return syncmodel.TableChanges[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("scanning upserted qr_linked_code id: %w", err)
			}
			written[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableChanges[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("iterating upserted qr_linked_code ids: %w", err)
		}
	}

	for _, id := range in.Deletions {
		tag, err := db.Exec(ctx, "DELETE FROM qr_linked_codes WHERE user_id = $1 AND id = $2", userID, id)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("deleting qr_linked_code: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableQRLinkedCodes, id); err != nil {
			return syncmodel.TableChanges[syncmodel.QRLinkedCode, string]{}, err
		}
		deleted[id] = struct{}{}
	}

	rows, err := db.Query(ctx, `SELECT id, scan_value, name, created_at, updated_at
		FROM qr_linked_codes
		WHERE user_id = $1 AND ($2 = 0 OR server_updated_at > $2)
		ORDER BY id`, userID, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("listing changed qr_linked_codes: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.QRLinkedCode
	for rows.Next() {
		var rec syncmodel.QRLinkedCode
		if err := rows.Scan(&rec.ID, &rec.ScanValue, &rec.Name, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return syncmodel.TableChanges[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("scanning changed qr_linked_code: %w", err)
		}
		if _, ok := written[rec.ID]; ok {
			continue
		}
		upserts = append(upserts, rec)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableChanges[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("iterating changed qr_linked_codes: %w", err)
	}

	tombstones, err := r.listTombstones(ctx, db, userID, syncTableQRLinkedCodes, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.QRLinkedCode, string]{}, err
	}
	var outDeletions []string
	for _, id := range tombstones {
		if _, ok := deleted[id]; ok {
			continue
		}
		outDeletions = append(outDeletions, id)
	}

	return syncmodel.TableChanges[syncmodel.QRLinkedCode, string]{Upserts: upserts, Deletions: outDeletions}, nil
}

func (r *PgxSyncRepository) SyncStreakSessionDailyRollups(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]) (syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}

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
			return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("upserting streak_session_daily_rollups: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sessionID, localDay string
			if err := rows.Scan(&sessionID, &localDay); err != nil {
				return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("scanning upserted streak_session_daily_rollup key: %w", err)
			}
			key, err := encodeStreakSessionDailyRollupKey(syncmodel.StreakSessionDailyRollupKey{SessionID: sessionID, LocalDay: localDay})
			if err != nil {
				return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, err
			}
			written[key] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("iterating upserted streak_session_daily_rollup keys: %w", err)
		}
	}

	for _, keyObj := range in.Deletions {
		key, err := encodeStreakSessionDailyRollupKey(keyObj)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, err
		}
		tag, err := db.Exec(ctx, "DELETE FROM streak_session_daily_rollups WHERE user_id = $1 AND session_id = $2 AND local_day = $3", userID, keyObj.SessionID, keyObj.LocalDay)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("deleting streak_session_daily_rollup: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableStreakSessionDailyRollups, key); err != nil {
			return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, err
		}
		deleted[key] = struct{}{}
	}

	rows, err := db.Query(ctx, `SELECT session_id, local_day, effective_ms, updated_at
		FROM streak_session_daily_rollups
		WHERE user_id = $1 AND ($2 = 0 OR server_updated_at > $2)
		ORDER BY session_id, local_day`, userID, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("listing changed streak_session_daily_rollups: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.StreakSessionDailyRollup
	for rows.Next() {
		var rec syncmodel.StreakSessionDailyRollup
		if err := rows.Scan(&rec.SessionID, &rec.LocalDay, &rec.EffectiveMS, &rec.UpdatedAt); err != nil {
			return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("scanning changed streak_session_daily_rollup: %w", err)
		}
		key, err := encodeStreakSessionDailyRollupKey(syncmodel.StreakSessionDailyRollupKey{SessionID: rec.SessionID, LocalDay: rec.LocalDay})
		if err != nil {
			return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, err
		}
		if _, ok := written[key]; ok {
			continue
		}
		upserts = append(upserts, rec)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("iterating changed streak_session_daily_rollups: %w", err)
	}

	tombstones, err := r.listTombstones(ctx, db, userID, syncTableStreakSessionDailyRollups, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, err
	}
	var outDeletions []syncmodel.StreakSessionDailyRollupKey
	for _, encoded := range tombstones {
		if _, ok := deleted[encoded]; ok {
			continue
		}
		key, err := decodeStreakSessionDailyRollupKey(encoded)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, err
		}
		outDeletions = append(outDeletions, key)
	}

	return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{Upserts: upserts, Deletions: outDeletions}, nil
}

func (r *PgxSyncRepository) SyncStreakDailyAggregates(ctx context.Context, db DBTX, userID string, in syncmodel.TableSync[syncmodel.StreakDailyAggregate, string]) (syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string], error) {
	written := map[string]struct{}{}
	deleted := map[string]struct{}{}
	refreshMetrics := false

	if len(in.Upserts) > 0 {
		var sb strings.Builder
		args := []any{userID}
		sb.WriteString(`INSERT INTO streak_daily_aggregates (user_id, local_day, effective_ms, qualified, source_session_count, updated_at) VALUES `)
		for i, rec := range in.Upserts {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := len(args) + 1
			fmt.Fprintf(&sb, "($1, $%d, $%d, $%d, $%d, $%d)",
				base, base+1, base+2, base+3, base+4)
			args = append(args, rec.LocalDay, rec.EffectiveMS, rec.Qualified, rec.SourceSessionCount, rec.UpdatedAt)
		}
		sb.WriteString(` ON CONFLICT (user_id, local_day) DO UPDATE SET
			effective_ms = EXCLUDED.effective_ms, qualified = EXCLUDED.qualified,
			source_session_count = EXCLUDED.source_session_count, updated_at = EXCLUDED.updated_at
			WHERE EXCLUDED.updated_at > streak_daily_aggregates.updated_at
			RETURNING local_day`)
		rows, err := db.Query(ctx, sb.String(), args...)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string]{}, fmt.Errorf("upserting streak_daily_aggregates: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var localDay string
			if err := rows.Scan(&localDay); err != nil {
				return syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string]{}, fmt.Errorf("scanning upserted streak_daily_aggregate key: %w", err)
			}
			written[localDay] = struct{}{}
			refreshMetrics = true
		}
		if err := rows.Err(); err != nil {
			return syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string]{}, fmt.Errorf("iterating upserted streak_daily_aggregate keys: %w", err)
		}
	}

	for _, localDay := range in.Deletions {
		tag, err := db.Exec(ctx, "DELETE FROM streak_daily_aggregates WHERE user_id = $1 AND local_day = $2", userID, localDay)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string]{}, fmt.Errorf("deleting streak_daily_aggregate: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := r.insertTombstone(ctx, db, userID, syncTableStreakDailyAggregates, localDay); err != nil {
			return syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string]{}, err
		}
		deleted[localDay] = struct{}{}
		refreshMetrics = true
	}

	if refreshMetrics {
		if err := (&SocialRepository{}).RefreshLeaderboardMetrics(ctx, db, userID); err != nil {
			return syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string]{}, err
		}
	}

	rows, err := db.Query(ctx, `SELECT local_day, effective_ms, qualified, source_session_count, updated_at
		FROM streak_daily_aggregates
		WHERE user_id = $1 AND ($2 = 0 OR server_updated_at > $2)
		ORDER BY local_day`, userID, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string]{}, fmt.Errorf("listing changed streak_daily_aggregates: %w", err)
	}
	defer rows.Close()

	var upserts []syncmodel.StreakDailyAggregate
	for rows.Next() {
		var rec syncmodel.StreakDailyAggregate
		if err := rows.Scan(&rec.LocalDay, &rec.EffectiveMS, &rec.Qualified, &rec.SourceSessionCount, &rec.UpdatedAt); err != nil {
			return syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string]{}, fmt.Errorf("scanning changed streak_daily_aggregate: %w", err)
		}
		if _, ok := written[rec.LocalDay]; ok {
			continue
		}
		upserts = append(upserts, rec)
	}
	if err := rows.Err(); err != nil {
		return syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string]{}, fmt.Errorf("iterating changed streak_daily_aggregates: %w", err)
	}

	tombstones, err := r.listTombstones(ctx, db, userID, syncTableStreakDailyAggregates, in.LastSyncedAt)
	if err != nil {
		return syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string]{}, err
	}
	var outDeletions []string
	for _, id := range tombstones {
		if _, ok := deleted[id]; ok {
			continue
		}
		outDeletions = append(outDeletions, id)
	}

	return syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string]{Upserts: upserts, Deletions: outDeletions}, nil
}

func (r *PgxSyncRepository) insertTombstone(ctx context.Context, db DBTX, userID string, tableName string, recordID string) error {
	_, err := db.Exec(ctx, `INSERT INTO sync_tombstones (user_id, table_name, record_id, deleted_at)
		VALUES ($1, $2, $3, now())`, userID, tableName, recordID)
	if err != nil {
		return fmt.Errorf("inserting tombstone for %s: %w", tableName, err)
	}
	return nil
}

func (r *PgxSyncRepository) listTombstones(ctx context.Context, db DBTX, userID string, tableName string, lastSyncedAt int64) ([]string, error) {
	var rows pgx.Rows
	var err error
	if lastSyncedAt == 0 {
		rows, err = db.Query(ctx, `SELECT DISTINCT record_id
			FROM sync_tombstones
			WHERE user_id = $1 AND table_name = $2
			ORDER BY record_id`, userID, tableName)
	} else {
		rows, err = db.Query(ctx, `SELECT DISTINCT record_id
			FROM sync_tombstones
			WHERE user_id = $1 AND table_name = $2 AND server_deleted_at > $3
			ORDER BY record_id`, userID, tableName, lastSyncedAt)
	}
	if err != nil {
		return nil, fmt.Errorf("listing tombstones for %s: %w", tableName, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var recordID string
		if err := rows.Scan(&recordID); err != nil {
			return nil, fmt.Errorf("scanning tombstone for %s: %w", tableName, err)
		}
		out = append(out, recordID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tombstones for %s: %w", tableName, err)
	}
	return out, nil
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

func (r *PgxSyncRepository) collectModeCascade(ctx context.Context, db DBTX, userID, modeID string) (map[string][]string, error) {
	out := map[string][]string{}

	rows, err := db.Query(ctx, `SELECT mode_id, platform, app_identifier FROM mode_blocked_apps WHERE user_id = $1 AND mode_id = $2`, userID, modeID)
	if err != nil {
		return nil, fmt.Errorf("querying mode_blocked_apps cascade: %w", err)
	}
	for rows.Next() {
		var modeIDVal string
		var platform string
		var appIdentifier string
		if err := rows.Scan(&modeIDVal, &platform, &appIdentifier); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning mode_blocked_apps cascade: %w", err)
		}
		key, err := encodeModeBlockedAppKey(syncmodel.ModeBlockedAppKey{ModeID: modeIDVal, Platform: syncmodel.DevicePlatform(platform), AppIdentifier: appIdentifier})
		if err != nil {
			rows.Close()
			return nil, err
		}
		out[syncTableModeBlockedApps] = append(out[syncTableModeBlockedApps], key)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterating mode_blocked_apps cascade: %w", err)
	}
	rows.Close()

	rows, err = db.Query(ctx, `SELECT id FROM schedules WHERE user_id = $1 AND mode_id = $2`, userID, modeID)
	if err != nil {
		return nil, fmt.Errorf("querying schedules cascade: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning schedules cascade: %w", err)
		}
		out[syncTableSchedules] = append(out[syncTableSchedules], id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterating schedules cascade: %w", err)
	}
	rows.Close()

	rows, err = db.Query(ctx, `SELECT session_id FROM restriction_sessions WHERE user_id = $1 AND mode_id = $2`, userID, modeID)
	if err != nil {
		return nil, fmt.Errorf("querying restriction_sessions cascade: %w", err)
	}
	sessions := make([]string, 0)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning restriction_sessions cascade: %w", err)
		}
		sessions = append(sessions, sessionID)
		out[syncTableRestrictionSessions] = append(out[syncTableRestrictionSessions], sessionID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterating restriction_sessions cascade: %w", err)
	}
	rows.Close()

	rows, err = db.Query(ctx, `SELECT id FROM restriction_lifecycle_events WHERE user_id = $1 AND mode_id = $2`, userID, modeID)
	if err != nil {
		return nil, fmt.Errorf("querying restriction_lifecycle_events cascade: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning restriction_lifecycle_events cascade: %w", err)
		}
		out[syncTableRestrictionLifecycleEvents] = append(out[syncTableRestrictionLifecycleEvents], id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterating restriction_lifecycle_events cascade: %w", err)
	}
	rows.Close()

	if len(sessions) > 0 {
		rows, err = db.Query(ctx, `SELECT session_id, local_day FROM streak_session_daily_rollups WHERE user_id = $1 AND session_id = ANY($2)`, userID, sessions)
		if err != nil {
			return nil, fmt.Errorf("querying streak_session_daily_rollups cascade: %w", err)
		}
		for rows.Next() {
			var sessionID string
			var localDay string
			if err := rows.Scan(&sessionID, &localDay); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scanning streak_session_daily_rollups cascade: %w", err)
			}
			key, err := encodeStreakSessionDailyRollupKey(syncmodel.StreakSessionDailyRollupKey{SessionID: sessionID, LocalDay: localDay})
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[syncTableStreakSessionDailyRollups] = append(out[syncTableStreakSessionDailyRollups], key)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterating streak_session_daily_rollups cascade: %w", err)
		}
		rows.Close()
	}

	return out, nil
}

func (r *PgxSyncRepository) collectRestrictionSessionCascade(ctx context.Context, db DBTX, userID, sessionID string) (map[string][]string, error) {
	out := map[string][]string{}

	rows, err := db.Query(ctx, `SELECT id FROM restriction_lifecycle_events WHERE user_id = $1 AND session_id = $2`, userID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("querying restriction_lifecycle_events session cascade: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning restriction_lifecycle_events session cascade: %w", err)
		}
		out[syncTableRestrictionLifecycleEvents] = append(out[syncTableRestrictionLifecycleEvents], id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterating restriction_lifecycle_events session cascade: %w", err)
	}
	rows.Close()

	rows, err = db.Query(ctx, `SELECT session_id, local_day FROM streak_session_daily_rollups WHERE user_id = $1 AND session_id = $2`, userID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("querying streak_session_daily_rollups session cascade: %w", err)
	}
	for rows.Next() {
		var sessionIDVal string
		var localDay string
		if err := rows.Scan(&sessionIDVal, &localDay); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning streak_session_daily_rollups session cascade: %w", err)
		}
		key, err := encodeStreakSessionDailyRollupKey(syncmodel.StreakSessionDailyRollupKey{SessionID: sessionIDVal, LocalDay: localDay})
		if err != nil {
			rows.Close()
			return nil, err
		}
		out[syncTableStreakSessionDailyRollups] = append(out[syncTableStreakSessionDailyRollups], key)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterating streak_session_daily_rollups session cascade: %w", err)
	}
	rows.Close()

	return out, nil
}
