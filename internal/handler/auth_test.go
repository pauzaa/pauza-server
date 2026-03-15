package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/middleware"
	"github.com/IsorilovA/pauza-server/internal/service"
)

type mockEmailSender struct{}

func (m *mockEmailSender) Probe(_ context.Context) error { return nil }
func (m *mockEmailSender) SendOTP(_ context.Context, _, _ string, _ mail.Purpose) error {
	return nil
}

func newTestAuthHandler() *AuthHandler {
	svc := service.NewAuthService(nil, nil, nil, nil, nil, nil, &mockEmailSender{}, "test-secret-abcdefghijklmnopqrstuvwxyz", time.Minute, time.Hour, noopLogger())
	return NewAuthHandler(svc, nil, noopLogger())
}

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type uploadPhotoResponse struct {
	ProfilePictureURL string `json:"profile_picture_url"`
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
	startFn                   func(ctx context.Context, in service.StartAuthInput) (service.StartAuthOutput, error)
	verifyOTPFn               func(ctx context.Context, in service.VerifyOTPInput) (service.AuthOutput, error)
	refreshFn                 func(ctx context.Context, in service.RefreshInput) (service.RefreshOutput, error)
	logoutFn                  func(ctx context.Context, in service.LogoutInput) error
	getMeFn                   func(ctx context.Context, in service.GetMeInput) (service.UserProfile, error)
	updateMeFn                func(ctx context.Context, in service.UpdateMeInput) (service.UserProfile, error)
	updateProfilePhotoFn      func(ctx context.Context, in service.UpdateProfilePhotoInput) (service.UserProfile, error)
	getNotificationPrefsFn    func(ctx context.Context, in service.GetNotificationPreferencesInput) (service.NotificationPreferences, error)
	updateNotificationPrefsFn func(ctx context.Context, in service.UpdateNotificationPreferencesInput) (service.NotificationPreferences, error)
	getPrivacyPrefsFn         func(ctx context.Context, in service.GetPrivacyPreferencesInput) (service.PrivacyPreferences, error)
	updatePrivacyPrefsFn      func(ctx context.Context, in service.UpdatePrivacyPreferencesInput) (service.PrivacyPreferences, error)
	checkUsernameAvailableFn  func(ctx context.Context, in service.UsernameAvailableInput) (service.UsernameAvailableOutput, error)
	requestDeleteFn           func(ctx context.Context, in service.DeleteAccountRequestInput) (service.MessageOutput, error)
	confirmDeleteFn           func(ctx context.Context, in service.DeleteAccountConfirmInput) (service.MessageOutput, error)
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
func (m *mockAuthService) Logout(ctx context.Context, in service.LogoutInput) error {
	if m.logoutFn != nil {
		return m.logoutFn(ctx, in)
	}
	return nil
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
func (m *mockAuthService) GetNotificationPreferences(ctx context.Context, in service.GetNotificationPreferencesInput) (service.NotificationPreferences, error) {
	return m.getNotificationPrefsFn(ctx, in)
}
func (m *mockAuthService) UpdateNotificationPreferences(ctx context.Context, in service.UpdateNotificationPreferencesInput) (service.NotificationPreferences, error) {
	return m.updateNotificationPrefsFn(ctx, in)
}
func (m *mockAuthService) GetPrivacyPreferences(ctx context.Context, in service.GetPrivacyPreferencesInput) (service.PrivacyPreferences, error) {
	return m.getPrivacyPrefsFn(ctx, in)
}
func (m *mockAuthService) UpdatePrivacyPreferences(ctx context.Context, in service.UpdatePrivacyPreferencesInput) (service.PrivacyPreferences, error) {
	return m.updatePrivacyPrefsFn(ctx, in)
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

type fakePhotoStore struct {
	saveFn func(ctx context.Context, file multipart.File, extension string) (string, error)
}

func (f *fakePhotoStore) Save(ctx context.Context, file multipart.File, extension string) (string, error) {
	return f.saveFn(ctx, file, extension)
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
	}, nil, noopLogger())

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
					PushEnabled:        true,
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
	}, nil, noopLogger())

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
			return service.AuthOutput{}, service.UnauthorizedError("Invalid or expired OTP")
		},
	}, nil, noopLogger())

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
	}, nil, noopLogger())

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

func TestGetNotificationPreferences_Success(t *testing.T) {
	t.Parallel()

	h := NewAuthHandler(&mockAuthService{
		getNotificationPrefsFn: func(_ context.Context, in service.GetNotificationPreferencesInput) (service.NotificationPreferences, error) {
			if in.UserID != "user-123" {
				t.Fatalf("user_id = %q", in.UserID)
			}
			return service.NotificationPreferences{PushEnabled: false}, nil
		},
	}, nil, noopLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/notification-preferences", nil)
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	h.GetNotificationPreferences(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body notificationPreferencesResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.PushEnabled {
		t.Fatal("push_enabled = true, want false")
	}
}

func TestUpdateNotificationPreferences_Success(t *testing.T) {
	t.Parallel()

	h := NewAuthHandler(&mockAuthService{
		updateNotificationPrefsFn: func(_ context.Context, in service.UpdateNotificationPreferencesInput) (service.NotificationPreferences, error) {
			if in.UserID != "user-123" {
				t.Fatalf("user_id = %q", in.UserID)
			}
			if in.PushEnabled == nil || *in.PushEnabled {
				t.Fatalf("push_enabled = %v, want false", in.PushEnabled)
			}
			return service.NotificationPreferences{PushEnabled: false}, nil
		},
	}, nil, noopLogger())

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/me/notification-preferences", strings.NewReader(`{"push_enabled":false}`))
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	h.UpdateNotificationPreferences(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body notificationPreferencesResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.PushEnabled {
		t.Fatal("push_enabled = true, want false")
	}
}

func TestGetPrivacyPreferences_Success(t *testing.T) {
	t.Parallel()

	h := NewAuthHandler(&mockAuthService{
		getPrivacyPrefsFn: func(_ context.Context, in service.GetPrivacyPreferencesInput) (service.PrivacyPreferences, error) {
			if in.UserID != "user-123" {
				t.Fatalf("user_id = %q", in.UserID)
			}
			return service.PrivacyPreferences{LeaderboardVisible: false}, nil
		},
	}, nil, noopLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/privacy", nil)
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	h.GetPrivacyPreferences(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body privacyPreferencesResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.LeaderboardVisible {
		t.Fatal("leaderboard_visible = true, want false")
	}
}

func TestUpdatePrivacyPreferences_Success(t *testing.T) {
	t.Parallel()

	h := NewAuthHandler(&mockAuthService{
		updatePrivacyPrefsFn: func(_ context.Context, in service.UpdatePrivacyPreferencesInput) (service.PrivacyPreferences, error) {
			if in.UserID != "user-123" {
				t.Fatalf("user_id = %q", in.UserID)
			}
			if in.LeaderboardVisible == nil || *in.LeaderboardVisible {
				t.Fatalf("leaderboard_visible = %v, want false", in.LeaderboardVisible)
			}
			return service.PrivacyPreferences{LeaderboardVisible: false}, nil
		},
	}, nil, noopLogger())

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/me/privacy", strings.NewReader(`{"leaderboard_visible":false}`))
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	h.UpdatePrivacyPreferences(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body privacyPreferencesResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.LeaderboardVisible {
		t.Fatal("leaderboard_visible = true, want false")
	}
}

func TestUpdateMe_RejectsRemovedPreferenceFields(t *testing.T) {
	t.Parallel()

	h := NewAuthHandler(&mockAuthService{
		updateMeFn: func(context.Context, service.UpdateMeInput) (service.UserProfile, error) {
			t.Fatal("updateMe should not be called")
			return service.UserProfile{}, nil
		},
	}, nil, noopLogger())

	for _, body := range []string{`{"leaderboard_visible":false}`, `{"push_enabled":false}`} {
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/me", strings.NewReader(body))
		req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-123"}))
		rec := httptest.NewRecorder()

		h.UpdateMe(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422 for body %s", rec.Code, body)
		}
	}
}

func TestUploadPhoto_Success(t *testing.T) {
	t.Parallel()

	var gotUserID string
	var gotURL string
	h := NewAuthHandler(&mockAuthService{
		updateProfilePhotoFn: func(_ context.Context, in service.UpdateProfilePhotoInput) (service.UserProfile, error) {
			gotUserID = in.UserID
			gotURL = in.ProfilePictureURL
			return service.UserProfile{ProfilePictureURL: &in.ProfilePictureURL}, nil
		},
	}, &fakePhotoStore{
		saveFn: func(_ context.Context, _ multipart.File, extension string) (string, error) {
			if extension != ".png" {
				t.Fatalf("extension = %q, want %q", extension, ".png")
			}
			return "https://api.test/photos/uploaded.png", nil
		},
	}, noopLogger())

	req := newUploadPhotoRequest(t, "avatar.png", "image/png", []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	})
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	h.UploadPhoto(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotUserID != "user-123" {
		t.Fatalf("user_id = %q, want %q", gotUserID, "user-123")
	}
	if gotURL != "https://api.test/photos/uploaded.png" {
		t.Fatalf("profile_picture_url = %q", gotURL)
	}

	var body uploadPhotoResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ProfilePictureURL != gotURL {
		t.Fatalf("response profile_picture_url = %q, want %q", body.ProfilePictureURL, gotURL)
	}
}

func TestUploadPhoto_ValidationMissingPhoto(t *testing.T) {
	t.Parallel()

	h := NewAuthHandler(&mockAuthService{}, &fakePhotoStore{
		saveFn: func(context.Context, multipart.File, string) (string, error) {
			t.Fatal("photo store should not be called")
			return "", nil
		},
	}, noopLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/me/photo", strings.NewReader(""))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	h.UploadPhoto(rec, req)

	assertValidationEnvelope(t, rec, []string{"photo"})
}

func TestUploadPhoto_InternalWhenStoreIsMissing(t *testing.T) {
	t.Parallel()

	h := NewAuthHandler(&mockAuthService{}, nil, noopLogger())
	req := newUploadPhotoRequest(t, "avatar.jpg", "image/jpeg", []byte{
		0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00, 0x08, 0x06, 0x06, 0x07, 0x06, 0x05, 0x08, 0x07, 0x07,
	})
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-123"}))
	rec := httptest.NewRecorder()

	h.UploadPhoto(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func newUploadPhotoRequest(t *testing.T, filename, contentType string, payload []byte) *http.Request {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreatePart(uploadPhotoHeader(filename, contentType))
	if err != nil {
		t.Fatalf("create multipart part: %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("write multipart payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/me/photo", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func uploadPhotoHeader(filename, contentType string) textproto.MIMEHeader {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="photo"; filename="`+filename+`"`)
	header.Set("Content-Type", contentType)
	return header
}
