package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/middleware"
	"github.com/IsorilovA/pauza-server/internal/service"
	"github.com/IsorilovA/pauza-server/internal/validate"
)

// AuthServicer defines the behavior the auth handler needs from the service
// layer. It decouples the handler from the concrete *service.AuthService
// so that tests can substitute a lightweight stub without a database.
type AuthServicer interface {
	Register(ctx context.Context, in service.RegisterInput) (service.RegisterOutput, error)
	VerifyOTP(ctx context.Context, in service.VerifyOTPInput) (service.AuthOutput, error)
	Login(ctx context.Context, in service.LoginInput) (service.AuthOutput, error)
	Refresh(ctx context.Context, in service.RefreshInput) (service.RefreshOutput, error)
	ForgotPassword(ctx context.Context, in service.ForgotPasswordInput) (service.MessageOutput, error)
	ResetPassword(ctx context.Context, in service.ResetPasswordInput) (service.MessageOutput, error)
	GetMe(ctx context.Context, in service.GetMeInput) (service.UserProfile, error)
}

// Compile-time check: *service.AuthService satisfies AuthServicer.
var _ AuthServicer = (*service.AuthService)(nil)

// AuthHandler handles authentication-related HTTP requests. It is a thin
// adapter that validates input, delegates to AuthServicer, and maps service
// errors/outputs to HTTP responses.
type AuthHandler struct {
	svc    AuthServicer
	logger *slog.Logger
}

// NewAuthHandler creates an AuthHandler with the given dependencies.
func NewAuthHandler(svc AuthServicer, logger *slog.Logger) *AuthHandler {
	return &AuthHandler{
		svc:    svc,
		logger: logger,
	}
}

// ---------------------------------------------------------------------------
// HTTP request / response types
// ---------------------------------------------------------------------------

// registerRequest is the expected JSON body for POST /api/v1/auth/register.
type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// registerResponse is the JSON response for a successful registration.
type registerResponse struct {
	OTPRequired bool `json:"otp_required"`
}

// verifyOTPRequest is the expected JSON body for POST /api/v1/auth/verify-otp.
type verifyOTPRequest struct {
	Email string `json:"email"`
	OTP   string `json:"otp"`
}

// authResponse is the JSON response for authentication endpoints that return
// tokens and a user profile (e.g. verify-otp, login).
type authResponse struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	User         userResponse `json:"user"`
}

// userResponse is the user profile object returned in auth responses.
// The shape matches BACKEND_SPEC Section 5.3.
type userResponse struct {
	ID                 string  `json:"id"`
	Email              string  `json:"email"`
	Name               string  `json:"name"`
	Username           string  `json:"username"`
	ProfilePictureURL  *string `json:"profile_picture_url"`
	LeaderboardVisible bool    `json:"leaderboard_visible"`
	CreatedAt          string  `json:"created_at"`
	// Subscription is null for newly registered users and free-tier users.
	Subscription *subscriptionResponse `json:"subscription"`
}

// subscriptionResponse represents the user's active subscription, if any.
// The shape matches BACKEND_SPEC Section 5.3 (GET /api/v1/me).
type subscriptionResponse struct {
	PlanID           string         `json:"plan_id"`
	PlanName         string         `json:"plan_name"`
	Status           string         `json:"status"`
	IsStudent        bool           `json:"is_student"`
	CurrentPeriodEnd *string        `json:"current_period_end"`
	Features         map[string]any `json:"features"`
}

// loginRequest is the expected JSON body for POST /api/v1/auth/login.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// refreshRequest is the expected JSON body for POST /api/v1/auth/refresh.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// refreshResponse is the JSON response for a successful token refresh.
type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// forgotPasswordRequest is the expected JSON body for POST /api/v1/auth/forgot-password.
type forgotPasswordRequest struct {
	Email string `json:"email"`
}

// messageResponse is the JSON response for endpoints that return a simple message.
type messageResponse struct {
	Message string `json:"message"`
}

// resetPasswordRequest is the expected JSON body for POST /api/v1/auth/reset-password.
type resetPasswordRequest struct {
	Email       string `json:"email"`
	OTP         string `json:"otp"`
	NewPassword string `json:"new_password"`
}

// ---------------------------------------------------------------------------
// JSON decoding
// ---------------------------------------------------------------------------

// decodeJSONBody decodes the request body into dst. It rejects unknown
// fields and trailing data after the first JSON object. It distinguishes
// an oversized body (MaxBytesError → "Request body too large") from a
// malformed/invalid payload, writing the appropriate 422 error response
// and returning false when decoding fails.
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

	// Reject trailing JSON documents after the first object.
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

// ---------------------------------------------------------------------------
// Service error → HTTP response mapping
// ---------------------------------------------------------------------------

// writeServiceError maps a service-layer error to the appropriate HTTP
// error response. It extracts the user-facing message from the error chain
// (the part after the sentinel error prefix) and uses it directly.
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrConflict):
		apperror.Conflict(w, serviceMessage(err, service.ErrConflict, "Conflict"))
	case errors.Is(err, service.ErrUnauthorized):
		apperror.Unauthorized(w, serviceMessage(err, service.ErrUnauthorized, "Unauthorized"))
	case errors.Is(err, service.ErrRateLimited):
		apperror.RateLimited(w, serviceMessage(err, service.ErrRateLimited, "Too many requests"))
	default:
		// ErrInternal and any unexpected errors → generic 500.
		apperror.InternalError(w)
	}
}

// serviceMessage extracts the human-readable message from a wrapped sentinel
// error. For example, fmt.Errorf("%w: email already registered", ErrConflict)
// produces "email already registered" when unwrapping past the sentinel.
// If extraction fails, fallback is returned.
func serviceMessage(err, sentinel error, fallback string) string {
	full := err.Error()
	prefix := sentinel.Error() + ": "
	if after, found := strings.CutPrefix(full, prefix); found && after != "" {
		// Capitalize first letter.
		return strings.ToUpper(after[:1]) + after[1:]
	}
	return fallback
}

// ---------------------------------------------------------------------------
// Service output → HTTP response conversion
// ---------------------------------------------------------------------------

// userProfileToResponse converts a service.UserProfile to a userResponse
// suitable for JSON serialization.
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

// subscriptionInfoToResponse converts a service.SubscriptionInfo to a
// subscriptionResponse suitable for JSON serialization.
func subscriptionInfoToResponse(info *service.SubscriptionInfo) *subscriptionResponse {
	resp := &subscriptionResponse{
		PlanID:    info.PlanID,
		PlanName:  info.PlanName,
		Status:    info.Status,
		IsStudent: info.IsStudent,
		Features:  info.Features,
	}
	if info.CurrentPeriodEnd != nil {
		s := info.CurrentPeriodEnd.UTC().Format(time.RFC3339)
		resp.CurrentPeriodEnd = &s
	}
	return resp
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// Register handles POST /api/v1/auth/register.
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	// Validate fields and collect errors.
	fields := make(apperror.FieldErrors)
	if msg := validate.Email(req.Email); msg != "" {
		fields["email"] = msg
	}
	if msg := validate.Password(req.Password); msg != "" {
		fields["password"] = msg
	}
	if len(fields) > 0 {
		apperror.ValidationFieldErrors(w, "Invalid request body", fields)
		return
	}

	out, err := h.svc.Register(r.Context(), service.RegisterInput{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(registerResponse{OTPRequired: out.OTPRequired}); err != nil {
		h.logger.Error("encoding register response", "err", err)
	}
}

// VerifyOTP handles POST /api/v1/auth/verify-otp.
func (h *AuthHandler) VerifyOTP(w http.ResponseWriter, r *http.Request) {
	var req verifyOTPRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	// Validate fields and collect errors.
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
	if err := json.NewEncoder(w).Encode(authResponse{
		AccessToken:  out.AccessToken,
		RefreshToken: out.RefreshToken,
		User:         userProfileToResponse(out.User),
	}); err != nil {
		h.logger.Error("encoding verify-otp response", "err", err)
	}
}

// Login handles POST /api/v1/auth/login.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	// Validate fields and collect errors.
	fields := make(apperror.FieldErrors)
	if msg := validate.Email(req.Email); msg != "" {
		fields["email"] = msg
	}
	if msg := validate.Password(req.Password); msg != "" {
		fields["password"] = msg
	}
	if len(fields) > 0 {
		apperror.ValidationFieldErrors(w, "Invalid request body", fields)
		return
	}

	out, err := h.svc.Login(r.Context(), service.LoginInput{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(authResponse{
		AccessToken:  out.AccessToken,
		RefreshToken: out.RefreshToken,
		User:         userProfileToResponse(out.User),
	}); err != nil {
		h.logger.Error("encoding login response", "err", err)
	}
}

// Refresh handles POST /api/v1/auth/refresh.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	// Validate non-empty refresh token.
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
	if err := json.NewEncoder(w).Encode(refreshResponse{
		AccessToken:  out.AccessToken,
		RefreshToken: out.RefreshToken,
	}); err != nil {
		h.logger.Error("encoding refresh response", "err", err)
	}
}

// ForgotPassword handles POST /api/v1/auth/forgot-password.
func (h *AuthHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req forgotPasswordRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	// Validate email.
	if msg := validate.Email(req.Email); msg != "" {
		apperror.ValidationFieldErrors(w, "Invalid request body", apperror.FieldErrors{
			"email": msg,
		})
		return
	}

	// Record start time after common parsing/validation so the timing
	// floor only covers the account-specific code paths where divergence
	// would leak account existence.
	start := time.Now()

	// timingPad waits until forgotPasswordMinDuration has elapsed since
	// start, but returns immediately if the request context is cancelled.
	// This normalises response latency between code paths while allowing
	// the handler to abort promptly on client disconnect or shutdown.
	timingPad := func() {
		if elapsed := time.Since(start); elapsed < service.ForgotPasswordMinDuration {
			t := time.NewTimer(service.ForgotPasswordMinDuration - elapsed)
			defer t.Stop()
			select {
			case <-t.C:
			case <-ctx.Done():
			}
		}
	}

	out, err := h.svc.ForgotPassword(ctx, service.ForgotPasswordInput{
		Email: req.Email,
	})
	if err != nil {
		timingPad()
		writeServiceError(w, err)
		return
	}

	timingPad()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(messageResponse{
		Message: out.Message,
	}); err != nil {
		h.logger.Error("encoding forgot-password response", "err", err)
	}
}

// ResetPassword handles POST /api/v1/auth/reset-password.
func (h *AuthHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	// Validate fields and collect errors.
	fields := make(apperror.FieldErrors)
	if msg := validate.Email(req.Email); msg != "" {
		fields["email"] = msg
	}
	if msg := validate.OTP(req.OTP); msg != "" {
		fields["otp"] = msg
	}
	if msg := validate.Password(req.NewPassword); msg != "" {
		fields["new_password"] = msg
	}
	if len(fields) > 0 {
		apperror.ValidationFieldErrors(w, "Invalid request body", fields)
		return
	}

	out, err := h.svc.ResetPassword(r.Context(), service.ResetPasswordInput{
		Email:       req.Email,
		OTP:         req.OTP,
		NewPassword: req.NewPassword,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(messageResponse{
		Message: out.Message,
	}); err != nil {
		h.logger.Error("encoding reset-password response", "err", err)
	}
}

// GetMe handles GET /api/v1/me.
func (h *AuthHandler) GetMe(w http.ResponseWriter, r *http.Request) {
	authUser, ok := middleware.UserFromContext(r.Context())
	if !ok {
		apperror.Unauthorized(w, "missing or invalid authentication")
		return
	}

	profile, err := h.svc.GetMe(r.Context(), service.GetMeInput{
		UserID: authUser.UserID,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	resp := userProfileToResponse(profile)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("encoding get-me response", "err", err)
	}
}
