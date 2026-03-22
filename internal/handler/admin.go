package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/domain"
	"github.com/IsorilovA/pauza-server/internal/pagination"
	"github.com/IsorilovA/pauza-server/internal/revenuecat"
	"github.com/IsorilovA/pauza-server/internal/service"
)

type AdminLoginService interface {
	Login(ctx context.Context, in service.LoginInput) (service.LoginOutput, error)
}

type AdminUsersService interface {
	ListUsers(ctx context.Context, in service.ListUsersInput) (service.ListUsersOutput, error)
	GetUserDetail(ctx context.Context, in service.GetUserDetailInput) (service.UserDetailOutput, error)
}

type AdminStatsService interface {
	GetPlatformStats(ctx context.Context) (service.PlatformStatsOutput, error)
}

type AdminEntitlementsService interface {
	ManageEntitlement(ctx context.Context, in service.ManageEntitlementInput) (service.MessageOutput, error)
	ListEntitlements(ctx context.Context, in service.ListEntitlementsInput) (service.ListEntitlementsOutput, error)
}

type AdminTimeSeriesService interface {
	GetUserGrowth(ctx context.Context, in service.TimeSeriesInput) (service.TimeSeriesOutput, error)
	GetActiveUsers(ctx context.Context, in service.TimeSeriesInput) (service.TimeSeriesOutput, error)
}

type AdminRCService interface {
	GetRCOverview(ctx context.Context) (*revenuecat.OverviewMetrics, error)
	GetRCChart(ctx context.Context, params revenuecat.ChartParams) (*revenuecat.ChartResponse, error)
	GetUserRCSubscription(ctx context.Context, in service.GetUserDetailInput) (*service.RCSubscriberOutput, error)
}

var _ AdminLoginService = (*service.AdminService)(nil)
var _ AdminUsersService = (*service.AdminService)(nil)
var _ AdminStatsService = (*service.AdminService)(nil)
var _ AdminEntitlementsService = (*service.AdminService)(nil)
var _ AdminTimeSeriesService = (*service.AdminService)(nil)
var _ AdminRCService = (*service.AdminService)(nil)

type AdminService interface {
	AdminLoginService
	AdminUsersService
	AdminStatsService
	AdminEntitlementsService
	AdminTimeSeriesService
	AdminRCService
}

// AdminHandler handles admin HTTP endpoints.
type AdminHandler struct {
	svc    AdminService
	logger *slog.Logger
}

// NewAdminHandler creates an AdminHandler with the given service and logger.
func NewAdminHandler(svc AdminService, logger *slog.Logger) *AdminHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AdminHandler{
		svc:    svc,
		logger: logger,
	}
}

// ---------------------------------------------------------------------------
// Request / response types
// ---------------------------------------------------------------------------

type adminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type adminLoginResponse struct {
	AccessToken string `json:"access_token"`
}

type adminUserItemResponse struct {
	ID                       string  `json:"id"`
	Email                    string  `json:"email"`
	Name                     string  `json:"name"`
	Username                 string  `json:"username"`
	ProfilePictureURL        *string `json:"profile_picture_url"`
	PremiumEntitlementActive bool    `json:"premium_entitlement_active"`
	CreatedAt                int64   `json:"created_at"`
}

type adminListUsersResponse struct {
	Users      []adminUserItemResponse `json:"users"`
	Pagination paginationResponse      `json:"pagination"`
}

type paginationResponse struct {
	Page  int `json:"page"`
	Limit int `json:"limit"`
	Total int `json:"total"`
}

type adminUserDetailResponse struct {
	ID                  string  `json:"id"`
	Email               string  `json:"email"`
	Name                string  `json:"name"`
	Username            string  `json:"username"`
	ProfilePictureURL   *string `json:"profile_picture_url"`
	LeaderboardVisible  bool    `json:"leaderboard_visible"`
	CreatedAt           int64   `json:"created_at"`
	IsPremium           bool    `json:"is_premium"`
	CurrentPeriodEnd    *int64  `json:"current_period_end"`
	RevenueCatAppUserID *string `json:"revenuecat_app_user_id"`
	FriendCount         int     `json:"friend_count"`
	TotalSessions       int     `json:"total_sessions"`
	LastSessionTime     *int64  `json:"last_session_time"`
}

type adminStatsResponse struct {
	TotalUsers          int     `json:"total_users"`
	ActiveUsers30d      int     `json:"active_users_30d"`
	PremiumUsers        int     `json:"users_with_premium_entitlement"`
	TotalFriendships    int     `json:"total_friendships"`
	AvgStreakDays       float64 `json:"avg_streak_days"`
	AvgDailyFocusTimeMS float64 `json:"avg_daily_focus_time_ms"`
}

type manageEntitlementRequest struct {
	Action      domain.AdminOverrideAction `json:"action"`
	Entitlement domain.Entitlement         `json:"entitlement"`
	ExpiresAt   *int64                     `json:"expires_at"`
}

type adminEntitlementItemResponse struct {
	UserID           string  `json:"user_id"`
	Email            string  `json:"email"`
	Username         string  `json:"username"`
	Entitlement      string  `json:"entitlement"`
	IsActive         bool    `json:"is_active"`
	CurrentPeriodEnd *int64 `json:"current_period_end"`
	UpdatedAt        int64  `json:"updated_at"`
}

type adminListEntitlementsResponse struct {
	Entitlements []adminEntitlementItemResponse `json:"entitlements"`
	Pagination   paginationResponse             `json:"pagination"`
}

type timeSeriesPoint struct {
	Date  string `json:"date"`
	Value int    `json:"value"`
}

type timeSeriesResponse struct {
	Data        []timeSeriesPoint `json:"data"`
	Granularity string            `json:"granularity"`
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// Login handles POST /api/v1/admin/login.
func (h *AdminHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req adminLoginRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	fields := make(apperror.FieldErrors)
	if strings.TrimSpace(req.Username) == "" {
		fields["username"] = "username is required"
	}
	if req.Password == "" {
		fields["password"] = "password is required"
	}
	if len(fields) > 0 {
		apperror.ValidationFieldErrors(w, "Invalid request body", fields)
		return
	}

	out, err := h.svc.Login(r.Context(), service.LoginInput{
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, adminLoginResponse{AccessToken: out.Token}, "admin-login")
}

// ListUsers handles GET /api/v1/admin/users.
func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	page, limit := pagination.FromRequest(r)
	search := r.URL.Query().Get("search")

	out, err := h.svc.ListUsers(r.Context(), service.ListUsersInput{
		Page:   page,
		Limit:  limit,
		Search: search,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	users := make([]adminUserItemResponse, len(out.Users))
	for i, u := range out.Users {
		users[i] = adminUserItemResponse{
			ID:                       u.ID,
			Email:                    u.Email,
			Name:                     u.Name,
			Username:                 u.Username,
			ProfilePictureURL:        u.ProfilePictureURL,
			PremiumEntitlementActive: u.IsPremium,
			CreatedAt:                u.CreatedAt.UnixMilli(),
		}
	}

	resp := adminListUsersResponse{
		Users: users,
		Pagination: paginationResponse{
			Page:  out.Page,
			Limit: out.Limit,
			Total: out.Total,
		},
	}

	writeJSON(w, h.logger, http.StatusOK, resp, "admin-list-users")
}

// GetUserDetail handles GET /api/v1/admin/users/{id}.
func (h *AdminHandler) GetUserDetail(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}

	out, err := h.svc.GetUserDetail(r.Context(), service.GetUserDetailInput{
		UserID: userID,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	resp := adminUserDetailResponse{
		ID:                  out.ID,
		Email:               out.Email,
		Name:                out.Name,
		Username:            out.Username,
		ProfilePictureURL:   out.ProfilePictureURL,
		LeaderboardVisible:  out.LeaderboardVisible,
		CreatedAt:           out.CreatedAt.UnixMilli(),
		IsPremium:           out.IsPremium,
		RevenueCatAppUserID: out.RevenueCatAppUserID,
		FriendCount:         out.FriendCount,
		TotalSessions:       out.TotalSessions,
		LastSessionTime:     out.LastSessionTime,
		CurrentPeriodEnd:    toUnixMsPtr(out.CurrentPeriodEnd),
	}

	writeJSON(w, h.logger, http.StatusOK, resp, "admin-user-detail")
}

// GetPlatformStats handles GET /api/v1/admin/stats.
func (h *AdminHandler) GetPlatformStats(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.GetPlatformStats(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}

	resp := adminStatsResponse{
		TotalUsers:          out.TotalUsers,
		ActiveUsers30d:      out.ActiveUsers30d,
		PremiumUsers:        out.PremiumUsers,
		TotalFriendships:    out.TotalFriendships,
		AvgStreakDays:       out.AvgStreakDays,
		AvgDailyFocusTimeMS: out.AvgDailyFocusTimeMS,
	}

	writeJSON(w, h.logger, http.StatusOK, resp, "admin-stats")
}

// ManageEntitlement handles POST /api/v1/admin/users/{id}/entitlements.
func (h *AdminHandler) ManageEntitlement(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}

	var req manageEntitlementRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	fields := make(apperror.FieldErrors)
	if req.Action == domain.AdminOverrideActionUnknown {
		fields["action"] = "action must be grant or revoke"
	}
	if req.Entitlement == domain.EntitlementUnknown {
		fields["entitlement"] = "entitlement must be premium"
	}
	if req.Action == domain.AdminOverrideGrant && req.ExpiresAt == nil {
		fields["expires_at"] = "expires_at is required for grant actions"
	}
	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		if *req.ExpiresAt <= 0 {
			fields["expires_at"] = "expires_at must be a positive Unix millisecond timestamp"
		} else {
			t := time.UnixMilli(*req.ExpiresAt)
			if !t.After(time.Now()) {
				fields["expires_at"] = "expires_at must be in the future"
			} else {
				expiresAt = &t
			}
		}
	}

	if len(fields) > 0 {
		apperror.ValidationFieldErrors(w, "Invalid request body", fields)
		return
	}

	out, err := h.svc.ManageEntitlement(r.Context(), service.ManageEntitlementInput{
		UserID:      userID,
		Entitlement: req.Entitlement,
		Action:      req.Action,
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeMessageResponse(w, h.logger, http.StatusOK, out.Message, "admin-manage-entitlement")
}

// ListEntitlements handles GET /api/v1/admin/entitlements.
func (h *AdminHandler) ListEntitlements(w http.ResponseWriter, r *http.Request) {
	page, limit := pagination.FromRequest(r)
	entitlement, err := domain.ParseEntitlement(r.URL.Query().Get("entitlement"))
	if err != nil {
		apperror.ValidationFieldErrors(w, "Invalid query parameter", apperror.FieldErrors{
			"entitlement": "entitlement must be premium",
		})
		return
	}

	var isActive *bool
	if raw := r.URL.Query().Get("is_active"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			apperror.ValidationFieldErrors(w, "Invalid query parameter", apperror.FieldErrors{
				"is_active": "is_active must be a boolean",
			})
			return
		}
		isActive = &parsed
	}

	out, err := h.svc.ListEntitlements(r.Context(), service.ListEntitlementsInput{
		Page:        page,
		Limit:       limit,
		Entitlement: entitlement,
		IsActive:    isActive,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	entitlements := make([]adminEntitlementItemResponse, len(out.Entitlements))
	for i, e := range out.Entitlements {
		entitlements[i] = adminEntitlementItemResponse{
			UserID:           e.UserID,
			Email:            e.Email,
			Username:         e.Username,
			Entitlement:      e.Entitlement,
			IsActive:         e.IsActive,
			CurrentPeriodEnd: toUnixMsPtr(e.CurrentPeriodEnd),
			UpdatedAt:        e.UpdatedAt.UnixMilli(),
		}
	}

	resp := adminListEntitlementsResponse{
		Entitlements: entitlements,
		Pagination: paginationResponse{
			Page:  out.Page,
			Limit: out.Limit,
			Total: out.Total,
		},
	}

	writeJSON(w, h.logger, http.StatusOK, resp, "admin-list-entitlements")
}

// ---------------------------------------------------------------------------
// Time-series helpers
// ---------------------------------------------------------------------------

var validRanges = map[string]bool{"30d": true, "90d": true, "1y": true, "all": true}

func parseTimeSeriesInput(r *http.Request) service.TimeSeriesInput {
	rangeVal := r.URL.Query().Get("range")
	if !validRanges[rangeVal] {
		rangeVal = "30d"
	}

	return service.TimeSeriesInput{
		Granularity: r.URL.Query().Get("granularity"), // empty = auto-select in service
		Range:       rangeVal,
	}
}

func toTimeSeriesResponse(points []service.TimeSeriesPoint, granularity string) timeSeriesResponse {
	data := make([]timeSeriesPoint, len(points))
	for i, p := range points {
		data[i] = timeSeriesPoint{
			Date:  p.Date.Format("2006-01-02"),
			Value: p.Value,
		}
	}
	return timeSeriesResponse{
		Data:        data,
		Granularity: granularity,
	}
}

// GetUserGrowth handles GET /api/v1/admin/stats/user-growth.
func (h *AdminHandler) GetUserGrowth(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.GetUserGrowth(r.Context(), parseTimeSeriesInput(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, toTimeSeriesResponse(out.Points, out.Granularity), "admin-user-growth")
}

// GetActiveUsers handles GET /api/v1/admin/stats/active-users.
func (h *AdminHandler) GetActiveUsers(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.GetActiveUsers(r.Context(), parseTimeSeriesInput(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, toTimeSeriesResponse(out.Points, out.Granularity), "admin-active-users")
}

// GetRCOverview handles GET /api/v1/admin/revenuecat/overview.
func (h *AdminHandler) GetRCOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := h.svc.GetRCOverview(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, overview, "admin-rc-overview")
}

var allowedChartNames = map[string]bool{
	"revenue":          true,
	"customers_new":    true,
	"customers_active": true,
	"churn":            true,
}

// rangeToDateStrings converts a range string (e.g. "30d") to start/end YYYY-MM-DD date strings.
func rangeToDateStrings(rangeVal string) (string, string) {
	now := time.Now().UTC()
	end := now.Format("2006-01-02")

	var start time.Time
	switch rangeVal {
	case "90d":
		start = now.AddDate(0, 0, -90)
	case "1y":
		start = now.AddDate(-1, 0, 0)
	case "all":
		start = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	default: // "30d" or unknown
		start = now.AddDate(0, 0, -30)
	}
	return start.Format("2006-01-02"), end
}

// GetRCChart handles GET /api/v1/admin/revenuecat/charts/{chart_name}.
func (h *AdminHandler) GetRCChart(w http.ResponseWriter, r *http.Request) {
	chartName := chi.URLParam(r, "chart_name")
	if !allowedChartNames[chartName] {
		apperror.ValidationFieldErrors(w, "Invalid query parameter", apperror.FieldErrors{
			"chart_name": "must be one of: revenue, customers_new, customers_active, churn",
		})
		return
	}

	rangeVal := r.URL.Query().Get("range")
	startDate, endDate := rangeToDateStrings(rangeVal)

	chart, err := h.svc.GetRCChart(r.Context(), revenuecat.ChartParams{
		ChartName: chartName,
		StartDate: startDate,
		EndDate:   endDate,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, chart, "admin-rc-chart")
}

type adminRCSubscriberResponse struct {
	AppUserID    string                          `json:"app_user_id"`
	Entitlements []adminRCSubscriberEntitlementResponse `json:"entitlements"`
}

type adminRCSubscriberEntitlementResponse struct {
	EntitlementID          string  `json:"entitlement_id"`
	IsActive               bool    `json:"is_active"`
	ProductIdentifier      string  `json:"product_identifier"`
	PurchaseDate           int64  `json:"purchase_date"`
	ExpiresDate            *int64 `json:"expires_date"`
	GracePeriodExpiresDate *int64 `json:"grace_period_expires_date"`
}

// GetUserRCSubscription handles GET /api/v1/admin/users/{id}/revenuecat.
func (h *AdminHandler) GetUserRCSubscription(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUUIDParam(w, r, "id")
	if !ok {
		return
	}

	out, err := h.svc.GetUserRCSubscription(r.Context(), service.GetUserDetailInput{UserID: userID})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	entitlements := make([]adminRCSubscriberEntitlementResponse, len(out.Entitlements))
	for i, e := range out.Entitlements {
		entitlements[i] = adminRCSubscriberEntitlementResponse{
			EntitlementID:          e.EntitlementID,
			IsActive:               e.IsActive,
			ProductIdentifier:      e.ProductIdentifier,
			PurchaseDate:           e.PurchaseDate.UnixMilli(),
			ExpiresDate:            toUnixMsPtr(e.ExpiresDate),
			GracePeriodExpiresDate: toUnixMsPtr(e.GracePeriodExpiresDate),
		}
	}

	writeJSON(w, h.logger, http.StatusOK, adminRCSubscriberResponse{
		AppUserID:    out.AppUserID,
		Entitlements: entitlements,
	}, "admin-user-rc")
}
