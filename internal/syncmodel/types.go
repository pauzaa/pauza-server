package syncmodel

import (
	"encoding/json"
	"fmt"

	"github.com/IsorilovA/pauza-server/internal/domain"
)

// parseEnum checks whether raw maps to one of the allowed values and returns
// the typed result. kind is included in the error message for debugging.
func parseEnum[T ~string](kind, raw string, valid ...T) (T, error) {
	v := T(raw)
	for _, allowed := range valid {
		if v == allowed {
			return v, nil
		}
	}
	var zero T
	return zero, fmt.Errorf("invalid %s %q", kind, raw)
}

type ModeEndingPausingScenario string

const (
	ModeEndingPausingScenarioManual ModeEndingPausingScenario = "manual"
	ModeEndingPausingScenarioNFC    ModeEndingPausingScenario = "nfc"
	ModeEndingPausingScenarioQR     ModeEndingPausingScenario = "qr"
)

func (s *ModeEndingPausingScenario) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	v, err := parseEnum("ending pausing scenario", raw, ModeEndingPausingScenarioManual, ModeEndingPausingScenarioNFC, ModeEndingPausingScenarioQR)
	if err != nil {
		return err
	}
	*s = v
	return nil
}

// DevicePlatform is an alias for domain.DevicePlatform so that existing
// syncmodel field types remain compatible without changing every struct.
type DevicePlatform = domain.DevicePlatform

type RestrictionSessionSource string

const (
	RestrictionSessionSourceManual   RestrictionSessionSource = "manual"
	RestrictionSessionSourceSchedule RestrictionSessionSource = "schedule"
)

func (s *RestrictionSessionSource) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	v, err := parseEnum("restriction session source", raw, RestrictionSessionSourceManual, RestrictionSessionSourceSchedule)
	if err != nil {
		return err
	}
	*s = v
	return nil
}

type RestrictionSessionIntegrityStatus string

const (
	RestrictionSessionIntegrityStatusOK      RestrictionSessionIntegrityStatus = "ok"
	RestrictionSessionIntegrityStatusAnomaly RestrictionSessionIntegrityStatus = "anomaly"
)

func (s *RestrictionSessionIntegrityStatus) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	v, err := parseEnum("integrity status", raw, RestrictionSessionIntegrityStatusOK, RestrictionSessionIntegrityStatusAnomaly)
	if err != nil {
		return err
	}
	*s = v
	return nil
}

type RestrictionLifecycleAction string

const (
	RestrictionLifecycleActionStarted RestrictionLifecycleAction = "START"
	RestrictionLifecycleActionPaused  RestrictionLifecycleAction = "PAUSE"
	RestrictionLifecycleActionResumed RestrictionLifecycleAction = "RESUME"
	RestrictionLifecycleActionEnded   RestrictionLifecycleAction = "END"
)

func (a *RestrictionLifecycleAction) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	v, err := parseEnum("lifecycle action", raw, RestrictionLifecycleActionStarted, RestrictionLifecycleActionPaused, RestrictionLifecycleActionResumed, RestrictionLifecycleActionEnded)
	if err != nil {
		return err
	}
	*a = v
	return nil
}

type RestrictionLifecycleReason string

const (
	RestrictionLifecycleReasonManual    RestrictionLifecycleReason = "manual"
	RestrictionLifecycleReasonNFC       RestrictionLifecycleReason = "nfc"
	RestrictionLifecycleReasonQR        RestrictionLifecycleReason = "qr"
	RestrictionLifecycleReasonTimer     RestrictionLifecycleReason = "timer"
	RestrictionLifecycleReasonEmergency RestrictionLifecycleReason = "emergency"
	RestrictionLifecycleReasonSchedule  RestrictionLifecycleReason = "schedule"
)

func (r *RestrictionLifecycleReason) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	v, err := parseEnum("lifecycle reason", raw, RestrictionLifecycleReasonManual, RestrictionLifecycleReasonNFC, RestrictionLifecycleReasonQR, RestrictionLifecycleReasonTimer, RestrictionLifecycleReasonEmergency, RestrictionLifecycleReasonSchedule)
	if err != nil {
		return err
	}
	*r = v
	return nil
}

type TableSync[T any, D any] struct {
	Cursor    int64 `json:"cursor"`
	Upserts   []T   `json:"upserts"`
	Deletions []D   `json:"deletions"`
}

type TableResult[T any, D any] struct {
	NextCursor int64 `json:"next_cursor"`
	Upserts    []T   `json:"upserts"`
	Deletions  []D   `json:"deletions"`
}

type Mode struct {
	ID                    string                    `json:"id"`
	Title                 string                    `json:"title"`
	TextOnScreen          string                    `json:"text_on_screen"`
	Description           *string                   `json:"description"`
	AllowedPausesCount    int                       `json:"allowed_pauses_count"`
	MinimumDurationMS     *int                      `json:"minimum_duration_ms"`
	EndingPausingScenario ModeEndingPausingScenario `json:"ending_pausing_scenario"`
	IconToken             string                    `json:"icon_token"`
	CreatedAt             int64                     `json:"created_at"`
	UpdatedAt             int64                     `json:"updated_at"`
}

type ModeBlockedApp struct {
	ModeID        string         `json:"mode_id"`
	Platform      DevicePlatform `json:"platform"`
	AppIdentifier string         `json:"app_identifier"`
	CreatedAt     int64          `json:"created_at"`
	UpdatedAt     int64          `json:"updated_at"`
}

type ModeBlockedAppKey struct {
	ModeID        string         `json:"mode_id"`
	Platform      DevicePlatform `json:"platform"`
	AppIdentifier string         `json:"app_identifier"`
}

type Schedule struct {
	ID          string `json:"id"`
	ModeID      string `json:"mode_id"`
	Days        string `json:"days"`
	StartMinute int    `json:"start_minute"`
	EndMinute   int    `json:"end_minute"`
	Enabled     int    `json:"enabled"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type RestrictionSession struct {
	SessionID         string                            `json:"session_id"`
	ModeID            string                            `json:"mode_id"`
	Source            RestrictionSessionSource          `json:"source"`
	StartedAt         int64                             `json:"started_at"`
	EndedAt           *int64                            `json:"ended_at"`
	PauseCount        int                               `json:"pause_count"`
	TotalPausedMS     int                               `json:"total_paused_ms"`
	LastPausedAt      *int64                            `json:"last_paused_at"`
	IntegrityStatus   RestrictionSessionIntegrityStatus `json:"integrity_status"`
	LastAnomalyReason *string                           `json:"last_anomaly_reason"`
	LastEventID       string                            `json:"last_event_id"`
	CreatedAt         int64                             `json:"created_at"`
	UpdatedAt         int64                             `json:"updated_at"`
}

type RestrictionLifecycleEvent struct {
	ID         string                     `json:"id"`
	SessionID  string                     `json:"session_id"`
	ModeID     string                     `json:"mode_id"`
	Action     RestrictionLifecycleAction `json:"action"`
	Source     RestrictionSessionSource   `json:"source"`
	Reason     RestrictionLifecycleReason `json:"reason"`
	OccurredAt int64                      `json:"occurred_at"`
	CreatedAt  int64                      `json:"created_at"`
}

type NFCLinkedChip struct {
	ID             string `json:"id"`
	ChipIdentifier string `json:"chip_identifier"`
	Name           string `json:"name"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

type QRLinkedCode struct {
	ID        string `json:"id"`
	ScanValue string `json:"scan_value"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type StreakSessionDailyRollup struct {
	SessionID   string `json:"session_id"`
	LocalDay    string `json:"local_day"`
	EffectiveMS int    `json:"effective_ms"`
	UpdatedAt   int64  `json:"updated_at"`
}

type StreakSessionDailyRollupKey struct {
	SessionID string `json:"session_id"`
	LocalDay  string `json:"local_day"`
}

type StreakDailyAggregate struct {
	LocalDay           string `json:"local_day"`
	EffectiveMS        int    `json:"effective_ms"`
	Qualified          int    `json:"qualified"`
	SourceSessionCount int    `json:"source_session_count"`
	UpdatedAt          int64  `json:"updated_at"`
}

type Tables struct {
	Modes                      *TableSync[Mode, string]                                          `json:"modes,omitempty"`
	ModeBlockedApps            *TableSync[ModeBlockedApp, ModeBlockedAppKey]                     `json:"mode_blocked_apps,omitempty"`
	Schedules                  *TableSync[Schedule, string]                                      `json:"schedules,omitempty"`
	RestrictionSessions        *TableSync[RestrictionSession, string]                            `json:"restriction_sessions,omitempty"`
	RestrictionLifecycleEvents *TableSync[RestrictionLifecycleEvent, string]                     `json:"restriction_lifecycle_events,omitempty"`
	NFCLinkedChips             *TableSync[NFCLinkedChip, string]                                 `json:"nfc_linked_chips,omitempty"`
	QRLinkedCodes              *TableSync[QRLinkedCode, string]                                  `json:"qr_linked_codes,omitempty"`
	StreakSessionDailyRollups  *TableSync[StreakSessionDailyRollup, StreakSessionDailyRollupKey] `json:"streak_session_daily_rollups,omitempty"`
	StreakDailyAggregates      *TableSync[StreakDailyAggregate, string]                          `json:"streak_daily_aggregates,omitempty"`
}

type TableResultByTable struct {
	Modes                      *TableResult[Mode, string]                                          `json:"modes,omitempty"`
	ModeBlockedApps            *TableResult[ModeBlockedApp, ModeBlockedAppKey]                     `json:"mode_blocked_apps,omitempty"`
	Schedules                  *TableResult[Schedule, string]                                      `json:"schedules,omitempty"`
	RestrictionSessions        *TableResult[RestrictionSession, string]                            `json:"restriction_sessions,omitempty"`
	RestrictionLifecycleEvents *TableResult[RestrictionLifecycleEvent, string]                     `json:"restriction_lifecycle_events,omitempty"`
	NFCLinkedChips             *TableResult[NFCLinkedChip, string]                                 `json:"nfc_linked_chips,omitempty"`
	QRLinkedCodes              *TableResult[QRLinkedCode, string]                                  `json:"qr_linked_codes,omitempty"`
	StreakSessionDailyRollups  *TableResult[StreakSessionDailyRollup, StreakSessionDailyRollupKey] `json:"streak_session_daily_rollups,omitempty"`
	StreakDailyAggregates      *TableResult[StreakDailyAggregate, string]                          `json:"streak_daily_aggregates,omitempty"`
}

type Response struct {
	Tables TableResultByTable `json:"tables"`
}
