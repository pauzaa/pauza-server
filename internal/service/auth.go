package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/redact"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

// ---------------------------------------------------------------------------
// Sentinel errors — handlers map these to HTTP status codes.
// ---------------------------------------------------------------------------

// ErrConflict indicates the operation conflicts with existing state
// (e.g. duplicate verified email).
var ErrConflict = errors.New("conflict")

// ErrUnauthorized indicates invalid credentials, expired/invalid OTP,
// or invalid refresh token.
var ErrUnauthorized = errors.New("unauthorized")

// ErrRateLimited indicates too many attempts (e.g. OTP verification).
var ErrRateLimited = errors.New("rate limited")

// ErrInternal indicates an unexpected internal failure. The handler should
// log the wrapped cause and return a generic 500 to the client.
var ErrInternal = errors.New("internal error")

// ---------------------------------------------------------------------------
// Input / output types
// ---------------------------------------------------------------------------

// RegisterInput holds the validated fields for a registration request.
type RegisterInput struct {
	Email    string
	Password string
}

// RegisterOutput holds the result of a successful registration.
type RegisterOutput struct {
	OTPRequired bool
}

// VerifyOTPInput holds the validated fields for an OTP verification request.
type VerifyOTPInput struct {
	Email string
	OTP   string
}

// AuthOutput holds the result of a successful authentication that returns
// tokens and a user profile (verify-otp, login).
type AuthOutput struct {
	AccessToken  string
	RefreshToken string
	User         UserProfile
}

// UserProfile is the service-layer representation of a user, independent of
// HTTP response serialization.
type UserProfile struct {
	ID                 string
	Email              string
	Name               string
	Username           string
	ProfilePictureURL  *string
	LeaderboardVisible bool
	CreatedAt          time.Time
	Subscription       *EntitlementInfo
}

// EntitlementInfo represents a stored entitlement snapshot.
type EntitlementInfo struct {
	Entitlement      string
	IsActive         bool
	CurrentPeriodEnd *time.Time
}

// LoginInput holds the validated fields for a login request.
type LoginInput struct {
	Email    string
	Password string
}

// RefreshInput holds the validated fields for a token refresh request.
type RefreshInput struct {
	RefreshToken string
}

// RefreshOutput holds the result of a successful token refresh.
type RefreshOutput struct {
	AccessToken  string
	RefreshToken string
}

// ForgotPasswordInput holds the validated fields for a forgot-password request.
type ForgotPasswordInput struct {
	Email string
}

// ResetPasswordInput holds the validated fields for a reset-password request.
type ResetPasswordInput struct {
	Email       string
	OTP         string
	NewPassword string
}

// MessageOutput holds a simple message result.
type MessageOutput struct {
	Message string
}

// GetMeInput holds the authenticated user context for a profile request.
type GetMeInput struct {
	UserID string
}

// UpdateMeInput holds the validated fields for a profile update request.
// Pointer fields are nil when the caller did not provide that field (PATCH
// semantics).
type UpdateMeInput struct {
	UserID             string
	Name               *string
	Username           *string
	LeaderboardVisible *bool
}

// UsernameAvailableInput holds the query for a username availability check.
type UsernameAvailableInput struct {
	UserID   string
	Username string
}

// UsernameAvailableOutput holds the result of a username availability check.
type UsernameAvailableOutput struct {
	Available bool
}

// DeleteMeInput holds the validated fields for an account deletion request.
type DeleteMeInput struct {
	UserID   string
	Password string
}

// ---------------------------------------------------------------------------
// Service
// ---------------------------------------------------------------------------

// AuthService encapsulates authentication business logic, coordinating
// between the repository, auth utilities, and mail sender.
type AuthService struct {
	pool               repository.Pool
	repo               repository.AuthRepository
	otpAttemptBudgeter interface {
		CountFailedOTPAttemptsSinceForUpdate(ctx context.Context, db repository.DBTX, userID, purpose string, since time.Time) (int, error)
	}
	otpAttemptRetrier interface {
		GetOldestFailedOTPAttemptSinceForUpdate(ctx context.Context, db repository.DBTX, userID, purpose string, since time.Time) (time.Time, error)
	}
	mailer             mail.Sender
	jwtSecret          string
	jwtAccessTokenTTL  time.Duration
	jwtRefreshTokenTTL time.Duration
	logger             *slog.Logger
	otpAttemptRecorder interface {
		InsertFailedOTPAttempt(ctx context.Context, db repository.DBTX, userID, purpose string, attemptedAt time.Time) error
	}
}

const cleanupTimeout = 5 * time.Second

// NewAuthService creates an AuthService with the given dependencies.
func NewAuthService(
	pool repository.Pool,
	repo repository.AuthRepository,
	mailer mail.Sender,
	jwtSecret string,
	jwtAccessTokenTTL time.Duration,
	jwtRefreshTokenTTL time.Duration,
	logger *slog.Logger,
) *AuthService {
	budgeter, _ := repo.(interface {
		CountFailedOTPAttemptsSinceForUpdate(ctx context.Context, db repository.DBTX, userID, purpose string, since time.Time) (int, error)
	})
	retrier, _ := repo.(interface {
		GetOldestFailedOTPAttemptSinceForUpdate(ctx context.Context, db repository.DBTX, userID, purpose string, since time.Time) (time.Time, error)
	})
	recorder, _ := repo.(interface {
		InsertFailedOTPAttempt(ctx context.Context, db repository.DBTX, userID, purpose string, attemptedAt time.Time) error
	})
	return &AuthService{
		pool:               pool,
		repo:               repo,
		otpAttemptBudgeter: budgeter,
		otpAttemptRetrier:  retrier,
		mailer:             mailer,
		jwtSecret:          jwtSecret,
		jwtAccessTokenTTL:  jwtAccessTokenTTL,
		jwtRefreshTokenTTL: jwtRefreshTokenTTL,
		logger:             logger,
		otpAttemptRecorder: recorder,
	}
}

// Register handles the registration use case: create an unverified user,
// generate an OTP, send it via email, and return a result indicating that
// OTP verification is required. Any existing email returns a conflict.
// On SMTP failure the created rows are cleaned up.
func (s *AuthService) Register(ctx context.Context, in RegisterInput) (RegisterOutput, error) {
	email := normalizeEmail(in.Email)

	// Hash the password before starting the transaction so the (potentially
	// slow) bcrypt work does not hold any row locks.
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		s.logger.Error("hashing password", "err", err)
		return RegisterOutput{}, ErrInternal
	}

	// Perform the existing-user check and new user + OTP insert inside a
	// single transaction.
	regTx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning registration transaction", "err", err)
		return RegisterOutput{}, ErrInternal
	}
	defer regTx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// Check if a user already exists with this email (FOR UPDATE lock).
	_, err = s.repo.GetUserByEmailForUpdate(ctx, regTx, email)
	if err == nil {
		return RegisterOutput{}, fmt.Errorf("%w: email already registered", ErrConflict)
	} else if !errors.Is(err, repository.ErrNotFound) {
		s.logger.Error("checking existing user", "err", err)
		return RegisterOutput{}, ErrInternal
	}

	// Insert the new unverified user with retry for username collisions.
	const maxUsernameRetries = 3
	var userID string
	for attempt := range maxUsernameRetries {
		username, genErr := generateUsername()
		if genErr != nil {
			s.logger.Error("generating username", "err", genErr)
			return RegisterOutput{}, ErrInternal
		}

		// pgx v5: Begin on an existing Tx creates a SAVEPOINT.
		sp, spErr := regTx.Begin(ctx)
		if spErr != nil {
			s.logger.Error("creating savepoint for user insert", "err", spErr)
			return RegisterOutput{}, ErrInternal
		}

		insertedID, insertErr := s.repo.InsertUser(ctx, sp, email, hash, username)
		if insertErr == nil {
			if err := sp.Commit(ctx); err != nil {
				s.logger.Error("releasing savepoint after user insert", "err", err)
				return RegisterOutput{}, ErrInternal
			}
			userID = insertedID
			break
		}

		// Roll back the savepoint so the outer transaction stays usable.
		if rbErr := sp.Rollback(ctx); rbErr != nil {
			s.logger.Error("rolling back savepoint after user insert failure", "err", rbErr)
			return RegisterOutput{}, ErrInternal
		}

		if isUniqueViolation(insertErr, "idx_users_email") {
			return RegisterOutput{}, fmt.Errorf("%w: email already registered", ErrConflict)
		}
		if isUniqueViolation(insertErr, "users_username_key") ||
			isUniqueViolation(insertErr, "idx_users_username") {
			s.logger.Warn("username collision, retrying", "attempt", attempt+1)
			continue
		}

		s.logger.Error("inserting user", "err", insertErr)
		return RegisterOutput{}, ErrInternal
	}
	if userID == "" {
		s.logger.Error("failed to generate unique username after retries")
		return RegisterOutput{}, ErrInternal
	}

	// Generate OTP.
	otp, err := auth.GenerateOTP()
	if err != nil {
		s.logger.Error("generating otp", "err", err)
		return RegisterOutput{}, ErrInternal
	}
	otpHash, err := auth.HashOTP(otp)
	if err != nil {
		s.logger.Error("hashing otp", "err", err)
		return RegisterOutput{}, ErrInternal
	}
	expiresAt := time.Now().UTC().Add(auth.OTPExpiry)
	otpID, err := s.repo.InsertOTP(ctx, regTx, userID, otpHash, "email_verification", expiresAt)
	if err != nil {
		s.logger.Error("inserting otp", "err", err)
		return RegisterOutput{}, ErrInternal
	}

	// Commit the user + OTP rows before the SMTP call so we do not hold a
	// DB connection/transaction open for the duration of the network send.
	if err := regTx.Commit(ctx); err != nil {
		s.logger.Error("committing registration transaction", "err", err)
		return RegisterOutput{}, ErrInternal
	}

	// Send OTP via email. If this fails, clean up the rows we just committed.
	if err := s.mailer.SendOTP(ctx, email, otp, mail.PurposeEmailVerification); err != nil {
		s.logger.Error("sending otp email", "email", redact.Email(email), "err", err)
		s.cleanupFailedRegistration(otpID, userID)
		return RegisterOutput{}, ErrInternal
	}

	return RegisterOutput{OTPRequired: true}, nil
}

// cleanupFailedRegistration removes the OTP and unverified user rows created
// during a registration whose SMTP send failed. It runs in a new transaction
// and logs any errors without propagating them.
func (s *AuthService) cleanupFailedRegistration(otpID, userID string) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()

	cleanupTx, txErr := s.pool.Begin(cleanupCtx)
	if txErr != nil {
		s.logger.Error("beginning smtp-failure cleanup transaction", "err", txErr)
		return
	}

	if err := s.repo.DeleteOTPByID(cleanupCtx, cleanupTx, otpID); err != nil {
		s.logger.Error("cleaning up otp after email failure", "err", err)
		if rbErr := cleanupTx.Rollback(cleanupCtx); rbErr != nil {
			s.logger.Error("rolling back smtp-failure cleanup transaction", "err", rbErr)
		}
		return
	}

	deleted, err := s.repo.DeleteUnverifiedUser(cleanupCtx, cleanupTx, userID)
	if err != nil {
		s.logger.Error("cleaning up user after email failure", "err", err)
		if rbErr := cleanupTx.Rollback(cleanupCtx); rbErr != nil {
			s.logger.Error("rolling back smtp-failure cleanup transaction", "err", rbErr)
		}
		return
	}
	if deleted == 0 {
		s.logger.Debug("smtp-failure cleanup: user already removed by concurrent registration",
			"user_id", userID)
	}

	if commitErr := cleanupTx.Commit(cleanupCtx); commitErr != nil {
		s.logger.Error("committing smtp-failure cleanup transaction", "err", commitErr)
	}
}

// VerifyOTP handles the OTP verification use case: validate the OTP,
// activate the user, issue tokens, and return the auth result.
func (s *AuthService) VerifyOTP(ctx context.Context, in VerifyOTPInput) (AuthOutput, error) {
	email := normalizeEmail(in.Email)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning verify-otp transaction", "err", err)
		return AuthOutput{}, ErrInternal
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// Resolve email to user.
	user, err := s.repo.GetUserByEmail(ctx, tx, email)
	if errors.Is(err, repository.ErrNotFound) {
		return AuthOutput{}, fmt.Errorf("%w: invalid or expired OTP", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("resolving user for verify-otp", "err", err)
		return AuthOutput{}, ErrInternal
	}

	// Query active OTP with FOR UPDATE lock.
	otp, err := s.repo.GetActiveOTPForUpdate(ctx, tx, user.ID, "email_verification")
	if errors.Is(err, repository.ErrNotFound) {
		return AuthOutput{}, fmt.Errorf("%w: invalid or expired OTP", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("querying otp", "err", err)
		return AuthOutput{}, ErrInternal
	}

	// Too many failed attempts for this email verification window.
	attemptWindowStart := time.Now().UTC().Add(-auth.OTPExpiry)
	if s.otpAttemptBudgeter == nil || s.otpAttemptRetrier == nil || s.otpAttemptRecorder == nil {
		s.logger.Error("verify-otp failed-attempt persistence not configured")
		return AuthOutput{}, ErrInternal
	}

	attemptsUsed, err := s.otpAttemptBudgeter.CountFailedOTPAttemptsSinceForUpdate(ctx, tx, user.ID, "email_verification", attemptWindowStart)
	if err != nil {
		s.logger.Error("counting verify-otp failed attempts", "err", err)
		return AuthOutput{}, ErrInternal
	}
	if attemptsUsed >= auth.MaxOTPAttempts {
		rateLimitedErr := s.newOTPAttemptsRateLimitedError(ctx, tx, user.ID, "email_verification", attemptWindowStart)
		if errors.Is(rateLimitedErr, ErrInternal) {
			return AuthOutput{}, ErrInternal
		}
		return AuthOutput{}, rateLimitedErr
	}

	// Code mismatch: increment attempts and return unauthorized.
	otpMatch, err := auth.VerifyOTP(otp.CodeHash, in.OTP)
	if err != nil {
		s.logger.Error("verifying otp hash", "err", err)
		return AuthOutput{}, ErrInternal
	}
	if !otpMatch {
		failedAttemptAt := time.Now().UTC()
		if err := s.otpAttemptRecorder.InsertFailedOTPAttempt(ctx, tx, user.ID, "email_verification", failedAttemptAt); err != nil {
			s.logger.Error("recording failed verify-otp attempt", "err", err)
			return AuthOutput{}, ErrInternal
		}
		if err := s.repo.IncrementOTPAttempts(ctx, tx, otp.ID); err != nil {
			s.logger.Error("incrementing otp attempts", "err", err)
		} else if commitErr := tx.Commit(ctx); commitErr != nil {
			s.logger.Error("committing otp attempt increment", "err", commitErr)
		}
		return AuthOutput{}, fmt.Errorf("%w: invalid or expired OTP", ErrUnauthorized)
	}

	// Code matches — mark OTP used.
	if err := s.repo.MarkOTPUsed(ctx, tx, otp.ID); err != nil {
		s.logger.Error("marking otp used", "err", err)
		return AuthOutput{}, ErrInternal
	}

	// Activate the user account.
	if err := s.repo.SetEmailVerified(ctx, tx, user.ID); err != nil {
		s.logger.Error("verifying user email", "err", err)
		return AuthOutput{}, ErrInternal
	}

	// Look up the full user profile.
	fullUser, err := s.repo.GetUserByID(ctx, tx, user.ID)
	if err != nil {
		s.logger.Error("querying user profile after verification", "err", err)
		return AuthOutput{}, ErrInternal
	}

	// Issue access token.
	accessToken, err := auth.IssueAccessToken(fullUser.ID, fullUser.Email, s.jwtSecret, s.jwtAccessTokenTTL)
	if err != nil {
		s.logger.Error("issuing access token", "err", err)
		return AuthOutput{}, ErrInternal
	}

	// Generate and store refresh token.
	rawRefresh, hashRefresh, err := auth.GenerateRefreshToken()
	if err != nil {
		s.logger.Error("generating refresh token", "err", err)
		return AuthOutput{}, ErrInternal
	}
	refreshExpiresAt := time.Now().UTC().Add(s.jwtRefreshTokenTTL)
	if err := s.repo.InsertRefreshToken(ctx, tx, fullUser.ID, hashRefresh, refreshExpiresAt); err != nil {
		s.logger.Error("inserting refresh token", "err", err)
		return AuthOutput{}, ErrInternal
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("committing verify-otp transaction", "err", err)
		return AuthOutput{}, ErrInternal
	}

	// Subscription is intentionally nil here: verify-otp completes initial
	// registration, so the user cannot have an active subscription yet.
	return AuthOutput{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		User: UserProfile{
			ID:                 fullUser.ID,
			Email:              fullUser.Email,
			Name:               fullUser.Name,
			Username:           fullUser.Username,
			ProfilePictureURL:  fullUser.ProfilePictureURL,
			LeaderboardVisible: fullUser.LeaderboardVisible,
			CreatedAt:          fullUser.CreatedAt,
		},
	}, nil
}

// Login handles the login use case: verify credentials, issue tokens,
// and return the auth result with entitlement info.
func (s *AuthService) Login(ctx context.Context, in LoginInput) (AuthOutput, error) {
	email := normalizeEmail(in.Email)

	// Query verified user by email.
	user, err := s.repo.GetVerifiedUserByEmail(ctx, s.pool, email)
	if errors.Is(err, repository.ErrNotFound) {
		// Perform a dummy bcrypt comparison so the response latency is
		// indistinguishable from a real password check, preventing
		// timing-based account enumeration.
		auth.DummyCheckPassword(in.Password)
		return AuthOutput{}, fmt.Errorf("%w: invalid email or password", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("querying user for login", "err", err)
		return AuthOutput{}, ErrInternal
	}

	// Check password.
	match, err := auth.CheckPassword(user.PasswordHash, in.Password)
	if err != nil {
		s.logger.Error("checking password", "err", err)
		return AuthOutput{}, ErrInternal
	}
	if !match {
		return AuthOutput{}, fmt.Errorf("%w: invalid email or password", ErrUnauthorized)
	}

	subscription, err := s.lookupEntitlementSnapshot(ctx, user.ID)
	if err != nil {
		s.logger.Error("querying user entitlement", "user_id", user.ID, "err", err)
		return AuthOutput{}, ErrInternal
	}

	// Issue tokens and store refresh token inside a transaction so that
	// the access token is never returned without a persisted refresh token.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning login transaction", "err", err)
		return AuthOutput{}, ErrInternal
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// Issue access token.
	accessToken, err := auth.IssueAccessToken(user.ID, email, s.jwtSecret, s.jwtAccessTokenTTL)
	if err != nil {
		s.logger.Error("issuing access token", "err", err)
		return AuthOutput{}, ErrInternal
	}

	// Generate and store refresh token.
	rawRefresh, hashRefresh, err := auth.GenerateRefreshToken()
	if err != nil {
		s.logger.Error("generating refresh token", "err", err)
		return AuthOutput{}, ErrInternal
	}
	refreshExpiresAt := time.Now().UTC().Add(s.jwtRefreshTokenTTL)
	if err := s.repo.InsertRefreshToken(ctx, tx, user.ID, hashRefresh, refreshExpiresAt); err != nil {
		s.logger.Error("inserting refresh token", "err", err)
		return AuthOutput{}, ErrInternal
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("committing login transaction", "err", err)
		return AuthOutput{}, ErrInternal
	}

	return AuthOutput{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		User: UserProfile{
			ID:                 user.ID,
			Email:              email,
			Name:               user.Name,
			Username:           user.Username,
			ProfilePictureURL:  user.ProfilePictureURL,
			LeaderboardVisible: user.LeaderboardVisible,
			CreatedAt:          user.CreatedAt,
			Subscription:       subscription,
		},
	}, nil
}

// Refresh handles the token refresh use case: validate the old refresh token,
// rotate it, issue new tokens. Implements reuse detection (revoked token
// triggers revocation of all user tokens).
func (s *AuthService) Refresh(ctx context.Context, in RefreshInput) (RefreshOutput, error) {
	tokenHash := auth.HashRefreshToken(in.RefreshToken)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning refresh transaction", "err", err)
		return RefreshOutput{}, ErrInternal
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	tok, err := s.repo.GetRefreshTokenByHashForUpdate(ctx, tx, tokenHash)
	if errors.Is(err, repository.ErrNotFound) {
		return RefreshOutput{}, fmt.Errorf("%w: invalid refresh token", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("querying refresh token", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	// Reuse detection: if the token has been revoked, revoke ALL tokens for
	// the user (indicates possible token theft).
	if tok.Revoked {
		if err := s.repo.RevokeAllRefreshTokens(ctx, tx, tok.UserID); err != nil {
			s.logger.Error("revoking all refresh tokens after reuse", "err", err)
		} else if commitErr := tx.Commit(ctx); commitErr != nil {
			s.logger.Error("commit failed after reuse-detection revoke-all: tokens may still be active",
				"user_id", tok.UserID, "err", commitErr)
		}
		return RefreshOutput{}, fmt.Errorf("%w: invalid refresh token", ErrUnauthorized)
	}

	// Expired token.
	if time.Now().UTC().After(tok.ExpiresAt) {
		tx.Rollback(ctx) //nolint:errcheck // best-effort; deferred rollback is the safety net
		return RefreshOutput{}, fmt.Errorf("%w: invalid refresh token", ErrUnauthorized)
	}

	if err := s.repo.RevokeRefreshToken(ctx, tx, tok.ID); err != nil {
		s.logger.Error("revoking current refresh token", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	// Look up user email for the access token claims.
	email, err := s.repo.GetUserEmailByID(ctx, tx, tok.UserID)
	if err != nil {
		s.logger.Error("querying user email for refresh", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	// Issue new access token.
	accessToken, err := auth.IssueAccessToken(tok.UserID, email, s.jwtSecret, s.jwtAccessTokenTTL)
	if err != nil {
		s.logger.Error("issuing access token", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	// Generate and store new refresh token.
	rawRefresh, hashRefresh, err := auth.GenerateRefreshToken()
	if err != nil {
		s.logger.Error("generating refresh token", "err", err)
		return RefreshOutput{}, ErrInternal
	}
	refreshExpiresAt := time.Now().UTC().Add(s.jwtRefreshTokenTTL)
	if err := s.repo.InsertRefreshToken(ctx, tx, tok.UserID, hashRefresh, refreshExpiresAt); err != nil {
		s.logger.Error("inserting new refresh token", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("committing refresh transaction", "err", err)
		return RefreshOutput{}, ErrInternal
	}

	return RefreshOutput{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
	}, nil
}

// ForgotPasswordMinDuration is the minimum wall-clock time the
// forgot-password operation will take after the caller begins timing.
// This normalizes response timing to reduce timing-based account enumeration.
const ForgotPasswordMinDuration = 500 * time.Millisecond

// ForgotPassword handles the forgot-password use case: look up the verified
// user, generate an OTP, store it, and send a password-reset email. Returns
// a generic message when the user is unknown or unverified (anti-enumeration),
// but returns ErrInternal for infrastructure or delivery failures.
//
// The caller is responsible for applying the timing floor
// (ForgotPasswordMinDuration) around this call.
func (s *AuthService) ForgotPassword(ctx context.Context, in ForgotPasswordInput) (MessageOutput, error) {
	email := normalizeEmail(in.Email)

	msg := MessageOutput{Message: "If the email is registered, a reset code has been sent."}

	if err := s.mailer.Probe(ctx); err != nil {
		s.logger.Error("forgot-password failure", "stage", "mail_probe", "err", err)
		return MessageOutput{}, ErrInternal
	}

	// Generate OTP.
	otp, err := auth.GenerateOTP()
	if err != nil {
		s.logger.Error("forgot-password failure", "stage", "otp_generation", "err", err)
		return MessageOutput{}, ErrInternal
	}
	otpHash, err := auth.HashOTP(otp)
	if err != nil {
		s.logger.Error("forgot-password failure", "stage", "otp_generation", "err", err)
		return MessageOutput{}, ErrInternal
	}

	// Insert OTP inside a transaction for consistency.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("forgot-password failure", "stage", "begin_tx", "err", err)
		return MessageOutput{}, ErrInternal
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// Lock the user row before replacing password-reset codes so concurrent
	// forgot-password requests cannot leave multiple active codes behind.
	user, err := s.repo.GetUserByEmailForUpdate(ctx, tx, email)
	if errors.Is(err, repository.ErrNotFound) {
		// Unknown user — return generic message to prevent enumeration.
		return msg, nil
	}
	if err != nil {
		s.logger.Error("forgot-password failure", "stage", "user_lookup", "err", err)
		return MessageOutput{}, ErrInternal
	}
	if !user.EmailVerified {
		return msg, nil
	}

	if err := s.repo.DeleteOTPsByUserAndPurpose(ctx, tx, user.ID, "password_reset"); err != nil {
		s.logger.Error("forgot-password failure", "stage", "otp_delete_existing", "err", err)
		return MessageOutput{}, ErrInternal
	}

	expiresAt := time.Now().UTC().Add(auth.OTPExpiry)
	otpID, err := s.repo.InsertOTP(ctx, tx, user.ID, otpHash, "password_reset", expiresAt)
	if err != nil {
		s.logger.Error("forgot-password failure", "stage", "otp_insert", "err", err)
		return MessageOutput{}, ErrInternal
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("forgot-password failure", "stage", "commit_tx", "err", err)
		return MessageOutput{}, ErrInternal
	}

	// Send OTP email.
	if err := s.mailer.SendOTP(ctx, email, otp, mail.PurposePasswordReset); err != nil {
		s.logger.Error("sending password reset email", "email", redact.Email(email), "err", err)
		s.cleanupFailedPasswordResetOTP(otpID)

		return MessageOutput{}, ErrInternal
	}

	return msg, nil
}

// cleanupFailedPasswordResetOTP removes the password-reset OTP row created
// for a forgot-password request whose delivery later failed. It runs in a new
// transaction and logs any errors without propagating them.
func (s *AuthService) cleanupFailedPasswordResetOTP(otpID string) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()

	cleanupTx, txErr := s.pool.Begin(cleanupCtx)
	if txErr != nil {
		s.logger.Error("beginning password-reset email failure cleanup transaction", "err", txErr)
		return
	}

	if err := s.repo.DeleteOTPByID(cleanupCtx, cleanupTx, otpID); err != nil {
		s.logger.Error("cleaning up password-reset otp after email failure", "err", err)
		if rbErr := cleanupTx.Rollback(cleanupCtx); rbErr != nil {
			s.logger.Error("rolling back password-reset email failure cleanup transaction", "err", rbErr)
		}
		return
	}

	if commitErr := cleanupTx.Commit(cleanupCtx); commitErr != nil {
		s.logger.Error("committing password-reset email failure cleanup transaction", "err", commitErr)
	}
}

// ResetPassword handles the reset-password use case: validate the OTP,
// update the password, and revoke all refresh tokens.
func (s *AuthService) ResetPassword(ctx context.Context, in ResetPasswordInput) (MessageOutput, error) {
	email := normalizeEmail(in.Email)

	// Hash the new password before starting the transaction so the
	// (potentially slow) bcrypt work does not hold the row lock.
	hash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		s.logger.Error("hashing new password", "err", err)
		return MessageOutput{}, ErrInternal
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning reset-password transaction", "err", err)
		return MessageOutput{}, ErrInternal
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// Resolve email to verified user.
	user, err := s.repo.GetVerifiedUserByEmail(ctx, tx, email)
	if errors.Is(err, repository.ErrNotFound) {
		return MessageOutput{}, fmt.Errorf("%w: invalid or expired OTP", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("resolving user for reset-password", "err", err)
		return MessageOutput{}, ErrInternal
	}

	// Query active OTP with FOR UPDATE lock.
	otp, err := s.repo.GetActiveOTPForUpdate(ctx, tx, user.ID, "password_reset")
	if errors.Is(err, repository.ErrNotFound) {
		return MessageOutput{}, fmt.Errorf("%w: invalid or expired OTP", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("querying password reset otp", "err", err)
		return MessageOutput{}, ErrInternal
	}

	// Too many failed attempts for this password reset window.
	attemptWindowStart := time.Now().UTC().Add(-auth.OTPExpiry)
	if s.otpAttemptBudgeter == nil || s.otpAttemptRetrier == nil || s.otpAttemptRecorder == nil {
		s.logger.Error("reset-password failed-attempt persistence not configured")
		return MessageOutput{}, ErrInternal
	}

	attemptsUsed, err := s.otpAttemptBudgeter.CountFailedOTPAttemptsSinceForUpdate(ctx, tx, user.ID, "password_reset", attemptWindowStart)
	if err != nil {
		s.logger.Error("counting reset-password failed attempts", "err", err)
		return MessageOutput{}, ErrInternal
	}
	if attemptsUsed >= auth.MaxOTPAttempts {
		rateLimitedErr := s.newOTPAttemptsRateLimitedError(ctx, tx, user.ID, "password_reset", attemptWindowStart)
		if errors.Is(rateLimitedErr, ErrInternal) {
			return MessageOutput{}, ErrInternal
		}
		return MessageOutput{}, rateLimitedErr
	}

	// Code mismatch: increment attempts and return unauthorized.
	resetOTPMatch, err := auth.VerifyOTP(otp.CodeHash, in.OTP)
	if err != nil {
		s.logger.Error("verifying reset otp hash", "err", err)
		return MessageOutput{}, ErrInternal
	}
	if !resetOTPMatch {
		failedAttemptAt := time.Now().UTC()
		if err := s.otpAttemptRecorder.InsertFailedOTPAttempt(ctx, tx, user.ID, "password_reset", failedAttemptAt); err != nil {
			s.logger.Error("recording failed reset-password attempt", "err", err)
			return MessageOutput{}, ErrInternal
		}
		if err := s.repo.IncrementOTPAttempts(ctx, tx, otp.ID); err != nil {
			s.logger.Error("incrementing otp attempts", "err", err)
		} else if commitErr := tx.Commit(ctx); commitErr != nil {
			s.logger.Error("committing otp attempt increment", "err", commitErr)
		}
		return MessageOutput{}, fmt.Errorf("%w: invalid or expired OTP", ErrUnauthorized)
	}

	// Mark OTP used.
	if err := s.repo.MarkOTPUsed(ctx, tx, otp.ID); err != nil {
		s.logger.Error("marking reset otp used", "err", err)
		return MessageOutput{}, ErrInternal
	}
	if err := s.repo.DeleteOTPsByUserAndPurpose(ctx, tx, user.ID, "password_reset"); err != nil {
		s.logger.Error("deleting password reset otps after successful reset", "err", err)
		return MessageOutput{}, ErrInternal
	}

	// Update user's password.
	affected, err := s.repo.UpdatePassword(ctx, tx, user.ID, hash)
	if err != nil {
		s.logger.Error("updating user password", "err", err)
		return MessageOutput{}, ErrInternal
	}
	if affected == 0 {
		return MessageOutput{}, fmt.Errorf("%w: invalid or expired OTP", ErrUnauthorized)
	}

	// Revoke all refresh tokens for this user.
	if err := s.repo.RevokeAllRefreshTokens(ctx, tx, user.ID); err != nil {
		s.logger.Error("revoking refresh tokens after password reset", "err", err)
		return MessageOutput{}, ErrInternal
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("committing reset-password transaction", "err", err)
		return MessageOutput{}, ErrInternal
	}

	return MessageOutput{Message: "Password reset successfully"}, nil
}

// GetMe handles the get-profile use case: look up the authenticated user
// and return their profile with entitlement info.
func (s *AuthService) GetMe(ctx context.Context, in GetMeInput) (UserProfile, error) {
	user, err := s.repo.GetVerifiedUserByID(ctx, s.pool, in.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return UserProfile{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("querying user profile", "err", err)
		return UserProfile{}, ErrInternal
	}

	subscription, err := s.lookupEntitlementSnapshot(ctx, user.ID)
	if err != nil {
		s.logger.Error("querying user entitlement", "user_id", user.ID, "err", err)
		return UserProfile{}, ErrInternal
	}

	return UserProfile{
		ID:                 user.ID,
		Email:              user.Email,
		Name:               user.Name,
		Username:           user.Username,
		ProfilePictureURL:  user.ProfilePictureURL,
		LeaderboardVisible: user.LeaderboardVisible,
		CreatedAt:          user.CreatedAt,
		Subscription:       subscription,
	}, nil
}

// UpdateMe handles the profile update use case: apply the provided fields
// to the authenticated user's profile and return the updated profile with
// entitlement info.
func (s *AuthService) UpdateMe(ctx context.Context, in UpdateMeInput) (UserProfile, error) {
	updated, err := s.repo.UpdateUser(ctx, s.pool, in.UserID, in.Name, in.Username, in.LeaderboardVisible)
	if errors.Is(err, repository.ErrNotFound) {
		return UserProfile{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
	}
	if err != nil {
		if isUniqueViolation(err, "users_username_key") || isUniqueViolation(err, "idx_users_username") {
			return UserProfile{}, fmt.Errorf("%w: username already taken", ErrConflict)
		}
		s.logger.Error("updating user profile", "err", err)
		return UserProfile{}, ErrInternal
	}

	subscription, err := s.lookupEntitlementSnapshot(ctx, updated.ID)
	if err != nil {
		s.logger.Error("querying user entitlement", "user_id", updated.ID, "err", err)
		return UserProfile{}, ErrInternal
	}

	return UserProfile{
		ID:                 updated.ID,
		Email:              updated.Email,
		Name:               updated.Name,
		Username:           updated.Username,
		ProfilePictureURL:  updated.ProfilePictureURL,
		LeaderboardVisible: updated.LeaderboardVisible,
		CreatedAt:          updated.CreatedAt,
		Subscription:       subscription,
	}, nil
}

// CheckUsernameAvailable handles the username availability check use case.
func (s *AuthService) CheckUsernameAvailable(ctx context.Context, in UsernameAvailableInput) (UsernameAvailableOutput, error) {
	taken, err := s.repo.IsUsernameTaken(ctx, s.pool, in.Username, in.UserID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return UsernameAvailableOutput{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
		}
		s.logger.Error("checking username availability", "err", err)
		return UsernameAvailableOutput{}, ErrInternal
	}
	return UsernameAvailableOutput{Available: !taken}, nil
}

// DeleteMe handles the account deletion use case: verify the password,
// then permanently delete the user and all associated data.
func (s *AuthService) DeleteMe(ctx context.Context, in DeleteMeInput) (MessageOutput, error) {
	// Look up the verified user to get the password hash.
	user, err := s.repo.GetVerifiedUserByID(ctx, s.pool, in.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return MessageOutput{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("querying user for account deletion", "err", err)
		return MessageOutput{}, ErrInternal
	}

	// Verify password.
	match, err := auth.CheckPassword(user.PasswordHash, in.Password)
	if err != nil {
		s.logger.Error("checking password for account deletion", "err", err)
		return MessageOutput{}, ErrInternal
	}
	if !match {
		return MessageOutput{}, fmt.Errorf("%w: incorrect password", ErrUnauthorized)
	}

	// Delete the user. ON DELETE CASCADE handles dependent rows.
	if err := s.repo.DeleteUser(ctx, s.pool, in.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return MessageOutput{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
		}
		s.logger.Error("deleting user account", "err", err)
		return MessageOutput{}, ErrInternal
	}

	return MessageOutput{Message: "Account deleted"}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// normalizeEmail lowercases and trims whitespace from an email address.
func normalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

type retryAfterError struct {
	err        error
	retryAfter time.Duration
}

func (e retryAfterError) Error() string {
	return e.err.Error()
}

func (e retryAfterError) Unwrap() error {
	return e.err
}

func (e retryAfterError) RetryAfter() time.Duration {
	return e.retryAfter
}

// RetryAfter extracts retry-after metadata from a service error, if present.
func RetryAfter(err error) (time.Duration, bool) {
	var withRetryAfter interface {
		RetryAfter() time.Duration
	}
	if !errors.As(err, &withRetryAfter) {
		return 0, false
	}
	return withRetryAfter.RetryAfter(), true
}

func (s *AuthService) newOTPAttemptsRateLimitedError(ctx context.Context, db repository.DBTX, userID, purpose string, since time.Time) error {
	retryAfter := auth.OTPExpiry
	oldestAttemptAt, err := s.otpAttemptRetrier.GetOldestFailedOTPAttemptSinceForUpdate(ctx, db, userID, purpose, since)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		s.logger.Error("querying oldest failed otp attempt for retry-after", "purpose", purpose, "err", err)
		return ErrInternal
	}
	if err == nil {
		retryAfter = time.Until(oldestAttemptAt.Add(auth.OTPExpiry))
	}

	if retryAfter < time.Second {
		retryAfter = time.Second
	}

	return retryAfterError{
		err:        fmt.Errorf("%w: too many verification attempts", ErrRateLimited),
		retryAfter: retryAfter,
	}
}

// generateUsername returns a random username in the form "user_" + 24 hex chars
// (96 bits of entropy).
func generateUsername() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random username: %w", err)
	}
	return "user_" + hex.EncodeToString(b), nil
}

// isUniqueViolation checks whether a Postgres error is a unique_violation (23505)
// on the specified constraint name. If constraint is empty, any unique violation matches.
func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	if pgErr.Code != pgerrcode.UniqueViolation {
		return false
	}
	if constraint != "" && pgErr.ConstraintName != constraint {
		return false
	}
	return true
}

// lookupEntitlementSnapshot fetches the user's stored premium entitlement snapshot.
// If no snapshot exists, nil is returned.
func (s *AuthService) lookupEntitlementSnapshot(ctx context.Context, userID string) (*EntitlementInfo, error) {
	row, err := s.repo.GetEntitlementSnapshot(ctx, s.pool, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	info := &EntitlementInfo{
		Entitlement:      row.Entitlement,
		IsActive:         row.IsActive,
		CurrentPeriodEnd: row.CurrentPeriodEnd,
	}
	return info, nil
}
