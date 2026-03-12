package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/photostore"
	"github.com/IsorilovA/pauza-server/internal/service"
	"github.com/IsorilovA/pauza-server/internal/validate"
)

type AuthSessionService interface {
	Start(ctx context.Context, in service.StartAuthInput) (service.StartAuthOutput, error)
	VerifyOTP(ctx context.Context, in service.VerifyOTPInput) (service.AuthOutput, error)
	Refresh(ctx context.Context, in service.RefreshInput) (service.RefreshOutput, error)
}

type AuthProfileService interface {
	GetMe(ctx context.Context, in service.GetMeInput) (service.UserProfile, error)
	UpdateMe(ctx context.Context, in service.UpdateMeInput) (service.UserProfile, error)
	UpdateProfilePhoto(ctx context.Context, in service.UpdateProfilePhotoInput) (service.UserProfile, error)
	CheckUsernameAvailable(ctx context.Context, in service.UsernameAvailableInput) (service.UsernameAvailableOutput, error)
}

type AuthPreferencesService interface {
	GetNotificationPreferences(ctx context.Context, in service.GetNotificationPreferencesInput) (service.NotificationPreferences, error)
	UpdateNotificationPreferences(ctx context.Context, in service.UpdateNotificationPreferencesInput) (service.NotificationPreferences, error)
	GetPrivacyPreferences(ctx context.Context, in service.GetPrivacyPreferencesInput) (service.PrivacyPreferences, error)
	UpdatePrivacyPreferences(ctx context.Context, in service.UpdatePrivacyPreferencesInput) (service.PrivacyPreferences, error)
}

type AuthDeletionService interface {
	RequestAccountDeletion(ctx context.Context, in service.DeleteAccountRequestInput) (service.MessageOutput, error)
	ConfirmAccountDeletion(ctx context.Context, in service.DeleteAccountConfirmInput) (service.MessageOutput, error)
}

var _ AuthSessionService = (*service.AuthService)(nil)
var _ AuthProfileService = (*service.AuthService)(nil)
var _ AuthPreferencesService = (*service.AuthService)(nil)
var _ AuthDeletionService = (*service.AuthService)(nil)

type AuthService interface {
	AuthSessionService
	AuthProfileService
	AuthPreferencesService
	AuthDeletionService
}

type AuthHandler struct {
	svc        AuthService
	photoStore photostore.Store
	logger     *slog.Logger
}

func NewAuthHandler(svc AuthService, photoStore photostore.Store, logger *slog.Logger) *AuthHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthHandler{
		svc:        svc,
		photoStore: photoStore,
		logger:     logger,
	}
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
	PushEnabled        bool                  `json:"push_enabled"`
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
	Name     *string `json:"name"`
	Username *string `json:"username"`
}

type usernameAvailableResponse struct {
	Available bool `json:"available"`
}

type notificationPreferencesRequest struct {
	PushEnabled *bool `json:"push_enabled"`
}

type notificationPreferencesResponse struct {
	PushEnabled bool `json:"push_enabled"`
}

type privacyPreferencesRequest struct {
	LeaderboardVisible *bool `json:"leaderboard_visible"`
}

type privacyPreferencesResponse struct {
	LeaderboardVisible bool `json:"leaderboard_visible"`
}

type profilePhotoResponse struct {
	ProfilePictureURL string `json:"profile_picture_url"`
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

func userProfileToResponse(p service.UserProfile) userResponse {
	resp := userResponse{
		ID:                 p.ID,
		Email:              p.Email,
		Name:               p.Name,
		Username:           p.Username,
		ProfilePictureURL:  p.ProfilePictureURL,
		PushEnabled:        p.PushEnabled,
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

func notificationPreferencesToResponse(p service.NotificationPreferences) notificationPreferencesResponse {
	return notificationPreferencesResponse{PushEnabled: p.PushEnabled}
}

func privacyPreferencesToResponse(p service.PrivacyPreferences) privacyPreferencesResponse {
	return privacyPreferencesResponse{LeaderboardVisible: p.LeaderboardVisible}
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

	writeJSON(w, h.logger, http.StatusOK, startResponse{OTPRequired: out.OTPRequired}, "auth-start")
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

	writeJSON(w, h.logger, http.StatusOK, authOutputToResponse(out), "verify-otp")
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

	writeJSON(w, h.logger, http.StatusOK, refreshOutputToResponse(out), "refresh")
}

func (h *AuthHandler) GetMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	out, err := h.svc.GetMe(r.Context(), service.GetMeInput{UserID: userID})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, userProfileToResponse(out), "get-me")
}

func (h *AuthHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
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
		UserID:   userID,
		Name:     req.Name,
		Username: req.Username,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, userProfileToResponse(out), "update-me")
}

func (h *AuthHandler) UsernameAvailable(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
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
		UserID:   userID,
		Username: username,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, usernameAvailableResponse{Available: out.Available}, "username-available")
}

func (h *AuthHandler) GetNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	out, err := h.svc.GetNotificationPreferences(r.Context(), service.GetNotificationPreferencesInput{UserID: userID})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, notificationPreferencesToResponse(out), "get-notification-preferences")
}

func (h *AuthHandler) UpdateNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req notificationPreferencesRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	out, err := h.svc.UpdateNotificationPreferences(r.Context(), service.UpdateNotificationPreferencesInput{
		UserID:      userID,
		PushEnabled: req.PushEnabled,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, notificationPreferencesToResponse(out), "update-notification-preferences")
}

func (h *AuthHandler) GetPrivacyPreferences(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	out, err := h.svc.GetPrivacyPreferences(r.Context(), service.GetPrivacyPreferencesInput{UserID: userID})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, privacyPreferencesToResponse(out), "get-privacy-preferences")
}

func (h *AuthHandler) UpdatePrivacyPreferences(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req privacyPreferencesRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	out, err := h.svc.UpdatePrivacyPreferences(r.Context(), service.UpdatePrivacyPreferencesInput{
		UserID:             userID,
		LeaderboardVisible: req.LeaderboardVisible,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, privacyPreferencesToResponse(out), "update-privacy-preferences")
}

func (h *AuthHandler) UploadPhoto(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if h.photoStore == nil {
		h.logger.Error("photo upload requested without configured photo store")
		apperror.InternalError(w)
		return
	}

	file, header, err := r.FormFile("photo")
	if err != nil {
		apperror.ValidationFieldErrors(w, "Invalid request body", apperror.FieldErrors{"photo": "photo is required"})
		return
	}
	defer file.Close()

	if header.Size > 5<<20 {
		apperror.ValidationFieldErrors(w, "Invalid request body", apperror.FieldErrors{"photo": "photo must not exceed 5 MB"})
		return
	}
	if msg := ValidatePhotoUpload(file); msg != "" {
		apperror.ValidationFieldErrors(w, "Invalid request body", apperror.FieldErrors{"photo": msg})
		return
	}

	ext := ".jpg"
	if header.Header.Get("Content-Type") == "image/png" {
		ext = ".png"
	}

	photoURL, err := h.photoStore.Save(r.Context(), file, ext)
	if err != nil {
		h.logger.Error("saving photo upload", "err", err)
		apperror.InternalError(w)
		return
	}

	out, err := h.svc.UpdateProfilePhoto(r.Context(), service.UpdateProfilePhotoInput{
		UserID:            userID,
		ProfilePictureURL: photoURL,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, h.logger, http.StatusOK, profilePhotoResponse{ProfilePictureURL: derefString(out.ProfilePictureURL)}, "upload-photo")
}

func (h *AuthHandler) DeleteRequest(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	out, err := h.svc.RequestAccountDeletion(r.Context(), service.DeleteAccountRequestInput{UserID: userID})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeMessageResponse(w, h.logger, http.StatusOK, out.Message, "delete-request")
}

func (h *AuthHandler) DeleteConfirm(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
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
		UserID: userID,
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

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
