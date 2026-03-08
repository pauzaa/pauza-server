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
	"github.com/IsorilovA/pauza-server/internal/repository"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// newTestService constructs an AuthService wired to the given fakes. It uses
// a discard logger and deterministic JWT settings suitable for unit tests.
func newTestService(repo *fakeAuthRepo, mailer *fakeSender) *AuthService {
	return NewAuthService(
		&fakePool{},
		repo,
		mailer,
		"test-jwt-secret",
		15*time.Minute,
		7*24*time.Hour,
		slog.New(slog.NewTextHandler(
			devNull{},
			&slog.HandlerOptions{Level: slog.LevelError},
		)),
	)
}

// devNull implements io.Writer and discards all output.
type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }

// pgUniqueViolation creates a pgconn.PgError with unique_violation code and
// the given constraint name, mimicking a Postgres unique-violation error.
func pgUniqueViolation(constraint string) *pgconn.PgError {
	return &pgconn.PgError{
		Code:           pgerrcode.UniqueViolation,
		ConstraintName: constraint,
	}
}

// verifiedUser returns a repository.UserRow representing a verified user.
func verifiedUser() repository.UserRow {
	return repository.UserRow{
		ID:                 "user-001",
		Email:              "alice@example.com",
		PasswordHash:       mustHash("correct-password"),
		Name:               "Alice",
		Username:           "user_abc123",
		LeaderboardVisible: true,
		EmailVerified:      true,
		CreatedAt:          time.Now().UTC().Add(-24 * time.Hour),
	}
}

// mustHash returns a bcrypt hash of s or panics. Only for test fixtures.
func mustHash(s string) string {
	h, err := auth.HashPassword(s)
	if err != nil {
		panic(fmt.Sprintf("mustHash: %v", err))
	}
	return h
}

// mustHashOTP returns a bcrypt hash of an OTP code or panics.
func mustHashOTP(code string) string {
	h, err := auth.HashOTP(code)
	if err != nil {
		panic(fmt.Sprintf("mustHashOTP: %v", err))
	}
	return h
}

// errBoom is a generic internal error used to simulate failures in fakes.
var errBoom = errors.New("boom")

// ---------------------------------------------------------------------------
// Register tests
// ---------------------------------------------------------------------------

// TestRegister_ConflictOnVerifiedEmail verifies that registering with an
// email that already belongs to a verified user returns ErrConflict.
func TestRegister_ConflictOnVerifiedEmail(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getUserByEmailForUpdateFn: func(_ context.Context, _ repository.DBTX, email string) (repository.UserRow, error) {
			return repository.UserRow{
				ID:            "existing-user",
				Email:         email,
				EmailVerified: true,
			}, nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.Register(context.Background(), RegisterInput{
		Email:    "alice@example.com",
		Password: "StrongP@ss1",
	})

	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Register() error = %v, want ErrConflict", err)
	}
}

// TestRegister_ConflictOnEmailUniqueIndex verifies that a Postgres
// unique-violation on the email index during InsertUser returns ErrConflict.
func TestRegister_ConflictOnEmailUniqueIndex(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{}, repository.ErrNotFound
		},
		insertUserFn: func(context.Context, repository.DBTX, string, string, string) (string, error) {
			return "", pgUniqueViolation("idx_users_email")
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.Register(context.Background(), RegisterInput{
		Email:    "alice@example.com",
		Password: "StrongP@ss1",
	})

	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Register() error = %v, want ErrConflict", err)
	}
}

// TestRegister_SMTPFailure_CleansUpAndReturnsInternal verifies that when the
// mailer fails after committing the user + OTP rows, the service attempts
// cleanup and returns ErrInternal.
func TestRegister_SMTPFailure_CleansUpAndReturnsInternal(t *testing.T) {
	t.Parallel()

	var (
		deletedOTP  atomic.Bool
		deletedUser atomic.Bool
	)

	repo := &fakeAuthRepo{
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{}, repository.ErrNotFound
		},
		insertUserFn: func(context.Context, repository.DBTX, string, string, string) (string, error) {
			return "new-user-id", nil
		},
		insertOTPFn: func(context.Context, repository.DBTX, string, string, string, time.Time) (string, error) {
			return "otp-id", nil
		},
		deleteOTPByIDFn: func(_ context.Context, _ repository.DBTX, otpID string) error {
			if otpID == "otp-id" {
				deletedOTP.Store(true)
			}
			return nil
		},
		deleteUnverifiedUserFn: func(_ context.Context, _ repository.DBTX, userID string) (int64, error) {
			if userID == "new-user-id" {
				deletedUser.Store(true)
			}
			return 1, nil
		},
	}

	mailer := &fakeSender{
		sendOTPFn: func(context.Context, string, string, string) error {
			return errors.New("SMTP connection refused")
		},
	}

	svc := newTestService(repo, mailer)

	_, err := svc.Register(context.Background(), RegisterInput{
		Email:    "bob@example.com",
		Password: "StrongP@ss1",
	})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("Register() error = %v, want ErrInternal", err)
	}

	if !deletedOTP.Load() {
		t.Error("expected OTP row to be cleaned up after SMTP failure")
	}
	if !deletedUser.Load() {
		t.Error("expected user row to be cleaned up after SMTP failure")
	}
}

// TestRegister_StaleUnverifiedUser_Replaced verifies that re-registering with
// an email that has an existing unverified user removes the stale rows and
// proceeds with a new registration.
func TestRegister_StaleUnverifiedUser_Replaced(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{
				ID:            "stale-user",
				Email:         "alice@example.com",
				EmailVerified: false,
			}, nil
		},
		deleteOTPsByUserAndPurposeFn: func(context.Context, repository.DBTX, string, string) error {
			return nil
		},
		deleteUnverifiedUserFn: func(context.Context, repository.DBTX, string) (int64, error) {
			return 1, nil
		},
		insertUserFn: func(context.Context, repository.DBTX, string, string, string) (string, error) {
			return "new-user-id", nil
		},
		insertOTPFn: func(context.Context, repository.DBTX, string, string, string, time.Time) (string, error) {
			return "otp-id", nil
		},
	}

	mailer := &fakeSender{}
	svc := newTestService(repo, mailer)

	out, err := svc.Register(context.Background(), RegisterInput{
		Email:    "alice@example.com",
		Password: "StrongP@ss1",
	})

	if err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}
	if !out.OTPRequired {
		t.Error("expected OTPRequired = true")
	}
	if calls := mailer.sendOTPCalls(); len(calls) != 1 {
		t.Errorf("expected 1 SendOTP call, got %d", len(calls))
	}
}

// TestRegister_UsernameCollision_Retries verifies that the service retries
// user insertion when a username unique-violation occurs.
func TestRegister_UsernameCollision_Retries(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	repo := &fakeAuthRepo{
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{}, repository.ErrNotFound
		},
		insertUserFn: func(context.Context, repository.DBTX, string, string, string) (string, error) {
			if attempts.Add(1) <= 2 {
				return "", pgUniqueViolation("users_username_key")
			}
			return "user-id", nil
		},
		insertOTPFn: func(context.Context, repository.DBTX, string, string, string, time.Time) (string, error) {
			return "otp-id", nil
		},
	}

	svc := newTestService(repo, &fakeSender{})

	out, err := svc.Register(context.Background(), RegisterInput{
		Email:    "charlie@example.com",
		Password: "StrongP@ss1",
	})

	if err != nil {
		t.Fatalf("Register() unexpected error: %v", err)
	}
	if !out.OTPRequired {
		t.Error("expected OTPRequired = true")
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("expected 3 InsertUser attempts, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// Login tests
// ---------------------------------------------------------------------------

// TestLogin_UnknownEmail_ReturnsUnauthorized verifies that logging in with an
// unregistered email returns ErrUnauthorized (not ErrNotFound or similar).
func TestLogin_UnknownEmail_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{}, repository.ErrNotFound
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.Login(context.Background(), LoginInput{
		Email:    "nobody@example.com",
		Password: "anything",
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Login() error = %v, want ErrUnauthorized", err)
	}
}

// TestLogin_WrongPassword_ReturnsUnauthorized verifies that providing the
// wrong password for a verified user returns ErrUnauthorized.
func TestLogin_WrongPassword_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.Login(context.Background(), LoginInput{
		Email:    user.Email,
		Password: "wrong-password",
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Login() error = %v, want ErrUnauthorized", err)
	}
}

// TestLogin_Success_ReturnsTokensAndProfile verifies the happy path: correct
// credentials produce non-empty tokens and the expected user profile.
func TestLogin_Success_ReturnsTokensAndProfile(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		insertRefreshTokenFn: func(context.Context, repository.DBTX, string, string, time.Time) error {
			return nil
		},
		getActiveSubscriptionFn: func(context.Context, repository.DBTX, string) (repository.SubscriptionRow, error) {
			return repository.SubscriptionRow{}, repository.ErrNotFound
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.Login(context.Background(), LoginInput{
		Email:    user.Email,
		Password: "correct-password",
	})

	if err != nil {
		t.Fatalf("Login() unexpected error: %v", err)
	}
	if out.AccessToken == "" {
		t.Error("expected non-empty AccessToken")
	}
	if out.RefreshToken == "" {
		t.Error("expected non-empty RefreshToken")
	}
	if out.User.ID != user.ID {
		t.Errorf("User.ID = %q, want %q", out.User.ID, user.ID)
	}
	if out.User.Email != user.Email {
		t.Errorf("User.Email = %q, want %q", out.User.Email, user.Email)
	}
}

// TestLogin_DBError_ReturnsInternal verifies that a repository failure during
// the user lookup returns ErrInternal, not a leaked error.
func TestLogin_DBError_ReturnsInternal(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{}, errBoom
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.Login(context.Background(), LoginInput{
		Email:    "alice@example.com",
		Password: "anything",
	})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("Login() error = %v, want ErrInternal", err)
	}
}

// ---------------------------------------------------------------------------
// Refresh tests
// ---------------------------------------------------------------------------

// TestRefresh_UnknownToken_ReturnsUnauthorized verifies that an unrecognized
// refresh token returns ErrUnauthorized.
func TestRefresh_UnknownToken_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getRefreshTokenByHashForUpdateFn: func(context.Context, repository.DBTX, string) (repository.RefreshTokenRow, error) {
			return repository.RefreshTokenRow{}, repository.ErrNotFound
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.Refresh(context.Background(), RefreshInput{
		RefreshToken: "nonexistent-token",
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Refresh() error = %v, want ErrUnauthorized", err)
	}
}

// TestRefresh_RevokedToken_RevokesAllAndReturnsUnauthorized verifies that
// presenting a revoked refresh token triggers revocation of ALL tokens for
// that user (reuse detection) and returns ErrUnauthorized.
func TestRefresh_RevokedToken_RevokesAllAndReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	var revokedAll atomic.Bool
	repo := &fakeAuthRepo{
		getRefreshTokenByHashForUpdateFn: func(context.Context, repository.DBTX, string) (repository.RefreshTokenRow, error) {
			return repository.RefreshTokenRow{
				ID:        "tok-1",
				UserID:    "user-001",
				Revoked:   true,
				ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
			}, nil
		},
		revokeAllRefreshTokensFn: func(_ context.Context, _ repository.DBTX, userID string) error {
			if userID == "user-001" {
				revokedAll.Store(true)
			}
			return nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.Refresh(context.Background(), RefreshInput{
		RefreshToken: "reused-token",
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Refresh() error = %v, want ErrUnauthorized", err)
	}
	if !revokedAll.Load() {
		t.Error("expected RevokeAllRefreshTokens to be called for reuse detection")
	}
}

// TestRefresh_ExpiredToken_ReturnsUnauthorized verifies that an expired but
// non-revoked token returns ErrUnauthorized.
func TestRefresh_ExpiredToken_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getRefreshTokenByHashForUpdateFn: func(context.Context, repository.DBTX, string) (repository.RefreshTokenRow, error) {
			return repository.RefreshTokenRow{
				ID:        "tok-2",
				UserID:    "user-001",
				Revoked:   false,
				ExpiresAt: time.Now().UTC().Add(-1 * time.Hour), // expired
			}, nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.Refresh(context.Background(), RefreshInput{
		RefreshToken: "expired-token",
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Refresh() error = %v, want ErrUnauthorized", err)
	}
}

// TestRefresh_Success_RotatesToken verifies that a valid refresh produces new
// access and refresh tokens (rotation).
func TestRefresh_Success_RotatesToken(t *testing.T) {
	t.Parallel()

	var revokedTokenID string
	repo := &fakeAuthRepo{
		getRefreshTokenByHashForUpdateFn: func(context.Context, repository.DBTX, string) (repository.RefreshTokenRow, error) {
			return repository.RefreshTokenRow{
				ID:        "tok-3",
				UserID:    "user-001",
				Revoked:   false,
				ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
			}, nil
		},
		revokeRefreshTokenFn: func(_ context.Context, _ repository.DBTX, tokenID string) error {
			revokedTokenID = tokenID
			return nil
		},
		getUserEmailByIDFn: func(context.Context, repository.DBTX, string) (string, error) {
			return "alice@example.com", nil
		},
		insertRefreshTokenFn: func(context.Context, repository.DBTX, string, string, time.Time) error {
			return nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.Refresh(context.Background(), RefreshInput{
		RefreshToken: "valid-token",
	})

	if err != nil {
		t.Fatalf("Refresh() unexpected error: %v", err)
	}
	if out.AccessToken == "" {
		t.Error("expected non-empty AccessToken")
	}
	if out.RefreshToken == "" {
		t.Error("expected non-empty RefreshToken")
	}
	if revokedTokenID != "tok-3" {
		t.Errorf("expected old token tok-3 to be revoked, got %q", revokedTokenID)
	}
}

// ---------------------------------------------------------------------------
// ForgotPassword tests
// ---------------------------------------------------------------------------

// TestForgotPassword_UnknownEmail_ReturnsGenericMessage verifies anti-
// enumeration: an unknown email returns the same generic message as a known
// email, with no error.
func TestForgotPassword_UnknownEmail_ReturnsGenericMessage(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{}, repository.ErrNotFound
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.ForgotPassword(context.Background(), ForgotPasswordInput{
		Email: "ghost@example.com",
	})

	if err != nil {
		t.Fatalf("ForgotPassword() unexpected error: %v", err)
	}
	if out.Message == "" {
		t.Error("expected a non-empty message")
	}
}

// TestForgotPassword_DBError_ReturnsInternal verifies that a repository
// failure during user lookup returns ErrInternal.
func TestForgotPassword_DBError_ReturnsInternal(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{}, errBoom
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.ForgotPassword(context.Background(), ForgotPasswordInput{
		Email: "alice@example.com",
	})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("ForgotPassword() error = %v, want ErrInternal", err)
	}
}

// TestForgotPassword_OTPInsertFailure_ReturnsInternal verifies that a
// repository failure during OTP insertion returns ErrInternal.
func TestForgotPassword_OTPInsertFailure_ReturnsInternal(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		insertOTPFn: func(context.Context, repository.DBTX, string, string, string, time.Time) (string, error) {
			return "", errBoom
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.ForgotPassword(context.Background(), ForgotPasswordInput{
		Email: user.Email,
	})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("ForgotPassword() error = %v, want ErrInternal", err)
	}
}

// TestForgotPassword_SMTPFailure_StillReturnsSuccess verifies that an SMTP
// failure during password-reset email delivery does NOT surface as an error
// to the caller — the operation returns the generic message to avoid leaking
// email-delivery status.
func TestForgotPassword_SMTPFailure_StillReturnsSuccess(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		insertOTPFn: func(context.Context, repository.DBTX, string, string, string, time.Time) (string, error) {
			return "otp-id", nil
		},
	}
	mailer := &fakeSender{
		sendOTPFn: func(context.Context, string, string, string) error {
			return errors.New("SMTP timeout")
		},
	}
	svc := newTestService(repo, mailer)

	out, err := svc.ForgotPassword(context.Background(), ForgotPasswordInput{
		Email: user.Email,
	})

	if err != nil {
		t.Fatalf("ForgotPassword() unexpected error: %v", err)
	}
	if out.Message == "" {
		t.Error("expected a non-empty message")
	}
}

// TestForgotPassword_Success verifies the happy path: known user, OTP stored,
// email sent, generic message returned.
func TestForgotPassword_Success(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	var insertedOTPUserID string
	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		insertOTPFn: func(_ context.Context, _ repository.DBTX, userID, _ string, purpose string, _ time.Time) (string, error) {
			insertedOTPUserID = userID
			if purpose != "password_reset" {
				return "", fmt.Errorf("unexpected purpose %q", purpose)
			}
			return "otp-id", nil
		},
	}
	mailer := &fakeSender{}
	svc := newTestService(repo, mailer)

	out, err := svc.ForgotPassword(context.Background(), ForgotPasswordInput{
		Email: user.Email,
	})

	if err != nil {
		t.Fatalf("ForgotPassword() unexpected error: %v", err)
	}
	if out.Message == "" {
		t.Error("expected a non-empty message")
	}
	if insertedOTPUserID != user.ID {
		t.Errorf("OTP inserted for user %q, want %q", insertedOTPUserID, user.ID)
	}
	if calls := mailer.sendOTPCalls(); len(calls) != 1 {
		t.Fatalf("expected 1 SendOTP call, got %d", len(calls))
	}
}

// ---------------------------------------------------------------------------
// ResetPassword tests
// ---------------------------------------------------------------------------

// TestResetPassword_UnknownEmail_ReturnsUnauthorized verifies that attempting
// a reset for an unregistered email returns ErrUnauthorized.
func TestResetPassword_UnknownEmail_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{}, repository.ErrNotFound
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.ResetPassword(context.Background(), ResetPasswordInput{
		Email:       "ghost@example.com",
		OTP:         "123456",
		NewPassword: "NewP@ssw0rd",
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ResetPassword() error = %v, want ErrUnauthorized", err)
	}
}

// TestResetPassword_NoActiveOTP_ReturnsUnauthorized verifies that when no
// active password-reset OTP exists for the user, ErrUnauthorized is returned.
func TestResetPassword_NoActiveOTP_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{}, repository.ErrNotFound
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.ResetPassword(context.Background(), ResetPasswordInput{
		Email:       user.Email,
		OTP:         "123456",
		NewPassword: "NewP@ssw0rd",
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ResetPassword() error = %v, want ErrUnauthorized", err)
	}
}

// TestResetPassword_TooManyAttempts_ReturnsRateLimited verifies that when the
// OTP has been attempted too many times, ErrRateLimited is returned.
func TestResetPassword_TooManyAttempts_ReturnsRateLimited(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{
				ID:       "otp-1",
				CodeHash: mustHashOTP("999999"),
				Attempts: auth.MaxOTPAttempts, // already at max
			}, nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.ResetPassword(context.Background(), ResetPasswordInput{
		Email:       user.Email,
		OTP:         "999999",
		NewPassword: "NewP@ssw0rd",
	})

	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("ResetPassword() error = %v, want ErrRateLimited", err)
	}
}

// TestResetPassword_WrongOTP_IncrementsAndReturnsUnauthorized verifies that a
// wrong OTP code increments the attempt counter and returns ErrUnauthorized.
func TestResetPassword_WrongOTP_IncrementsAndReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	var incremented atomic.Bool
	user := verifiedUser()
	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{
				ID:       "otp-1",
				CodeHash: mustHashOTP("111111"),
				Attempts: 0,
			}, nil
		},
		incrementOTPAttemptsFn: func(_ context.Context, _ repository.DBTX, otpID string) error {
			if otpID == "otp-1" {
				incremented.Store(true)
			}
			return nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.ResetPassword(context.Background(), ResetPasswordInput{
		Email:       user.Email,
		OTP:         "000000", // wrong
		NewPassword: "NewP@ssw0rd",
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ResetPassword() error = %v, want ErrUnauthorized", err)
	}
	if !incremented.Load() {
		t.Error("expected IncrementOTPAttempts to be called")
	}
}

// TestResetPassword_UpdatePasswordAffectsZeroRows_ReturnsUnauthorized
// verifies that when UpdatePassword touches 0 rows (race condition:
// user deleted between OTP check and update), ErrUnauthorized is returned.
func TestResetPassword_UpdatePasswordAffectsZeroRows_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	otpCode := "123456"
	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{
				ID:       "otp-1",
				CodeHash: mustHashOTP(otpCode),
				Attempts: 0,
			}, nil
		},
		markOTPUsedFn: func(context.Context, repository.DBTX, string) error {
			return nil
		},
		updatePasswordFn: func(context.Context, repository.DBTX, string, string) (int64, error) {
			return 0, nil // zero rows affected
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.ResetPassword(context.Background(), ResetPasswordInput{
		Email:       user.Email,
		OTP:         otpCode,
		NewPassword: "NewP@ssw0rd",
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ResetPassword() error = %v, want ErrUnauthorized", err)
	}
}

// TestResetPassword_DBErrorOnUpdatePassword_ReturnsInternal verifies that a
// repository failure during password update returns ErrInternal.
func TestResetPassword_DBErrorOnUpdatePassword_ReturnsInternal(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	otpCode := "123456"
	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{
				ID:       "otp-1",
				CodeHash: mustHashOTP(otpCode),
				Attempts: 0,
			}, nil
		},
		markOTPUsedFn: func(context.Context, repository.DBTX, string) error {
			return nil
		},
		updatePasswordFn: func(context.Context, repository.DBTX, string, string) (int64, error) {
			return 0, errBoom
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.ResetPassword(context.Background(), ResetPasswordInput{
		Email:       user.Email,
		OTP:         otpCode,
		NewPassword: "NewP@ssw0rd",
	})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("ResetPassword() error = %v, want ErrInternal", err)
	}
}

// TestResetPassword_Success verifies the happy path: correct OTP, password
// updated, all refresh tokens revoked, success message returned.
func TestResetPassword_Success(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	otpCode := "654321"
	var (
		passwordUpdated atomic.Bool
		tokensRevoked   atomic.Bool
	)

	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{
				ID:       "otp-1",
				CodeHash: mustHashOTP(otpCode),
				Attempts: 0,
			}, nil
		},
		markOTPUsedFn: func(context.Context, repository.DBTX, string) error {
			return nil
		},
		updatePasswordFn: func(_ context.Context, _ repository.DBTX, userID string, _ string) (int64, error) {
			if userID == user.ID {
				passwordUpdated.Store(true)
			}
			return 1, nil
		},
		revokeAllRefreshTokensFn: func(_ context.Context, _ repository.DBTX, userID string) error {
			if userID == user.ID {
				tokensRevoked.Store(true)
			}
			return nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.ResetPassword(context.Background(), ResetPasswordInput{
		Email:       user.Email,
		OTP:         otpCode,
		NewPassword: "NewP@ssw0rd",
	})

	if err != nil {
		t.Fatalf("ResetPassword() unexpected error: %v", err)
	}
	if out.Message == "" {
		t.Error("expected a non-empty success message")
	}
	if !passwordUpdated.Load() {
		t.Error("expected UpdatePassword to be called")
	}
	if !tokensRevoked.Load() {
		t.Error("expected RevokeAllRefreshTokens to be called")
	}
}

// TestResetPassword_RevokeAllTokensFailure_ReturnsInternal verifies that a
// failure to revoke refresh tokens after a password reset returns ErrInternal
// rather than succeeding silently.
func TestResetPassword_RevokeAllTokensFailure_ReturnsInternal(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	otpCode := "654321"
	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{
				ID:       "otp-1",
				CodeHash: mustHashOTP(otpCode),
				Attempts: 0,
			}, nil
		},
		markOTPUsedFn: func(context.Context, repository.DBTX, string) error {
			return nil
		},
		updatePasswordFn: func(context.Context, repository.DBTX, string, string) (int64, error) {
			return 1, nil
		},
		revokeAllRefreshTokensFn: func(context.Context, repository.DBTX, string) error {
			return errBoom
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.ResetPassword(context.Background(), ResetPasswordInput{
		Email:       user.Email,
		OTP:         otpCode,
		NewPassword: "NewP@ssw0rd",
	})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("ResetPassword() error = %v, want ErrInternal", err)
	}
}
