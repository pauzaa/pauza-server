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

var _ AdminLoginService = (*service.AdminService)(nil)
var _ AdminUsersService = (*service.AdminService)(nil)
var _ AdminStatsService = (*service.AdminService)(nil)
var _ AdminEntitlementsService = (*service.AdminService)(nil)

type AdminService interface {
	AdminLoginService
	AdminUsersService
	AdminStatsService
	AdminEntitlementsService
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
	CreatedAt                string  `json:"created_at"`
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
	CreatedAt           string  `json:"created_at"`
	IsPremium           bool    `json:"is_premium"`
	CurrentPeriodEnd    *string `json:"current_period_end"`
	RevenueCatAppUserID *string `json:"revenuecat_app_user_id"`
	FriendCount         int     `json:"friend_count"`
	TotalSessions       int     `json:"total_sessions"`
	LastSessionTime     *int64  `json:"last_session_time"`
}

type adminStatsResponse struct {
	TotalUsers                int     `json:"total_users"`
	ActiveUsers30d            int     `json:"active_users_30d"`
	PremiumUsers              int     `json:"users_with_premium_entitlement"`
	ActivePremiumEntitlements int     `json:"active_premium_entitlements"`
	TotalFriendships          int     `json:"total_friendships"`
	AvgStreakDays             float64 `json:"avg_streak_days"`
	AvgDailyFocusTimeMS       float64 `json:"avg_daily_focus_time_ms"`
}

type manageEntitlementRequest struct {
	Action      domain.AdminOverrideAction `json:"action"`
	Entitlement domain.Entitlement         `json:"entitlement"`
	ExpiresAt   *string                    `json:"expires_at"`
}

type adminEntitlementItemResponse struct {
	UserID           string  `json:"user_id"`
	Email            string  `json:"email"`
	Username         string  `json:"username"`
	Entitlement      string  `json:"entitlement"`
	IsActive         bool    `json:"is_active"`
	CurrentPeriodEnd *string `json:"current_period_end"`
	UpdatedAt        string  `json:"updated_at"`
}

type adminListEntitlementsResponse struct {
	Entitlements []adminEntitlementItemResponse `json:"entitlements"`
	Pagination   paginationResponse             `json:"pagination"`
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
			CreatedAt:                u.CreatedAt.UTC().Format(time.RFC3339),
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
	userID := chi.URLParam(r, "id")
	if userID == "" {
		apperror.ValidationFieldErrors(w, "Invalid request", apperror.FieldErrors{
			"id": "user id is required",
		})
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
		CreatedAt:           out.CreatedAt.UTC().Format(time.RFC3339),
		IsPremium:           out.IsPremium,
		RevenueCatAppUserID: out.RevenueCatAppUserID,
		FriendCount:         out.FriendCount,
		TotalSessions:       out.TotalSessions,
		LastSessionTime:     out.LastSessionTime,
	}
	if out.CurrentPeriodEnd != nil {
		s := out.CurrentPeriodEnd.UTC().Format(time.RFC3339)
		resp.CurrentPeriodEnd = &s
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
		TotalUsers:                out.TotalUsers,
		ActiveUsers30d:            out.ActiveUsers30d,
		PremiumUsers:              out.PremiumUsers,
		ActivePremiumEntitlements: out.ActivePremiumEntitlements,
		TotalFriendships:          out.TotalFriendships,
		AvgStreakDays:             out.AvgStreakDays,
		AvgDailyFocusTimeMS:       out.AvgDailyFocusTimeMS,
	}

	writeJSON(w, h.logger, http.StatusOK, resp, "admin-stats")
}

// ManageEntitlement handles POST /api/v1/admin/users/{id}/entitlements.
func (h *AdminHandler) ManageEntitlement(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		apperror.ValidationFieldErrors(w, "Invalid request", apperror.FieldErrors{
			"id": "user id is required",
		})
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
	var expiresAt *time.Time
	if req.ExpiresAt != nil {
		t, parseErr := time.Parse(time.RFC3339, *req.ExpiresAt)
		if parseErr != nil {
			fields["expires_at"] = "expires_at must be a valid RFC3339 timestamp"
		} else if !t.After(time.Now()) {
			fields["expires_at"] = "expires_at must be in the future"
		} else {
			expiresAt = &t
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
		item := adminEntitlementItemResponse{
			UserID:      e.UserID,
			Email:       e.Email,
			Username:    e.Username,
			Entitlement: e.Entitlement,
			IsActive:    e.IsActive,
			UpdatedAt:   e.UpdatedAt.UTC().Format(time.RFC3339),
		}
		if e.CurrentPeriodEnd != nil {
			s := e.CurrentPeriodEnd.UTC().Format(time.RFC3339)
			item.CurrentPeriodEnd = &s
		}
		entitlements[i] = item
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
