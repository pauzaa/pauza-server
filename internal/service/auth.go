package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

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
	Subscription       *SubscriptionInfo
}

// SubscriptionInfo represents an active subscription.
type SubscriptionInfo struct {
	PlanID           string
	PlanName         string
	Status           string
	IsStudent        bool
	CurrentPeriodEnd *time.Time
	Features         map[string]any
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

// ---------------------------------------------------------------------------
// Service
// ---------------------------------------------------------------------------

// AuthService encapsulates authentication business logic, coordinating
// between the repository, auth utilities, and mail sender.
type AuthService struct {
	pool               *pgxpool.Pool
	repo               repository.AuthRepository
	mailer             mail.Sender
	jwtSecret          string
	jwtAccessTokenTTL  time.Duration
	jwtRefreshTokenTTL time.Duration
	logger             *slog.Logger
}

// NewAuthService creates an AuthService with the given dependencies.
func NewAuthService(
	pool *pgxpool.Pool,
	repo repository.AuthRepository,
	mailer mail.Sender,
	jwtSecret string,
	jwtAccessTokenTTL time.Duration,
	jwtRefreshTokenTTL time.Duration,
	logger *slog.Logger,
) *AuthService {
	return &AuthService{
		pool:               pool,
		repo:               repo,
		mailer:             mailer,
		jwtSecret:          jwtSecret,
		jwtAccessTokenTTL:  jwtAccessTokenTTL,
		jwtRefreshTokenTTL: jwtRefreshTokenTTL,
		logger:             logger,
	}
}

// Register handles the registration use case: create an unverified user,
// generate an OTP, send it via email, and return a result indicating that
// OTP verification is required. On SMTP failure the created rows are
// cleaned up.
func (s *AuthService) Register(ctx context.Context, in RegisterInput) (RegisterOutput, error) {
	email := normalizeEmail(in.Email)

	// Hash the password before starting the transaction so the (potentially
	// slow) bcrypt work does not hold any row locks.
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		s.logger.Error("hashing password", "err", err)
		return RegisterOutput{}, ErrInternal
	}

	// Perform the existing-user check, stale-unverified cleanup, and new
	// user + OTP insert inside a single transaction.
	regTx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning registration transaction", "err", err)
		return RegisterOutput{}, ErrInternal
	}
	defer regTx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// Check if a user already exists with this email (FOR UPDATE lock).
	existing, err := s.repo.GetUserByEmailForUpdate(ctx, regTx, email)
	if err == nil {
		// User row found.
		if existing.EmailVerified {
			return RegisterOutput{}, fmt.Errorf("%w: email already registered", ErrConflict)
		}
		// Unverified user exists — clean up OTPs and user row.
		if err := s.repo.DeleteOTPsByUserAndPurpose(ctx, regTx, existing.ID, "email_verification"); err != nil {
			s.logger.Error("deleting stale otp rows", "err", err)
			return RegisterOutput{}, ErrInternal
		}
		deleted, err := s.repo.DeleteUnverifiedUser(ctx, regTx, existing.ID)
		if err != nil {
			s.logger.Error("deleting stale unverified user", "err", err)
			return RegisterOutput{}, ErrInternal
		}
		if deleted == 0 {
			s.logger.Error("stale-user delete affected 0 rows despite FOR UPDATE lock",
				"existing_user_id", existing.ID,
				"email", redact.Email(email))
			return RegisterOutput{}, fmt.Errorf("%w: email already registered", ErrConflict)
		}
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
		s.cleanupFailedRegistration(ctx, otpID, userID)
		return RegisterOutput{}, ErrInternal
	}

	return RegisterOutput{OTPRequired: true}, nil
}

// cleanupFailedRegistration removes the OTP and unverified user rows created
// during a registration whose SMTP send failed. It runs in a new transaction
// and logs any errors without propagating them.
func (s *AuthService) cleanupFailedRegistration(ctx context.Context, otpID, userID string) {
	cleanupTx, txErr := s.pool.Begin(ctx)
	if txErr != nil {
		s.logger.Error("beginning smtp-failure cleanup transaction", "err", txErr)
		return
	}

	if err := s.repo.DeleteOTPByID(ctx, cleanupTx, otpID); err != nil {
		s.logger.Error("cleaning up otp after email failure", "err", err)
		if rbErr := cleanupTx.Rollback(ctx); rbErr != nil {
			s.logger.Error("rolling back smtp-failure cleanup transaction", "err", rbErr)
		}
		return
	}

	deleted, err := s.repo.DeleteUnverifiedUser(ctx, cleanupTx, userID)
	if err != nil {
		s.logger.Error("cleaning up user after email failure", "err", err)
		if rbErr := cleanupTx.Rollback(ctx); rbErr != nil {
			s.logger.Error("rolling back smtp-failure cleanup transaction", "err", rbErr)
		}
		return
	}
	if deleted == 0 {
		s.logger.Debug("smtp-failure cleanup: user already removed by concurrent registration",
			"user_id", userID)
	}

	if commitErr := cleanupTx.Commit(ctx); commitErr != nil {
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

	// Too many attempts on this OTP.
	if otp.Attempts >= auth.MaxOTPAttempts {
		return AuthOutput{}, fmt.Errorf("%w: too many verification attempts", ErrRateLimited)
	}

	// Code mismatch: increment attempts and return unauthorized.
	otpMatch, err := auth.VerifyOTP(otp.CodeHash, in.OTP)
	if err != nil {
		s.logger.Error("verifying otp hash", "err", err)
		return AuthOutput{}, ErrInternal
	}
	if !otpMatch {
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
// and return the auth result with subscription info.
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
			Subscription:       s.lookupSubscription(ctx, user.ID),
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
// a generic message regardless of whether the user exists (anti-enumeration).
//
// The caller is responsible for applying the timing floor
// (ForgotPasswordMinDuration) around this call.
func (s *AuthService) ForgotPassword(ctx context.Context, in ForgotPasswordInput) (MessageOutput, error) {
	email := normalizeEmail(in.Email)

	msg := MessageOutput{Message: "If the email is registered, a reset code has been sent."}

	// Look up verified user.
	user, err := s.repo.GetVerifiedUserByEmail(ctx, s.pool, email)
	if errors.Is(err, repository.ErrNotFound) {
		// Unknown user — return generic message to prevent enumeration.
		return msg, nil
	}
	if err != nil {
		s.logger.Error("forgot-password failure", "stage", "user_lookup", "err", err)
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

	expiresAt := time.Now().UTC().Add(auth.OTPExpiry)
	if _, err := s.repo.InsertOTP(ctx, tx, user.ID, otpHash, "password_reset", expiresAt); err != nil {
		s.logger.Error("forgot-password failure", "stage", "otp_insert", "err", err)
		return MessageOutput{}, ErrInternal
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("forgot-password failure", "stage", "commit_tx", "err", err)
		return MessageOutput{}, ErrInternal
	}

	// Send OTP email. On failure, log and still return success — SMTP
	// outages should not change the response shape visible to the client.
	if err := s.mailer.SendOTP(ctx, email, otp, mail.PurposePasswordReset); err != nil {
		s.logger.Error("sending password reset email", "email", redact.Email(email), "err", err)
	}

	return msg, nil
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

	// Too many attempts on this OTP.
	if otp.Attempts >= auth.MaxOTPAttempts {
		return MessageOutput{}, fmt.Errorf("%w: too many verification attempts", ErrRateLimited)
	}

	// Code mismatch: increment attempts and return unauthorized.
	resetOTPMatch, err := auth.VerifyOTP(otp.CodeHash, in.OTP)
	if err != nil {
		s.logger.Error("verifying reset otp hash", "err", err)
		return MessageOutput{}, ErrInternal
	}
	if !resetOTPMatch {
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
// and return their profile with subscription info.
func (s *AuthService) GetMe(ctx context.Context, in GetMeInput) (UserProfile, error) {
	user, err := s.repo.GetVerifiedUserByID(ctx, s.pool, in.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return UserProfile{}, fmt.Errorf("%w: missing or invalid authentication", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("querying user profile", "err", err)
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
		Subscription:       s.lookupSubscription(ctx, user.ID),
	}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// normalizeEmail lowercases and trims whitespace from an email address.
func normalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
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

// lookupSubscription fetches the user's active subscription. It is intentionally
// non-fatal: a subscription lookup failure must never block authentication.
// If no active subscription exists or the query errors, nil is returned.
func (s *AuthService) lookupSubscription(ctx context.Context, userID string) *SubscriptionInfo {
	row, err := s.repo.GetActiveSubscription(ctx, s.pool, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	if err != nil {
		s.logger.Warn("querying user subscription", "user_id", userID, "err", err)
		return nil
	}

	info := &SubscriptionInfo{
		PlanID:           row.PlanID,
		PlanName:         row.PlanName,
		Status:           row.Status,
		IsStudent:        row.IsStudent,
		CurrentPeriodEnd: row.CurrentPeriodEnd,
	}

	if len(row.FeaturesJSON) > 0 {
		if err := json.Unmarshal(row.FeaturesJSON, &info.Features); err != nil {
			s.logger.Warn("unmarshalling subscription features", "user_id", userID, "err", err)
		}
	}
	if info.Features == nil {
		info.Features = make(map[string]any)
	}
	return info
}
