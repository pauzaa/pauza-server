package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/service"
)

// AIAnalysisService is the service interface consumed by the AI handler.
type AIAnalysisService interface {
	AnalyzeUsage(ctx context.Context, in service.AnalyzeUsageInput) (service.AnalysisOutput, error)
	SuggestSchedule(ctx context.Context, in service.SuggestScheduleInput) (service.AnalysisOutput, error)
	GenerateDailyReport(ctx context.Context, in service.DailyReportInput) (service.AnalysisOutput, error)
	DetectAddiction(ctx context.Context, in service.DetectAddictionInput) (service.AnalysisOutput, error)
}

var _ AIAnalysisService = (*service.AIService)(nil)

// AIHandler handles AI analysis HTTP endpoints.
type AIHandler struct {
	svc    AIAnalysisService
	logger *slog.Logger
}

// NewAIHandler creates a new AIHandler.
func NewAIHandler(svc AIAnalysisService, logger *slog.Logger) *AIHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AIHandler{svc: svc, logger: logger}
}

// --- Request types ---

type appUsageItem struct {
	AppIdentifier string `json:"app_identifier"`
	AppName       string `json:"app_name"`
	TotalTimeMS   int64  `json:"total_time_ms"`
	LaunchCount   int    `json:"launch_count"`
	Category      string `json:"category"`
}

type analyzeUsageRequest struct {
	Period            string         `json:"period"`
	AppUsage          []appUsageItem `json:"app_usage"`
	TotalScreenTimeMS int64          `json:"total_screen_time_ms"`
	TotalUnlocks      int            `json:"total_unlocks"`
}

type scheduleSlotItem struct {
	Days        []int `json:"days"`
	StartMinute int   `json:"start_minute"`
	EndMinute   int   `json:"end_minute"`
}

type suggestScheduleRequest struct {
	AppUsage            []appUsageItem     `json:"app_usage"`
	CurrentSchedules    []scheduleSlotItem `json:"current_schedules"`
	PreferredFocusHours int                `json:"preferred_focus_hours"`
	Timezone            string             `json:"timezone"`
}

type focusSessionItem struct {
	StartedAt   int64 `json:"started_at"`
	EndedAt     int64 `json:"ended_at"`
	PauseCount  int   `json:"pause_count"`
	EffectiveMS int64 `json:"effective_ms"`
}

type dailyReportRequest struct {
	Date              string             `json:"date"`
	AppUsage          []appUsageItem     `json:"app_usage"`
	FocusSessions     []focusSessionItem `json:"focus_sessions"`
	TotalScreenTimeMS int64              `json:"total_screen_time_ms"`
	TotalUnlocks      int                `json:"total_unlocks"`
	StreakDays        int                `json:"streak_days"`
}

type dayAppUsageItem struct {
	Date string         `json:"date"`
	Apps []appUsageItem `json:"apps"`
}

type dayScreenTimeItem struct {
	Date              string `json:"date"`
	TotalScreenTimeMS int64  `json:"total_screen_time_ms"`
	TotalUnlocks      int    `json:"total_unlocks"`
}

type dayFirstUnlockItem struct {
	Date            string `json:"date"`
	TimeOfDayMinute int    `json:"time_of_day_minute"`
}

type detectAddictionRequest struct {
	AppUsageHistory        []dayAppUsageItem    `json:"app_usage_history"`
	DailyScreenTimeHistory []dayScreenTimeItem  `json:"daily_screen_time_history"`
	FirstUnlockTimes       []dayFirstUnlockItem `json:"first_unlock_times"`
}

type analysisResponse struct {
	Analysis string `json:"analysis"`
}

// --- Handlers ---

func (h *AIHandler) AnalyzeUsage(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req analyzeUsageRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	if fields := validateAnalyzeUsage(req); len(fields) > 0 {
		apperror.ValidationFieldErrors(w, "Invalid request body", fields)
		return
	}

	out, err := h.svc.AnalyzeUsage(r.Context(), service.AnalyzeUsageInput{
		UserID:            userID,
		Period:            req.Period,
		AppUsage:          toServiceAppUsage(req.AppUsage),
		TotalScreenTimeMS: req.TotalScreenTimeMS,
		TotalUnlocks:      req.TotalUnlocks,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, analysisResponse{Analysis: out.Analysis}, "ai.analyze_usage")
}

func (h *AIHandler) SuggestSchedule(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req suggestScheduleRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	if fields := validateSuggestSchedule(req); len(fields) > 0 {
		apperror.ValidationFieldErrors(w, "Invalid request body", fields)
		return
	}

	schedules := make([]service.ScheduleSlot, len(req.CurrentSchedules))
	for i, s := range req.CurrentSchedules {
		schedules[i] = service.ScheduleSlot{
			Days:        s.Days,
			StartMinute: s.StartMinute,
			EndMinute:   s.EndMinute,
		}
	}

	out, err := h.svc.SuggestSchedule(r.Context(), service.SuggestScheduleInput{
		UserID:              userID,
		AppUsage:            toServiceAppUsage(req.AppUsage),
		CurrentSchedules:    schedules,
		PreferredFocusHours: req.PreferredFocusHours,
		Timezone:            req.Timezone,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, analysisResponse{Analysis: out.Analysis}, "ai.suggest_schedule")
}

func (h *AIHandler) GenerateDailyReport(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req dailyReportRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	if fields := validateDailyReport(req); len(fields) > 0 {
		apperror.ValidationFieldErrors(w, "Invalid request body", fields)
		return
	}

	sessions := make([]service.FocusSessionSummary, len(req.FocusSessions))
	for i, s := range req.FocusSessions {
		sessions[i] = service.FocusSessionSummary{
			StartedAt:   s.StartedAt,
			EndedAt:     s.EndedAt,
			PauseCount:  s.PauseCount,
			EffectiveMS: s.EffectiveMS,
		}
	}

	out, err := h.svc.GenerateDailyReport(r.Context(), service.DailyReportInput{
		UserID:            userID,
		Date:              req.Date,
		AppUsage:          toServiceAppUsage(req.AppUsage),
		FocusSessions:     sessions,
		TotalScreenTimeMS: req.TotalScreenTimeMS,
		TotalUnlocks:      req.TotalUnlocks,
		StreakDays:        req.StreakDays,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, analysisResponse{Analysis: out.Analysis}, "ai.daily_report")
}

func (h *AIHandler) DetectAddiction(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req detectAddictionRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	if fields := validateDetectAddiction(req); len(fields) > 0 {
		apperror.ValidationFieldErrors(w, "Invalid request body", fields)
		return
	}

	history := make([]service.DayAppUsage, len(req.AppUsageHistory))
	for i, d := range req.AppUsageHistory {
		history[i] = service.DayAppUsage{
			Date: d.Date,
			Apps: toServiceAppUsage(d.Apps),
		}
	}

	screenTime := make([]service.DayScreenTime, len(req.DailyScreenTimeHistory))
	for i, d := range req.DailyScreenTimeHistory {
		screenTime[i] = service.DayScreenTime{
			Date:              d.Date,
			TotalScreenTimeMS: d.TotalScreenTimeMS,
			TotalUnlocks:      d.TotalUnlocks,
		}
	}

	unlocks := make([]service.DayFirstUnlock, len(req.FirstUnlockTimes))
	for i, d := range req.FirstUnlockTimes {
		unlocks[i] = service.DayFirstUnlock{
			Date:            d.Date,
			TimeOfDayMinute: d.TimeOfDayMinute,
		}
	}

	out, err := h.svc.DetectAddiction(r.Context(), service.DetectAddictionInput{
		UserID:                 userID,
		AppUsageHistory:        history,
		DailyScreenTimeHistory: screenTime,
		FirstUnlockTimes:       unlocks,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, analysisResponse{Analysis: out.Analysis}, "ai.detect_addiction")
}

// --- Validation helpers ---

const dateLayout = "2006-01-02"

// Validation limits for string and numeric fields.
const (
	maxAppIdentifierLen = 256
	maxAppNameLen       = 256
	maxDailyTimeMS      = 86_400_000  // 24 hours
	maxWeeklyTimeMS     = 604_800_000 // 7 days
	maxLaunchCount      = 100_000
	maxUnlocks          = 100_000
)

func isValidDate(s string) bool {
	if len(s) != 10 {
		return false
	}
	_, err := time.Parse(dateLayout, s)
	return err == nil
}

// validateAppUsageItems validates a slice of appUsageItem with the given
// time cap (daily or weekly). It writes errors into the provided fields map
// using the given prefix (e.g. "app_usage" or "app_usage_history[0].apps").
func validateAppUsageItems(items []appUsageItem, prefix string, maxTimeMS int64, fields apperror.FieldErrors) {
	for i, item := range items {
		if item.AppIdentifier == "" {
			fields[fmt.Sprintf("%s[%d].app_identifier", prefix, i)] = "app_identifier is required"
		} else if len(item.AppIdentifier) > maxAppIdentifierLen {
			fields[fmt.Sprintf("%s[%d].app_identifier", prefix, i)] = fmt.Sprintf("app_identifier must not exceed %d characters", maxAppIdentifierLen)
		}
		if item.AppName == "" {
			fields[fmt.Sprintf("%s[%d].app_name", prefix, i)] = "app_name is required"
		} else if len(item.AppName) > maxAppNameLen {
			fields[fmt.Sprintf("%s[%d].app_name", prefix, i)] = fmt.Sprintf("app_name must not exceed %d characters", maxAppNameLen)
		}
		if item.TotalTimeMS < 0 {
			fields[fmt.Sprintf("%s[%d].total_time_ms", prefix, i)] = "total_time_ms must be >= 0"
		} else if item.TotalTimeMS > maxTimeMS {
			fields[fmt.Sprintf("%s[%d].total_time_ms", prefix, i)] = fmt.Sprintf("total_time_ms must not exceed %d", maxTimeMS)
		}
		if item.LaunchCount < 0 {
			fields[fmt.Sprintf("%s[%d].launch_count", prefix, i)] = "launch_count must be >= 0"
		} else if item.LaunchCount > maxLaunchCount {
			fields[fmt.Sprintf("%s[%d].launch_count", prefix, i)] = fmt.Sprintf("launch_count must not exceed %d", maxLaunchCount)
		}
	}
}

func validateAnalyzeUsage(req analyzeUsageRequest) apperror.FieldErrors {
	fields := apperror.FieldErrors{}
	if req.Period != "daily" && req.Period != "weekly" {
		fields["period"] = "period must be daily or weekly"
	}
	var timeCap int64 = maxDailyTimeMS
	if req.Period == "weekly" {
		timeCap = maxWeeklyTimeMS
	}
	if len(req.AppUsage) == 0 {
		fields["app_usage"] = "app_usage is required"
	} else if len(req.AppUsage) > 500 {
		fields["app_usage"] = "app_usage must not exceed 500 items"
	} else {
		validateAppUsageItems(req.AppUsage, "app_usage", timeCap, fields)
	}
	if req.TotalScreenTimeMS < 0 {
		fields["total_screen_time_ms"] = "total_screen_time_ms must be >= 0"
	} else if req.TotalScreenTimeMS > timeCap {
		fields["total_screen_time_ms"] = fmt.Sprintf("total_screen_time_ms must not exceed %d", timeCap)
	}
	if req.TotalUnlocks < 0 {
		fields["total_unlocks"] = "total_unlocks must be >= 0"
	} else if req.TotalUnlocks > maxUnlocks {
		fields["total_unlocks"] = fmt.Sprintf("total_unlocks must not exceed %d", maxUnlocks)
	}
	return fields
}

func validateSuggestSchedule(req suggestScheduleRequest) apperror.FieldErrors {
	fields := apperror.FieldErrors{}
	if len(req.AppUsage) == 0 {
		fields["app_usage"] = "app_usage is required"
	} else if len(req.AppUsage) > 500 {
		fields["app_usage"] = "app_usage must not exceed 500 items"
	} else {
		validateAppUsageItems(req.AppUsage, "app_usage", maxWeeklyTimeMS, fields)
	}
	if req.PreferredFocusHours < 1 || req.PreferredFocusHours > 16 {
		fields["preferred_focus_hours"] = "preferred_focus_hours must be between 1 and 16"
	}
	if req.Timezone == "" {
		fields["timezone"] = "timezone is required"
	} else if _, err := time.LoadLocation(req.Timezone); err != nil {
		fields["timezone"] = "timezone must be a valid IANA timezone"
	}
	for i, slot := range req.CurrentSchedules {
		for j, d := range slot.Days {
			if d < 0 || d > 6 {
				fields[fmt.Sprintf("current_schedules[%d].days[%d]", i, j)] = "day must be between 0 and 6"
			}
		}
		startOK := slot.StartMinute >= 0 && slot.StartMinute <= 1439
		endOK := slot.EndMinute >= 0 && slot.EndMinute <= 1439
		if !startOK {
			fields[fmt.Sprintf("current_schedules[%d].start_minute", i)] = "start_minute must be between 0 and 1439"
		}
		if !endOK {
			fields[fmt.Sprintf("current_schedules[%d].end_minute", i)] = "end_minute must be between 0 and 1439"
		} else if startOK && slot.EndMinute <= slot.StartMinute {
			fields[fmt.Sprintf("current_schedules[%d].end_minute", i)] = "end_minute must be greater than start_minute"
		}
	}
	return fields
}

func validateDailyReport(req dailyReportRequest) apperror.FieldErrors {
	fields := apperror.FieldErrors{}
	if req.Date == "" {
		fields["date"] = "date is required"
	} else if !isValidDate(req.Date) {
		fields["date"] = "date must be in YYYY-MM-DD format"
	}
	if len(req.AppUsage) == 0 {
		fields["app_usage"] = "app_usage is required"
	} else if len(req.AppUsage) > 500 {
		fields["app_usage"] = "app_usage must not exceed 500 items"
	} else {
		validateAppUsageItems(req.AppUsage, "app_usage", maxDailyTimeMS, fields)
	}
	if req.TotalScreenTimeMS < 0 {
		fields["total_screen_time_ms"] = "total_screen_time_ms must be >= 0"
	} else if req.TotalScreenTimeMS > maxDailyTimeMS {
		fields["total_screen_time_ms"] = fmt.Sprintf("total_screen_time_ms must not exceed %d", maxDailyTimeMS)
	}
	if req.TotalUnlocks < 0 {
		fields["total_unlocks"] = "total_unlocks must be >= 0"
	} else if req.TotalUnlocks > maxUnlocks {
		fields["total_unlocks"] = fmt.Sprintf("total_unlocks must not exceed %d", maxUnlocks)
	}
	if req.StreakDays < 0 {
		fields["streak_days"] = "streak_days must be >= 0"
	}
	if len(req.FocusSessions) > 100 {
		fields["focus_sessions"] = "focus_sessions must not exceed 100 items"
	} else {
		for i, s := range req.FocusSessions {
			if s.StartedAt <= 0 {
				fields[fmt.Sprintf("focus_sessions[%d].started_at", i)] = "started_at must be > 0"
			}
			if s.EndedAt <= 0 {
				fields[fmt.Sprintf("focus_sessions[%d].ended_at", i)] = "ended_at must be > 0"
			}
			if s.StartedAt > 0 && s.EndedAt > 0 && s.EndedAt <= s.StartedAt {
				fields[fmt.Sprintf("focus_sessions[%d].ended_at", i)] = "ended_at must be greater than started_at"
			}
			if s.EffectiveMS < 0 {
				fields[fmt.Sprintf("focus_sessions[%d].effective_ms", i)] = "effective_ms must be >= 0"
			}
			if s.PauseCount < 0 {
				fields[fmt.Sprintf("focus_sessions[%d].pause_count", i)] = "pause_count must be >= 0"
			}
		}
	}
	return fields
}

func validateDetectAddiction(req detectAddictionRequest) apperror.FieldErrors {
	fields := apperror.FieldErrors{}
	if len(req.AppUsageHistory) == 0 {
		fields["app_usage_history"] = "app_usage_history is required"
	} else if len(req.AppUsageHistory) > 365 {
		fields["app_usage_history"] = "app_usage_history must not exceed 365 items"
	} else {
		for i, day := range req.AppUsageHistory {
			if !isValidDate(day.Date) {
				fields[fmt.Sprintf("app_usage_history[%d].date", i)] = "date must be in YYYY-MM-DD format"
			}
			if len(day.Apps) > 500 {
				fields[fmt.Sprintf("app_usage_history[%d].apps", i)] = "apps must not exceed 500 items"
			} else {
				validateAppUsageItems(day.Apps, fmt.Sprintf("app_usage_history[%d].apps", i), maxDailyTimeMS, fields)
			}
		}
	}
	if len(req.DailyScreenTimeHistory) == 0 {
		fields["daily_screen_time_history"] = "daily_screen_time_history is required"
	} else if len(req.DailyScreenTimeHistory) > 365 {
		fields["daily_screen_time_history"] = "daily_screen_time_history must not exceed 365 items"
	} else {
		for i, day := range req.DailyScreenTimeHistory {
			if !isValidDate(day.Date) {
				fields[fmt.Sprintf("daily_screen_time_history[%d].date", i)] = "date must be in YYYY-MM-DD format"
			}
			if day.TotalScreenTimeMS < 0 {
				fields[fmt.Sprintf("daily_screen_time_history[%d].total_screen_time_ms", i)] = "total_screen_time_ms must be >= 0"
			} else if day.TotalScreenTimeMS > maxDailyTimeMS {
				fields[fmt.Sprintf("daily_screen_time_history[%d].total_screen_time_ms", i)] = fmt.Sprintf("total_screen_time_ms must not exceed %d", maxDailyTimeMS)
			}
			if day.TotalUnlocks < 0 {
				fields[fmt.Sprintf("daily_screen_time_history[%d].total_unlocks", i)] = "total_unlocks must be >= 0"
			} else if day.TotalUnlocks > maxUnlocks {
				fields[fmt.Sprintf("daily_screen_time_history[%d].total_unlocks", i)] = fmt.Sprintf("total_unlocks must not exceed %d", maxUnlocks)
			}
		}
	}
	if len(req.FirstUnlockTimes) > 365 {
		fields["first_unlock_times"] = "first_unlock_times must not exceed 365 items"
	} else {
		for i, day := range req.FirstUnlockTimes {
			if !isValidDate(day.Date) {
				fields[fmt.Sprintf("first_unlock_times[%d].date", i)] = "date must be in YYYY-MM-DD format"
			}
			if day.TimeOfDayMinute < 0 || day.TimeOfDayMinute > 1439 {
				fields[fmt.Sprintf("first_unlock_times[%d].time_of_day_minute", i)] = "time_of_day_minute must be between 0 and 1439"
			}
		}
	}
	return fields
}

// --- Mapping helpers ---

func toServiceAppUsage(items []appUsageItem) []service.AppUsage {
	out := make([]service.AppUsage, len(items))
	for i, a := range items {
		out[i] = service.AppUsage{
			AppIdentifier: a.AppIdentifier,
			AppName:       a.AppName,
			TotalTimeMS:   a.TotalTimeMS,
			LaunchCount:   a.LaunchCount,
			Category:      a.Category,
		}
	}
	return out
}
