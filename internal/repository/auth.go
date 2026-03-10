package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is the common subset of methods shared by *pgxpool.Pool and pgx.Tx.
// Repository methods accept a DBTX so callers can run queries against the
// pool directly or inside an explicit transaction.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Compile-time checks: both pool and transaction satisfy DBTX.
var (
	_ DBTX = (*pgxpool.Pool)(nil)
	_ DBTX = (pgx.Tx)(nil)
)

// ---------------------------------------------------------------------------
// Row types
// ---------------------------------------------------------------------------

// UserRow holds the columns returned by user lookups.
type UserRow struct {
	ID                 string
	Email              string
	PasswordHash       string
	Name               string
	Username           string
	ProfilePictureURL  *string
	LeaderboardVisible bool
	EmailVerified      bool
	CreatedAt          time.Time
}

// OTPRow holds the columns returned by OTP lookups.
type OTPRow struct {
	ID       string
	CodeHash string
	Attempts int
}

// RefreshTokenRow holds the columns returned by refresh-token lookups.
type RefreshTokenRow struct {
	ID        string
	UserID    string
	Revoked   bool
	ExpiresAt time.Time
}

// EntitlementRow holds the columns returned by entitlement lookups.
type EntitlementRow struct {
	Entitlement      string
	IsActive         bool
	CurrentPeriodEnd *time.Time
}

// ---------------------------------------------------------------------------
// Interface
// ---------------------------------------------------------------------------

// AuthRepository defines the data-access operations used by the auth
// handlers. Every method accepts a DBTX so it can be called against
// the pool or inside an explicit transaction.
type AuthRepository interface {
	// --- users ---

	// GetUserByEmail returns the user matching the (already-lowercased)
	// email. Returns ErrNotFound when no row exists.
	GetUserByEmail(ctx context.Context, db DBTX, email string) (UserRow, error)

	// GetUserByEmailForUpdate is like GetUserByEmail but acquires a
	// FOR UPDATE row lock.
	GetUserByEmailForUpdate(ctx context.Context, db DBTX, email string) (UserRow, error)

	// GetVerifiedUserByEmail returns the verified (email_verified = true)
	// user matching the email. Returns ErrNotFound when no row exists.
	GetVerifiedUserByEmail(ctx context.Context, db DBTX, email string) (UserRow, error)

	// GetVerifiedUserByID returns the verified user matching the given
	// ID. Returns ErrNotFound when no row exists.
	GetVerifiedUserByID(ctx context.Context, db DBTX, userID string) (UserRow, error)

	// GetVerifiedUserByIDForUpdate is like GetVerifiedUserByID but acquires a
	// FOR UPDATE row lock.
	GetVerifiedUserByIDForUpdate(ctx context.Context, db DBTX, userID string) (UserRow, error)

	// GetUserByID returns the user matching the given ID (regardless of
	// verification status). Returns ErrNotFound when no row exists.
	GetUserByID(ctx context.Context, db DBTX, userID string) (UserRow, error)

	// InsertUser creates a new unverified user and returns the generated
	// UUID. The caller is responsible for handling unique-violation errors
	// (email or username collisions).
	InsertUser(ctx context.Context, db DBTX, email, passwordHash, username string) (string, error)

	// SetEmailVerified marks a user's email as verified.
	SetEmailVerified(ctx context.Context, db DBTX, userID string) error

	// UpdatePassword sets a new password hash for a verified user and
	// returns the number of rows affected.
	UpdatePassword(ctx context.Context, db DBTX, userID, passwordHash string) (int64, error)

	// DeleteUnverifiedUser deletes an unverified user by ID and returns
	// the number of rows affected.
	DeleteUnverifiedUser(ctx context.Context, db DBTX, userID string) (int64, error)

	// UpdateUser applies partial updates to a verified user's profile.
	// Only non-nil fields are modified. Returns the full updated UserRow.
	// The caller is responsible for handling unique-violation errors
	// (username collisions).
	UpdateUser(ctx context.Context, db DBTX, userID string, name *string, username *string, leaderboardVisible *bool) (UserRow, error)

	// IsUsernameTaken returns true if a username is already taken by any
	// user (case-insensitive). The optional excludeUserID, when non-empty,
	// excludes that user from the check (useful for "is my current
	// username still available" scenarios).
	IsUsernameTaken(ctx context.Context, db DBTX, username string, excludeUserID string) (bool, error)

	// DeleteUser permanently deletes a verified user by ID. Dependent
	// rows are removed by ON DELETE CASCADE constraints.
	DeleteUser(ctx context.Context, db DBTX, userID string) error

	// --- otp_codes ---

	// InsertOTP inserts a new OTP row and returns the generated UUID.
	InsertOTP(ctx context.Context, db DBTX, userID, codeHash, purpose string, expiresAt time.Time) (string, error)

	// GetActiveOTPForUpdate returns the most recent unused, non-expired
	// OTP matching user and purpose, locking the row with FOR UPDATE.
	// Returns ErrNotFound when no matching row exists.
	GetActiveOTPForUpdate(ctx context.Context, db DBTX, userID, purpose string) (OTPRow, error)

	// IncrementOTPAttempts increments the attempts counter on an OTP row.
	IncrementOTPAttempts(ctx context.Context, db DBTX, otpID string) error

	// MarkOTPUsed sets used = true on an OTP row.
	MarkOTPUsed(ctx context.Context, db DBTX, otpID string) error

	// DeleteOTPsByUserAndPurpose removes all OTP rows for a user/purpose.
	DeleteOTPsByUserAndPurpose(ctx context.Context, db DBTX, userID, purpose string) error

	// DeleteOTPByID removes a single OTP row by its ID.
	DeleteOTPByID(ctx context.Context, db DBTX, otpID string) error

	// --- refresh_tokens ---

	// InsertRefreshToken stores a new refresh token hash.
	InsertRefreshToken(ctx context.Context, db DBTX, userID, tokenHash string, expiresAt time.Time) error

	// GetRefreshTokenByHashForUpdate returns the refresh token matching
	// the given hash, locking the row with FOR UPDATE. Returns
	// ErrNotFound when no matching row exists.
	GetRefreshTokenByHashForUpdate(ctx context.Context, db DBTX, tokenHash string) (RefreshTokenRow, error)

	// RevokeRefreshToken revokes a single refresh token by ID and records the
	// first time that token was revoked.
	RevokeRefreshToken(ctx context.Context, db DBTX, tokenID string) error

	// RevokeAllRefreshTokens revokes every refresh token for a user and records
	// the first time each token was revoked.
	RevokeAllRefreshTokens(ctx context.Context, db DBTX, userID string) error

	// GetUserEmailByID returns the email for a user. Returns ErrNotFound
	// when no row exists.
	GetUserEmailByID(ctx context.Context, db DBTX, userID string) (string, error)

	// --- entitlements ---

	// GetEntitlementSnapshot returns the user's stored premium entitlement
	// snapshot, if any. Returns ErrNotFound when no premium snapshot exists.
	GetEntitlementSnapshot(ctx context.Context, db DBTX, userID string) (EntitlementRow, error)
}

// ErrNotFound is returned when a queried row does not exist.
var ErrNotFound = errors.New("not found")

// Compile-time check: PgxAuthRepository satisfies AuthRepository.
var _ AuthRepository = (*PgxAuthRepository)(nil)

// ---------------------------------------------------------------------------
// pgx implementation
// ---------------------------------------------------------------------------

// PgxAuthRepository implements AuthRepository using pgx queries.
type PgxAuthRepository struct{}

// NewPgxAuthRepository returns a PgxAuthRepository.
func NewPgxAuthRepository() *PgxAuthRepository {
	return &PgxAuthRepository{}
}

// userColumns is the shared column list used by all user-scanning queries.
const userColumns = `id, email, password_hash, name, username, profile_picture_url,
		        leaderboard_visible, email_verified, created_at`

// scanUserRow scans a pgx.Row into a UserRow. The row must contain exactly
// the columns listed in userColumns, in that order.
func scanUserRow(row pgx.Row) (UserRow, error) {
	var u UserRow
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.Username,
		&u.ProfilePictureURL, &u.LeaderboardVisible, &u.EmailVerified, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserRow{}, ErrNotFound
	}
	if err != nil {
		return UserRow{}, err
	}
	return u, nil
}

// --- users ----------------------------------------------------------------

func (r *PgxAuthRepository) GetUserByEmail(ctx context.Context, db DBTX, email string) (UserRow, error) {
	row := db.QueryRow(ctx,
		`SELECT `+userColumns+`
		 FROM users WHERE lower(email) = $1`,
		email,
	)
	u, err := scanUserRow(row)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return UserRow{}, fmt.Errorf("getting user by email: %w", err)
	}
	return u, err
}

func (r *PgxAuthRepository) GetUserByEmailForUpdate(ctx context.Context, db DBTX, email string) (UserRow, error) {
	row := db.QueryRow(ctx,
		`SELECT `+userColumns+`
		 FROM users WHERE lower(email) = $1 FOR UPDATE`,
		email,
	)
	u, err := scanUserRow(row)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return UserRow{}, fmt.Errorf("getting user by email for update: %w", err)
	}
	return u, err
}

func (r *PgxAuthRepository) GetVerifiedUserByEmail(ctx context.Context, db DBTX, email string) (UserRow, error) {
	row := db.QueryRow(ctx,
		`SELECT `+userColumns+`
		 FROM users WHERE lower(email) = $1 AND email_verified = true`,
		email,
	)
	u, err := scanUserRow(row)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return UserRow{}, fmt.Errorf("getting verified user by email: %w", err)
	}
	return u, err
}

func (r *PgxAuthRepository) GetVerifiedUserByID(ctx context.Context, db DBTX, userID string) (UserRow, error) {
	row := db.QueryRow(ctx,
		`SELECT `+userColumns+`
		 FROM users WHERE id = $1 AND email_verified = true`,
		userID,
	)
	u, err := scanUserRow(row)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return UserRow{}, fmt.Errorf("getting verified user by id: %w", err)
	}
	return u, err
}

func (r *PgxAuthRepository) GetVerifiedUserByIDForUpdate(ctx context.Context, db DBTX, userID string) (UserRow, error) {
	row := db.QueryRow(ctx,
		`SELECT `+userColumns+`
		 FROM users WHERE id = $1 AND email_verified = true FOR UPDATE`,
		userID,
	)
	u, err := scanUserRow(row)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return UserRow{}, fmt.Errorf("getting verified user by id for update: %w", err)
	}
	return u, err
}

func (r *PgxAuthRepository) GetUserByID(ctx context.Context, db DBTX, userID string) (UserRow, error) {
	row := db.QueryRow(ctx,
		`SELECT `+userColumns+`
		 FROM users WHERE id = $1`,
		userID,
	)
	u, err := scanUserRow(row)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return UserRow{}, fmt.Errorf("getting user by id: %w", err)
	}
	return u, err
}

func (r *PgxAuthRepository) InsertUser(ctx context.Context, db DBTX, email, passwordHash, username string) (string, error) {
	var id string
	err := db.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, username, email_verified)
		 VALUES ($1, $2, $3, false)
		 RETURNING id`,
		email, passwordHash, username,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("inserting user: %w", err)
	}
	return id, nil
}

func (r *PgxAuthRepository) SetEmailVerified(ctx context.Context, db DBTX, userID string) error {
	_, err := db.Exec(ctx,
		"UPDATE users SET email_verified = true WHERE id = $1",
		userID,
	)
	if err != nil {
		return fmt.Errorf("setting email verified: %w", err)
	}
	return nil
}

func (r *PgxAuthRepository) UpdatePassword(ctx context.Context, db DBTX, userID, passwordHash string) (int64, error) {
	tag, err := db.Exec(ctx,
		"UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2 AND email_verified = true",
		passwordHash, userID,
	)
	if err != nil {
		return 0, fmt.Errorf("updating password: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *PgxAuthRepository) DeleteUnverifiedUser(ctx context.Context, db DBTX, userID string) (int64, error) {
	tag, err := db.Exec(ctx,
		"DELETE FROM users WHERE id = $1 AND email_verified = false",
		userID,
	)
	if err != nil {
		return 0, fmt.Errorf("deleting unverified user: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *PgxAuthRepository) UpdateUser(ctx context.Context, db DBTX, userID string, name *string, username *string, leaderboardVisible *bool) (UserRow, error) {
	// Build a dynamic SET clause from the non-nil fields.
	var setClauses []string
	var args []any
	argIdx := 1

	if name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *name)
		argIdx++
	}
	if username != nil {
		setClauses = append(setClauses, fmt.Sprintf("username = $%d", argIdx))
		args = append(args, *username)
		argIdx++
	}
	if leaderboardVisible != nil {
		setClauses = append(setClauses, fmt.Sprintf("leaderboard_visible = $%d", argIdx))
		args = append(args, *leaderboardVisible)
		argIdx++
	}

	// If no fields to update, just return the current user.
	if len(setClauses) == 0 {
		return r.GetVerifiedUserByID(ctx, db, userID)
	}

	setClauses = append(setClauses, "updated_at = now()")
	args = append(args, userID)

	query := fmt.Sprintf(
		"UPDATE users SET %s WHERE id = $%d AND email_verified = true RETURNING %s",
		strings.Join(setClauses, ", "),
		argIdx,
		userColumns,
	)

	row := db.QueryRow(ctx, query, args...)
	u, err := scanUserRow(row)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return UserRow{}, fmt.Errorf("updating user: %w", err)
	}
	return u, err
}

func (r *PgxAuthRepository) IsUsernameTaken(ctx context.Context, db DBTX, username string, excludeUserID string) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1
			FROM users AS other
			WHERE auth_user.id = $2
			  AND lower(other.username) = lower($1)
			  AND other.id != auth_user.id
		)
		FROM users AS auth_user
		WHERE auth_user.id = $2
		  AND auth_user.email_verified = true`,
		username, excludeUserID,
	).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("checking username availability: %w", err)
	}
	return exists, nil
}

func (r *PgxAuthRepository) DeleteUser(ctx context.Context, db DBTX, userID string) error {
	tag, err := db.Exec(ctx,
		"DELETE FROM users WHERE id = $1 AND email_verified = true",
		userID,
	)
	if err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- otp_codes ------------------------------------------------------------

func (r *PgxAuthRepository) InsertOTP(ctx context.Context, db DBTX, userID, codeHash, purpose string, expiresAt time.Time) (string, error) {
	var id string
	err := db.QueryRow(ctx,
		`INSERT INTO otp_codes (user_id, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		userID, codeHash, purpose, expiresAt,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("inserting otp: %w", err)
	}
	return id, nil
}

func (r *PgxAuthRepository) GetActiveOTPForUpdate(ctx context.Context, db DBTX, userID, purpose string) (OTPRow, error) {
	var o OTPRow
	err := db.QueryRow(ctx,
		`SELECT id, code_hash, attempts
		 FROM otp_codes
		 WHERE user_id = $1
		   AND purpose = $2
		   AND used = false
		   AND expires_at > now()
		 ORDER BY created_at DESC
		 LIMIT 1
		 FOR UPDATE`,
		userID, purpose,
	).Scan(&o.ID, &o.CodeHash, &o.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return OTPRow{}, ErrNotFound
	}
	if err != nil {
		return OTPRow{}, fmt.Errorf("getting active otp for update: %w", err)
	}
	return o, nil
}

func (r *PgxAuthRepository) CountFailedOTPAttemptsSinceForUpdate(ctx context.Context, db DBTX, userID, purpose string, since time.Time) (int, error) {
	var attempts int
	err := db.QueryRow(ctx,
		`SELECT COUNT(*)
		 FROM (
		 	SELECT 1
		 	FROM otp_failed_attempts
		 	WHERE user_id = $1
		 	  AND purpose = $2
		 	  AND attempted_at >= $3
		 	FOR UPDATE
		 ) AS locked_attempts`,
		userID, purpose, since,
	).Scan(&attempts)
	if err != nil {
		return 0, fmt.Errorf("counting failed otp attempts since for update: %w", err)
	}
	return attempts, nil
}

func (r *PgxAuthRepository) GetOldestFailedOTPAttemptSinceForUpdate(ctx context.Context, db DBTX, userID, purpose string, since time.Time) (time.Time, error) {
	var attemptedAt time.Time
	err := db.QueryRow(ctx,
		`SELECT attempted_at
		 FROM otp_failed_attempts
		 WHERE user_id = $1
		   AND purpose = $2
		   AND attempted_at >= $3
		 ORDER BY attempted_at ASC
		 LIMIT 1
		 FOR UPDATE`,
		userID, purpose, since,
	).Scan(&attemptedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("getting oldest failed otp attempt since for update: %w", err)
	}
	return attemptedAt, nil
}

func (r *PgxAuthRepository) InsertFailedOTPAttempt(ctx context.Context, db DBTX, userID, purpose string, attemptedAt time.Time) error {
	_, err := db.Exec(ctx,
		`INSERT INTO otp_failed_attempts (user_id, purpose, attempted_at)
		 VALUES ($1, $2, $3)`,
		userID, purpose, attemptedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting failed otp attempt: %w", err)
	}
	return nil
}

func (r *PgxAuthRepository) IncrementOTPAttempts(ctx context.Context, db DBTX, otpID string) error {
	_, err := db.Exec(ctx,
		"UPDATE otp_codes SET attempts = attempts + 1 WHERE id = $1",
		otpID,
	)
	if err != nil {
		return fmt.Errorf("incrementing otp attempts: %w", err)
	}
	return nil
}

func (r *PgxAuthRepository) MarkOTPUsed(ctx context.Context, db DBTX, otpID string) error {
	_, err := db.Exec(ctx,
		"UPDATE otp_codes SET used = true WHERE id = $1",
		otpID,
	)
	if err != nil {
		return fmt.Errorf("marking otp used: %w", err)
	}
	return nil
}

func (r *PgxAuthRepository) DeleteOTPsByUserAndPurpose(ctx context.Context, db DBTX, userID, purpose string) error {
	_, err := db.Exec(ctx,
		"DELETE FROM otp_codes WHERE user_id = $1 AND purpose = $2",
		userID, purpose,
	)
	if err != nil {
		return fmt.Errorf("deleting otps by user and purpose: %w", err)
	}
	return nil
}

func (r *PgxAuthRepository) DeleteOTPByID(ctx context.Context, db DBTX, otpID string) error {
	_, err := db.Exec(ctx,
		"DELETE FROM otp_codes WHERE id = $1",
		otpID,
	)
	if err != nil {
		return fmt.Errorf("deleting otp by id: %w", err)
	}
	return nil
}

// --- refresh_tokens -------------------------------------------------------

func (r *PgxAuthRepository) InsertRefreshToken(ctx context.Context, db DBTX, userID, tokenHash string, expiresAt time.Time) error {
	_, err := db.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("inserting refresh token: %w", err)
	}
	return nil
}

func (r *PgxAuthRepository) GetRefreshTokenByHashForUpdate(ctx context.Context, db DBTX, tokenHash string) (RefreshTokenRow, error) {
	var t RefreshTokenRow
	err := db.QueryRow(ctx,
		`SELECT id, user_id, revoked, expires_at
		 FROM refresh_tokens
		 WHERE token_hash = $1
		 FOR UPDATE`,
		tokenHash,
	).Scan(&t.ID, &t.UserID, &t.Revoked, &t.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RefreshTokenRow{}, ErrNotFound
	}
	if err != nil {
		return RefreshTokenRow{}, fmt.Errorf("getting refresh token by hash for update: %w", err)
	}
	return t, nil
}

func (r *PgxAuthRepository) RevokeRefreshToken(ctx context.Context, db DBTX, tokenID string) error {
	_, err := db.Exec(ctx,
		`UPDATE refresh_tokens
		 SET revoked = true,
		     revoked_at = COALESCE(revoked_at, now())
		 WHERE id = $1`,
		tokenID,
	)
	if err != nil {
		return fmt.Errorf("revoking refresh token: %w", err)
	}
	return nil
}

func (r *PgxAuthRepository) RevokeAllRefreshTokens(ctx context.Context, db DBTX, userID string) error {
	_, err := db.Exec(ctx,
		`UPDATE refresh_tokens
		 SET revoked = true,
		     revoked_at = COALESCE(revoked_at, now())
		 WHERE user_id = $1`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("revoking all refresh tokens: %w", err)
	}
	return nil
}

func (r *PgxAuthRepository) GetUserEmailByID(ctx context.Context, db DBTX, userID string) (string, error) {
	var email string
	err := db.QueryRow(ctx,
		"SELECT email FROM users WHERE id = $1",
		userID,
	).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("getting user email by id: %w", err)
	}
	return email, nil
}

// --- entitlements ---------------------------------------------------------

func (r *PgxAuthRepository) GetEntitlementSnapshot(ctx context.Context, db DBTX, userID string) (EntitlementRow, error) {
	var e EntitlementRow
	err := db.QueryRow(ctx,
		`SELECT entitlement, is_active, current_period_end
		 FROM user_entitlements
		 WHERE user_id = $1 AND entitlement = 'premium'
		 LIMIT 1`,
		userID,
	).Scan(&e.Entitlement, &e.IsActive, &e.CurrentPeriodEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		return EntitlementRow{}, ErrNotFound
	}
	if err != nil {
		return EntitlementRow{}, fmt.Errorf("getting entitlement snapshot: %w", err)
	}
	return e, nil
}
