package handler

import (
	"context"
	"log/slog"
	"net/http"

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

func validateAnalyzeUsage(req analyzeUsageRequest) apperror.FieldErrors {
	fields := apperror.FieldErrors{}
	if req.Period != "daily" && req.Period != "weekly" {
		fields["period"] = "period must be daily or weekly"
	}
	if len(req.AppUsage) == 0 {
		fields["app_usage"] = "app_usage is required"
	}
	return fields
}

func validateSuggestSchedule(req suggestScheduleRequest) apperror.FieldErrors {
	fields := apperror.FieldErrors{}
	if len(req.AppUsage) == 0 {
		fields["app_usage"] = "app_usage is required"
	}
	if req.PreferredFocusHours < 1 || req.PreferredFocusHours > 16 {
		fields["preferred_focus_hours"] = "preferred_focus_hours must be between 1 and 16"
	}
	if req.Timezone == "" {
		fields["timezone"] = "timezone is required"
	}
	return fields
}

func validateDailyReport(req dailyReportRequest) apperror.FieldErrors {
	fields := apperror.FieldErrors{}
	if req.Date == "" {
		fields["date"] = "date is required"
	}
	if len(req.AppUsage) == 0 {
		fields["app_usage"] = "app_usage is required"
	}
	return fields
}

func validateDetectAddiction(req detectAddictionRequest) apperror.FieldErrors {
	fields := apperror.FieldErrors{}
	if len(req.AppUsageHistory) == 0 {
		fields["app_usage_history"] = "app_usage_history is required"
	}
	if len(req.DailyScreenTimeHistory) == 0 {
		fields["daily_screen_time_history"] = "daily_screen_time_history is required"
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
