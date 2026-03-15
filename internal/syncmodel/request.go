package syncmodel

import (
	"strconv"
	"strings"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
)

type RequestTableSync[T any, D any] struct {
	Cursor    *int64 `json:"cursor"`
	Upserts   []T    `json:"upserts"`
	Deletions []D    `json:"deletions"`
}

type RequestTables struct {
	Modes                      *RequestTableSync[ModeRequest, string]                                          `json:"modes,omitempty"`
	ModeBlockedApps            *RequestTableSync[ModeBlockedAppRequest, ModeBlockedAppKey]                     `json:"mode_blocked_apps,omitempty"`
	Schedules                  *RequestTableSync[ScheduleRequest, string]                                      `json:"schedules,omitempty"`
	RestrictionSessions        *RequestTableSync[RestrictionSessionRequest, string]                            `json:"restriction_sessions,omitempty"`
	RestrictionLifecycleEvents *RequestTableSync[RestrictionLifecycleEventRequest, string]                     `json:"restriction_lifecycle_events,omitempty"`
	NFCLinkedChips             *RequestTableSync[NFCLinkedChipRequest, string]                                 `json:"nfc_linked_chips,omitempty"`
	QRLinkedCodes              *RequestTableSync[QRLinkedCodeRequest, string]                                  `json:"qr_linked_codes,omitempty"`
	StreakSessionDailyRollups  *RequestTableSync[StreakSessionDailyRollupRequest, StreakSessionDailyRollupKey] `json:"streak_session_daily_rollups,omitempty"`
	StreakDailyAggregates      *RequestTableSync[StreakDailyAggregateRequest, string]                          `json:"streak_daily_aggregates,omitempty"`
}

type ModeRequest struct {
	ID                    string                    `json:"id"`
	Title                 string                    `json:"title"`
	TextOnScreen          string                    `json:"text_on_screen"`
	Description           *string                   `json:"description"`
	AllowedPausesCount    *int                      `json:"allowed_pauses_count"`
	MinimumDurationMS     *int                      `json:"minimum_duration_ms"`
	EndingPausingScenario ModeEndingPausingScenario `json:"ending_pausing_scenario"`
	IconToken             string                    `json:"icon_token"`
	CreatedAt             *int64                    `json:"created_at"`
	UpdatedAt             *int64                    `json:"updated_at"`
}

type ModeBlockedAppRequest struct {
	ModeID        string         `json:"mode_id"`
	Platform      DevicePlatform `json:"platform"`
	AppIdentifier string         `json:"app_identifier"`
	CreatedAt     *int64         `json:"created_at"`
	UpdatedAt     *int64         `json:"updated_at"`
}

type ScheduleRequest struct {
	ID          string `json:"id"`
	ModeID      string `json:"mode_id"`
	Days        string `json:"days"`
	StartMinute *int   `json:"start_minute"`
	EndMinute   *int   `json:"end_minute"`
	Enabled     *int   `json:"enabled"`
	CreatedAt   *int64 `json:"created_at"`
	UpdatedAt   *int64 `json:"updated_at"`
}

type RestrictionSessionRequest struct {
	SessionID         string                            `json:"session_id"`
	ModeID            string                            `json:"mode_id"`
	Source            RestrictionSessionSource          `json:"source"`
	StartedAt         *int64                            `json:"started_at"`
	EndedAt           *int64                            `json:"ended_at"`
	PauseCount        *int                              `json:"pause_count"`
	TotalPausedMS     *int                              `json:"total_paused_ms"`
	LastPausedAt      *int64                            `json:"last_paused_at"`
	IntegrityStatus   RestrictionSessionIntegrityStatus `json:"integrity_status"`
	LastAnomalyReason *string                           `json:"last_anomaly_reason"`
	LastEventID       string                            `json:"last_event_id"`
	CreatedAt         *int64                            `json:"created_at"`
	UpdatedAt         *int64                            `json:"updated_at"`
}

type RestrictionLifecycleEventRequest struct {
	ID         string                     `json:"id"`
	SessionID  string                     `json:"session_id"`
	ModeID     string                     `json:"mode_id"`
	Action     RestrictionLifecycleAction `json:"action"`
	Source     RestrictionSessionSource   `json:"source"`
	Reason     RestrictionLifecycleReason `json:"reason"`
	OccurredAt *int64                     `json:"occurred_at"`
	CreatedAt  *int64                     `json:"created_at"`
}

type NFCLinkedChipRequest struct {
	ID             string `json:"id"`
	ChipIdentifier string `json:"chip_identifier"`
	Name           string `json:"name"`
	CreatedAt      *int64 `json:"created_at"`
	UpdatedAt      *int64 `json:"updated_at"`
}

type QRLinkedCodeRequest struct {
	ID        string `json:"id"`
	ScanValue string `json:"scan_value"`
	Name      string `json:"name"`
	CreatedAt *int64 `json:"created_at"`
	UpdatedAt *int64 `json:"updated_at"`
}

type StreakSessionDailyRollupRequest struct {
	SessionID   string `json:"session_id"`
	LocalDay    string `json:"local_day"`
	EffectiveMS *int   `json:"effective_ms"`
	UpdatedAt   *int64 `json:"updated_at"`
}

type StreakDailyAggregateRequest struct {
	LocalDay           string `json:"local_day"`
	EffectiveMS        *int   `json:"effective_ms"`
	Qualified          *int   `json:"qualified"`
	SourceSessionCount *int   `json:"source_session_count"`
	UpdatedAt          *int64 `json:"updated_at"`
}

type Request struct {
	Tables RequestTables `json:"tables"`
}

// maxUpsertBatch is the maximum number of rows accepted per table per sync
// request. PostgreSQL caps query parameters at 65535; with the largest row
// having ~13 columns, this leaves comfortable headroom.
const maxUpsertBatch = 5000

func (r Request) ValidateAndConvert() (Tables, apperror.FieldErrors) {
	fields := make(apperror.FieldErrors)
	out := Tables{}

	if r.Tables.Modes != nil {
		validateTableCursor(fields, "tables.modes", r.Tables.Modes.Cursor)
		validateBatchSize(fields, "tables.modes", len(r.Tables.Modes.Upserts))
		out.Modes = convertModes(fields, *r.Tables.Modes)
	}
	if r.Tables.ModeBlockedApps != nil {
		validateTableCursor(fields, "tables.mode_blocked_apps", r.Tables.ModeBlockedApps.Cursor)
		validateBatchSize(fields, "tables.mode_blocked_apps", len(r.Tables.ModeBlockedApps.Upserts))
		out.ModeBlockedApps = convertModeBlockedApps(fields, *r.Tables.ModeBlockedApps)
	}
	if r.Tables.Schedules != nil {
		validateTableCursor(fields, "tables.schedules", r.Tables.Schedules.Cursor)
		validateBatchSize(fields, "tables.schedules", len(r.Tables.Schedules.Upserts))
		out.Schedules = convertSchedules(fields, *r.Tables.Schedules)
	}
	if r.Tables.RestrictionSessions != nil {
		validateTableCursor(fields, "tables.restriction_sessions", r.Tables.RestrictionSessions.Cursor)
		validateBatchSize(fields, "tables.restriction_sessions", len(r.Tables.RestrictionSessions.Upserts))
		out.RestrictionSessions = convertRestrictionSessions(fields, *r.Tables.RestrictionSessions)
	}
	if r.Tables.RestrictionLifecycleEvents != nil {
		validateTableCursor(fields, "tables.restriction_lifecycle_events", r.Tables.RestrictionLifecycleEvents.Cursor)
		validateBatchSize(fields, "tables.restriction_lifecycle_events", len(r.Tables.RestrictionLifecycleEvents.Upserts))
		if len(r.Tables.RestrictionLifecycleEvents.Deletions) > 0 {
			fields["tables.restriction_lifecycle_events.deletions"] = "deletions not supported for append-only table"
		}
		out.RestrictionLifecycleEvents = convertRestrictionLifecycleEvents(fields, *r.Tables.RestrictionLifecycleEvents)
	}
	if r.Tables.NFCLinkedChips != nil {
		validateTableCursor(fields, "tables.nfc_linked_chips", r.Tables.NFCLinkedChips.Cursor)
		validateBatchSize(fields, "tables.nfc_linked_chips", len(r.Tables.NFCLinkedChips.Upserts))
		out.NFCLinkedChips = convertNFCLinkedChips(fields, *r.Tables.NFCLinkedChips)
	}
	if r.Tables.QRLinkedCodes != nil {
		validateTableCursor(fields, "tables.qr_linked_codes", r.Tables.QRLinkedCodes.Cursor)
		validateBatchSize(fields, "tables.qr_linked_codes", len(r.Tables.QRLinkedCodes.Upserts))
		out.QRLinkedCodes = convertQRLinkedCodes(fields, *r.Tables.QRLinkedCodes)
	}
	if r.Tables.StreakSessionDailyRollups != nil {
		validateTableCursor(fields, "tables.streak_session_daily_rollups", r.Tables.StreakSessionDailyRollups.Cursor)
		validateBatchSize(fields, "tables.streak_session_daily_rollups", len(r.Tables.StreakSessionDailyRollups.Upserts))
		out.StreakSessionDailyRollups = convertStreakSessionDailyRollups(fields, *r.Tables.StreakSessionDailyRollups)
	}
	if r.Tables.StreakDailyAggregates != nil {
		validateTableCursor(fields, "tables.streak_daily_aggregates", r.Tables.StreakDailyAggregates.Cursor)
		if len(r.Tables.StreakDailyAggregates.Upserts) > 0 {
			fields["tables.streak_daily_aggregates.upserts"] = "upserts not supported for server-derived table"
		}
		if len(r.Tables.StreakDailyAggregates.Deletions) > 0 {
			fields["tables.streak_daily_aggregates.deletions"] = "deletions not supported for server-derived table"
		}
		cursor := cursorValue(r.Tables.StreakDailyAggregates.Cursor)
		out.StreakDailyAggregates = &TableSync[StreakDailyAggregate, string]{Cursor: cursor}
	}

	return out, fields
}

func validateTableCursor(fields apperror.FieldErrors, tablePath string, cursor *int64) {
	if cursor == nil {
		fields[tablePath+".cursor"] = "cursor is required"
		return
	}
	if *cursor < 0 {
		fields[tablePath+".cursor"] = "cursor must be >= 0"
	}
}

func validateBatchSize(fields apperror.FieldErrors, tablePath string, count int) {
	if count > maxUpsertBatch {
		fields[tablePath+".upserts"] = "upserts exceeds maximum batch size of " + itoa(maxUpsertBatch)
	}
}

func cursorValue(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func convertModes(fields apperror.FieldErrors, in RequestTableSync[ModeRequest, string]) *TableSync[Mode, string] {
	out := &TableSync[Mode, string]{Cursor: cursorValue(in.Cursor)}
	seen := make(map[string]bool)
	for i, rec := range in.Upserts {
		path := "tables.modes.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.ID) == "" {
			fields[path+".id"] = "id is required"
		} else {
			if seen[rec.ID] {
				fields[path+".id"] = "duplicate key in batch"
			}
			seen[rec.ID] = true
		}
		if strings.TrimSpace(rec.Title) == "" {
			fields[path+".title"] = "title is required"
		}
		if strings.TrimSpace(rec.TextOnScreen) == "" {
			fields[path+".text_on_screen"] = "text_on_screen is required"
		}
		if rec.EndingPausingScenario == "" {
			fields[path+".ending_pausing_scenario"] = "ending_pausing_scenario is required"
		}
		if strings.TrimSpace(rec.IconToken) == "" {
			fields[path+".icon_token"] = "icon_token is required"
		}
		if rec.AllowedPausesCount == nil {
			fields[path+".allowed_pauses_count"] = "allowed_pauses_count is required"
		} else if *rec.AllowedPausesCount < 0 {
			fields[path+".allowed_pauses_count"] = "allowed_pauses_count must be >= 0"
		}
		if rec.MinimumDurationMS != nil && *rec.MinimumDurationMS < 1000 {
			fields[path+".minimum_duration_ms"] = "minimum_duration_ms must be >= 1000"
		}
		if rec.CreatedAt == nil {
			fields[path+".created_at"] = "created_at is required"
		}
		if rec.UpdatedAt == nil {
			fields[path+".updated_at"] = "updated_at is required"
		}
		if rec.CreatedAt == nil || rec.UpdatedAt == nil || rec.AllowedPausesCount == nil {
			continue
		}
		out.Upserts = append(out.Upserts, Mode{
			ID:                    rec.ID,
			Title:                 rec.Title,
			TextOnScreen:          rec.TextOnScreen,
			Description:           rec.Description,
			AllowedPausesCount:    *rec.AllowedPausesCount,
			MinimumDurationMS:     rec.MinimumDurationMS,
			EndingPausingScenario: rec.EndingPausingScenario,
			IconToken:             rec.IconToken,
			CreatedAt:             *rec.CreatedAt,
			UpdatedAt:             *rec.UpdatedAt,
		})
	}
	for i, id := range in.Deletions {
		if strings.TrimSpace(id) == "" {
			fields["tables.modes.deletions["+itoa(i)+"]"] = "id is required"
			continue
		}
		out.Deletions = append(out.Deletions, id)
	}
	deleteSet := make(map[string]bool)
	for _, id := range out.Deletions {
		deleteSet[id] = true
	}
	for _, u := range out.Upserts {
		if deleteSet[u.ID] {
			fields["tables.modes"] = "same key appears in both upserts and deletions"
			break
		}
	}
	return out
}

func convertModeBlockedApps(fields apperror.FieldErrors, in RequestTableSync[ModeBlockedAppRequest, ModeBlockedAppKey]) *TableSync[ModeBlockedApp, ModeBlockedAppKey] {
	out := &TableSync[ModeBlockedApp, ModeBlockedAppKey]{Cursor: cursorValue(in.Cursor)}
	seen := make(map[string]bool)
	for i, rec := range in.Upserts {
		path := "tables.mode_blocked_apps.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.ModeID) == "" {
			fields[path+".mode_id"] = "mode_id is required"
		}
		if rec.Platform == "" {
			fields[path+".platform"] = "platform must be one of android, ios"
		}
		if strings.TrimSpace(rec.AppIdentifier) == "" {
			fields[path+".app_identifier"] = "app_identifier is required"
		}
		compositeKey := rec.ModeID + "\x00" + string(rec.Platform) + "\x00" + rec.AppIdentifier
		if strings.TrimSpace(rec.ModeID) != "" && rec.Platform != "" && strings.TrimSpace(rec.AppIdentifier) != "" {
			if seen[compositeKey] {
				fields[path+".mode_id"] = "duplicate key in batch"
			}
			seen[compositeKey] = true
		}
		if rec.CreatedAt == nil {
			fields[path+".created_at"] = "created_at is required"
		}
		if rec.UpdatedAt == nil {
			fields[path+".updated_at"] = "updated_at is required"
		}
		if rec.CreatedAt == nil || rec.UpdatedAt == nil {
			continue
		}
		out.Upserts = append(out.Upserts, ModeBlockedApp{ModeID: rec.ModeID, Platform: rec.Platform, AppIdentifier: rec.AppIdentifier, CreatedAt: *rec.CreatedAt, UpdatedAt: *rec.UpdatedAt})
	}
	for i, d := range in.Deletions {
		path := "tables.mode_blocked_apps.deletions[" + itoa(i) + "]"
		if strings.TrimSpace(d.ModeID) == "" {
			fields[path+".mode_id"] = "mode_id is required"
		}
		if d.Platform == "" {
			fields[path+".platform"] = "platform must be one of android, ios"
		}
		if strings.TrimSpace(d.AppIdentifier) == "" {
			fields[path+".app_identifier"] = "app_identifier is required"
		}
		out.Deletions = append(out.Deletions, ModeBlockedAppKey{ModeID: d.ModeID, Platform: d.Platform, AppIdentifier: d.AppIdentifier})
	}
	deleteSet := make(map[string]bool)
	for _, d := range out.Deletions {
		deleteSet[d.ModeID+"\x00"+string(d.Platform)+"\x00"+d.AppIdentifier] = true
	}
	for _, u := range out.Upserts {
		if deleteSet[u.ModeID+"\x00"+string(u.Platform)+"\x00"+u.AppIdentifier] {
			fields["tables.mode_blocked_apps"] = "same key appears in both upserts and deletions"
			break
		}
	}
	return out
}

func convertSchedules(fields apperror.FieldErrors, in RequestTableSync[ScheduleRequest, string]) *TableSync[Schedule, string] {
	out := &TableSync[Schedule, string]{Cursor: cursorValue(in.Cursor)}
	seen := make(map[string]bool)
	for i, rec := range in.Upserts {
		path := "tables.schedules.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.ID) == "" {
			fields[path+".id"] = "id is required"
		} else {
			if seen[rec.ID] {
				fields[path+".id"] = "duplicate key in batch"
			}
			seen[rec.ID] = true
		}
		if strings.TrimSpace(rec.ModeID) == "" {
			fields[path+".mode_id"] = "mode_id is required"
		}
		if strings.TrimSpace(rec.Days) == "" {
			fields[path+".days"] = "days is required"
		} else {
			validDays := map[string]bool{"mon": true, "tue": true, "wed": true, "thu": true, "fri": true, "sat": true, "sun": true}
			for _, d := range strings.Split(rec.Days, ",") {
				if !validDays[strings.TrimSpace(d)] {
					fields[path+".days"] = "days must be comma-separated values from {mon,tue,wed,thu,fri,sat,sun}"
					break
				}
			}
		}
		if rec.StartMinute == nil {
			fields[path+".start_minute"] = "start_minute is required"
		} else if *rec.StartMinute < 0 || *rec.StartMinute > 1439 {
			fields[path+".start_minute"] = "start_minute must be between 0 and 1439"
		}
		if rec.EndMinute == nil {
			fields[path+".end_minute"] = "end_minute is required"
		} else if *rec.EndMinute < 0 || *rec.EndMinute > 1439 {
			fields[path+".end_minute"] = "end_minute must be between 0 and 1439"
		}
		if rec.Enabled == nil {
			fields[path+".enabled"] = "enabled is required"
		} else if *rec.Enabled != 0 && *rec.Enabled != 1 {
			fields[path+".enabled"] = "enabled must be 0 or 1"
		}
		if rec.CreatedAt == nil {
			fields[path+".created_at"] = "created_at is required"
		}
		if rec.UpdatedAt == nil {
			fields[path+".updated_at"] = "updated_at is required"
		}
		if rec.StartMinute == nil || rec.EndMinute == nil || rec.Enabled == nil || rec.CreatedAt == nil || rec.UpdatedAt == nil {
			continue
		}
		out.Upserts = append(out.Upserts, Schedule{ID: rec.ID, ModeID: rec.ModeID, Days: rec.Days, StartMinute: *rec.StartMinute, EndMinute: *rec.EndMinute, Enabled: *rec.Enabled, CreatedAt: *rec.CreatedAt, UpdatedAt: *rec.UpdatedAt})
	}
	for i, id := range in.Deletions {
		if strings.TrimSpace(id) == "" {
			fields["tables.schedules.deletions["+itoa(i)+"]"] = "id is required"
			continue
		}
		out.Deletions = append(out.Deletions, id)
	}
	deleteSet := make(map[string]bool)
	for _, id := range out.Deletions {
		deleteSet[id] = true
	}
	for _, u := range out.Upserts {
		if deleteSet[u.ID] {
			fields["tables.schedules"] = "same key appears in both upserts and deletions"
			break
		}
	}
	return out
}

func convertRestrictionSessions(fields apperror.FieldErrors, in RequestTableSync[RestrictionSessionRequest, string]) *TableSync[RestrictionSession, string] {
	out := &TableSync[RestrictionSession, string]{Cursor: cursorValue(in.Cursor)}
	seen := make(map[string]bool)
	for i, rec := range in.Upserts {
		path := "tables.restriction_sessions.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.SessionID) == "" {
			fields[path+".session_id"] = "session_id is required"
		} else {
			if seen[rec.SessionID] {
				fields[path+".session_id"] = "duplicate key in batch"
			}
			seen[rec.SessionID] = true
		}
		if strings.TrimSpace(rec.ModeID) == "" {
			fields[path+".mode_id"] = "mode_id is required"
		}
		if rec.Source == "" {
			fields[path+".source"] = "source must be one of manual, schedule"
		}
		if rec.StartedAt == nil {
			fields[path+".started_at"] = "started_at is required"
		}
		if rec.PauseCount == nil {
			fields[path+".pause_count"] = "pause_count is required"
		} else if *rec.PauseCount < 0 {
			fields[path+".pause_count"] = "pause_count must be >= 0"
		}
		if rec.TotalPausedMS == nil {
			fields[path+".total_paused_ms"] = "total_paused_ms is required"
		} else if *rec.TotalPausedMS < 0 {
			fields[path+".total_paused_ms"] = "total_paused_ms must be >= 0"
		}
		if rec.IntegrityStatus == "" {
			fields[path+".integrity_status"] = "integrity_status must be one of ok, anomaly"
		}
		if strings.TrimSpace(rec.LastEventID) == "" {
			fields[path+".last_event_id"] = "last_event_id is required"
		}
		if rec.CreatedAt == nil {
			fields[path+".created_at"] = "created_at is required"
		}
		if rec.UpdatedAt == nil {
			fields[path+".updated_at"] = "updated_at is required"
		}
		if rec.StartedAt != nil && rec.EndedAt != nil && *rec.EndedAt < *rec.StartedAt {
			fields[path+".ended_at"] = "ended_at must be >= started_at"
		}
		if rec.StartedAt == nil || rec.PauseCount == nil || rec.TotalPausedMS == nil || rec.CreatedAt == nil || rec.UpdatedAt == nil {
			continue
		}
		out.Upserts = append(out.Upserts, RestrictionSession{
			SessionID:         rec.SessionID,
			ModeID:            rec.ModeID,
			Source:            rec.Source,
			StartedAt:         *rec.StartedAt,
			EndedAt:           rec.EndedAt,
			PauseCount:        *rec.PauseCount,
			TotalPausedMS:     *rec.TotalPausedMS,
			LastPausedAt:      rec.LastPausedAt,
			IntegrityStatus:   rec.IntegrityStatus,
			LastAnomalyReason: rec.LastAnomalyReason,
			LastEventID:       rec.LastEventID,
			CreatedAt:         *rec.CreatedAt,
			UpdatedAt:         *rec.UpdatedAt,
		})
	}
	for i, id := range in.Deletions {
		if strings.TrimSpace(id) == "" {
			fields["tables.restriction_sessions.deletions["+itoa(i)+"]"] = "id is required"
			continue
		}
		out.Deletions = append(out.Deletions, id)
	}
	deleteSet := make(map[string]bool)
	for _, id := range out.Deletions {
		deleteSet[id] = true
	}
	for _, u := range out.Upserts {
		if deleteSet[u.SessionID] {
			fields["tables.restriction_sessions"] = "same key appears in both upserts and deletions"
			break
		}
	}
	return out
}

func convertRestrictionLifecycleEvents(fields apperror.FieldErrors, in RequestTableSync[RestrictionLifecycleEventRequest, string]) *TableSync[RestrictionLifecycleEvent, string] {
	out := &TableSync[RestrictionLifecycleEvent, string]{Cursor: cursorValue(in.Cursor)}
	seen := make(map[string]bool)
	for i, rec := range in.Upserts {
		path := "tables.restriction_lifecycle_events.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.ID) == "" {
			fields[path+".id"] = "id is required"
		} else {
			if seen[rec.ID] {
				fields[path+".id"] = "duplicate key in batch"
			}
			seen[rec.ID] = true
		}
		if strings.TrimSpace(rec.SessionID) == "" {
			fields[path+".session_id"] = "session_id is required"
		}
		if strings.TrimSpace(rec.ModeID) == "" {
			fields[path+".mode_id"] = "mode_id is required"
		}
		if rec.Action == "" {
			fields[path+".action"] = "action must be one of START, PAUSE, RESUME, END"
		}
		if rec.Source == "" {
			fields[path+".source"] = "source must be one of manual, schedule"
		}
		if rec.Reason == "" {
			fields[path+".reason"] = "reason is required"
		}
		if rec.OccurredAt == nil {
			fields[path+".occurred_at"] = "occurred_at is required"
		}
		if rec.CreatedAt == nil {
			fields[path+".created_at"] = "created_at is required"
		}
		if rec.OccurredAt == nil || rec.CreatedAt == nil {
			continue
		}
		out.Upserts = append(out.Upserts, RestrictionLifecycleEvent{ID: rec.ID, SessionID: rec.SessionID, ModeID: rec.ModeID, Action: rec.Action, Source: rec.Source, Reason: rec.Reason, OccurredAt: *rec.OccurredAt, CreatedAt: *rec.CreatedAt})
	}
	for i, id := range in.Deletions {
		if strings.TrimSpace(id) == "" {
			fields["tables.restriction_lifecycle_events.deletions["+itoa(i)+"]"] = "id is required"
			continue
		}
		out.Deletions = append(out.Deletions, id)
	}
	return out
}

func convertNFCLinkedChips(fields apperror.FieldErrors, in RequestTableSync[NFCLinkedChipRequest, string]) *TableSync[NFCLinkedChip, string] {
	out := &TableSync[NFCLinkedChip, string]{Cursor: cursorValue(in.Cursor)}
	seen := make(map[string]bool)
	for i, rec := range in.Upserts {
		path := "tables.nfc_linked_chips.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.ID) == "" {
			fields[path+".id"] = "id is required"
		} else {
			if seen[rec.ID] {
				fields[path+".id"] = "duplicate key in batch"
			}
			seen[rec.ID] = true
		}
		if strings.TrimSpace(rec.ChipIdentifier) == "" {
			fields[path+".chip_identifier"] = "chip_identifier is required"
		}
		if strings.TrimSpace(rec.Name) == "" {
			fields[path+".name"] = "name is required"
		}
		if rec.CreatedAt == nil {
			fields[path+".created_at"] = "created_at is required"
		}
		if rec.UpdatedAt == nil {
			fields[path+".updated_at"] = "updated_at is required"
		}
		if rec.CreatedAt == nil || rec.UpdatedAt == nil {
			continue
		}
		out.Upserts = append(out.Upserts, NFCLinkedChip{ID: rec.ID, ChipIdentifier: rec.ChipIdentifier, Name: rec.Name, CreatedAt: *rec.CreatedAt, UpdatedAt: *rec.UpdatedAt})
	}
	for i, id := range in.Deletions {
		if strings.TrimSpace(id) == "" {
			fields["tables.nfc_linked_chips.deletions["+itoa(i)+"]"] = "id is required"
			continue
		}
		out.Deletions = append(out.Deletions, id)
	}
	deleteSet := make(map[string]bool)
	for _, id := range out.Deletions {
		deleteSet[id] = true
	}
	for _, u := range out.Upserts {
		if deleteSet[u.ID] {
			fields["tables.nfc_linked_chips"] = "same key appears in both upserts and deletions"
			break
		}
	}
	return out
}

func convertQRLinkedCodes(fields apperror.FieldErrors, in RequestTableSync[QRLinkedCodeRequest, string]) *TableSync[QRLinkedCode, string] {
	out := &TableSync[QRLinkedCode, string]{Cursor: cursorValue(in.Cursor)}
	seen := make(map[string]bool)
	for i, rec := range in.Upserts {
		path := "tables.qr_linked_codes.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.ID) == "" {
			fields[path+".id"] = "id is required"
		} else {
			if seen[rec.ID] {
				fields[path+".id"] = "duplicate key in batch"
			}
			seen[rec.ID] = true
		}
		if strings.TrimSpace(rec.ScanValue) == "" {
			fields[path+".scan_value"] = "scan_value is required"
		}
		if strings.TrimSpace(rec.Name) == "" {
			fields[path+".name"] = "name is required"
		}
		if rec.CreatedAt == nil {
			fields[path+".created_at"] = "created_at is required"
		}
		if rec.UpdatedAt == nil {
			fields[path+".updated_at"] = "updated_at is required"
		}
		if rec.CreatedAt == nil || rec.UpdatedAt == nil {
			continue
		}
		out.Upserts = append(out.Upserts, QRLinkedCode{ID: rec.ID, ScanValue: rec.ScanValue, Name: rec.Name, CreatedAt: *rec.CreatedAt, UpdatedAt: *rec.UpdatedAt})
	}
	for i, id := range in.Deletions {
		if strings.TrimSpace(id) == "" {
			fields["tables.qr_linked_codes.deletions["+itoa(i)+"]"] = "id is required"
			continue
		}
		out.Deletions = append(out.Deletions, id)
	}
	deleteSet := make(map[string]bool)
	for _, id := range out.Deletions {
		deleteSet[id] = true
	}
	for _, u := range out.Upserts {
		if deleteSet[u.ID] {
			fields["tables.qr_linked_codes"] = "same key appears in both upserts and deletions"
			break
		}
	}
	return out
}

func convertStreakSessionDailyRollups(fields apperror.FieldErrors, in RequestTableSync[StreakSessionDailyRollupRequest, StreakSessionDailyRollupKey]) *TableSync[StreakSessionDailyRollup, StreakSessionDailyRollupKey] {
	out := &TableSync[StreakSessionDailyRollup, StreakSessionDailyRollupKey]{Cursor: cursorValue(in.Cursor)}
	seen := make(map[string]bool)
	for i, rec := range in.Upserts {
		path := "tables.streak_session_daily_rollups.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.SessionID) == "" {
			fields[path+".session_id"] = "session_id is required"
		}
		if !isLocalDay(rec.LocalDay) {
			fields[path+".local_day"] = "local_day must be YYYY-MM-DD"
		}
		compositeKey := rec.SessionID + "\x00" + rec.LocalDay
		if strings.TrimSpace(rec.SessionID) != "" && isLocalDay(rec.LocalDay) {
			if seen[compositeKey] {
				fields[path+".session_id"] = "duplicate key in batch"
			}
			seen[compositeKey] = true
		}
		if rec.EffectiveMS == nil {
			fields[path+".effective_ms"] = "effective_ms is required"
		} else if *rec.EffectiveMS < 0 {
			fields[path+".effective_ms"] = "effective_ms must be >= 0"
		}
		if rec.UpdatedAt == nil {
			fields[path+".updated_at"] = "updated_at is required"
		}
		if rec.EffectiveMS == nil || rec.UpdatedAt == nil {
			continue
		}
		out.Upserts = append(out.Upserts, StreakSessionDailyRollup{SessionID: rec.SessionID, LocalDay: rec.LocalDay, EffectiveMS: *rec.EffectiveMS, UpdatedAt: *rec.UpdatedAt})
	}
	for i, d := range in.Deletions {
		path := "tables.streak_session_daily_rollups.deletions[" + itoa(i) + "]"
		if strings.TrimSpace(d.SessionID) == "" {
			fields[path+".session_id"] = "session_id is required"
		}
		if !isLocalDay(d.LocalDay) {
			fields[path+".local_day"] = "local_day must be YYYY-MM-DD"
		}
		out.Deletions = append(out.Deletions, StreakSessionDailyRollupKey{SessionID: d.SessionID, LocalDay: d.LocalDay})
	}
	deleteSet := make(map[string]bool)
	for _, d := range out.Deletions {
		deleteSet[d.SessionID+"\x00"+d.LocalDay] = true
	}
	for _, u := range out.Upserts {
		if deleteSet[u.SessionID+"\x00"+u.LocalDay] {
			fields["tables.streak_session_daily_rollups"] = "same key appears in both upserts and deletions"
			break
		}
	}
	return out
}

func isLocalDay(v string) bool {
	_, err := time.Parse("2006-01-02", v)
	return err == nil
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
