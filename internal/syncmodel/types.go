package syncmodel

import (
	"encoding/json"
	"fmt"
)

type ModeEndingPausingScenario string

const (
	ModeEndingPausingScenarioManual ModeEndingPausingScenario = "manual"
	ModeEndingPausingScenarioNFC    ModeEndingPausingScenario = "nfc"
	ModeEndingPausingScenarioQR     ModeEndingPausingScenario = "qr"
)

func parseModeEndingPausingScenario(raw string) (ModeEndingPausingScenario, error) {
	switch ModeEndingPausingScenario(raw) {
	case ModeEndingPausingScenarioManual, ModeEndingPausingScenarioNFC, ModeEndingPausingScenarioQR:
		return ModeEndingPausingScenario(raw), nil
	default:
		return "", fmt.Errorf("invalid ending pausing scenario %q", raw)
	}
}

func (s *ModeEndingPausingScenario) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	value, err := parseModeEndingPausingScenario(raw)
	if err != nil {
		return err
	}
	*s = value
	return nil
}

type DevicePlatform string

const (
	DevicePlatformAndroid DevicePlatform = "android"
	DevicePlatformIOS     DevicePlatform = "ios"
)

func parseDevicePlatform(raw string) (DevicePlatform, error) {
	switch DevicePlatform(raw) {
	case DevicePlatformAndroid, DevicePlatformIOS:
		return DevicePlatform(raw), nil
	default:
		return "", fmt.Errorf("invalid platform %q", raw)
	}
}

func (p *DevicePlatform) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	value, err := parseDevicePlatform(raw)
	if err != nil {
		return err
	}
	*p = value
	return nil
}

type RestrictionSessionSource string

const (
	RestrictionSessionSourceManual   RestrictionSessionSource = "manual"
	RestrictionSessionSourceSchedule RestrictionSessionSource = "schedule"
)

func parseRestrictionSessionSource(raw string) (RestrictionSessionSource, error) {
	switch RestrictionSessionSource(raw) {
	case RestrictionSessionSourceManual, RestrictionSessionSourceSchedule:
		return RestrictionSessionSource(raw), nil
	default:
		return "", fmt.Errorf("invalid restriction session source %q", raw)
	}
}

func (s *RestrictionSessionSource) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	value, err := parseRestrictionSessionSource(raw)
	if err != nil {
		return err
	}
	*s = value
	return nil
}

type RestrictionSessionIntegrityStatus string

const (
	RestrictionSessionIntegrityStatusOK      RestrictionSessionIntegrityStatus = "ok"
	RestrictionSessionIntegrityStatusAnomaly RestrictionSessionIntegrityStatus = "anomaly"
)

func parseRestrictionSessionIntegrityStatus(raw string) (RestrictionSessionIntegrityStatus, error) {
	switch RestrictionSessionIntegrityStatus(raw) {
	case RestrictionSessionIntegrityStatusOK, RestrictionSessionIntegrityStatusAnomaly:
		return RestrictionSessionIntegrityStatus(raw), nil
	default:
		return "", fmt.Errorf("invalid integrity status %q", raw)
	}
}

func (s *RestrictionSessionIntegrityStatus) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	value, err := parseRestrictionSessionIntegrityStatus(raw)
	if err != nil {
		return err
	}
	*s = value
	return nil
}

type RestrictionLifecycleAction string

const (
	RestrictionLifecycleActionStarted RestrictionLifecycleAction = "START"
	RestrictionLifecycleActionPaused  RestrictionLifecycleAction = "PAUSE"
	RestrictionLifecycleActionResumed RestrictionLifecycleAction = "RESUME"
	RestrictionLifecycleActionEnded   RestrictionLifecycleAction = "END"
)

func parseRestrictionLifecycleAction(raw string) (RestrictionLifecycleAction, error) {
	switch RestrictionLifecycleAction(raw) {
	case RestrictionLifecycleActionStarted, RestrictionLifecycleActionPaused, RestrictionLifecycleActionResumed, RestrictionLifecycleActionEnded:
		return RestrictionLifecycleAction(raw), nil
	default:
		return "", fmt.Errorf("invalid lifecycle action %q", raw)
	}
}

func (a *RestrictionLifecycleAction) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	value, err := parseRestrictionLifecycleAction(raw)
	if err != nil {
		return err
	}
	*a = value
	return nil
}

type RestrictionLifecycleReason string

const (
	RestrictionLifecycleReasonManual RestrictionLifecycleReason = "manual"
	RestrictionLifecycleReasonNFC    RestrictionLifecycleReason = "nfc"
	RestrictionLifecycleReasonQR     RestrictionLifecycleReason = "qr"
	RestrictionLifecycleReasonTimer  RestrictionLifecycleReason = "timer"
)

func parseRestrictionLifecycleReason(raw string) (RestrictionLifecycleReason, error) {
	switch RestrictionLifecycleReason(raw) {
	case RestrictionLifecycleReasonManual, RestrictionLifecycleReasonNFC, RestrictionLifecycleReasonQR, RestrictionLifecycleReasonTimer:
		return RestrictionLifecycleReason(raw), nil
	default:
		return "", fmt.Errorf("invalid lifecycle reason %q", raw)
	}
}

func (r *RestrictionLifecycleReason) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	value, err := parseRestrictionLifecycleReason(raw)
	if err != nil {
		return err
	}
	*r = value
	return nil
}

type TableSync[T any, D any] struct {
	LastSyncedAt int64 `json:"last_synced_at"`
	Upserts      []T   `json:"upserts"`
	Deletions    []D   `json:"deletions"`
}

type TableChanges[T any, D any] struct {
	Upserts   []T `json:"upserts"`
	Deletions []D `json:"deletions"`
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

type Request struct {
	Tables RequestTables `json:"tables"`
}

type TableChangesByTable struct {
	Modes                      *TableChanges[Mode, string]                                          `json:"modes,omitempty"`
	ModeBlockedApps            *TableChanges[ModeBlockedApp, ModeBlockedAppKey]                     `json:"mode_blocked_apps,omitempty"`
	Schedules                  *TableChanges[Schedule, string]                                      `json:"schedules,omitempty"`
	RestrictionSessions        *TableChanges[RestrictionSession, string]                            `json:"restriction_sessions,omitempty"`
	RestrictionLifecycleEvents *TableChanges[RestrictionLifecycleEvent, string]                     `json:"restriction_lifecycle_events,omitempty"`
	NFCLinkedChips             *TableChanges[NFCLinkedChip, string]                                 `json:"nfc_linked_chips,omitempty"`
	QRLinkedCodes              *TableChanges[QRLinkedCode, string]                                  `json:"qr_linked_codes,omitempty"`
	StreakSessionDailyRollups  *TableChanges[StreakSessionDailyRollup, StreakSessionDailyRollupKey] `json:"streak_session_daily_rollups,omitempty"`
	StreakDailyAggregates      *TableChanges[StreakDailyAggregate, string]                          `json:"streak_daily_aggregates,omitempty"`
}

type Response struct {
	ServerTime int64               `json:"server_time"`
	Tables     TableChangesByTable `json:"tables"`
}
