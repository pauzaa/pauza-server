package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/service"
)

type mockEmailSender struct{}

func (m *mockEmailSender) Probe(_ context.Context) error { return nil }
func (m *mockEmailSender) SendOTP(_ context.Context, _, _, _ string) error {
	return nil
}

func newTestAuthHandler() *AuthHandler {
	svc := service.NewAuthService(nil, nil, &mockEmailSender{}, "test-secret-abcdefghijklmnopqrstuvwxyz", time.Minute, time.Hour, noopLogger())
	return NewAuthHandler(svc, noopLogger())
}

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func assertValidationEnvelope(t *testing.T, rec *httptest.ResponseRecorder, expectedFields []string) {
	t.Helper()
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Fields map[string]string `json:"fields"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeValidationError {
		t.Fatalf("error.code = %q, want %q", resp.Error.Code, apperror.CodeValidationError)
	}
	for _, field := range expectedFields {
		if _, ok := resp.Error.Details.Fields[field]; !ok {
			t.Fatalf("missing validation error for field %q", field)
		}
	}
}

func TestStart_Validation(t *testing.T) {
	t.Parallel()

	h := newTestAuthHandler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/start", strings.NewReader(`{"email":"bad"}`))
	rec := httptest.NewRecorder()

	h.Start(rec, req)

	assertValidationEnvelope(t, rec, []string{"email"})
}

func TestVerifyOTP_Validation(t *testing.T) {
	t.Parallel()

	h := newTestAuthHandler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify", strings.NewReader(`{"email":"user@example.com","otp":"abc"}`))
	rec := httptest.NewRecorder()

	h.VerifyOTP(rec, req)

	assertValidationEnvelope(t, rec, []string{"otp"})
}

func TestRefresh_Validation(t *testing.T) {
	t.Parallel()

	h := newTestAuthHandler()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{"refresh_token":""}`))
	rec := httptest.NewRecorder()

	h.Refresh(rec, req)

	assertValidationEnvelope(t, rec, []string{"refresh_token"})
}

type mockAuthService struct {
	startFn                  func(ctx context.Context, in service.StartAuthInput) (service.StartAuthOutput, error)
	verifyOTPFn              func(ctx context.Context, in service.VerifyOTPInput) (service.AuthOutput, error)
	refreshFn                func(ctx context.Context, in service.RefreshInput) (service.RefreshOutput, error)
	getMeFn                  func(ctx context.Context, in service.GetMeInput) (service.UserProfile, error)
	updateMeFn               func(ctx context.Context, in service.UpdateMeInput) (service.UserProfile, error)
	updateProfilePhotoFn     func(ctx context.Context, in service.UpdateProfilePhotoInput) (service.UserProfile, error)
	checkUsernameAvailableFn func(ctx context.Context, in service.UsernameAvailableInput) (service.UsernameAvailableOutput, error)
	requestDeleteFn          func(ctx context.Context, in service.DeleteAccountRequestInput) (service.MessageOutput, error)
	confirmDeleteFn          func(ctx context.Context, in service.DeleteAccountConfirmInput) (service.MessageOutput, error)
}

func (m *mockAuthService) Start(ctx context.Context, in service.StartAuthInput) (service.StartAuthOutput, error) {
	return m.startFn(ctx, in)
}
func (m *mockAuthService) VerifyOTP(ctx context.Context, in service.VerifyOTPInput) (service.AuthOutput, error) {
	return m.verifyOTPFn(ctx, in)
}
func (m *mockAuthService) Refresh(ctx context.Context, in service.RefreshInput) (service.RefreshOutput, error) {
	return m.refreshFn(ctx, in)
}
func (m *mockAuthService) GetMe(ctx context.Context, in service.GetMeInput) (service.UserProfile, error) {
	return m.getMeFn(ctx, in)
}
func (m *mockAuthService) UpdateMe(ctx context.Context, in service.UpdateMeInput) (service.UserProfile, error) {
	return m.updateMeFn(ctx, in)
}
func (m *mockAuthService) UpdateProfilePhoto(ctx context.Context, in service.UpdateProfilePhotoInput) (service.UserProfile, error) {
	if m.updateProfilePhotoFn != nil {
		return m.updateProfilePhotoFn(ctx, in)
	}
	return service.UserProfile{}, nil
}
func (m *mockAuthService) CheckUsernameAvailable(ctx context.Context, in service.UsernameAvailableInput) (service.UsernameAvailableOutput, error) {
	return m.checkUsernameAvailableFn(ctx, in)
}
func (m *mockAuthService) RequestAccountDeletion(ctx context.Context, in service.DeleteAccountRequestInput) (service.MessageOutput, error) {
	if m.requestDeleteFn != nil {
		return m.requestDeleteFn(ctx, in)
	}
	return service.MessageOutput{}, nil
}
func (m *mockAuthService) ConfirmAccountDeletion(ctx context.Context, in service.DeleteAccountConfirmInput) (service.MessageOutput, error) {
	if m.confirmDeleteFn != nil {
		return m.confirmDeleteFn(ctx, in)
	}
	return service.MessageOutput{}, nil
}

func TestStart_Success(t *testing.T) {
	t.Parallel()

	h := NewAuthHandler(&mockAuthService{
		startFn: func(_ context.Context, in service.StartAuthInput) (service.StartAuthOutput, error) {
			if in.Email != "user@example.com" {
				t.Fatalf("email = %q", in.Email)
			}
			return service.StartAuthOutput{OTPRequired: true}, nil
		},
	}, noopLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/start", strings.NewReader(`{"email":"user@example.com"}`))
	rec := httptest.NewRecorder()
	h.Start(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body startResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.OTPRequired {
		t.Fatal("expected OTPRequired = true")
	}
}

func TestVerifyOTP_Success(t *testing.T) {
	t.Parallel()

	periodEnd := time.Date(2026, time.March, 10, 10, 0, 0, 0, time.UTC)
	createdAt := time.Date(2026, time.March, 9, 9, 30, 0, 0, time.UTC)
	profilePictureURL := "https://cdn.example.com/user.png"

	h := NewAuthHandler(&mockAuthService{
		verifyOTPFn: func(_ context.Context, in service.VerifyOTPInput) (service.AuthOutput, error) {
			if in.Email != "user@example.com" {
				t.Fatalf("email = %q", in.Email)
			}
			if in.OTP != "123456" {
				t.Fatalf("otp = %q", in.OTP)
			}
			return service.AuthOutput{
				AccessToken:  "access-token",
				RefreshToken: "refresh-token",
				User: service.UserProfile{
					ID:                 "user-id",
					Email:              "user@example.com",
					Name:               "Test User",
					Username:           "test_user",
					ProfilePictureURL:  &profilePictureURL,
					LeaderboardVisible: true,
					CreatedAt:          createdAt,
					Subscription: &service.EntitlementInfo{
						Entitlement:      "premium",
						IsActive:         true,
						CurrentPeriodEnd: &periodEnd,
					},
				},
			}, nil
		},
	}, noopLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify", strings.NewReader(`{"email":"user@example.com","otp":"123456"}`))
	rec := httptest.NewRecorder()

	h.VerifyOTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body authResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.AccessToken != "access-token" {
		t.Fatalf("access_token = %q", body.AccessToken)
	}
	if body.RefreshToken != "refresh-token" {
		t.Fatalf("refresh_token = %q", body.RefreshToken)
	}
	if body.User.ID != "user-id" || body.User.Email != "user@example.com" {
		t.Fatalf("user = %+v", body.User)
	}
	if body.User.CreatedAt != createdAt.Format(time.RFC3339) {
		t.Fatalf("created_at = %q, want %q", body.User.CreatedAt, createdAt.Format(time.RFC3339))
	}
	if body.User.Subscription == nil {
		t.Fatal("expected subscription to be present")
	}
	if body.User.Subscription.CurrentPeriodEnd == nil || *body.User.Subscription.CurrentPeriodEnd != periodEnd.Format(time.RFC3339) {
		t.Fatalf("current_period_end = %v, want %q", body.User.Subscription.CurrentPeriodEnd, periodEnd.Format(time.RFC3339))
	}
}

func TestVerifyOTP_ServiceUnauthorized(t *testing.T) {
	t.Parallel()

	h := NewAuthHandler(&mockAuthService{
		verifyOTPFn: func(_ context.Context, _ service.VerifyOTPInput) (service.AuthOutput, error) {
			return service.AuthOutput{}, fmt.Errorf("%w: invalid or expired OTP", service.ErrUnauthorized)
		},
	}, noopLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify", strings.NewReader(`{"email":"user@example.com","otp":"123456"}`))
	rec := httptest.NewRecorder()

	h.VerifyOTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeUnauthorized {
		t.Fatalf("error.code = %q, want %q", resp.Error.Code, apperror.CodeUnauthorized)
	}
	if resp.Error.Message != "Invalid or expired OTP" {
		t.Fatalf("error.message = %q", resp.Error.Message)
	}
}

func TestRefresh_Success(t *testing.T) {
	t.Parallel()

	h := NewAuthHandler(&mockAuthService{
		refreshFn: func(_ context.Context, in service.RefreshInput) (service.RefreshOutput, error) {
			if in.RefreshToken != "opaque-token" {
				t.Fatalf("refresh_token = %q", in.RefreshToken)
			}
			return service.RefreshOutput{
				AccessToken:  "rotated-access",
				RefreshToken: "rotated-refresh",
			}, nil
		},
	}, noopLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{"refresh_token":"opaque-token"}`))
	rec := httptest.NewRecorder()

	h.Refresh(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body refreshResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.AccessToken != "rotated-access" || body.RefreshToken != "rotated-refresh" {
		t.Fatalf("body = %+v", body)
	}
}
