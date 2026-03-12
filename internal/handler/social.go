package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/repository"
	"github.com/IsorilovA/pauza-server/internal/service"
	"github.com/IsorilovA/pauza-server/internal/validate"
)

type SocialServicer interface {
	RegisterDevice(ctx context.Context, in service.DeviceInput) (service.MessageOutput, error)
	UnregisterDevice(ctx context.Context, in service.DeviceInput) (service.MessageOutput, error)
	RequestFriend(ctx context.Context, in service.FriendRequestInput) (service.FriendMutationOutput, error)
	ListFriends(ctx context.Context, userID string, page, limit int) (service.FriendListOutput, error)
	ListFriendRequests(ctx context.Context, userID string, direction repository.FriendRequestDirection) (service.FriendRequestsOutput, error)
	AcceptFriend(ctx context.Context, userID, friendshipID string) (service.FriendMutationOutput, error)
	DeclineFriend(ctx context.Context, userID, friendshipID string) (service.MessageOutput, error)
	RemoveFriend(ctx context.Context, userID, friendshipID string) (service.MessageOutput, error)
	SearchUsers(ctx context.Context, userID, prefix string) ([]repository.BasicUserRow, error)
	FriendStats(ctx context.Context, userID, friendshipID string, days int) (service.FriendStatsOutput, error)
	LeaderboardByStreak(ctx context.Context, userID string, page, limit int) (service.LeaderboardOutput, error)
	LeaderboardByFocusTime(ctx context.Context, userID string, page, limit int) (service.LeaderboardOutput, error)
}

type SocialHandler struct {
	svc    SocialServicer
	logger *slog.Logger
}

func NewSocialHandler(svc SocialServicer) *SocialHandler {
	return &SocialHandler{svc: svc, logger: slog.Default()}
}

type deviceRequest struct {
	FCMToken string                    `json:"fcm_token"`
	Platform repository.DevicePlatform `json:"platform"`
}

type unregisterDeviceRequest struct {
	FCMToken string `json:"fcm_token"`
}

type friendRequestRequest struct {
	Username string `json:"username"`
}

type searchUsersResponse struct {
	Users []repository.BasicUserRow `json:"users"`
}

func (h *SocialHandler) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req deviceRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	fields := apperror.FieldErrors{}
	if req.FCMToken == "" {
		fields["fcm_token"] = "fcm_token is required"
	}
	if !req.Platform.Valid() {
		fields["platform"] = "platform must be one of: android, ios"
	}
	if len(fields) > 0 {
		apperror.ValidationFieldErrors(w, "Invalid request body", fields)
		return
	}
	out, err := h.svc.RegisterDevice(r.Context(), service.DeviceInput{UserID: userID, FCMToken: req.FCMToken, Platform: req.Platform})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeMessageResponse(w, h.logger, http.StatusOK, out.Message, "register-device")
}

func (h *SocialHandler) UnregisterDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req unregisterDeviceRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.FCMToken == "" {
		apperror.ValidationFieldErrors(w, "Invalid request body", apperror.FieldErrors{"fcm_token": "fcm_token is required"})
		return
	}
	out, err := h.svc.UnregisterDevice(r.Context(), service.DeviceInput{UserID: userID, FCMToken: req.FCMToken})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeMessageResponse(w, h.logger, http.StatusOK, out.Message, "unregister-device")
}

func (h *SocialHandler) ListFriends(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	page, limit := paginationParams(r)
	out, err := h.svc.ListFriends(r.Context(), userID, page, limit)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, out, "list-friends")
}

func (h *SocialHandler) RequestFriend(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var req friendRequestRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if msg := validate.Username(req.Username); msg != "" {
		apperror.ValidationFieldErrors(w, "Invalid request body", apperror.FieldErrors{"username": msg})
		return
	}
	out, err := h.svc.RequestFriend(r.Context(), service.FriendRequestInput{UserID: userID, Username: req.Username})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusCreated, out, "request-friend")
}

func (h *SocialHandler) ListIncomingRequests(w http.ResponseWriter, r *http.Request) {
	h.listRequests(w, r, repository.FriendRequestIncoming)
}
func (h *SocialHandler) ListOutgoingRequests(w http.ResponseWriter, r *http.Request) {
	h.listRequests(w, r, repository.FriendRequestOutgoing)
}

func (h *SocialHandler) listRequests(w http.ResponseWriter, r *http.Request, direction repository.FriendRequestDirection) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	out, err := h.svc.ListFriendRequests(r.Context(), userID, direction)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, out, "list-friend-requests")
}

func (h *SocialHandler) AcceptFriend(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	out, err := h.svc.AcceptFriend(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, out, "accept-friend")
}

func (h *SocialHandler) DeclineFriend(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	out, err := h.svc.DeclineFriend(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeMessageResponse(w, h.logger, http.StatusOK, out.Message, "decline-friend")
}

func (h *SocialHandler) RemoveFriend(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	out, err := h.svc.RemoveFriend(r.Context(), userID, chi.URLParam(r, "id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeMessageResponse(w, h.logger, http.StatusOK, out.Message, "remove-friend")
}

func (h *SocialHandler) SearchUsers(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	q := r.URL.Query().Get("q")
	if len(q) < 3 {
		apperror.ValidationFieldErrors(w, "Invalid request body", apperror.FieldErrors{"q": "q must be at least 3 characters"})
		return
	}
	users, err := h.svc.SearchUsers(r.Context(), userID, q)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, searchUsersResponse{Users: users}, "search-users")
}

func (h *SocialHandler) FriendStats(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 90 {
			days = parsed
		}
	}
	out, err := h.svc.FriendStats(r.Context(), userID, chi.URLParam(r, "id"), days)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, out, "friend-stats")
}

func (h *SocialHandler) LeaderboardStreaks(w http.ResponseWriter, r *http.Request) {
	h.leaderboard(w, r, true)
}
func (h *SocialHandler) LeaderboardFocusTime(w http.ResponseWriter, r *http.Request) {
	h.leaderboard(w, r, false)
}

func (h *SocialHandler) leaderboard(w http.ResponseWriter, r *http.Request, byStreak bool) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	page, limit := paginationParams(r)
	var (
		out service.LeaderboardOutput
		err error
	)
	if byStreak {
		out, err = h.svc.LeaderboardByStreak(r.Context(), userID, page, limit)
	} else {
		out, err = h.svc.LeaderboardByFocusTime(r.Context(), userID, page, limit)
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, out, "leaderboard")
}

func paginationParams(r *http.Request) (int, int) {
	page := 1
	limit := 20
	if raw := r.URL.Query().Get("page"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	return page, limit
}
