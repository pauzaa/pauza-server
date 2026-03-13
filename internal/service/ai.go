package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/IsorilovA/pauza-server/internal/ai"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

// aiEntitlementChecker is the narrow interface the AI service needs to verify
// that the calling user holds an active premium subscription.
type aiEntitlementChecker interface {
	EffectivePremiumActive(ctx context.Context, db repository.DBTX, userID string) (bool, error)
}

// AIService orchestrates AI-powered analysis features. Every public method
// checks the caller's premium entitlement before forwarding data to the AI
// provider.
type AIService struct {
	provider    ai.Provider
	pool        repository.Pool
	entitlement aiEntitlementChecker
	logger      *slog.Logger
}

// NewAIService creates a new AIService.
func NewAIService(provider ai.Provider, pool repository.Pool, entitlement aiEntitlementChecker, logger *slog.Logger) *AIService {
	if logger == nil {
		logger = slog.Default()
	}
	return &AIService{
		provider:    provider,
		pool:        pool,
		entitlement: entitlement,
		logger:      logger,
	}
}

// --- Input / Output types ---

type AppUsage struct {
	AppIdentifier string `json:"app_identifier"`
	AppName       string `json:"app_name"`
	TotalTimeMS   int64  `json:"total_time_ms"`
	LaunchCount   int    `json:"launch_count"`
	Category      string `json:"category,omitempty"`
}

type AnalyzeUsageInput struct {
	UserID           string     `json:"-"`
	Period           string     `json:"period"`
	AppUsage         []AppUsage `json:"app_usage"`
	TotalScreenTimeMS int64     `json:"total_screen_time_ms"`
	TotalUnlocks     int        `json:"total_unlocks"`
}

type ScheduleSlot struct {
	Days        []int `json:"days"`
	StartMinute int   `json:"start_minute"`
	EndMinute   int   `json:"end_minute"`
}

type SuggestScheduleInput struct {
	UserID              string         `json:"-"`
	AppUsage            []AppUsage     `json:"app_usage"`
	CurrentSchedules    []ScheduleSlot `json:"current_schedules"`
	PreferredFocusHours int            `json:"preferred_focus_hours"`
	Timezone            string         `json:"timezone"`
}

type FocusSessionSummary struct {
	StartedAt   int64 `json:"started_at"`
	EndedAt     int64 `json:"ended_at"`
	PauseCount  int   `json:"pause_count"`
	EffectiveMS int64 `json:"effective_ms"`
}

type DailyReportInput struct {
	UserID            string                `json:"-"`
	Date              string                `json:"date"`
	AppUsage          []AppUsage            `json:"app_usage"`
	FocusSessions     []FocusSessionSummary `json:"focus_sessions"`
	TotalScreenTimeMS int64                 `json:"total_screen_time_ms"`
	TotalUnlocks      int                   `json:"total_unlocks"`
	StreakDays        int                   `json:"streak_days"`
}

type DayAppUsage struct {
	Date string     `json:"date"`
	Apps []AppUsage `json:"apps"`
}

type DayScreenTime struct {
	Date              string `json:"date"`
	TotalScreenTimeMS int64  `json:"total_screen_time_ms"`
	TotalUnlocks      int    `json:"total_unlocks"`
}

type DayFirstUnlock struct {
	Date             string `json:"date"`
	TimeOfDayMinute  int    `json:"time_of_day_minute"`
}

type DetectAddictionInput struct {
	UserID                 string          `json:"-"`
	AppUsageHistory        []DayAppUsage   `json:"app_usage_history"`
	DailyScreenTimeHistory []DayScreenTime `json:"daily_screen_time_history"`
	FirstUnlockTimes       []DayFirstUnlock `json:"first_unlock_times"`
}

type AnalysisOutput struct {
	Analysis string `json:"analysis"`
}

// --- Public methods ---

func (s *AIService) AnalyzeUsage(ctx context.Context, in AnalyzeUsageInput) (AnalysisOutput, error) {
	if err := s.requirePremium(ctx, in.UserID); err != nil {
		return AnalysisOutput{}, err
	}
	return s.complete(ctx, usageAnalysisPrompt, in)
}

func (s *AIService) SuggestSchedule(ctx context.Context, in SuggestScheduleInput) (AnalysisOutput, error) {
	if err := s.requirePremium(ctx, in.UserID); err != nil {
		return AnalysisOutput{}, err
	}
	return s.complete(ctx, focusSchedulePrompt, in)
}

func (s *AIService) GenerateDailyReport(ctx context.Context, in DailyReportInput) (AnalysisOutput, error) {
	if err := s.requirePremium(ctx, in.UserID); err != nil {
		return AnalysisOutput{}, err
	}
	return s.complete(ctx, dailyReportPrompt, in)
}

func (s *AIService) DetectAddiction(ctx context.Context, in DetectAddictionInput) (AnalysisOutput, error) {
	if err := s.requirePremium(ctx, in.UserID); err != nil {
		return AnalysisOutput{}, err
	}
	return s.complete(ctx, addictionCheckPrompt, in)
}

// --- Internal helpers ---

func (s *AIService) requirePremium(ctx context.Context, userID string) error {
	active, err := s.entitlement.EffectivePremiumActive(ctx, s.pool, userID)
	if err != nil {
		s.logger.Error("checking premium entitlement for AI", "err", err)
		return ErrInternal
	}
	if !active {
		return SubscriptionRequiredError("Premium subscription required for AI features")
	}
	return nil
}

func (s *AIService) complete(ctx context.Context, systemPrompt string, userData any) (AnalysisOutput, error) {
	userJSON, err := json.Marshal(userData)
	if err != nil {
		s.logger.Error("marshalling AI user data", "err", err)
		return AnalysisOutput{}, ErrInternal
	}

	messages := []ai.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: string(userJSON)},
	}

	text, err := s.provider.Complete(ctx, messages)
	if err != nil {
		s.logger.Error("AI provider error", "err", err)
		return AnalysisOutput{}, ErrInternal
	}

	return AnalysisOutput{Analysis: text}, nil
}
