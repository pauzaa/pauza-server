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

func newTestServiceWithPool(pool *fakePool, repo *fakeAuthRepo, mailer *fakeSender) *AuthService {
	return NewAuthService(
		pool,
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

// TestRegister_SMTPFailure_CleanupUsesFreshBackgroundContext verifies that the
// SMTP-failure cleanup path starts a fresh background context so it still runs
// after the request context has been canceled.
func TestRegister_SMTPFailure_CleanupUsesFreshBackgroundContext(t *testing.T) {
	t.Parallel()

	requestCtx, cancel := context.WithCancel(context.Background())
	var (
		cleanupCtx          context.Context
		cleanupCtxErrAtCall error
		deletedOTP          atomic.Bool
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
		deleteOTPByIDFn: func(ctx context.Context, _ repository.DBTX, otpID string) error {
			cleanupCtx = ctx
			cleanupCtxErrAtCall = ctx.Err()
			if otpID == "otp-id" {
				deletedOTP.Store(true)
			}
			return nil
		},
		deleteUnverifiedUserFn: func(context.Context, repository.DBTX, string) (int64, error) {
			return 1, nil
		},
	}
	mailer := &fakeSender{
		sendOTPFn: func(context.Context, string, string, string) error {
			cancel()
			return errors.New("smtp down")
		},
	}

	svc := newTestServiceWithPool(&fakePool{}, repo, mailer)

	_, err := svc.Register(requestCtx, RegisterInput{Email: "bob@example.com", Password: "StrongP@ss1"})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("Register() error = %v, want ErrInternal", err)
	}
	if !deletedOTP.Load() {
		t.Fatal("expected cleanup delete to run after send failure")
	}
	if cleanupCtx == nil {
		t.Fatal("cleanup context was not captured")
	}
	if cleanupCtx == requestCtx {
		t.Fatal("cleanup used request context, want fresh background context")
	}
	if cleanupCtxErrAtCall != nil {
		t.Fatalf("cleanup context err at DeleteOTPByID call = %v, want nil", cleanupCtxErrAtCall)
	}
	if requestCtx.Err() == nil {
		t.Fatal("request context err = nil, want canceled")
	}
}

// TestRegister_ConflictOnUnverifiedEmail verifies that registering with an
// email that already belongs to an unverified user also returns ErrConflict.
func TestRegister_ConflictOnUnverifiedEmail(t *testing.T) {
	t.Parallel()

	mailer := &fakeSender{}
	repo := &fakeAuthRepo{
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{
				ID:            "unverified-user",
				Email:         "alice@example.com",
				EmailVerified: false,
			}, nil
		},
	}

	svc := newTestService(repo, mailer)

	_, err := svc.Register(context.Background(), RegisterInput{
		Email:    "alice@example.com",
		Password: "StrongP@ss1",
	})

	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Register() error = %v, want ErrConflict", err)
	}
	if calls := mailer.sendOTPCalls(); len(calls) != 0 {
		t.Errorf("expected 0 SendOTP calls, got %d", len(calls))
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
		getEntitlementSnapshotFn: func(context.Context, repository.DBTX, string) (repository.EntitlementRow, error) {
			return repository.EntitlementRow{}, repository.ErrNotFound
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
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
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
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
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

// TestForgotPassword_ProbeFailure_ReturnsInternal verifies that when the mail
// transport probe fails, ForgotPassword returns ErrInternal before touching the
// repository or attempting delivery.
func TestForgotPassword_ProbeFailure_ReturnsInternal(t *testing.T) {
	t.Parallel()

	repoCalled := atomic.Bool{}
	repo := &fakeAuthRepo{
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			repoCalled.Store(true)
			return repository.UserRow{}, nil
		},
	}
	mailer := &fakeSender{
		probeFn: func(context.Context) error {
			return errors.New("smtp unavailable")
		},
	}
	svc := newTestService(repo, mailer)

	_, err := svc.ForgotPassword(context.Background(), ForgotPasswordInput{Email: "alice@example.com"})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("ForgotPassword() error = %v, want ErrInternal", err)
	}
	if repoCalled.Load() {
		t.Fatal("expected mail probe failure to short-circuit before repository lookup")
	}
	if got := mailer.probeCallCount(); got != 1 {
		t.Fatalf("Probe() calls = %d, want 1", got)
	}
	if calls := mailer.sendOTPCalls(); len(calls) != 0 {
		t.Fatalf("SendOTP() calls = %d, want 0", len(calls))
	}
}

// TestForgotPassword_OTPInsertFailure_ReturnsInternal verifies that a
// repository failure during OTP insertion returns ErrInternal.
func TestForgotPassword_OTPInsertFailure_ReturnsInternal(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	repo := &fakeAuthRepo{
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		deleteOTPsByUserAndPurposeFn: func(context.Context, repository.DBTX, string, string) error {
			return nil
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

// TestForgotPassword_SendFailureAfterProbe_ReturnsInternalAndCleansUp
// verifies that a post-probe SendOTP failure returns ErrInternal while
// deleting the committed OTP.
func TestForgotPassword_SendFailureAfterProbe_ReturnsInternalAndCleansUp(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	var deletedOTP string
	repo := &fakeAuthRepo{
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		deleteOTPsByUserAndPurposeFn: func(context.Context, repository.DBTX, string, string) error {
			return nil
		},
		insertOTPFn: func(context.Context, repository.DBTX, string, string, string, time.Time) (string, error) {
			return "otp-id", nil
		},
		deleteOTPByIDFn: func(_ context.Context, _ repository.DBTX, otpID string) error {
			deletedOTP = otpID
			return nil
		},
	}
	mailer := &fakeSender{
		sendOTPFn: func(context.Context, string, string, string) error {
			return errors.New("SMTP timeout")
		},
	}
	svc := newTestService(repo, mailer)

	_, err := svc.ForgotPassword(context.Background(), ForgotPasswordInput{
		Email: user.Email,
	})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("ForgotPassword() error = %v, want ErrInternal", err)
	}
	if deletedOTP != "otp-id" {
		t.Fatalf("DeleteOTPByID() otpID = %q, want %q", deletedOTP, "otp-id")
	}
	if got := mailer.probeCallCount(); got != 1 {
		t.Fatalf("Probe() calls = %d, want 1", got)
	}
	if calls := mailer.sendOTPCalls(); len(calls) != 1 {
		t.Fatalf("SendOTP() calls = %d, want 1", len(calls))
	}
}

// TestForgotPassword_SendFailureAfterProbe_DoesNotReprobe verifies that a
// password-reset delivery failure returns ErrInternal immediately without a
// second mail probe.
func TestForgotPassword_SendFailureAfterProbe_DoesNotReprobe(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	var deletedOTP string
	repo := &fakeAuthRepo{
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		deleteOTPsByUserAndPurposeFn: func(context.Context, repository.DBTX, string, string) error {
			return nil
		},
		insertOTPFn: func(context.Context, repository.DBTX, string, string, string, time.Time) (string, error) {
			return "otp-id", nil
		},
		deleteOTPByIDFn: func(_ context.Context, _ repository.DBTX, otpID string) error {
			deletedOTP = otpID
			return nil
		},
	}
	mailer := &fakeSender{
		sendOTPFn: func(context.Context, string, string, string) error {
			return errors.New("SMTP timeout")
		},
	}
	svc := newTestService(repo, mailer)

	_, err := svc.ForgotPassword(context.Background(), ForgotPasswordInput{Email: user.Email})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("ForgotPassword() error = %v, want ErrInternal", err)
	}
	if deletedOTP != "otp-id" {
		t.Fatalf("DeleteOTPByID() otpID = %q, want %q", deletedOTP, "otp-id")
	}
	if got := mailer.probeCallCount(); got != 1 {
		t.Fatalf("Probe() calls = %d, want 1", got)
	}
	if calls := mailer.sendOTPCalls(); len(calls) != 1 {
		t.Fatalf("SendOTP() calls = %d, want 1", len(calls))
	}
}

// TestForgotPassword_SendFailure_CleanupUsesFreshBackgroundContext verifies
// that password-reset OTP cleanup runs with a fresh bounded background context
// even if the request context is canceled during the send failure path.
func TestForgotPassword_SendFailure_CleanupUsesFreshBackgroundContext(t *testing.T) {
	t.Parallel()

	requestCtx, cancel := context.WithCancel(context.Background())
	user := verifiedUser()
	var (
		cleanupCtx          context.Context
		cleanupCtxErrAtCall error
	)
	repo := &fakeAuthRepo{
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		deleteOTPsByUserAndPurposeFn: func(context.Context, repository.DBTX, string, string) error {
			return nil
		},
		insertOTPFn: func(context.Context, repository.DBTX, string, string, string, time.Time) (string, error) {
			return "otp-id", nil
		},
		deleteOTPByIDFn: func(ctx context.Context, _ repository.DBTX, otpID string) error {
			cleanupCtx = ctx
			cleanupCtxErrAtCall = ctx.Err()
			if otpID != "otp-id" {
				return fmt.Errorf("DeleteOTPByID otpID = %q, want %q", otpID, "otp-id")
			}
			return nil
		},
	}
	mailer := &fakeSender{
		sendOTPFn: func(context.Context, string, string, string) error {
			cancel()
			return errors.New("smtp timeout")
		},
	}
	svc := newTestServiceWithPool(&fakePool{}, repo, mailer)

	_, err := svc.ForgotPassword(requestCtx, ForgotPasswordInput{Email: user.Email})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("ForgotPassword() error = %v, want ErrInternal", err)
	}
	if cleanupCtx == nil {
		t.Fatal("cleanup context was not captured")
	}
	if cleanupCtx == requestCtx {
		t.Fatal("cleanup used request context, want fresh background context")
	}
	if cleanupCtxErrAtCall != nil {
		t.Fatalf("cleanup context err at DeleteOTPByID call = %v, want nil", cleanupCtxErrAtCall)
	}
	if requestCtx.Err() == nil {
		t.Fatal("request context err = nil, want canceled")
	}
}

// TestForgotPassword_Success verifies the happy path: known user, OTP stored,
// email sent, generic message returned.
func TestForgotPassword_Success(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	var insertedOTPUserID string
	repo := &fakeAuthRepo{
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		deleteOTPsByUserAndPurposeFn: func(context.Context, repository.DBTX, string, string) error {
			return nil
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
	if got := mailer.probeCallCount(); got != 1 {
		t.Errorf("Probe() calls = %d, want 1", got)
	}
	if calls := mailer.sendOTPCalls(); len(calls) != 1 {
		t.Fatalf("expected 1 SendOTP call, got %d", len(calls))
	}
}

// TestForgotPassword_NewIssuanceInvalidatesOlderResetOTPs verifies that issuing
// a new password-reset OTP removes older active reset codes first.
func TestForgotPassword_NewIssuanceInvalidatesOlderResetOTPs(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	var (
		deletedUserID  string
		deletedPurpose string
		insertedUserID string
	)
	repo := &fakeAuthRepo{
		getUserByEmailForUpdateFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		deleteOTPsByUserAndPurposeFn: func(_ context.Context, _ repository.DBTX, userID, purpose string) error {
			deletedUserID = userID
			deletedPurpose = purpose
			return nil
		},
		insertOTPFn: func(_ context.Context, _ repository.DBTX, userID, _ string, purpose string, _ time.Time) (string, error) {
			insertedUserID = userID
			if purpose != "password_reset" {
				return "", fmt.Errorf("unexpected purpose %q", purpose)
			}
			return "otp-id", nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.ForgotPassword(context.Background(), ForgotPasswordInput{Email: user.Email})

	if err != nil {
		t.Fatalf("ForgotPassword() unexpected error: %v", err)
	}
	if deletedUserID != user.ID {
		t.Fatalf("DeleteOTPsByUserAndPurpose userID = %q, want %q", deletedUserID, user.ID)
	}
	if deletedPurpose != "password_reset" {
		t.Fatalf("DeleteOTPsByUserAndPurpose purpose = %q, want %q", deletedPurpose, "password_reset")
	}
	if insertedUserID != user.ID {
		t.Fatalf("InsertOTP userID = %q, want %q", insertedUserID, user.ID)
	}
}

// TestVerifyOTP_FailedAttemptTimestampsAcrossFreshIssuance_ReturnsRateLimited
// verifies that verify-otp rate limiting uses persisted failed-attempt event
// timestamps, even when the currently active OTP is a fresh row.
func TestVerifyOTP_FailedAttemptTimestampsAcrossFreshIssuance_ReturnsRateLimited(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	oldestAttemptAt := time.Now().UTC().Add(-2 * time.Minute)
	repo := &fakeAuthRepo{
		getUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{
				ID:       "otp-latest",
				CodeHash: mustHashOTP("123456"),
				Attempts: 0,
			}, nil
		},
		countFailedOTPAttemptsSinceForUpdateFn: func(_ context.Context, _ repository.DBTX, userID, purpose string, since time.Time) (int, error) {
			if userID != user.ID {
				t.Fatalf("CountFailedOTPAttemptsSinceForUpdate userID = %q, want %q", userID, user.ID)
			}
			if purpose != "email_verification" {
				t.Fatalf("CountFailedOTPAttemptsSinceForUpdate purpose = %q, want %q", purpose, "email_verification")
			}
			lowerBound := time.Now().UTC().Add(-auth.OTPExpiry).Add(-2 * time.Second)
			upperBound := time.Now().UTC().Add(-auth.OTPExpiry).Add(2 * time.Second)
			if since.Before(lowerBound) || since.After(upperBound) {
				t.Fatalf("CountFailedOTPAttemptsSinceForUpdate since = %v, want near now-%v", since, auth.OTPExpiry)
			}
			return auth.MaxOTPAttempts, nil
		},
		getOldestFailedOTPAttemptSinceForUpdateFn: func(_ context.Context, _ repository.DBTX, userID, purpose string, since time.Time) (time.Time, error) {
			if userID != user.ID {
				t.Fatalf("GetOldestFailedOTPAttemptSinceForUpdate userID = %q, want %q", userID, user.ID)
			}
			if purpose != "email_verification" {
				t.Fatalf("GetOldestFailedOTPAttemptSinceForUpdate purpose = %q, want %q", purpose, "email_verification")
			}
			if oldestAttemptAt.Before(since) {
				t.Fatalf("test fixture oldestAttemptAt = %v, want >= since %v", oldestAttemptAt, since)
			}
			return oldestAttemptAt, nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.VerifyOTP(context.Background(), VerifyOTPInput{
		Email: user.Email,
		OTP:   "123456",
	})

	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("VerifyOTP() error = %v, want ErrRateLimited", err)
	}
	if retryAfter, ok := RetryAfter(err); !ok {
		t.Fatal("RetryAfter() metadata missing")
	} else {
		want := time.Until(oldestAttemptAt.Add(auth.OTPExpiry))
		if want < time.Second {
			want = time.Second
		}
		if retryAfter < want-2*time.Second || retryAfter > want+2*time.Second {
			t.Fatalf("RetryAfter() = %v, want near %v", retryAfter, want)
		}
	}
}

// TestVerifyOTP_WrongCode_RecordsFailedAttemptAndIncrementsLatestRowOnly
// verifies that a mismatched code records a failed-attempt event timestamp and
// increments only the current active OTP row.
func TestVerifyOTP_WrongCode_RecordsFailedAttemptAndIncrementsLatestRowOnly(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	var (
		incrementedOTP    string
		recordedUserID    string
		recordedPurpose   string
		recordedAttemptAt time.Time
	)
	repo := &fakeAuthRepo{
		getUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{
				ID:       "otp-latest",
				CodeHash: mustHashOTP("654321"),
				Attempts: 1,
			}, nil
		},
		countFailedOTPAttemptsSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (int, error) {
			return auth.MaxOTPAttempts - 1, nil
		},
		insertFailedOTPAttemptFn: func(_ context.Context, _ repository.DBTX, userID, purpose string, attemptedAt time.Time) error {
			recordedUserID = userID
			recordedPurpose = purpose
			recordedAttemptAt = attemptedAt
			return nil
		},
		incrementOTPAttemptsFn: func(_ context.Context, _ repository.DBTX, otpID string) error {
			incrementedOTP = otpID
			return nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.VerifyOTP(context.Background(), VerifyOTPInput{
		Email: user.Email,
		OTP:   "000000",
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("VerifyOTP() error = %v, want ErrUnauthorized", err)
	}
	if incrementedOTP != "otp-latest" {
		t.Fatalf("IncrementOTPAttempts otpID = %q, want %q", incrementedOTP, "otp-latest")
	}
	if recordedUserID != user.ID {
		t.Fatalf("InsertFailedOTPAttempt userID = %q, want %q", recordedUserID, user.ID)
	}
	if recordedPurpose != "email_verification" {
		t.Fatalf("InsertFailedOTPAttempt purpose = %q, want %q", recordedPurpose, "email_verification")
	}
	if recordedAttemptAt.IsZero() {
		t.Fatal("InsertFailedOTPAttempt attemptedAt = zero, want timestamp")
	}
	if time.Since(recordedAttemptAt) > 2*time.Second {
		t.Fatalf("InsertFailedOTPAttempt attemptedAt = %v, want recent timestamp", recordedAttemptAt)
	}
}

// TestGetMe_InactiveEntitlementSnapshotIncluded verifies that stored premium
// snapshots are returned even when they are inactive.
func TestGetMe_InactiveEntitlementSnapshotIncluded(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	periodEnd := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	repo := &fakeAuthRepo{
		getVerifiedUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		getEntitlementSnapshotFn: func(context.Context, repository.DBTX, string) (repository.EntitlementRow, error) {
			return repository.EntitlementRow{
				Entitlement:      "premium",
				IsActive:         false,
				CurrentPeriodEnd: &periodEnd,
			}, nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.GetMe(context.Background(), GetMeInput{UserID: user.ID})
	if err != nil {
		t.Fatalf("GetMe() unexpected error: %v", err)
	}
	if out.Subscription == nil {
		t.Fatal("Subscription = nil, want value")
	}
	if out.Subscription.Entitlement != "premium" {
		t.Errorf("Subscription.Entitlement = %q, want %q", out.Subscription.Entitlement, "premium")
	}
	if out.Subscription.IsActive {
		t.Error("Subscription.IsActive = true, want false")
	}
	if out.Subscription.CurrentPeriodEnd == nil || !out.Subscription.CurrentPeriodEnd.Equal(periodEnd) {
		t.Errorf("Subscription.CurrentPeriodEnd = %v, want %v", out.Subscription.CurrentPeriodEnd, periodEnd)
	}
}

// TestGetMe_EntitlementLookupError_ReturnsInternal verifies that operational
// entitlement lookup failures surface as ErrInternal.
func TestGetMe_EntitlementLookupError_ReturnsInternal(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	repo := &fakeAuthRepo{
		getVerifiedUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		getEntitlementSnapshotFn: func(context.Context, repository.DBTX, string) (repository.EntitlementRow, error) {
			return repository.EntitlementRow{}, errBoom
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.GetMe(context.Background(), GetMeInput{UserID: user.ID})
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("GetMe() error = %v, want ErrInternal", err)
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
		countFailedOTPAttemptsSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (int, error) {
			return auth.MaxOTPAttempts, nil
		},
		getOldestFailedOTPAttemptSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (time.Time, error) {
			return time.Now().UTC().Add(-time.Minute), nil
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

// TestResetPassword_FailedAttemptTimestampsAcrossFreshIssuance_ReturnsRateLimited
// verifies that reset-password rate limiting uses persisted failed-attempt
// event timestamps, even when the currently active OTP is a fresh row.
func TestResetPassword_FailedAttemptTimestampsAcrossFreshIssuance_ReturnsRateLimited(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	oldestAttemptAt := time.Now().UTC().Add(-3 * time.Minute)
	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{
				ID:       "otp-fresh",
				CodeHash: mustHashOTP("999999"),
				Attempts: 0,
			}, nil
		},
		countFailedOTPAttemptsSinceForUpdateFn: func(_ context.Context, _ repository.DBTX, userID, purpose string, since time.Time) (int, error) {
			if userID != user.ID {
				t.Fatalf("CountFailedOTPAttemptsSinceForUpdate userID = %q, want %q", userID, user.ID)
			}
			if purpose != "password_reset" {
				t.Fatalf("CountFailedOTPAttemptsSinceForUpdate purpose = %q, want %q", purpose, "password_reset")
			}
			lowerBound := time.Now().UTC().Add(-auth.OTPExpiry).Add(-2 * time.Second)
			upperBound := time.Now().UTC().Add(-auth.OTPExpiry).Add(2 * time.Second)
			if since.Before(lowerBound) || since.After(upperBound) {
				t.Fatalf("CountFailedOTPAttemptsSinceForUpdate since = %v, want near now-%v", since, auth.OTPExpiry)
			}
			return auth.MaxOTPAttempts, nil
		},
		getOldestFailedOTPAttemptSinceForUpdateFn: func(_ context.Context, _ repository.DBTX, userID, purpose string, since time.Time) (time.Time, error) {
			if userID != user.ID {
				t.Fatalf("GetOldestFailedOTPAttemptSinceForUpdate userID = %q, want %q", userID, user.ID)
			}
			if purpose != "password_reset" {
				t.Fatalf("GetOldestFailedOTPAttemptSinceForUpdate purpose = %q, want %q", purpose, "password_reset")
			}
			if oldestAttemptAt.Before(since) {
				t.Fatalf("test fixture oldestAttemptAt = %v, want >= since %v", oldestAttemptAt, since)
			}
			return oldestAttemptAt, nil
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
	if retryAfter, ok := RetryAfter(err); !ok {
		t.Fatal("RetryAfter() metadata missing")
	} else {
		want := time.Until(oldestAttemptAt.Add(auth.OTPExpiry))
		if want < time.Second {
			want = time.Second
		}
		if retryAfter < want-2*time.Second || retryAfter > want+2*time.Second {
			t.Fatalf("RetryAfter() = %v, want near %v", retryAfter, want)
		}
	}
}

// TestResetPassword_WrongOTP_IncrementsAndReturnsUnauthorized verifies that a
// wrong OTP code records a failed-attempt event, increments the attempt
// counter, and returns ErrUnauthorized.
func TestResetPassword_WrongOTP_IncrementsAndReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	var (
		incremented       atomic.Bool
		recordedUserID    string
		recordedPurpose   string
		recordedAttemptAt time.Time
	)
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
		countFailedOTPAttemptsSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (int, error) {
			return 0, nil
		},
		insertFailedOTPAttemptFn: func(_ context.Context, _ repository.DBTX, userID, purpose string, attemptedAt time.Time) error {
			recordedUserID = userID
			recordedPurpose = purpose
			recordedAttemptAt = attemptedAt
			return nil
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
	if recordedUserID != user.ID {
		t.Fatalf("InsertFailedOTPAttempt userID = %q, want %q", recordedUserID, user.ID)
	}
	if recordedPurpose != "password_reset" {
		t.Fatalf("InsertFailedOTPAttempt purpose = %q, want %q", recordedPurpose, "password_reset")
	}
	if recordedAttemptAt.IsZero() {
		t.Fatal("InsertFailedOTPAttempt attemptedAt = zero, want timestamp")
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
		countFailedOTPAttemptsSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (int, error) {
			return 0, nil
		},
		markOTPUsedFn: func(context.Context, repository.DBTX, string) error {
			return nil
		},
		deleteOTPsByUserAndPurposeFn: func(context.Context, repository.DBTX, string, string) error {
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
		countFailedOTPAttemptsSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (int, error) {
			return 0, nil
		},
		markOTPUsedFn: func(context.Context, repository.DBTX, string) error {
			return nil
		},
		deleteOTPsByUserAndPurposeFn: func(context.Context, repository.DBTX, string, string) error {
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
		countFailedOTPAttemptsSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (int, error) {
			return 0, nil
		},
		markOTPUsedFn: func(context.Context, repository.DBTX, string) error {
			return nil
		},
		deleteOTPsByUserAndPurposeFn: func(context.Context, repository.DBTX, string, string) error {
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

// TestResetPassword_Success_DeletesRemainingResetOTPs verifies that a
// successful password reset clears any remaining password-reset OTP rows.
func TestResetPassword_Success_DeletesRemainingResetOTPs(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	otpCode := "654321"
	var (
		deletedUserID  string
		deletedPurpose string
	)
	repo := &fakeAuthRepo{
		getVerifiedUserByEmailFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		getActiveOTPForUpdateFn: func(context.Context, repository.DBTX, string, string) (repository.OTPRow, error) {
			return repository.OTPRow{ID: "otp-latest", CodeHash: mustHashOTP(otpCode), Attempts: 0}, nil
		},
		countFailedOTPAttemptsSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (int, error) {
			return 0, nil
		},
		markOTPUsedFn: func(_ context.Context, _ repository.DBTX, otpID string) error {
			if otpID != "otp-latest" {
				return fmt.Errorf("MarkOTPUsed otpID = %q, want %q", otpID, "otp-latest")
			}
			return nil
		},
		deleteOTPsByUserAndPurposeFn: func(_ context.Context, _ repository.DBTX, userID, purpose string) error {
			deletedUserID = userID
			deletedPurpose = purpose
			return nil
		},
		updatePasswordFn: func(context.Context, repository.DBTX, string, string) (int64, error) {
			return 1, nil
		},
		revokeAllRefreshTokensFn: func(context.Context, repository.DBTX, string) error {
			return nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.ResetPassword(context.Background(), ResetPasswordInput{
		Email:       user.Email,
		OTP:         otpCode,
		NewPassword: "NewP@ssw0rd",
	})

	if err != nil {
		t.Fatalf("ResetPassword() unexpected error: %v", err)
	}
	if deletedUserID != user.ID {
		t.Fatalf("DeleteOTPsByUserAndPurpose userID = %q, want %q", deletedUserID, user.ID)
	}
	if deletedPurpose != "password_reset" {
		t.Fatalf("DeleteOTPsByUserAndPurpose purpose = %q, want %q", deletedPurpose, "password_reset")
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
		countFailedOTPAttemptsSinceForUpdateFn: func(context.Context, repository.DBTX, string, string, time.Time) (int, error) {
			return 0, nil
		},
		markOTPUsedFn: func(context.Context, repository.DBTX, string) error {
			return nil
		},
		deleteOTPsByUserAndPurposeFn: func(context.Context, repository.DBTX, string, string) error {
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

// ---------------------------------------------------------------------------
// UpdateMe tests
// ---------------------------------------------------------------------------

// TestUpdateMe_Success_ReturnsUpdatedProfile verifies the happy path: the
// profile is updated and the returned profile reflects the changes.
func TestUpdateMe_Success_ReturnsUpdatedProfile(t *testing.T) {
	t.Parallel()

	newName := "Bob"
	newUsername := "bob_new"
	updated := verifiedUser()
	updated.Name = newName
	updated.Username = newUsername

	repo := &fakeAuthRepo{
		updateUserFn: func(_ context.Context, _ repository.DBTX, userID string, name *string, username *string, _ *bool) (repository.UserRow, error) {
			if userID != "user-001" {
				t.Errorf("userID = %q, want %q", userID, "user-001")
			}
			if name == nil || *name != newName {
				t.Errorf("name = %v, want %q", name, newName)
			}
			if username == nil || *username != newUsername {
				t.Errorf("username = %v, want %q", username, newUsername)
			}
			return updated, nil
		},
		getEntitlementSnapshotFn: func(context.Context, repository.DBTX, string) (repository.EntitlementRow, error) {
			return repository.EntitlementRow{}, repository.ErrNotFound
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.UpdateMe(context.Background(), UpdateMeInput{
		UserID:   "user-001",
		Name:     &newName,
		Username: &newUsername,
	})

	if err != nil {
		t.Fatalf("UpdateMe() unexpected error: %v", err)
	}
	if out.Name != newName {
		t.Errorf("Name = %q, want %q", out.Name, newName)
	}
	if out.Username != newUsername {
		t.Errorf("Username = %q, want %q", out.Username, newUsername)
	}
}

// TestUpdateMe_InactiveEntitlementSnapshotIncluded verifies that UpdateMe
// returns an inactive premium snapshot in the profile payload.
func TestUpdateMe_InactiveEntitlementSnapshotIncluded(t *testing.T) {
	t.Parallel()

	updated := verifiedUser()
	periodEnd := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	repo := &fakeAuthRepo{
		updateUserFn: func(context.Context, repository.DBTX, string, *string, *string, *bool) (repository.UserRow, error) {
			return updated, nil
		},
		getEntitlementSnapshotFn: func(context.Context, repository.DBTX, string) (repository.EntitlementRow, error) {
			return repository.EntitlementRow{
				Entitlement:      "premium",
				IsActive:         false,
				CurrentPeriodEnd: &periodEnd,
			}, nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.UpdateMe(context.Background(), UpdateMeInput{UserID: updated.ID})
	if err != nil {
		t.Fatalf("UpdateMe() unexpected error: %v", err)
	}
	if out.Subscription == nil {
		t.Fatal("Subscription = nil, want value")
	}
	if out.Subscription.IsActive {
		t.Error("Subscription.IsActive = true, want false")
	}
	if out.Subscription.CurrentPeriodEnd == nil || !out.Subscription.CurrentPeriodEnd.Equal(periodEnd) {
		t.Errorf("Subscription.CurrentPeriodEnd = %v, want %v", out.Subscription.CurrentPeriodEnd, periodEnd)
	}
}

// TestUpdateMe_NoOp_ReturnsCurrentProfile verifies that when no fields are
// provided, the service returns the current profile unchanged.
func TestUpdateMe_NoOp_ReturnsCurrentProfile(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	repo := &fakeAuthRepo{
		updateUserFn: func(_ context.Context, _ repository.DBTX, _ string, name *string, username *string, lv *bool) (repository.UserRow, error) {
			if name != nil || username != nil || lv != nil {
				t.Error("expected all fields nil for no-op PATCH")
			}
			return user, nil
		},
		getEntitlementSnapshotFn: func(context.Context, repository.DBTX, string) (repository.EntitlementRow, error) {
			return repository.EntitlementRow{}, repository.ErrNotFound
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.UpdateMe(context.Background(), UpdateMeInput{
		UserID: "user-001",
	})

	if err != nil {
		t.Fatalf("UpdateMe() unexpected error: %v", err)
	}
	if out.ID != user.ID {
		t.Errorf("ID = %q, want %q", out.ID, user.ID)
	}
}

// TestUpdateMe_UsernameConflict_ReturnsConflict verifies that a unique violation
// on the username constraint returns ErrConflict.
func TestUpdateMe_UsernameConflict_ReturnsConflict(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		updateUserFn: func(context.Context, repository.DBTX, string, *string, *string, *bool) (repository.UserRow, error) {
			return repository.UserRow{}, pgUniqueViolation("users_username_key")
		},
	}
	svc := newTestService(repo, &fakeSender{})

	taken := "taken_name"
	_, err := svc.UpdateMe(context.Background(), UpdateMeInput{
		UserID:   "user-001",
		Username: &taken,
	})

	if !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateMe() error = %v, want ErrConflict", err)
	}
}

// TestUpdateMe_UsernameConflictOnIndex_ReturnsConflict verifies that a unique
// violation on the case-insensitive index also returns ErrConflict.
func TestUpdateMe_UsernameConflictOnIndex_ReturnsConflict(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		updateUserFn: func(context.Context, repository.DBTX, string, *string, *string, *bool) (repository.UserRow, error) {
			return repository.UserRow{}, pgUniqueViolation("idx_users_username")
		},
	}
	svc := newTestService(repo, &fakeSender{})

	taken := "Taken_Name"
	_, err := svc.UpdateMe(context.Background(), UpdateMeInput{
		UserID:   "user-001",
		Username: &taken,
	})

	if !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateMe() error = %v, want ErrConflict", err)
	}
}

// TestUpdateMe_UserNotFound_ReturnsUnauthorized verifies that when the user
// is not found during update, ErrUnauthorized is returned.
func TestUpdateMe_UserNotFound_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		updateUserFn: func(context.Context, repository.DBTX, string, *string, *string, *bool) (repository.UserRow, error) {
			return repository.UserRow{}, repository.ErrNotFound
		},
	}
	svc := newTestService(repo, &fakeSender{})

	name := "Alice"
	_, err := svc.UpdateMe(context.Background(), UpdateMeInput{
		UserID: "nonexistent",
		Name:   &name,
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("UpdateMe() error = %v, want ErrUnauthorized", err)
	}
}

// TestUpdateMe_DBError_ReturnsInternal verifies that a generic DB failure
// returns ErrInternal.
func TestUpdateMe_DBError_ReturnsInternal(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		updateUserFn: func(context.Context, repository.DBTX, string, *string, *string, *bool) (repository.UserRow, error) {
			return repository.UserRow{}, errBoom
		},
	}
	svc := newTestService(repo, &fakeSender{})

	name := "Alice"
	_, err := svc.UpdateMe(context.Background(), UpdateMeInput{
		UserID: "user-001",
		Name:   &name,
	})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("UpdateMe() error = %v, want ErrInternal", err)
	}
}

// ---------------------------------------------------------------------------
// CheckUsernameAvailable tests
// ---------------------------------------------------------------------------

// TestCheckUsernameAvailable_Available verifies that an available username
// returns Available = true.
func TestCheckUsernameAvailable_Available(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		isUsernameTakenFn: func(_ context.Context, _ repository.DBTX, username string, excludeUserID string) (bool, error) {
			if username != "new_name" {
				t.Errorf("username = %q, want %q", username, "new_name")
			}
			if excludeUserID != "user-001" {
				t.Errorf("excludeUserID = %q, want %q", excludeUserID, "user-001")
			}
			return false, nil // not taken
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.CheckUsernameAvailable(context.Background(), UsernameAvailableInput{
		UserID:   "user-001",
		Username: "new_name",
	})

	if err != nil {
		t.Fatalf("CheckUsernameAvailable() unexpected error: %v", err)
	}
	if !out.Available {
		t.Error("expected Available = true")
	}
}

// TestCheckUsernameAvailable_Taken verifies that a taken username returns
// Available = false.
func TestCheckUsernameAvailable_Taken(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		isUsernameTakenFn: func(context.Context, repository.DBTX, string, string) (bool, error) {
			return true, nil // taken
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.CheckUsernameAvailable(context.Background(), UsernameAvailableInput{
		UserID:   "user-001",
		Username: "taken_name",
	})

	if err != nil {
		t.Fatalf("CheckUsernameAvailable() unexpected error: %v", err)
	}
	if out.Available {
		t.Error("expected Available = false")
	}
}

// TestCheckUsernameAvailable_MissingUser_ReturnsUnauthorized verifies that a
// stale JWT subject is rejected when the authenticated user no longer exists.
func TestCheckUsernameAvailable_MissingUser_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		isUsernameTakenFn: func(_ context.Context, _ repository.DBTX, username, excludeUserID string) (bool, error) {
			if username != "some_name" {
				t.Errorf("username = %q, want %q", username, "some_name")
			}
			if excludeUserID != "user-001" {
				t.Errorf("excludeUserID = %q, want %q", excludeUserID, "user-001")
			}
			return false, repository.ErrNotFound
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.CheckUsernameAvailable(context.Background(), UsernameAvailableInput{
		UserID:   "user-001",
		Username: "some_name",
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("CheckUsernameAvailable() error = %v, want ErrUnauthorized", err)
	}
}

// TestCheckUsernameAvailable_DBError_ReturnsInternal verifies that a DB
// failure returns ErrInternal.
func TestCheckUsernameAvailable_DBError_ReturnsInternal(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		isUsernameTakenFn: func(context.Context, repository.DBTX, string, string) (bool, error) {
			return false, errBoom
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.CheckUsernameAvailable(context.Background(), UsernameAvailableInput{
		UserID:   "user-001",
		Username: "some_name",
	})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("CheckUsernameAvailable() error = %v, want ErrInternal", err)
	}
}

// ---------------------------------------------------------------------------
// DeleteMe tests
// ---------------------------------------------------------------------------

// TestDeleteMe_Success verifies the happy path: correct password, user
// deleted, success message returned.
func TestDeleteMe_Success(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	repo := &fakeAuthRepo{
		getVerifiedUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			if userID != user.ID {
				t.Errorf("userID = %q, want %q", userID, user.ID)
			}
			return user, nil
		},
		deleteUserFn: func(_ context.Context, _ repository.DBTX, userID string) error {
			if userID != user.ID {
				t.Errorf("deleteUser userID = %q, want %q", userID, user.ID)
			}
			return nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	out, err := svc.DeleteMe(context.Background(), DeleteMeInput{
		UserID:   user.ID,
		Password: "correct-password",
	})

	if err != nil {
		t.Fatalf("DeleteMe() unexpected error: %v", err)
	}
	if out.Message != "Account deleted" {
		t.Errorf("Message = %q, want %q", out.Message, "Account deleted")
	}
}

// TestDeleteMe_WrongPassword_ReturnsUnauthorized verifies that providing
// the wrong password returns ErrUnauthorized.
func TestDeleteMe_WrongPassword_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	repo := &fakeAuthRepo{
		getVerifiedUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.DeleteMe(context.Background(), DeleteMeInput{
		UserID:   user.ID,
		Password: "wrong-password",
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("DeleteMe() error = %v, want ErrUnauthorized", err)
	}
}

// TestDeleteMe_UserNotFound_ReturnsUnauthorized verifies that when the user
// doesn't exist, ErrUnauthorized is returned.
func TestDeleteMe_UserNotFound_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getVerifiedUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{}, repository.ErrNotFound
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.DeleteMe(context.Background(), DeleteMeInput{
		UserID:   "nonexistent",
		Password: "anything",
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("DeleteMe() error = %v, want ErrUnauthorized", err)
	}
}

// TestDeleteMe_DBErrorOnLookup_ReturnsInternal verifies that a DB failure
// during user lookup returns ErrInternal.
func TestDeleteMe_DBErrorOnLookup_ReturnsInternal(t *testing.T) {
	t.Parallel()

	repo := &fakeAuthRepo{
		getVerifiedUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{}, errBoom
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.DeleteMe(context.Background(), DeleteMeInput{
		UserID:   "user-001",
		Password: "anything",
	})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("DeleteMe() error = %v, want ErrInternal", err)
	}
}

// TestDeleteMe_DBErrorOnDelete_ReturnsInternal verifies that a DB failure
// during user deletion returns ErrInternal.
func TestDeleteMe_DBErrorOnDelete_ReturnsInternal(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	repo := &fakeAuthRepo{
		getVerifiedUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		deleteUserFn: func(context.Context, repository.DBTX, string) error {
			return errBoom
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.DeleteMe(context.Background(), DeleteMeInput{
		UserID:   user.ID,
		Password: "correct-password",
	})

	if !errors.Is(err, ErrInternal) {
		t.Fatalf("DeleteMe() error = %v, want ErrInternal", err)
	}
}

// TestDeleteMe_DeleteReturnsNotFound_ReturnsUnauthorized verifies that when
// DeleteUser returns ErrNotFound (race: user deleted between lookup and
// delete), ErrUnauthorized is returned.
func TestDeleteMe_DeleteReturnsNotFound_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	user := verifiedUser()
	repo := &fakeAuthRepo{
		getVerifiedUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return user, nil
		},
		deleteUserFn: func(context.Context, repository.DBTX, string) error {
			return repository.ErrNotFound
		},
	}
	svc := newTestService(repo, &fakeSender{})

	_, err := svc.DeleteMe(context.Background(), DeleteMeInput{
		UserID:   user.ID,
		Password: "correct-password",
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("DeleteMe() error = %v, want ErrUnauthorized", err)
	}
}
