package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/service"
	"github.com/IsorilovA/pauza-server/internal/syncmodel"
)

type SyncServicer interface {
	Sync(ctx context.Context, in service.SyncInput) (service.SyncOutput, error)
}

var _ SyncServicer = (*service.SyncService)(nil)

type SyncHandler struct {
	svc    SyncServicer
	logger *slog.Logger
}

func NewSyncHandler(svc SyncServicer) *SyncHandler {
	return &SyncHandler{svc: svc, logger: slog.Default()}
}

func NewSyncHandlerWithLogger(svc SyncServicer, logger *slog.Logger) *SyncHandler {
	return &SyncHandler{svc: svc, logger: logger}
}

type syncTableRequest[T any, D any] struct {
	LastSyncedAt *int64 `json:"last_synced_at"`
	Upserts      []T    `json:"upserts"`
	Deletions    []D    `json:"deletions"`
}

type syncRequest struct {
	Tables struct {
		Modes *syncTableRequest[modeRequest, string] `json:"modes,omitempty"`

		ModeBlockedApps *syncTableRequest[modeBlockedAppRequest, modeBlockedAppDeletion] `json:"mode_blocked_apps,omitempty"`

		Schedules *syncTableRequest[scheduleRequest, string] `json:"schedules,omitempty"`

		RestrictionSessions *syncTableRequest[restrictionSessionRequest, string] `json:"restriction_sessions,omitempty"`

		RestrictionLifecycleEvents *syncTableRequest[restrictionLifecycleEventRequest, string] `json:"restriction_lifecycle_events,omitempty"`

		NFCLinkedChips *syncTableRequest[nfcLinkedChipRequest, string] `json:"nfc_linked_chips,omitempty"`

		QRLinkedCodes *syncTableRequest[qrLinkedCodeRequest, string] `json:"qr_linked_codes,omitempty"`

		StreakSessionDailyRollups *syncTableRequest[streakSessionDailyRollupRequest, streakSessionDailyRollupDeletion] `json:"streak_session_daily_rollups,omitempty"`

		StreakDailyAggregates *syncTableRequest[streakDailyAggregateRequest, string] `json:"streak_daily_aggregates,omitempty"`
	} `json:"tables"`
}

type modeRequest struct {
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

type modeBlockedAppRequest struct {
	ModeID        string `json:"mode_id"`
	Platform      string `json:"platform"`
	AppIdentifier string `json:"app_identifier"`
	CreatedAt     *int64 `json:"created_at"`
	UpdatedAt     *int64 `json:"updated_at"`
}

type modeBlockedAppDeletion struct {
	ModeID        string `json:"mode_id"`
	Platform      string `json:"platform"`
	AppIdentifier string `json:"app_identifier"`
}

type scheduleRequest struct {
	ID          string `json:"id"`
	ModeID      string `json:"mode_id"`
	Days        string `json:"days"`
	StartMinute *int   `json:"start_minute"`
	EndMinute   *int   `json:"end_minute"`
	Enabled     *int   `json:"enabled"`
	CreatedAt   *int64 `json:"created_at"`
	UpdatedAt   *int64 `json:"updated_at"`
}

type restrictionSessionRequest struct {
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

type restrictionLifecycleEventRequest struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	ModeID     string `json:"mode_id"`
	Action     string `json:"action"`
	Source     string `json:"source"`
	Reason     string `json:"reason"`
	OccurredAt *int64 `json:"occurred_at"`
	CreatedAt  *int64 `json:"created_at"`
}

type nfcLinkedChipRequest struct {
	ID             string `json:"id"`
	ChipIdentifier string `json:"chip_identifier"`
	Name           string `json:"name"`
	CreatedAt      *int64 `json:"created_at"`
	UpdatedAt      *int64 `json:"updated_at"`
}

type qrLinkedCodeRequest struct {
	ID        string `json:"id"`
	ScanValue string `json:"scan_value"`
	Name      string `json:"name"`
	CreatedAt *int64 `json:"created_at"`
	UpdatedAt *int64 `json:"updated_at"`
}

type streakSessionDailyRollupRequest struct {
	SessionID   string `json:"session_id"`
	LocalDay    string `json:"local_day"`
	EffectiveMS *int   `json:"effective_ms"`
	UpdatedAt   *int64 `json:"updated_at"`
}

type streakSessionDailyRollupDeletion struct {
	SessionID string `json:"session_id"`
	LocalDay  string `json:"local_day"`
}

type streakDailyAggregateRequest struct {
	LocalDay           string `json:"local_day"`
	EffectiveMS        *int   `json:"effective_ms"`
	Qualified          *int   `json:"qualified"`
	SourceSessionCount *int   `json:"source_session_count"`
	UpdatedAt          *int64 `json:"updated_at"`
}

func (h *SyncHandler) Sync(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req syncRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	fields := make(apperror.FieldErrors)
	out := syncmodel.Tables{}

	if req.Tables.Modes != nil {
		validateTableCursor(fields, "tables.modes", req.Tables.Modes.LastSyncedAt)
		out.Modes = convertModes(fields, *req.Tables.Modes)
	}

	if req.Tables.ModeBlockedApps != nil {
		validateTableCursor(fields, "tables.mode_blocked_apps", req.Tables.ModeBlockedApps.LastSyncedAt)
		out.ModeBlockedApps = convertModeBlockedApps(fields, *req.Tables.ModeBlockedApps)
	}

	if req.Tables.Schedules != nil {
		validateTableCursor(fields, "tables.schedules", req.Tables.Schedules.LastSyncedAt)
		out.Schedules = convertSchedules(fields, *req.Tables.Schedules)
	}

	if req.Tables.RestrictionSessions != nil {
		validateTableCursor(fields, "tables.restriction_sessions", req.Tables.RestrictionSessions.LastSyncedAt)
		out.RestrictionSessions = convertRestrictionSessions(fields, *req.Tables.RestrictionSessions)
	}

	if req.Tables.RestrictionLifecycleEvents != nil {
		validateTableCursor(fields, "tables.restriction_lifecycle_events", req.Tables.RestrictionLifecycleEvents.LastSyncedAt)
		out.RestrictionLifecycleEvents = convertRestrictionLifecycleEvents(fields, *req.Tables.RestrictionLifecycleEvents)
	}

	if req.Tables.NFCLinkedChips != nil {
		validateTableCursor(fields, "tables.nfc_linked_chips", req.Tables.NFCLinkedChips.LastSyncedAt)
		out.NFCLinkedChips = convertNFCLinkedChips(fields, *req.Tables.NFCLinkedChips)
	}

	if req.Tables.QRLinkedCodes != nil {
		validateTableCursor(fields, "tables.qr_linked_codes", req.Tables.QRLinkedCodes.LastSyncedAt)
		out.QRLinkedCodes = convertQRLinkedCodes(fields, *req.Tables.QRLinkedCodes)
	}

	if req.Tables.StreakSessionDailyRollups != nil {
		validateTableCursor(fields, "tables.streak_session_daily_rollups", req.Tables.StreakSessionDailyRollups.LastSyncedAt)
		out.StreakSessionDailyRollups = convertStreakSessionDailyRollups(fields, *req.Tables.StreakSessionDailyRollups)
	}

	if req.Tables.StreakDailyAggregates != nil {
		validateTableCursor(fields, "tables.streak_daily_aggregates", req.Tables.StreakDailyAggregates.LastSyncedAt)
		out.StreakDailyAggregates = convertStreakDailyAggregates(fields, *req.Tables.StreakDailyAggregates)
	}

	if len(fields) > 0 {
		apperror.ValidationFieldErrors(w, "Invalid request body", fields)
		return
	}

	res, err := h.svc.Sync(r.Context(), service.SyncInput{UserID: userID, Tables: out})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, res, "sync")
}

func validateTableCursor(fields apperror.FieldErrors, tablePath string, cursor *int64) {
	if cursor == nil {
		fields[tablePath+".last_synced_at"] = "last_synced_at is required"
		return
	}
	if *cursor < 0 {
		fields[tablePath+".last_synced_at"] = "last_synced_at must be >= 0"
	}
}

func cursorValue(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func convertModes(fields apperror.FieldErrors, in syncTableRequest[modeRequest, string]) *syncmodel.TableSync[syncmodel.Mode, string] {
	out := &syncmodel.TableSync[syncmodel.Mode, string]{LastSyncedAt: cursorValue(in.LastSyncedAt), Deletions: in.Deletions}
	for i, rec := range in.Upserts {
		path := "tables.modes.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.ID) == "" {
			fields[path+".id"] = "id is required"
		}
		if strings.TrimSpace(rec.Title) == "" {
			fields[path+".title"] = "title is required"
		}
		if strings.TrimSpace(rec.TextOnScreen) == "" {
			fields[path+".text_on_screen"] = "text_on_screen is required"
		}
		if strings.TrimSpace(rec.EndingPausingScenario) == "" {
			fields[path+".ending_pausing_scenario"] = "ending_pausing_scenario is required"
		} else if rec.EndingPausingScenario != "nfc" && rec.EndingPausingScenario != "qr" && rec.EndingPausingScenario != "manual" {
			fields[path+".ending_pausing_scenario"] = "ending_pausing_scenario must be one of nfc, qr, manual"
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
		out.Upserts = append(out.Upserts, syncmodel.Mode{
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
	return out
}

func convertModeBlockedApps(fields apperror.FieldErrors, in syncTableRequest[modeBlockedAppRequest, modeBlockedAppDeletion]) *syncmodel.TableSync[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey] {
	out := &syncmodel.TableSync[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]{LastSyncedAt: cursorValue(in.LastSyncedAt)}
	for i, rec := range in.Upserts {
		path := "tables.mode_blocked_apps.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.ModeID) == "" {
			fields[path+".mode_id"] = "mode_id is required"
		}
		if rec.Platform != "android" && rec.Platform != "ios" {
			fields[path+".platform"] = "platform must be one of android, ios"
		}
		if strings.TrimSpace(rec.AppIdentifier) == "" {
			fields[path+".app_identifier"] = "app_identifier is required"
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
		out.Upserts = append(out.Upserts, syncmodel.ModeBlockedApp{ModeID: rec.ModeID, Platform: rec.Platform, AppIdentifier: rec.AppIdentifier, CreatedAt: *rec.CreatedAt, UpdatedAt: *rec.UpdatedAt})
	}
	for i, d := range in.Deletions {
		path := "tables.mode_blocked_apps.deletions[" + itoa(i) + "]"
		if strings.TrimSpace(d.ModeID) == "" {
			fields[path+".mode_id"] = "mode_id is required"
		}
		if d.Platform != "android" && d.Platform != "ios" {
			fields[path+".platform"] = "platform must be one of android, ios"
		}
		if strings.TrimSpace(d.AppIdentifier) == "" {
			fields[path+".app_identifier"] = "app_identifier is required"
		}
		out.Deletions = append(out.Deletions, syncmodel.ModeBlockedAppKey{ModeID: d.ModeID, Platform: d.Platform, AppIdentifier: d.AppIdentifier})
	}
	return out
}

func convertSchedules(fields apperror.FieldErrors, in syncTableRequest[scheduleRequest, string]) *syncmodel.TableSync[syncmodel.Schedule, string] {
	out := &syncmodel.TableSync[syncmodel.Schedule, string]{LastSyncedAt: cursorValue(in.LastSyncedAt), Deletions: in.Deletions}
	for i, rec := range in.Upserts {
		path := "tables.schedules.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.ID) == "" {
			fields[path+".id"] = "id is required"
		}
		if strings.TrimSpace(rec.ModeID) == "" {
			fields[path+".mode_id"] = "mode_id is required"
		}
		if strings.TrimSpace(rec.Days) == "" {
			fields[path+".days"] = "days is required"
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
		out.Upserts = append(out.Upserts, syncmodel.Schedule{ID: rec.ID, ModeID: rec.ModeID, Days: rec.Days, StartMinute: *rec.StartMinute, EndMinute: *rec.EndMinute, Enabled: *rec.Enabled, CreatedAt: *rec.CreatedAt, UpdatedAt: *rec.UpdatedAt})
	}
	return out
}

func convertRestrictionSessions(fields apperror.FieldErrors, in syncTableRequest[restrictionSessionRequest, string]) *syncmodel.TableSync[syncmodel.RestrictionSession, string] {
	out := &syncmodel.TableSync[syncmodel.RestrictionSession, string]{LastSyncedAt: cursorValue(in.LastSyncedAt), Deletions: in.Deletions}
	for i, rec := range in.Upserts {
		path := "tables.restriction_sessions.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.SessionID) == "" {
			fields[path+".session_id"] = "session_id is required"
		}
		if strings.TrimSpace(rec.ModeID) == "" {
			fields[path+".mode_id"] = "mode_id is required"
		}
		if rec.Source != "manual" && rec.Source != "schedule" {
			fields[path+".source"] = "source must be one of manual, schedule"
		}
		if rec.StartedAt == nil {
			fields[path+".started_at"] = "started_at is required"
		}
		if rec.PauseCount == nil {
			fields[path+".pause_count"] = "pause_count is required"
		}
		if rec.TotalPausedMS == nil {
			fields[path+".total_paused_ms"] = "total_paused_ms is required"
		}
		if rec.IntegrityStatus != "ok" && rec.IntegrityStatus != "anomaly" {
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
		if rec.StartedAt == nil || rec.PauseCount == nil || rec.TotalPausedMS == nil || rec.CreatedAt == nil || rec.UpdatedAt == nil {
			continue
		}
		out.Upserts = append(out.Upserts, syncmodel.RestrictionSession{
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
	return out
}

func convertRestrictionLifecycleEvents(fields apperror.FieldErrors, in syncTableRequest[restrictionLifecycleEventRequest, string]) *syncmodel.TableSync[syncmodel.RestrictionLifecycleEvent, string] {
	out := &syncmodel.TableSync[syncmodel.RestrictionLifecycleEvent, string]{LastSyncedAt: cursorValue(in.LastSyncedAt), Deletions: in.Deletions}
	for i, rec := range in.Upserts {
		path := "tables.restriction_lifecycle_events.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.ID) == "" {
			fields[path+".id"] = "id is required"
		}
		if strings.TrimSpace(rec.SessionID) == "" {
			fields[path+".session_id"] = "session_id is required"
		}
		if strings.TrimSpace(rec.ModeID) == "" {
			fields[path+".mode_id"] = "mode_id is required"
		}
		if rec.Action != "START" && rec.Action != "PAUSE" && rec.Action != "RESUME" && rec.Action != "END" {
			fields[path+".action"] = "action must be one of START, PAUSE, RESUME, END"
		}
		if rec.Source != "manual" && rec.Source != "schedule" {
			fields[path+".source"] = "source must be one of manual, schedule"
		}
		if strings.TrimSpace(rec.Reason) == "" {
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
		out.Upserts = append(out.Upserts, syncmodel.RestrictionLifecycleEvent{ID: rec.ID, SessionID: rec.SessionID, ModeID: rec.ModeID, Action: rec.Action, Source: rec.Source, Reason: rec.Reason, OccurredAt: *rec.OccurredAt, CreatedAt: *rec.CreatedAt})
	}
	return out
}

func convertNFCLinkedChips(fields apperror.FieldErrors, in syncTableRequest[nfcLinkedChipRequest, string]) *syncmodel.TableSync[syncmodel.NFCLinkedChip, string] {
	out := &syncmodel.TableSync[syncmodel.NFCLinkedChip, string]{LastSyncedAt: cursorValue(in.LastSyncedAt), Deletions: in.Deletions}
	for i, rec := range in.Upserts {
		path := "tables.nfc_linked_chips.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.ID) == "" {
			fields[path+".id"] = "id is required"
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
		out.Upserts = append(out.Upserts, syncmodel.NFCLinkedChip{ID: rec.ID, ChipIdentifier: rec.ChipIdentifier, Name: rec.Name, CreatedAt: *rec.CreatedAt, UpdatedAt: *rec.UpdatedAt})
	}
	return out
}

func convertQRLinkedCodes(fields apperror.FieldErrors, in syncTableRequest[qrLinkedCodeRequest, string]) *syncmodel.TableSync[syncmodel.QRLinkedCode, string] {
	out := &syncmodel.TableSync[syncmodel.QRLinkedCode, string]{LastSyncedAt: cursorValue(in.LastSyncedAt), Deletions: in.Deletions}
	for i, rec := range in.Upserts {
		path := "tables.qr_linked_codes.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.ID) == "" {
			fields[path+".id"] = "id is required"
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
		out.Upserts = append(out.Upserts, syncmodel.QRLinkedCode{ID: rec.ID, ScanValue: rec.ScanValue, Name: rec.Name, CreatedAt: *rec.CreatedAt, UpdatedAt: *rec.UpdatedAt})
	}
	return out
}

func convertStreakSessionDailyRollups(fields apperror.FieldErrors, in syncTableRequest[streakSessionDailyRollupRequest, streakSessionDailyRollupDeletion]) *syncmodel.TableSync[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey] {
	out := &syncmodel.TableSync[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]{LastSyncedAt: cursorValue(in.LastSyncedAt)}
	for i, rec := range in.Upserts {
		path := "tables.streak_session_daily_rollups.upserts[" + itoa(i) + "]"
		if strings.TrimSpace(rec.SessionID) == "" {
			fields[path+".session_id"] = "session_id is required"
		}
		if !isLocalDay(rec.LocalDay) {
			fields[path+".local_day"] = "local_day must be YYYY-MM-DD"
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
		out.Upserts = append(out.Upserts, syncmodel.StreakSessionDailyRollup{SessionID: rec.SessionID, LocalDay: rec.LocalDay, EffectiveMS: *rec.EffectiveMS, UpdatedAt: *rec.UpdatedAt})
	}
	for i, d := range in.Deletions {
		path := "tables.streak_session_daily_rollups.deletions[" + itoa(i) + "]"
		if strings.TrimSpace(d.SessionID) == "" {
			fields[path+".session_id"] = "session_id is required"
		}
		if !isLocalDay(d.LocalDay) {
			fields[path+".local_day"] = "local_day must be YYYY-MM-DD"
		}
		out.Deletions = append(out.Deletions, syncmodel.StreakSessionDailyRollupKey{SessionID: d.SessionID, LocalDay: d.LocalDay})
	}
	return out
}

func convertStreakDailyAggregates(fields apperror.FieldErrors, in syncTableRequest[streakDailyAggregateRequest, string]) *syncmodel.TableSync[syncmodel.StreakDailyAggregate, string] {
	out := &syncmodel.TableSync[syncmodel.StreakDailyAggregate, string]{LastSyncedAt: cursorValue(in.LastSyncedAt), Deletions: in.Deletions}
	for i, rec := range in.Upserts {
		path := "tables.streak_daily_aggregates.upserts[" + itoa(i) + "]"
		if !isLocalDay(rec.LocalDay) {
			fields[path+".local_day"] = "local_day must be YYYY-MM-DD"
		}
		if rec.EffectiveMS == nil {
			fields[path+".effective_ms"] = "effective_ms is required"
		} else if *rec.EffectiveMS < 0 {
			fields[path+".effective_ms"] = "effective_ms must be >= 0"
		}
		if rec.Qualified == nil {
			fields[path+".qualified"] = "qualified is required"
		} else if *rec.Qualified != 0 && *rec.Qualified != 1 {
			fields[path+".qualified"] = "qualified must be 0 or 1"
		}
		if rec.SourceSessionCount == nil {
			fields[path+".source_session_count"] = "source_session_count is required"
		} else if *rec.SourceSessionCount < 0 {
			fields[path+".source_session_count"] = "source_session_count must be >= 0"
		}
		if rec.UpdatedAt == nil {
			fields[path+".updated_at"] = "updated_at is required"
		}
		if rec.EffectiveMS == nil || rec.Qualified == nil || rec.SourceSessionCount == nil || rec.UpdatedAt == nil {
			continue
		}
		out.Upserts = append(out.Upserts, syncmodel.StreakDailyAggregate{LocalDay: rec.LocalDay, EffectiveMS: *rec.EffectiveMS, Qualified: *rec.Qualified, SourceSessionCount: *rec.SourceSessionCount, UpdatedAt: *rec.UpdatedAt})
	}
	return out
}

func isLocalDay(v string) bool {
	if len(v) != 10 {
		return false
	}
	if v[4] != '-' || v[7] != '-' {
		return false
	}
	for i := range v {
		if i == 4 || i == 7 {
			continue
		}
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return true
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
