package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

func newTestService(repo *fakeAuthRepo, sender *fakeSender) *AuthService {
	return NewAuthService(
		&fakePool{},
		repo,
		sender,
		"test-jwt-secret-abcdefghijklmnopqrstuvwxyz",
		15*time.Minute,
		7*24*time.Hour,
		slog.New(slog.NewTextHandler(devNull{}, &slog.HandlerOptions{Level: slog.LevelError})),
	)
}

func newTestServiceWithPool(pool *fakePool, repo *fakeAuthRepo, sender *fakeSender) *AuthService {
	return NewAuthService(
		pool,
		repo,
		sender,
		"test-jwt-secret-abcdefghijklmnopqrstuvwxyz",
		15*time.Minute,
		7*24*time.Hour,
		slog.New(slog.NewTextHandler(devNull{}, &slog.HandlerOptions{Level: slog.LevelError})),
	)
}

type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }

func pgUniqueViolation(constraint string) *pgconn.PgError {
	return &pgconn.PgError{
		Code:           pgerrcode.UniqueViolation,
		ConstraintName: constraint,
	}
}

func verifiedUser() repository.UserRow {
	return repository.UserRow{
		ID:                 "user-001",
		Email:              "alice@example.com",
		Name:               "Alice",
		Username:           "user_abc123",
		LeaderboardVisible: true,
		CreatedAt:          time.Now().UTC().Add(-24 * time.Hour),
	}
}

var errBoom = errors.New("boom")

func TestStart_ReplacesExistingOTPAndSendsEmail(t *testing.T) {
	t.Parallel()

	var deletedEmail, deletedPurpose string
	var insertedEmail, insertedPurpose string

	repo := &fakeAuthRepo{
		deleteOTPsByEmailAndPurposeFn: func(_ context.Context, _ repository.DBTX, email, purpose string) error {
			deletedEmail = email
			deletedPurpose = purpose
			return nil
		},
		insertOTPFn: func(_ context.Context, _ repository.DBTX, email string, userID *string, _ string, purpose string, _ time.Time) (string, error) {
			if userID != nil {
				t.Fatal("expected auth login OTP to be inserted without user_id")
			}
			insertedEmail = email
			insertedPurpose = purpose
			return "otp-id", nil
		},
	}
	sender := &fakeSender{}
	svc := newTestService(repo, sender)

	out, err := svc.Start(context.Background(), StartAuthInput{Email: "Alice@Example.com"})
	if err != nil {
		t.Fatalf("Start() unexpected error: %v", err)
	}
	if !out.OTPRequired {
		t.Fatal("expected OTPRequired = true")
	}
	if deletedEmail != "alice@example.com" || insertedEmail != "alice@example.com" {
		t.Fatalf("expected normalized email, got delete=%q insert=%q", deletedEmail, insertedEmail)
	}
	if deletedPurpose != mail.PurposeAuthLogin || insertedPurpose != mail.PurposeAuthLogin {
		t.Fatalf("unexpected OTP purpose delete=%q insert=%q", deletedPurpose, insertedPurpose)
	}
	calls := sender.sendOTPCalls()
	if len(calls) != 1 {
		t.Fatalf("SendOTP calls = %d, want 1", len(calls))
	}
	if calls[0].Purpose != mail.PurposeAuthLogin {
		t.Fatalf("SendOTP purpose = %q, want %q", calls[0].Purpose, mail.PurposeAuthLogin)
	}
}

func TestStart_SendFailure_CleansUpOTP(t *testing.T) {
	t.Parallel()

	var deletedOTP atomic.Bool
	repo := &fakeAuthRepo{
		deleteOTPsByEmailAndPurposeFn: func(context.Context, repository.DBTX, string, string) error { return nil },
		insertOTPFn: func(context.Context, repository.DBTX, string, *string, string, string, time.Time) (string, error) {
			return "otp-id", nil
		},
		deleteOTPByIDFn: func(_ context.Context, _ repository.DBTX, otpID string) error {
			if otpID == "otp-id" {
				deletedOTP.Store(true)
			}
			return nil
		},
	}
	sender := &fakeSender{
		sendOTPFn: func(context.Context, string, string, string) error { return errBoom },
	}
	svc := newTestService(repo, sender)

	_, err := svc.Start(context.Background(), StartAuthInput{Email: "alice@example.com"})
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("Start() error = %v, want ErrInternal", err)
	}
	if !deletedOTP.Load() {
		t.Fatal("expected OTP cleanup after email send failure")
	}
}

func TestVerifyOTP_CreatesUserAndReturnsTokens(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{ID: "otp-id", CodeHash: mustHashOTP("123456")}, nil
		},
		countFailedOTPAttemptsSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (int, error) {
			return 0, nil
		},
		markOTPUsedFn: func(context.Context, repository.DBTX, string) error { return nil },
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{}, repository.ErrNotFound
		},
		insertUserFn: func(context.Context, repository.DBTX, string, string) (string, error) {
			return "new-user-id", nil
		},
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			return repository.UserRow{
				ID:                 userID,
				Email:              "alice@example.com",
				Username:           "user_new123",
				LeaderboardVisible: true,
				CreatedAt:          time.Now().UTC(),
			}, nil
		},
		insertRefreshTokenFn: func(context.Context, repository.DBTX, string, string, time.Time) error {
			return nil
		},
		getEntitlementSnapshotFn: func(context.Context, repository.DBTX, string) (repository.EntitlementRow, error) {
			return repository.EntitlementRow{}, repository.ErrNotFound
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.VerifyOTP(context.Background(), VerifyOTPInput{
		Email: "alice@example.com",
		OTP:   "123456",
	})
	if err != nil {
		t.Fatalf("VerifyOTP() unexpected error: %v", err)
	}
	if out.AccessToken == "" || out.RefreshToken == "" {
		t.Fatal("expected non-empty tokens")
	}
	if out.User.ID != "new-user-id" {
		t.Fatalf("user id = %q, want new-user-id", out.User.ID)
	}
}

func TestVerifyOTP_WrongCode_RecordsAttempt(t *testing.T) {
	t.Parallel()

	var recordedEmail, recordedPurpose string
	repo := &fakeAuthRepo{
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{ID: "otp-id", CodeHash: mustHashOTP("123456")}, nil
		},
		countFailedOTPAttemptsSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (int, error) {
			return 0, nil
		},
		insertFailedOTPAttemptFn: func(_ context.Context, _ repository.DBTX, email string, _ *string, purpose string, _ time.Time) error {
			recordedEmail = email
			recordedPurpose = purpose
			return nil
		},
		getOldestFailedOTPAttemptSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (time.Time, error) {
			return time.Now().UTC(), nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.VerifyOTP(context.Background(), VerifyOTPInput{
		Email: "alice@example.com",
		OTP:   "654321",
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("VerifyOTP() error = %v, want ErrUnauthorized", err)
	}
	if recordedEmail != "alice@example.com" || recordedPurpose != mail.PurposeAuthLogin {
		t.Fatalf("recorded attempt = (%q, %q)", recordedEmail, recordedPurpose)
	}
}

func TestVerifyOTP_RateLimited(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{ID: "otp-id", CodeHash: mustHashOTP("123456")}, nil
		},
		countFailedOTPAttemptsSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (int, error) {
			return auth.MaxOTPAttempts, nil
		},
		getOldestFailedOTPAttemptSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (time.Time, error) {
			return time.Now().UTC().Add(-2 * time.Minute), nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.VerifyOTP(context.Background(), VerifyOTPInput{
		Email: "alice@example.com",
		OTP:   "123456",
	})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("VerifyOTP() error = %v, want ErrRateLimited", err)
	}
	if retryAfter, ok := RetryAfter(err); !ok || retryAfter <= 0 {
		t.Fatalf("RetryAfter(err) = (%v, %v), want positive duration", retryAfter, ok)
	}
}

func TestVerifyOTP_RetriesUsernameCollision(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	repo := &fakeAuthRepo{
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{ID: "otp-id", CodeHash: mustHashOTP("123456")}, nil
		},
		countFailedOTPAttemptsSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (int, error) {
			return 0, nil
		},
		markOTPUsedFn: func(context.Context, repository.DBTX, string) error { return nil },
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{}, repository.ErrNotFound
		},
		insertUserFn: func(context.Context, repository.DBTX, string, string) (string, error) {
			if attempts.Add(1) <= 2 {
				return "", pgUniqueViolation("users_username_key")
			}
			return "new-user-id", nil
		},
		getUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return verifiedUser(), nil
		},
		insertRefreshTokenFn: func(context.Context, repository.DBTX, string, string, time.Time) error { return nil },
		getEntitlementSnapshotFn: func(context.Context, repository.DBTX, string) (repository.EntitlementRow, error) {
			return repository.EntitlementRow{}, repository.ErrNotFound
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.VerifyOTP(context.Background(), VerifyOTPInput{
		Email: "alice@example.com",
		OTP:   "123456",
	})
	if err != nil {
		t.Fatalf("VerifyOTP() unexpected error: %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("InsertUser attempts = %d, want 3", got)
	}
}

func TestRefresh_Success_RotatesToken(t *testing.T) {
	t.Parallel()

	var revokedTokenID string
	repo := &fakeAuthRepo{
		getRefreshTokenByHashForUpdateFn: func(context.Context, repository.DBTX, string) (repository.RefreshTokenRow, error) {
			return repository.RefreshTokenRow{
				ID:        "tok-1",
				UserID:    "user-001",
				ExpiresAt: time.Now().UTC().Add(time.Hour),
			}, nil
		},
		revokeRefreshTokenFn: func(_ context.Context, _ repository.DBTX, tokenID string) error {
			revokedTokenID = tokenID
			return nil
		},
		getUserEmailByIDFn: func(context.Context, repository.DBTX, string) (string, error) {
			return "alice@example.com", nil
		},
		insertRefreshTokenFn: func(context.Context, repository.DBTX, string, string, time.Time) error { return nil },
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.Refresh(context.Background(), RefreshInput{RefreshToken: "valid"})
	if err != nil {
		t.Fatalf("Refresh() unexpected error: %v", err)
	}
	if out.AccessToken == "" || out.RefreshToken == "" {
		t.Fatal("expected non-empty tokens")
	}
	if revokedTokenID != "tok-1" {
		t.Fatalf("revoked token id = %q, want tok-1", revokedTokenID)
	}
}

func TestRefresh_RevokedToken_RevokesAll(t *testing.T) {
	t.Parallel()

	var revokeAllCalled atomic.Bool
	repo := &fakeAuthRepo{
		getRefreshTokenByHashForUpdateFn: func(context.Context, repository.DBTX, string) (repository.RefreshTokenRow, error) {
			return repository.RefreshTokenRow{
				ID:        "tok-1",
				UserID:    "user-001",
				Revoked:   true,
				ExpiresAt: time.Now().UTC().Add(time.Hour),
			}, nil
		},
		revokeAllRefreshTokensFn: func(context.Context, repository.DBTX, string) error {
			revokeAllCalled.Store(true)
			return nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.Refresh(context.Background(), RefreshInput{RefreshToken: "reused"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Refresh() error = %v, want ErrUnauthorized", err)
	}
	if !revokeAllCalled.Load() {
		t.Fatal("expected RevokeAllRefreshTokens to be called")
	}
}

func TestGetNotificationPreferences_Success(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getPushEnabledFn: func(_ context.Context, _ repository.DBTX, userID string) (bool, error) {
			if userID != "user-001" {
				t.Fatalf("userID = %q, want user-001", userID)
			}
			return false, nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.GetNotificationPreferences(context.Background(), GetNotificationPreferencesInput{UserID: "user-001"})
	if err != nil {
		t.Fatalf("GetNotificationPreferences() unexpected error: %v", err)
	}
	if out.PushEnabled {
		t.Fatal("PushEnabled = true, want false")
	}
}

func TestUpdateNotificationPreferences_NoPatchReturnsCurrentState(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getPushEnabledFn: func(context.Context, repository.DBTX, string) (bool, error) {
			return true, nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.UpdateNotificationPreferences(context.Background(), UpdateNotificationPreferencesInput{UserID: "user-001"})
	if err != nil {
		t.Fatalf("UpdateNotificationPreferences() unexpected error: %v", err)
	}
	if !out.PushEnabled {
		t.Fatal("PushEnabled = false, want true")
	}
}

func TestUpdateNotificationPreferences_Success(t *testing.T) {
	t.Parallel()

	var got bool
	repo := &fakeAuthRepo{
		updatePushEnabledFn: func(_ context.Context, _ repository.DBTX, userID string, pushEnabled bool) (bool, error) {
			if userID != "user-001" {
				t.Fatalf("userID = %q, want user-001", userID)
			}
			got = pushEnabled
			return pushEnabled, nil
		},
	}
	svc := newTestService(repo, &fakeSender{})
	disabled := false

	out, err := svc.UpdateNotificationPreferences(context.Background(), UpdateNotificationPreferencesInput{
		UserID:      "user-001",
		PushEnabled: &disabled,
	})
	if err != nil {
		t.Fatalf("UpdateNotificationPreferences() unexpected error: %v", err)
	}
	if got {
		t.Fatal("repository received true, want false")
	}
	if out.PushEnabled {
		t.Fatal("PushEnabled = true, want false")
	}
}

func TestGetPrivacyPreferences_Success(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			if userID != "user-001" {
				t.Fatalf("userID = %q, want user-001", userID)
			}
			return repository.UserRow{ID: userID, LeaderboardVisible: false}, nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.GetPrivacyPreferences(context.Background(), GetPrivacyPreferencesInput{UserID: "user-001"})
	if err != nil {
		t.Fatalf("GetPrivacyPreferences() unexpected error: %v", err)
	}
	if out.LeaderboardVisible {
		t.Fatal("LeaderboardVisible = true, want false")
	}
}

func TestUpdatePrivacyPreferences_NoPatchReturnsCurrentState(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{LeaderboardVisible: true}, nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.UpdatePrivacyPreferences(context.Background(), UpdatePrivacyPreferencesInput{UserID: "user-001"})
	if err != nil {
		t.Fatalf("UpdatePrivacyPreferences() unexpected error: %v", err)
	}
	if !out.LeaderboardVisible {
		t.Fatal("LeaderboardVisible = false, want true")
	}
}

func TestUpdatePrivacyPreferences_Success(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		updateUserFn: func(_ context.Context, _ repository.DBTX, userID string, name *string, username *string, leaderboardVisible *bool, profilePictureURL *string) (repository.UserRow, error) {
			if userID != "user-001" {
				t.Fatalf("userID = %q, want user-001", userID)
			}
			if name != nil || username != nil || profilePictureURL != nil {
				t.Fatal("unexpected profile fields passed to UpdateUser")
			}
			if leaderboardVisible == nil || *leaderboardVisible {
				t.Fatalf("leaderboardVisible = %v, want false", leaderboardVisible)
			}
			return repository.UserRow{LeaderboardVisible: *leaderboardVisible}, nil
		},
	}
	svc := newTestService(repo, &fakeSender{})
	hidden := false

	out, err := svc.UpdatePrivacyPreferences(context.Background(), UpdatePrivacyPreferencesInput{
		UserID:             "user-001",
		LeaderboardVisible: &hidden,
	})
	if err != nil {
		t.Fatalf("UpdatePrivacyPreferences() unexpected error: %v", err)
	}
	if out.LeaderboardVisible {
		t.Fatal("LeaderboardVisible = true, want false")
	}
}

func mustHashOTP(code string) string {
	h, err := auth.HashOTP(code)
	if err != nil {
		panic(fmt.Sprintf("mustHashOTP: %v", err))
	}
	return h
}
