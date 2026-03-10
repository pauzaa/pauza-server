package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/middleware"
	"github.com/IsorilovA/pauza-server/internal/service"
	"github.com/IsorilovA/pauza-server/internal/validate"
)

type AuthServicer interface {
	Start(ctx context.Context, in service.StartAuthInput) (service.StartAuthOutput, error)
	VerifyOTP(ctx context.Context, in service.VerifyOTPInput) (service.AuthOutput, error)
	Refresh(ctx context.Context, in service.RefreshInput) (service.RefreshOutput, error)
	GetMe(ctx context.Context, in service.GetMeInput) (service.UserProfile, error)
	UpdateMe(ctx context.Context, in service.UpdateMeInput) (service.UserProfile, error)
	UpdateProfilePhoto(ctx context.Context, in service.UpdateProfilePhotoInput) (service.UserProfile, error)
	CheckUsernameAvailable(ctx context.Context, in service.UsernameAvailableInput) (service.UsernameAvailableOutput, error)
	RequestAccountDeletion(ctx context.Context, in service.DeleteAccountRequestInput) (service.MessageOutput, error)
	ConfirmAccountDeletion(ctx context.Context, in service.DeleteAccountConfirmInput) (service.MessageOutput, error)
}

var _ AuthServicer = (*service.AuthService)(nil)

type AuthHandler struct {
	svc    AuthServicer
	logger *slog.Logger
}

func NewAuthHandler(svc AuthServicer, logger *slog.Logger) *AuthHandler {
	return &AuthHandler{svc: svc, logger: logger}
}

type startRequest struct {
	Email string `json:"email"`
}

type startResponse struct {
	OTPRequired bool `json:"otp_required"`
}

type verifyOTPRequest struct {
	Email string `json:"email"`
	OTP   string `json:"otp"`
}

type authResponse struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	User         userResponse `json:"user"`
}

type userResponse struct {
	ID                 string                `json:"id"`
	Email              string                `json:"email"`
	Name               string                `json:"name"`
	Username           string                `json:"username"`
	ProfilePictureURL  *string               `json:"profile_picture_url"`
	LeaderboardVisible bool                  `json:"leaderboard_visible"`
	CreatedAt          string                `json:"created_at"`
	Subscription       *subscriptionResponse `json:"subscription"`
}

type subscriptionResponse struct {
	Entitlement      string  `json:"entitlement"`
	IsActive         bool    `json:"is_active"`
	CurrentPeriodEnd *string `json:"current_period_end"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type updateMeRequest struct {
	Name               *string `json:"name"`
	Username           *string `json:"username"`
	LeaderboardVisible *bool   `json:"leaderboard_visible"`
}

type usernameAvailableResponse struct {
	Available bool `json:"available"`
}

type messageResponse struct {
	Message string `json:"message"`
}

type deleteConfirmRequest struct {
	OTP string `json:"otp"`
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(dst)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			apperror.ValidationError(w, "Request body too large", nil)
			return false
		}
		apperror.ValidationError(w, "Invalid request body", nil)
		return false
	}

	if dec.More() {
		apperror.ValidationError(w, "Invalid request body", nil)
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		apperror.ValidationError(w, "Invalid request body", nil)
		return false
	}

	return true
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrConflict):
		apperror.Conflict(w, serviceMessage(err, service.ErrConflict, "Conflict"))
	case errors.Is(err, service.ErrSubscriptionRequired):
		apperror.SubscriptionRequired(w, serviceMessage(err, service.ErrSubscriptionRequired, "Subscription required"))
	case errors.Is(err, service.ErrUnauthorized):
		apperror.Unauthorized(w, serviceMessage(err, service.ErrUnauthorized, "Unauthorized"))
	case errors.Is(err, service.ErrRateLimited):
		if retryAfter, ok := service.RetryAfter(err); ok {
			seconds := int(retryAfter / time.Second)
			if retryAfter%time.Second != 0 {
				seconds++
			}
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
		apperror.RateLimited(w, serviceMessage(err, service.ErrRateLimited, "Too many requests"))
	default:
		apperror.InternalError(w)
	}
}

func serviceMessage(err, sentinel error, fallback string) string {
	full := err.Error()
	prefix := sentinel.Error() + ": "
	if after, found := strings.CutPrefix(full, prefix); found && after != "" {
		return strings.ToUpper(after[:1]) + after[1:]
	}
	return fallback
}

func userProfileToResponse(p service.UserProfile) userResponse {
	resp := userResponse{
		ID:                 p.ID,
		Email:              p.Email,
		Name:               p.Name,
		Username:           p.Username,
		ProfilePictureURL:  p.ProfilePictureURL,
		LeaderboardVisible: p.LeaderboardVisible,
		CreatedAt:          p.CreatedAt.UTC().Format(time.RFC3339),
	}
	if p.Subscription != nil {
		resp.Subscription = subscriptionInfoToResponse(p.Subscription)
	}
	return resp
}

func subscriptionInfoToResponse(info *service.EntitlementInfo) *subscriptionResponse {
	resp := &subscriptionResponse{
		Entitlement: info.Entitlement,
		IsActive:    info.IsActive,
	}
	if info.CurrentPeriodEnd != nil {
		s := info.CurrentPeriodEnd.UTC().Format(time.RFC3339)
		resp.CurrentPeriodEnd = &s
	}
	return resp
}

func authOutputToResponse(out service.AuthOutput) authResponse {
	return authResponse{
		AccessToken:  out.AccessToken,
		RefreshToken: out.RefreshToken,
		User:         userProfileToResponse(out.User),
	}
}

func refreshOutputToResponse(out service.RefreshOutput) refreshResponse {
	return refreshResponse{
		AccessToken:  out.AccessToken,
		RefreshToken: out.RefreshToken,
	}
}

// Start handles POST /api/v1/auth/start.
func (h *AuthHandler) Start(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	if msg := validate.Email(req.Email); msg != "" {
		apperror.ValidationFieldErrors(w, "Invalid request body", apperror.FieldErrors{
			"email": msg,
		})
		return
	}

	out, err := h.svc.Start(r.Context(), service.StartAuthInput{Email: req.Email})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(startResponse{OTPRequired: out.OTPRequired}); err != nil {
		h.logger.Error("encoding auth start response", "err", err)
	}
}

// VerifyOTP handles POST /api/v1/auth/verify.
func (h *AuthHandler) VerifyOTP(w http.ResponseWriter, r *http.Request) {
	var req verifyOTPRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	fields := make(apperror.FieldErrors)
	if msg := validate.Email(req.Email); msg != "" {
		fields["email"] = msg
	}
	if msg := validate.OTP(req.OTP); msg != "" {
		fields["otp"] = msg
	}
	if len(fields) > 0 {
		apperror.ValidationFieldErrors(w, "Invalid request body", fields)
		return
	}

	out, err := h.svc.VerifyOTP(r.Context(), service.VerifyOTPInput{
		Email: req.Email,
		OTP:   req.OTP,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(authOutputToResponse(out)); err != nil {
		h.logger.Error("encoding verify-otp response", "err", err)
	}
}

// Refresh handles POST /api/v1/auth/refresh.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.RefreshToken) == "" {
		apperror.ValidationFieldErrors(w, "Invalid request body", apperror.FieldErrors{
			"refresh_token": "refresh_token is required",
		})
		return
	}

	out, err := h.svc.Refresh(r.Context(), service.RefreshInput{
		RefreshToken: req.RefreshToken,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(refreshOutputToResponse(out)); err != nil {
		h.logger.Error("encoding refresh response", "err", err)
	}
}

func (h *AuthHandler) GetMe(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok || user.UserID == "" {
		apperror.Unauthorized(w, "Missing or invalid authentication")
		return
	}

	out, err := h.svc.GetMe(r.Context(), service.GetMeInput{UserID: user.UserID})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(userProfileToResponse(out)); err != nil {
		h.logger.Error("encoding get-me response", "err", err)
	}
}

func (h *AuthHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok || user.UserID == "" {
		apperror.Unauthorized(w, "Missing or invalid authentication")
		return
	}

	var req updateMeRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	fields := make(apperror.FieldErrors)
	if req.Name != nil {
		if msg := validate.Name(*req.Name); msg != "" {
			fields["name"] = msg
		}
	}
	if req.Username != nil {
		if msg := validate.Username(*req.Username); msg != "" {
			fields["username"] = msg
		}
	}
	if len(fields) > 0 {
		apperror.ValidationFieldErrors(w, "Invalid request body", fields)
		return
	}

	out, err := h.svc.UpdateMe(r.Context(), service.UpdateMeInput{
		UserID:             user.UserID,
		Name:               req.Name,
		Username:           req.Username,
		LeaderboardVisible: req.LeaderboardVisible,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(userProfileToResponse(out)); err != nil {
		h.logger.Error("encoding update-me response", "err", err)
	}
}

func (h *AuthHandler) UsernameAvailable(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok || user.UserID == "" {
		apperror.Unauthorized(w, "Missing or invalid authentication")
		return
	}

	username := r.URL.Query().Get("username")
	if msg := validate.Username(username); msg != "" {
		apperror.ValidationFieldErrors(w, "Invalid request body", apperror.FieldErrors{
			"username": msg,
		})
		return
	}

	out, err := h.svc.CheckUsernameAvailable(r.Context(), service.UsernameAvailableInput{
		UserID:   user.UserID,
		Username: username,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(usernameAvailableResponse{Available: out.Available}); err != nil {
		h.logger.Error("encoding username-available response", "err", err)
	}
}

func (h *AuthHandler) UploadPhoto(w http.ResponseWriter, r *http.Request, photoURL string) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok || user.UserID == "" {
		apperror.Unauthorized(w, "Missing or invalid authentication")
		return
	}

	out, err := h.svc.UpdateProfilePhoto(r.Context(), service.UpdateProfilePhotoInput{
		UserID:            user.UserID,
		ProfilePictureURL: photoURL,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	resp := map[string]string{"profile_picture_url": derefString(out.ProfilePictureURL)}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("encoding upload-photo response", "err", err)
	}
}

func (h *AuthHandler) DeleteRequest(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok || user.UserID == "" {
		apperror.Unauthorized(w, "Missing or invalid authentication")
		return
	}

	out, err := h.svc.RequestAccountDeletion(r.Context(), service.DeleteAccountRequestInput{UserID: user.UserID})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeMessageResponse(w, h.logger, http.StatusOK, out.Message, "delete-request")
}

func (h *AuthHandler) DeleteConfirm(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok || user.UserID == "" {
		apperror.Unauthorized(w, "Missing or invalid authentication")
		return
	}

	var req deleteConfirmRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if msg := validate.OTP(req.OTP); msg != "" {
		apperror.ValidationFieldErrors(w, "Invalid request body", apperror.FieldErrors{"otp": msg})
		return
	}

	out, err := h.svc.ConfirmAccountDeletion(r.Context(), service.DeleteAccountConfirmInput{
		UserID: user.UserID,
		OTP:    req.OTP,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeMessageResponse(w, h.logger, http.StatusOK, out.Message, "delete-confirm")
}

func ValidatePhotoUpload(file multipart.File) string {
	header := make([]byte, 512)
	n, err := io.ReadFull(file, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "photo is invalid"
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "photo is invalid"
	}

	contentType := http.DetectContentType(header[:n])
	switch contentType {
	case "image/jpeg", "image/png":
		return ""
	default:
		return "photo must be a JPEG or PNG"
	}
}

func writeMessageResponse(w http.ResponseWriter, logger *slog.Logger, status int, message string, op string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(messageResponse{Message: message}); err != nil {
		logger.Error("encoding "+op+" response", "err", err)
	}
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
