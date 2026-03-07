package handler

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/internal/validate"
)

// AuthHandler handles authentication-related HTTP requests.
type AuthHandler struct {
	pool               *pgxpool.Pool
	mailer             mail.EmailSender
	jwtSecret          string
	jwtAccessTokenTTL  time.Duration
	jwtRefreshTokenTTL time.Duration
	logger             *slog.Logger
}

// NewAuthHandler creates an AuthHandler with the given dependencies.
func NewAuthHandler(
	pool *pgxpool.Pool,
	mailer mail.EmailSender,
	jwtSecret string,
	jwtAccessTokenTTL time.Duration,
	jwtRefreshTokenTTL time.Duration,
	logger *slog.Logger,
) *AuthHandler {
	return &AuthHandler{
		pool:               pool,
		mailer:             mailer,
		jwtSecret:          jwtSecret,
		jwtAccessTokenTTL:  jwtAccessTokenTTL,
		jwtRefreshTokenTTL: jwtRefreshTokenTTL,
		logger:             logger,
	}
}

// registerRequest is the expected JSON body for POST /api/v1/auth/register.
type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// registerResponse is the JSON response for a successful registration.
type registerResponse struct {
	OTPRequired bool `json:"otp_required"`
}

// normalizeEmail lowercases and trims whitespace from an email address.
func normalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// maskEmail returns a partially redacted email suitable for safe logging.
// "alice@example.com" becomes "a***@e***.com".
func maskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "***"
	}
	local := email[:at]
	domain := email[at+1:]

	maskedLocal := string(local[0]) + "***"

	dot := strings.LastIndex(domain, ".")
	if dot <= 0 {
		return maskedLocal + "@***"
	}
	maskedDomain := string(domain[0]) + "***" + domain[dot:]

	return maskedLocal + "@" + maskedDomain
}

// generateUsername returns a random username in the form "user_" + 8 hex chars.
func generateUsername() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random username: %w", err)
	}
	return "user_" + hex.EncodeToString(b), nil
}

// decodeJSONBody decodes the request body into dst. It distinguishes an
// oversized body (MaxBytesError → "Request body too large") from a
// malformed/invalid payload, writing the appropriate 422 error response
// and returning false when decoding fails.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	err := json.NewDecoder(r.Body).Decode(dst)
	if err == nil {
		return true
	}
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		apperror.ValidationError(w, "Request body too large", nil)
		return false
	}
	apperror.ValidationError(w, "Invalid request body", nil)
	return false
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
	PlanID           string                 `json:"plan_id"`
	PlanName         string                 `json:"plan_name"`
	Status           string                 `json:"status"`
	IsStudent        bool                   `json:"is_student"`
	CurrentPeriodEnd *string                `json:"current_period_end"`
	Features         map[string]interface{} `json:"features"`
}

// lookupSubscription fetches the user's active subscription (status IN
// ('active','trial')). It is intentionally non-fatal: a subscription lookup
// failure must never block authentication. If no active subscription exists
// or the query errors, nil is returned and the caller proceeds with
// user.Subscription = nil. This matches BACKEND_SPEC Section 5.3 where
// subscription is null for free-tier users.
func (h *AuthHandler) lookupSubscription(ctx context.Context, userID string) *subscriptionResponse {
	var sub subscriptionResponse
	var periodEnd *time.Time
	var featuresJSON []byte

	err := h.pool.QueryRow(ctx,
		`SELECT sp.id, sp.name, us.status, us.is_student, us.current_period_end, sp.features_json
		 FROM user_subscriptions us
		 JOIN subscription_plans sp ON sp.id = us.plan_id
		 WHERE us.user_id = $1 AND us.status IN ('active', 'trial')
		 ORDER BY us.created_at DESC
		 LIMIT 1`,
		userID,
	).Scan(&sub.PlanID, &sub.PlanName, &sub.Status, &sub.IsStudent, &periodEnd, &featuresJSON)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		h.logger.Warn("querying user subscription", "user_id", userID, "err", err)
		return nil
	}

	if periodEnd != nil {
		s := periodEnd.UTC().Format(time.RFC3339)
		sub.CurrentPeriodEnd = &s
	}
	if len(featuresJSON) > 0 {
		if err := json.Unmarshal(featuresJSON, &sub.Features); err != nil {
			h.logger.Warn("unmarshalling subscription features", "user_id", userID, "err", err)
		}
	}
	if sub.Features == nil {
		sub.Features = make(map[string]interface{})
	}
	return &sub
}

// Register handles POST /api/v1/auth/register.
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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

	email := normalizeEmail(req.Email)

	// Check if a verified user already exists with this email.
	var existingVerified bool
	err := h.pool.QueryRow(ctx,
		"SELECT email_verified FROM users WHERE email = $1", email,
	).Scan(&existingVerified)

	if err == nil {
		// User row found.
		if existingVerified {
			apperror.Conflict(w, "Email already registered")
			return
		}
		// Unverified user exists: atomically clean up stale email-verification
		// OTP rows and the user row. Only 'email_verification' OTPs are
		// deleted because password_reset OTPs cannot exist for an unverified
		// user (forgot-password checks email_verified = true). otp_codes is
		// linked by user_email (not FK), so there is no cascade from the
		// users DELETE.
		cleanupTx, txErr := h.pool.Begin(ctx)
		if txErr != nil {
			h.logger.Error("beginning cleanup transaction", "err", txErr)
			apperror.InternalError(w)
			return
		}
		defer cleanupTx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

		if _, txErr = cleanupTx.Exec(ctx,
			"DELETE FROM otp_codes WHERE user_email = $1 AND purpose = 'email_verification'",
			email,
		); txErr != nil {
			h.logger.Error("deleting stale otp rows", "err", txErr)
			apperror.InternalError(w)
			return
		}
		if _, txErr = cleanupTx.Exec(ctx, "DELETE FROM users WHERE email = $1 AND email_verified = false", email); txErr != nil {
			h.logger.Error("deleting stale unverified user", "err", txErr)
			apperror.InternalError(w)
			return
		}
		if txErr = cleanupTx.Commit(ctx); txErr != nil {
			h.logger.Error("committing cleanup transaction", "err", txErr)
			apperror.InternalError(w)
			return
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		h.logger.Error("checking existing user", "err", err)
		apperror.InternalError(w)
		return
	}

	// Hash the password.
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		h.logger.Error("hashing password", "err", err)
		apperror.InternalError(w)
		return
	}

	// Insert the user and OTP rows in a transaction, then send the email
	// outside the transaction to avoid holding a DB connection open during
	// a potentially slow SMTP call. If the email send fails, we clean up
	// the committed rows so there are no orphans.
	regTx, err := h.pool.Begin(ctx)
	if err != nil {
		h.logger.Error("beginning registration transaction", "err", err)
		apperror.InternalError(w)
		return
	}
	defer regTx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// Insert the new unverified user. The generated random username may
	// collide with an existing one (idx_users_username), so we retry with a
	// fresh username up to maxUsernameRetries times before giving up.
	// Each attempt runs inside a savepoint (nested tx) so that a constraint
	// violation does not abort the outer transaction.
	const maxUsernameRetries = 3
	var userID string
	for attempt := range maxUsernameRetries {
		username, genErr := generateUsername()
		if genErr != nil {
			h.logger.Error("generating username", "err", genErr)
			apperror.InternalError(w)
			return
		}

		// pgx v5: Begin on an existing Tx creates a SAVEPOINT.
		sp, spErr := regTx.Begin(ctx)
		if spErr != nil {
			h.logger.Error("creating savepoint for user insert", "err", spErr)
			apperror.InternalError(w)
			return
		}

		insertErr := sp.QueryRow(ctx,
			`INSERT INTO users (email, password_hash, username, email_verified)
			 VALUES ($1, $2, $3, false)
			 RETURNING id`,
			email, hash, username,
		).Scan(&userID)
		if insertErr == nil {
			// Release the savepoint on success.
			if err := sp.Commit(ctx); err != nil {
				h.logger.Error("releasing savepoint after user insert", "err", err)
				apperror.InternalError(w)
				return
			}
			break
		}

		// Roll back the savepoint so the outer transaction stays usable.
		// If the rollback itself fails the outer tx is poisoned; bail out.
		if rbErr := sp.Rollback(ctx); rbErr != nil {
			h.logger.Error("rolling back savepoint after user insert failure", "err", rbErr)
			apperror.InternalError(w)
			return
		}

		// Email already taken (race with concurrent registration).
		if isUniqueViolation(insertErr, "users_email_key") {
			apperror.Conflict(w, "Email already registered")
			return
		}

		// Username collision — retry with a new random username.
		// The schema defines two unique constraints on username:
		// "users_username_key" (column-level UNIQUE) and
		// "idx_users_username" (expression index on lower(username)).
		if isUniqueViolation(insertErr, "users_username_key") ||
			isUniqueViolation(insertErr, "idx_users_username") {
			h.logger.Warn("username collision, retrying", "attempt", attempt+1)
			continue
		}

		h.logger.Error("inserting user", "err", insertErr)
		apperror.InternalError(w)
		return
	}
	if userID == "" {
		h.logger.Error("failed to generate unique username after retries")
		apperror.InternalError(w)
		return
	}

	// Generate OTP.
	otp, err := auth.GenerateOTP()
	if err != nil {
		h.logger.Error("generating otp", "err", err)
		apperror.InternalError(w)
		return
	}

	// Insert OTP into otp_codes.
	expiresAt := time.Now().UTC().Add(auth.OTPExpiry)
	_, err = regTx.Exec(ctx,
		`INSERT INTO otp_codes (user_email, code, purpose, expires_at)
		 VALUES ($1, $2, 'email_verification', $3)`,
		email, otp, expiresAt,
	)
	if err != nil {
		h.logger.Error("inserting otp", "err", err)
		apperror.InternalError(w)
		return
	}

	// Commit the user + OTP rows before the SMTP call so we do not hold a
	// DB connection/transaction open for the duration of the network send.
	if err := regTx.Commit(ctx); err != nil {
		h.logger.Error("committing registration transaction", "err", err)
		apperror.InternalError(w)
		return
	}

	// Send OTP via email. If this fails, clean up the committed rows so
	// there are no orphaned unverified users or dangling OTP rows.
	if err := h.mailer.SendOTP(ctx, email, otp, mail.PurposeEmailVerification); err != nil {
		h.logger.Error("sending otp email", "email", maskEmail(email), "err", err)
		if _, cleanupErr := h.pool.Exec(ctx,
			"DELETE FROM otp_codes WHERE user_email = $1 AND purpose = 'email_verification' AND used = false",
			email,
		); cleanupErr != nil {
			h.logger.Error("cleaning up otp after email failure", "err", cleanupErr)
		}
		if _, cleanupErr := h.pool.Exec(ctx,
			"DELETE FROM users WHERE email = $1 AND email_verified = false",
			email,
		); cleanupErr != nil {
			h.logger.Error("cleaning up user after email failure", "err", cleanupErr)
		}
		apperror.InternalError(w)
		return
	}

	// Success.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(registerResponse{OTPRequired: true}); err != nil {
		h.logger.Error("encoding register response", "err", err)
	}
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

// VerifyOTP handles POST /api/v1/auth/verify-otp.
func (h *AuthHandler) VerifyOTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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

	email := normalizeEmail(req.Email)

	// Perform the entire OTP lookup, validation, and consumption inside a
	// single transaction with FOR UPDATE row locking to eliminate the
	// TOCTOU window where a concurrent request could consume the same OTP
	// between the SELECT and the write.
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.logger.Error("beginning verify-otp transaction", "err", err)
		apperror.InternalError(w)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// Query otp_codes for the most recent unused, non-expired row matching
	// email and purpose = 'email_verification'. FOR UPDATE locks the row
	// so concurrent requests block until this transaction completes.
	var otpID, storedCode string
	var attempts int
	err = tx.QueryRow(ctx,
		`SELECT id, code, attempts
		 FROM otp_codes
		 WHERE user_email = $1
		   AND purpose = 'email_verification'
		   AND used = false
		   AND expires_at > now()
		 ORDER BY created_at DESC
		 LIMIT 1
		 FOR UPDATE`,
		email,
	).Scan(&otpID, &storedCode, &attempts)

	if errors.Is(err, pgx.ErrNoRows) {
		apperror.Unauthorized(w, "Invalid or expired OTP")
		return
	}
	if err != nil {
		h.logger.Error("querying otp", "err", err)
		apperror.InternalError(w)
		return
	}

	// Too many attempts on this OTP.
	if attempts >= auth.MaxOTPAttempts {
		apperror.RateLimited(w, "Too many verification attempts")
		return
	}

	// Code mismatch: increment attempts and return 401.
	if subtle.ConstantTimeCompare([]byte(storedCode), []byte(req.OTP)) != 1 {
		if _, err := tx.Exec(ctx,
			"UPDATE otp_codes SET attempts = attempts + 1 WHERE id = $1", otpID,
		); err != nil {
			h.logger.Error("incrementing otp attempts", "err", err)
			// Still return Unauthorized even if the increment fails.
		} else if commitErr := tx.Commit(ctx); commitErr != nil {
			h.logger.Error("committing otp attempt increment", "err", commitErr)
		}
		apperror.Unauthorized(w, "Invalid or expired OTP")
		return
	}

	// Code matches — mark OTP used within the same locked transaction.
	if _, err := tx.Exec(ctx,
		"UPDATE otp_codes SET used = true WHERE id = $1", otpID,
	); err != nil {
		h.logger.Error("marking otp used", "err", err)
		apperror.InternalError(w)
		return
	}

	// Activate the user account.
	if _, err := tx.Exec(ctx,
		"UPDATE users SET email_verified = true WHERE email = $1", email,
	); err != nil {
		h.logger.Error("verifying user email", "err", err)
		apperror.InternalError(w)
		return
	}

	// Look up the user profile.
	var user userResponse
	var createdAt time.Time
	err = tx.QueryRow(ctx,
		`SELECT id, email, name, username, profile_picture_url, leaderboard_visible, created_at
		 FROM users
		 WHERE email = $1`,
		email,
	).Scan(&user.ID, &user.Email, &user.Name, &user.Username,
		&user.ProfilePictureURL, &user.LeaderboardVisible, &createdAt)
	if err != nil {
		h.logger.Error("querying user profile after verification", "err", err)
		apperror.InternalError(w)
		return
	}
	user.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	// Subscription is intentionally nil here: verify-otp completes initial
	// registration, so the user cannot have an active subscription yet.
	// Login uses h.lookupSubscription for returning users who may have one.

	// Issue access token.
	accessToken, err := auth.IssueAccessToken(user.ID, user.Email, h.jwtSecret, h.jwtAccessTokenTTL)
	if err != nil {
		h.logger.Error("issuing access token", "err", err)
		apperror.InternalError(w)
		return
	}

	// Generate and store refresh token.
	rawRefresh, hashRefresh, err := auth.GenerateRefreshToken()
	if err != nil {
		h.logger.Error("generating refresh token", "err", err)
		apperror.InternalError(w)
		return
	}
	refreshExpiresAt := time.Now().UTC().Add(h.jwtRefreshTokenTTL)
	if _, err := tx.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)`,
		user.ID, hashRefresh, refreshExpiresAt,
	); err != nil {
		h.logger.Error("inserting refresh token", "err", err)
		apperror.InternalError(w)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("committing verify-otp transaction", "err", err)
		apperror.InternalError(w)
		return
	}

	// Return success response.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(authResponse{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		User:         user,
	}); err != nil {
		h.logger.Error("encoding verify-otp response", "err", err)
	}
}

// Login handles POST /api/v1/auth/login.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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

	email := normalizeEmail(req.Email)

	// Query verified user by email.
	var userID, passwordHash, name, username string
	var profilePictureURL *string
	var leaderboardVisible bool
	var createdAt time.Time
	err := h.pool.QueryRow(ctx,
		`SELECT id, password_hash, name, username, profile_picture_url, leaderboard_visible, created_at
		 FROM users
		 WHERE email = $1 AND email_verified = true`,
		email,
	).Scan(&userID, &passwordHash, &name, &username,
		&profilePictureURL, &leaderboardVisible, &createdAt)

	if errors.Is(err, pgx.ErrNoRows) {
		apperror.Unauthorized(w, "Invalid email or password")
		return
	}
	if err != nil {
		h.logger.Error("querying user for login", "err", err)
		apperror.InternalError(w)
		return
	}

	// Check password.
	match, err := auth.CheckPassword(passwordHash, req.Password)
	if err != nil {
		h.logger.Error("checking password", "err", err)
		apperror.InternalError(w)
		return
	}
	if !match {
		apperror.Unauthorized(w, "Invalid email or password")
		return
	}

	// Issue access token.
	accessToken, err := auth.IssueAccessToken(userID, email, h.jwtSecret, h.jwtAccessTokenTTL)
	if err != nil {
		h.logger.Error("issuing access token", "err", err)
		apperror.InternalError(w)
		return
	}

	// Generate and store refresh token inside a transaction so that the
	// token insert and any future login-time mutations stay atomic.
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.logger.Error("beginning login transaction", "err", err)
		apperror.InternalError(w)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	rawRefresh, hashRefresh, err := auth.GenerateRefreshToken()
	if err != nil {
		h.logger.Error("generating refresh token", "err", err)
		apperror.InternalError(w)
		return
	}
	refreshExpiresAt := time.Now().UTC().Add(h.jwtRefreshTokenTTL)
	if _, err := tx.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)`,
		userID, hashRefresh, refreshExpiresAt,
	); err != nil {
		h.logger.Error("inserting refresh token", "err", err)
		apperror.InternalError(w)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("committing login transaction", "err", err)
		apperror.InternalError(w)
		return
	}

	user := userResponse{
		ID:                 userID,
		Email:              email,
		Name:               name,
		Username:           username,
		ProfilePictureURL:  profilePictureURL,
		LeaderboardVisible: leaderboardVisible,
		CreatedAt:          createdAt.UTC().Format(time.RFC3339),
		Subscription:       h.lookupSubscription(ctx, userID),
	}

	// Return success response.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(authResponse{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		User:         user,
	}); err != nil {
		h.logger.Error("encoding login response", "err", err)
	}
}

// Refresh handles POST /api/v1/auth/refresh.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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

	// Hash the incoming token and look up in DB.
	tokenHash := auth.HashRefreshToken(req.RefreshToken)

	var tokenID, userID string
	var revoked bool
	var expiresAt time.Time
	err := h.pool.QueryRow(ctx,
		`SELECT id, user_id, revoked, expires_at
		 FROM refresh_tokens
		 WHERE token_hash = $1`,
		tokenHash,
	).Scan(&tokenID, &userID, &revoked, &expiresAt)

	if errors.Is(err, pgx.ErrNoRows) {
		apperror.Unauthorized(w, "Invalid refresh token")
		return
	}
	if err != nil {
		h.logger.Error("querying refresh token", "err", err)
		apperror.InternalError(w)
		return
	}

	// Reuse detection: if the token has been revoked, revoke ALL tokens for
	// the user (indicates possible token theft).
	if revoked {
		if _, err := h.pool.Exec(ctx,
			"UPDATE refresh_tokens SET revoked = true WHERE user_id = $1",
			userID,
		); err != nil {
			h.logger.Error("revoking all refresh tokens after reuse", "err", err)
		}
		apperror.Unauthorized(w, "Invalid refresh token")
		return
	}

	// Expired token.
	if time.Now().UTC().After(expiresAt) {
		apperror.Unauthorized(w, "Invalid refresh token")
		return
	}

	// Revoke the current token and issue a new pair.
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.logger.Error("beginning refresh transaction", "err", err)
		apperror.InternalError(w)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	if _, err := tx.Exec(ctx,
		"UPDATE refresh_tokens SET revoked = true WHERE id = $1", tokenID,
	); err != nil {
		h.logger.Error("revoking current refresh token", "err", err)
		apperror.InternalError(w)
		return
	}

	// Look up user email for the access token claims.
	var email string
	err = tx.QueryRow(ctx,
		"SELECT email FROM users WHERE id = $1", userID,
	).Scan(&email)
	if err != nil {
		h.logger.Error("querying user email for refresh", "err", err)
		apperror.InternalError(w)
		return
	}

	// Issue new access token.
	accessToken, err := auth.IssueAccessToken(userID, email, h.jwtSecret, h.jwtAccessTokenTTL)
	if err != nil {
		h.logger.Error("issuing access token", "err", err)
		apperror.InternalError(w)
		return
	}

	// Generate and store new refresh token.
	rawRefresh, hashRefresh, err := auth.GenerateRefreshToken()
	if err != nil {
		h.logger.Error("generating refresh token", "err", err)
		apperror.InternalError(w)
		return
	}
	refreshExpiresAt := time.Now().UTC().Add(h.jwtRefreshTokenTTL)
	if _, err := tx.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)`,
		userID, hashRefresh, refreshExpiresAt,
	); err != nil {
		h.logger.Error("inserting new refresh token", "err", err)
		apperror.InternalError(w)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("committing refresh transaction", "err", err)
		apperror.InternalError(w)
		return
	}

	// Return success response.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(refreshResponse{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
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

	email := normalizeEmail(req.Email)

	// Always return 200 to prevent email enumeration.
	respondOK := func() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(messageResponse{
			Message: "If the email is registered, a reset code has been sent.",
		}); err != nil {
			h.logger.Error("encoding forgot-password response", "err", err)
		}
	}

	// Look up verified user.
	var userExists bool
	err := h.pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM users WHERE email = $1 AND email_verified = true)",
		email,
	).Scan(&userExists)
	if err != nil {
		// Return 200 even on DB errors to avoid leaking account existence
		// through response timing or status code differences.
		h.logger.Error("checking user for forgot-password", "err", err)
		respondOK()
		return
	}

	if !userExists {
		respondOK()
		return
	}

	// Generate OTP.
	otp, err := auth.GenerateOTP()
	if err != nil {
		h.logger.Error("generating otp for password reset", "err", err)
		respondOK()
		return
	}

	// Insert OTP into otp_codes.
	expiresAt := time.Now().UTC().Add(auth.OTPExpiry)
	_, err = h.pool.Exec(ctx,
		`INSERT INTO otp_codes (user_email, code, purpose, expires_at)
		 VALUES ($1, $2, 'password_reset', $3)`,
		email, otp, expiresAt,
	)
	if err != nil {
		h.logger.Error("inserting password reset otp", "err", err)
		respondOK()
		return
	}

	// Send OTP email. On failure, log and still return 200.
	if err := h.mailer.SendOTP(ctx, email, otp, mail.PurposePasswordReset); err != nil {
		h.logger.Error("sending password reset email", "email", maskEmail(email), "err", err)
	}

	respondOK()
}

// ResetPassword handles POST /api/v1/auth/reset-password.
func (h *AuthHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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

	email := normalizeEmail(req.Email)

	// Hash the new password before starting the transaction so the
	// (potentially slow) bcrypt work does not hold the row lock.
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		h.logger.Error("hashing new password", "err", err)
		apperror.InternalError(w)
		return
	}

	// Perform the entire OTP lookup, validation, consumption, and password
	// update inside a single transaction with FOR UPDATE row locking to
	// eliminate the TOCTOU window.
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		h.logger.Error("beginning reset-password transaction", "err", err)
		apperror.InternalError(w)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	// Query otp_codes for the most recent unused, non-expired row matching
	// email and purpose = 'password_reset'. FOR UPDATE locks the row
	// so concurrent requests block until this transaction completes.
	var otpID, storedCode string
	var attempts int
	err = tx.QueryRow(ctx,
		`SELECT id, code, attempts
		 FROM otp_codes
		 WHERE user_email = $1
		   AND purpose = 'password_reset'
		   AND used = false
		   AND expires_at > now()
		 ORDER BY created_at DESC
		 LIMIT 1
		 FOR UPDATE`,
		email,
	).Scan(&otpID, &storedCode, &attempts)

	if errors.Is(err, pgx.ErrNoRows) {
		apperror.Unauthorized(w, "Invalid or expired OTP")
		return
	}
	if err != nil {
		h.logger.Error("querying password reset otp", "err", err)
		apperror.InternalError(w)
		return
	}

	// Too many attempts on this OTP.
	if attempts >= auth.MaxOTPAttempts {
		apperror.RateLimited(w, "Too many verification attempts")
		return
	}

	// Code mismatch: increment attempts and return 401.
	if subtle.ConstantTimeCompare([]byte(storedCode), []byte(req.OTP)) != 1 {
		if _, err := tx.Exec(ctx,
			"UPDATE otp_codes SET attempts = attempts + 1 WHERE id = $1", otpID,
		); err != nil {
			h.logger.Error("incrementing otp attempts", "err", err)
			// Still return Unauthorized even if the increment fails.
		} else if commitErr := tx.Commit(ctx); commitErr != nil {
			h.logger.Error("committing otp attempt increment", "err", commitErr)
		}
		apperror.Unauthorized(w, "Invalid or expired OTP")
		return
	}

	// Mark OTP used.
	if _, err := tx.Exec(ctx,
		"UPDATE otp_codes SET used = true WHERE id = $1", otpID,
	); err != nil {
		h.logger.Error("marking reset otp used", "err", err)
		apperror.InternalError(w)
		return
	}

	// Update user's password.
	tag, err := tx.Exec(ctx,
		"UPDATE users SET password_hash = $1, updated_at = now() WHERE email = $2 AND email_verified = true",
		hash, email,
	)
	if err != nil {
		h.logger.Error("updating user password", "err", err)
		apperror.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		// The OTP was valid but no verified user exists for this email.
		// Return Unauthorized to avoid leaking account state.
		apperror.Unauthorized(w, "Invalid or expired OTP")
		return
	}

	// Revoke all refresh tokens for this user.
	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked = true
		 WHERE user_id = (SELECT id FROM users WHERE email = $1 AND email_verified = true)`,
		email,
	); err != nil {
		h.logger.Error("revoking refresh tokens after password reset", "err", err)
		apperror.InternalError(w)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("committing reset-password transaction", "err", err)
		apperror.InternalError(w)
		return
	}

	// Return success.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(messageResponse{
		Message: "Password reset successfully",
	}); err != nil {
		h.logger.Error("encoding reset-password response", "err", err)
	}
}
