package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

	for _, rec := range in.Upserts {
		var serverUpdatedAt int64
		err := db.QueryRow(ctx, "SELECT updated_at FROM modes WHERE user_id = $1 AND id = $2", userID, rec.ID).Scan(&serverUpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = db.Exec(ctx, `INSERT INTO modes (user_id, id, title, text_on_screen, description, allowed_pauses_count, minimum_duration_ms, ending_pausing_scenario, icon_token, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
				userID, rec.ID, rec.Title, rec.TextOnScreen, rec.Description, rec.AllowedPausesCount, rec.MinimumDurationMS, rec.EndingPausingScenario, rec.IconToken, rec.CreatedAt, rec.UpdatedAt,
			)
			if err != nil {
				return syncmodel.TableChanges[syncmodel.Mode, string]{}, fmt.Errorf("inserting mode: %w", err)
			}
			written[rec.ID] = struct{}{}
			continue
		}
		if err != nil {
			return syncmodel.TableChanges[syncmodel.Mode, string]{}, fmt.Errorf("looking up mode: %w", err)
		}
		if rec.UpdatedAt <= serverUpdatedAt {
			continue
		}
		_, err = db.Exec(ctx, `UPDATE modes SET title = $3, text_on_screen = $4, description = $5, allowed_pauses_count = $6, minimum_duration_ms = $7, ending_pausing_scenario = $8, icon_token = $9, created_at = $10, updated_at = $11
			WHERE user_id = $1 AND id = $2`,
			userID, rec.ID, rec.Title, rec.TextOnScreen, rec.Description, rec.AllowedPausesCount, rec.MinimumDurationMS, rec.EndingPausingScenario, rec.IconToken, rec.CreatedAt, rec.UpdatedAt,
		)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.Mode, string]{}, fmt.Errorf("updating mode: %w", err)
		}
		written[rec.ID] = struct{}{}
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

	for _, rec := range in.Upserts {
		key, err := encodeModeBlockedAppKey(syncmodel.ModeBlockedAppKey{ModeID: rec.ModeID, Platform: rec.Platform, AppIdentifier: rec.AppIdentifier})
		if err != nil {
			return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, err
		}
		var serverUpdatedAt int64
		err = db.QueryRow(ctx, "SELECT updated_at FROM mode_blocked_apps WHERE user_id = $1 AND mode_id = $2 AND platform = $3 AND app_identifier = $4", userID, rec.ModeID, rec.Platform, rec.AppIdentifier).Scan(&serverUpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = db.Exec(ctx, `INSERT INTO mode_blocked_apps (user_id, mode_id, platform, app_identifier, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6)`, userID, rec.ModeID, rec.Platform, rec.AppIdentifier, rec.CreatedAt, rec.UpdatedAt)
			if err != nil {
				return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("inserting mode_blocked_app: %w", err)
			}
			written[key] = struct{}{}
			continue
		}
		if err != nil {
			return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("looking up mode_blocked_app: %w", err)
		}
		if rec.UpdatedAt <= serverUpdatedAt {
			continue
		}
		_, err = db.Exec(ctx, `UPDATE mode_blocked_apps SET created_at = $5, updated_at = $6
			WHERE user_id = $1 AND mode_id = $2 AND platform = $3 AND app_identifier = $4`, userID, rec.ModeID, rec.Platform, rec.AppIdentifier, rec.CreatedAt, rec.UpdatedAt)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{}, fmt.Errorf("updating mode_blocked_app: %w", err)
		}
		written[key] = struct{}{}
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

	for _, rec := range in.Upserts {
		var serverUpdatedAt int64
		err := db.QueryRow(ctx, "SELECT updated_at FROM schedules WHERE user_id = $1 AND id = $2", userID, rec.ID).Scan(&serverUpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = db.Exec(ctx, `INSERT INTO schedules (user_id, id, mode_id, days, start_minute, end_minute, enabled, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, userID, rec.ID, rec.ModeID, rec.Days, rec.StartMinute, rec.EndMinute, rec.Enabled, rec.CreatedAt, rec.UpdatedAt)
			if err != nil {
				return syncmodel.TableChanges[syncmodel.Schedule, string]{}, fmt.Errorf("inserting schedule: %w", err)
			}
			written[rec.ID] = struct{}{}
			continue
		}
		if err != nil {
			return syncmodel.TableChanges[syncmodel.Schedule, string]{}, fmt.Errorf("looking up schedule: %w", err)
		}
		if rec.UpdatedAt <= serverUpdatedAt {
			continue
		}
		_, err = db.Exec(ctx, `UPDATE schedules SET mode_id = $3, days = $4, start_minute = $5, end_minute = $6, enabled = $7, created_at = $8, updated_at = $9
			WHERE user_id = $1 AND id = $2`, userID, rec.ID, rec.ModeID, rec.Days, rec.StartMinute, rec.EndMinute, rec.Enabled, rec.CreatedAt, rec.UpdatedAt)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.Schedule, string]{}, fmt.Errorf("updating schedule: %w", err)
		}
		written[rec.ID] = struct{}{}
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

	for _, rec := range in.Upserts {
		var serverUpdatedAt int64
		err := db.QueryRow(ctx, "SELECT updated_at FROM restriction_sessions WHERE user_id = $1 AND session_id = $2", userID, rec.SessionID).Scan(&serverUpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = db.Exec(ctx, `INSERT INTO restriction_sessions (user_id, session_id, mode_id, source, started_at, ended_at, pause_count, total_paused_ms, last_paused_at, integrity_status, last_anomaly_reason, last_event_id, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
				userID, rec.SessionID, rec.ModeID, rec.Source, rec.StartedAt, rec.EndedAt, rec.PauseCount, rec.TotalPausedMS, rec.LastPausedAt, rec.IntegrityStatus, rec.LastAnomalyReason, rec.LastEventID, rec.CreatedAt, rec.UpdatedAt)
			if err != nil {
				return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{}, fmt.Errorf("inserting restriction_session: %w", err)
			}
			written[rec.SessionID] = struct{}{}
			continue
		}
		if err != nil {
			return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{}, fmt.Errorf("looking up restriction_session: %w", err)
		}
		if rec.UpdatedAt <= serverUpdatedAt {
			continue
		}
		_, err = db.Exec(ctx, `UPDATE restriction_sessions
			SET mode_id = $3, source = $4, started_at = $5, ended_at = $6, pause_count = $7, total_paused_ms = $8, last_paused_at = $9, integrity_status = $10, last_anomaly_reason = $11, last_event_id = $12, created_at = $13, updated_at = $14
			WHERE user_id = $1 AND session_id = $2`,
			userID, rec.SessionID, rec.ModeID, rec.Source, rec.StartedAt, rec.EndedAt, rec.PauseCount, rec.TotalPausedMS, rec.LastPausedAt, rec.IntegrityStatus, rec.LastAnomalyReason, rec.LastEventID, rec.CreatedAt, rec.UpdatedAt)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.RestrictionSession, string]{}, fmt.Errorf("updating restriction_session: %w", err)
		}
		written[rec.SessionID] = struct{}{}
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

	for _, rec := range in.Upserts {
		var serverCreatedAt int64
		err := db.QueryRow(ctx, "SELECT created_at FROM restriction_lifecycle_events WHERE user_id = $1 AND id = $2", userID, rec.ID).Scan(&serverCreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = db.Exec(ctx, `INSERT INTO restriction_lifecycle_events (user_id, id, session_id, mode_id, action, source, reason, occurred_at, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, userID, rec.ID, rec.SessionID, rec.ModeID, rec.Action, rec.Source, rec.Reason, rec.OccurredAt, rec.CreatedAt)
			if err != nil {
				return syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("inserting restriction_lifecycle_event: %w", err)
			}
			written[rec.ID] = struct{}{}
			continue
		}
		if err != nil {
			return syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("looking up restriction_lifecycle_event: %w", err)
		}
		if rec.CreatedAt <= serverCreatedAt {
			continue
		}
		_, err = db.Exec(ctx, `UPDATE restriction_lifecycle_events
			SET session_id = $3, mode_id = $4, action = $5, source = $6, reason = $7, occurred_at = $8, created_at = $9
			WHERE user_id = $1 AND id = $2`, userID, rec.ID, rec.SessionID, rec.ModeID, rec.Action, rec.Source, rec.Reason, rec.OccurredAt, rec.CreatedAt)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.RestrictionLifecycleEvent, string]{}, fmt.Errorf("updating restriction_lifecycle_event: %w", err)
		}
		written[rec.ID] = struct{}{}
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
		WHERE user_id = $1 AND ($2 = 0 OR server_created_at > $2)
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

	for _, rec := range in.Upserts {
		var serverUpdatedAt int64
		err := db.QueryRow(ctx, "SELECT updated_at FROM nfc_linked_chips WHERE user_id = $1 AND id = $2", userID, rec.ID).Scan(&serverUpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = db.Exec(ctx, `INSERT INTO nfc_linked_chips (user_id, id, chip_identifier, name, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6)`, userID, rec.ID, rec.ChipIdentifier, rec.Name, rec.CreatedAt, rec.UpdatedAt)
			if err != nil {
				return syncmodel.TableChanges[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("inserting nfc_linked_chip: %w", err)
			}
			written[rec.ID] = struct{}{}
			continue
		}
		if err != nil {
			return syncmodel.TableChanges[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("looking up nfc_linked_chip: %w", err)
		}
		if rec.UpdatedAt <= serverUpdatedAt {
			continue
		}
		_, err = db.Exec(ctx, `UPDATE nfc_linked_chips SET chip_identifier = $3, name = $4, created_at = $5, updated_at = $6
			WHERE user_id = $1 AND id = $2`, userID, rec.ID, rec.ChipIdentifier, rec.Name, rec.CreatedAt, rec.UpdatedAt)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.NFCLinkedChip, string]{}, fmt.Errorf("updating nfc_linked_chip: %w", err)
		}
		written[rec.ID] = struct{}{}
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

	for _, rec := range in.Upserts {
		var serverUpdatedAt int64
		err := db.QueryRow(ctx, "SELECT updated_at FROM qr_linked_codes WHERE user_id = $1 AND id = $2", userID, rec.ID).Scan(&serverUpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = db.Exec(ctx, `INSERT INTO qr_linked_codes (user_id, id, scan_value, name, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6)`, userID, rec.ID, rec.ScanValue, rec.Name, rec.CreatedAt, rec.UpdatedAt)
			if err != nil {
				return syncmodel.TableChanges[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("inserting qr_linked_code: %w", err)
			}
			written[rec.ID] = struct{}{}
			continue
		}
		if err != nil {
			return syncmodel.TableChanges[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("looking up qr_linked_code: %w", err)
		}
		if rec.UpdatedAt <= serverUpdatedAt {
			continue
		}
		_, err = db.Exec(ctx, `UPDATE qr_linked_codes SET scan_value = $3, name = $4, created_at = $5, updated_at = $6
			WHERE user_id = $1 AND id = $2`, userID, rec.ID, rec.ScanValue, rec.Name, rec.CreatedAt, rec.UpdatedAt)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.QRLinkedCode, string]{}, fmt.Errorf("updating qr_linked_code: %w", err)
		}
		written[rec.ID] = struct{}{}
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

	for _, rec := range in.Upserts {
		key, err := encodeStreakSessionDailyRollupKey(syncmodel.StreakSessionDailyRollupKey{SessionID: rec.SessionID, LocalDay: rec.LocalDay})
		if err != nil {
			return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, err
		}
		var serverUpdatedAt int64
		err = db.QueryRow(ctx, "SELECT updated_at FROM streak_session_daily_rollups WHERE user_id = $1 AND session_id = $2 AND local_day = $3", userID, rec.SessionID, rec.LocalDay).Scan(&serverUpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = db.Exec(ctx, `INSERT INTO streak_session_daily_rollups (user_id, session_id, local_day, effective_ms, updated_at)
				VALUES ($1, $2, $3, $4, $5)`, userID, rec.SessionID, rec.LocalDay, rec.EffectiveMS, rec.UpdatedAt)
			if err != nil {
				return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("inserting streak_session_daily_rollup: %w", err)
			}
			written[key] = struct{}{}
			continue
		}
		if err != nil {
			return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("looking up streak_session_daily_rollup: %w", err)
		}
		if rec.UpdatedAt <= serverUpdatedAt {
			continue
		}
		_, err = db.Exec(ctx, `UPDATE streak_session_daily_rollups SET effective_ms = $4, updated_at = $5
			WHERE user_id = $1 AND session_id = $2 AND local_day = $3`, userID, rec.SessionID, rec.LocalDay, rec.EffectiveMS, rec.UpdatedAt)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{}, fmt.Errorf("updating streak_session_daily_rollup: %w", err)
		}
		written[key] = struct{}{}
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

	for _, rec := range in.Upserts {
		var serverUpdatedAt int64
		err := db.QueryRow(ctx, "SELECT updated_at FROM streak_daily_aggregates WHERE user_id = $1 AND local_day = $2", userID, rec.LocalDay).Scan(&serverUpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = db.Exec(ctx, `INSERT INTO streak_daily_aggregates (user_id, local_day, effective_ms, qualified, source_session_count, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6)`, userID, rec.LocalDay, rec.EffectiveMS, rec.Qualified, rec.SourceSessionCount, rec.UpdatedAt)
			if err != nil {
				return syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string]{}, fmt.Errorf("inserting streak_daily_aggregate: %w", err)
			}
			written[rec.LocalDay] = struct{}{}
			refreshMetrics = true
			continue
		}
		if err != nil {
			return syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string]{}, fmt.Errorf("looking up streak_daily_aggregate: %w", err)
		}
		if rec.UpdatedAt <= serverUpdatedAt {
			continue
		}
		_, err = db.Exec(ctx, `UPDATE streak_daily_aggregates SET effective_ms = $3, qualified = $4, source_session_count = $5, updated_at = $6
			WHERE user_id = $1 AND local_day = $2`, userID, rec.LocalDay, rec.EffectiveMS, rec.Qualified, rec.SourceSessionCount, rec.UpdatedAt)
		if err != nil {
			return syncmodel.TableChanges[syncmodel.StreakDailyAggregate, string]{}, fmt.Errorf("updating streak_daily_aggregate: %w", err)
		}
		written[rec.LocalDay] = struct{}{}
		refreshMetrics = true
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
		key, err := encodeModeBlockedAppKey(syncmodel.ModeBlockedAppKey{ModeID: modeIDVal, Platform: platform, AppIdentifier: appIdentifier})
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
