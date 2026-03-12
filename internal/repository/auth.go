package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/IsorilovA/pauza-server/internal/mail"
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

var (
	_ DBTX = (*pgxpool.Pool)(nil)
	_ DBTX = (pgx.Tx)(nil)
)

// UserRow holds the columns returned by user lookups.
type UserRow struct {
	ID                 string
	Email              string
	Name               string
	Username           string
	ProfilePictureURL  *string
	PushEnabled        bool
	LeaderboardVisible bool
	CreatedAt          time.Time
}

// OTPRow holds the columns returned by OTP lookups.
type OTPRow struct {
	ID       string
	CodeHash string
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

// UserRepository defines user read/write operations used by auth-adjacent
// services. Every method accepts a DBTX so it can be called against the pool
// or inside an explicit transaction.
type UserRepository interface {
	GetUserByEmail(ctx context.Context, db DBTX, email string) (UserRow, error)
	GetUserByEmailForUpdate(ctx context.Context, db DBTX, email string) (UserRow, error)
	GetUserByID(ctx context.Context, db DBTX, userID string) (UserRow, error)
	GetUserByIDForUpdate(ctx context.Context, db DBTX, userID string) (UserRow, error)
	InsertUser(ctx context.Context, db DBTX, email, username string) (string, error)
	UpdateUser(ctx context.Context, db DBTX, userID string, name *string, username *string, leaderboardVisible *bool, profilePictureURL *string) (UserRow, error)
	GetPushEnabled(ctx context.Context, db DBTX, userID string) (bool, error)
	UpdatePushEnabled(ctx context.Context, db DBTX, userID string, pushEnabled bool) (bool, error)
	IsUsernameTaken(ctx context.Context, db DBTX, username string, excludeUserID string) (bool, error)
	DeleteUser(ctx context.Context, db DBTX, userID string) error
}

// OTPRepository defines OTP and failed-attempt persistence operations.
type OTPRepository interface {
	InsertOTP(ctx context.Context, db DBTX, email string, userID *string, codeHash string, purpose mail.Purpose, expiresAt time.Time) (string, error)
	GetActiveOTPForUpdate(ctx context.Context, db DBTX, email string, purpose mail.Purpose) (OTPRow, error)
	CountFailedOTPAttemptsSinceForUpdate(ctx context.Context, db DBTX, email string, purpose mail.Purpose, since time.Time) (int, error)
	GetOldestFailedOTPAttemptSinceForUpdate(ctx context.Context, db DBTX, email string, purpose mail.Purpose, since time.Time) (time.Time, error)
	InsertFailedOTPAttempt(ctx context.Context, db DBTX, email string, userID *string, purpose mail.Purpose, attemptedAt time.Time) error
	MarkOTPUsed(ctx context.Context, db DBTX, otpID string) error
	DeleteOTPsByEmailAndPurpose(ctx context.Context, db DBTX, email string, purpose mail.Purpose) error
	DeleteOTPByID(ctx context.Context, db DBTX, otpID string) error
}

// RefreshTokenRepository defines refresh-token persistence operations.
type RefreshTokenRepository interface {
	InsertRefreshToken(ctx context.Context, db DBTX, userID, tokenHash string, expiresAt time.Time) error
	GetRefreshTokenByHashForUpdate(ctx context.Context, db DBTX, tokenHash string) (RefreshTokenRow, error)
	RevokeRefreshToken(ctx context.Context, db DBTX, tokenID string) error
	RevokeAllRefreshTokens(ctx context.Context, db DBTX, userID string) error
	GetUserEmailByID(ctx context.Context, db DBTX, userID string) (string, error)
}

// EntitlementSnapshotRepository defines entitlement snapshot lookups.
type EntitlementSnapshotRepository interface {
	GetEntitlementSnapshot(ctx context.Context, db DBTX, userID string) (EntitlementRow, error)
}

// ErrNotFound is returned when a queried row does not exist.
var ErrNotFound = errors.New("not found")

var _ UserRepository = (*PgxAuthRepository)(nil)
var _ OTPRepository = (*PgxAuthRepository)(nil)
var _ RefreshTokenRepository = (*PgxAuthRepository)(nil)
var _ EntitlementSnapshotRepository = (*PgxAuthRepository)(nil)

// PgxAuthRepository implements the auth-related repository interfaces using pgx queries.
type PgxAuthRepository struct{}

// NewPgxAuthRepository returns a PgxAuthRepository.
func NewPgxAuthRepository() *PgxAuthRepository {
	return &PgxAuthRepository{}
}

const userColumns = `id, email, name, username, profile_picture_url,
		        push_enabled, leaderboard_visible, created_at`

func scanUserRow(row pgx.Row) (UserRow, error) {
	var u UserRow
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Username, &u.ProfilePictureURL,
		&u.PushEnabled, &u.LeaderboardVisible, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserRow{}, ErrNotFound
	}
	if err != nil {
		return UserRow{}, err
	}
	return u, nil
}

func (r *PgxAuthRepository) GetUserByEmail(ctx context.Context, db DBTX, email string) (UserRow, error) {
	row := db.QueryRow(ctx,
		`SELECT `+userColumns+`
		 FROM users WHERE lower(email) = $1`,
		strings.ToLower(email),
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
		strings.ToLower(email),
	)
	u, err := scanUserRow(row)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return UserRow{}, fmt.Errorf("getting user by email for update: %w", err)
	}
	return u, err
}

func (r *PgxAuthRepository) GetUserByIDForUpdate(ctx context.Context, db DBTX, userID string) (UserRow, error) {
	row := db.QueryRow(ctx,
		`SELECT `+userColumns+`
		 FROM users WHERE id = $1 FOR UPDATE`,
		userID,
	)
	u, err := scanUserRow(row)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return UserRow{}, fmt.Errorf("getting user by id for update: %w", err)
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

func (r *PgxAuthRepository) InsertUser(ctx context.Context, db DBTX, email, username string) (string, error) {
	var id string
	err := db.QueryRow(ctx,
		`INSERT INTO users (email, username)
		 VALUES ($1, $2)
		 RETURNING id`,
		email, username,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("inserting user: %w", err)
	}
	return id, nil
}

func (r *PgxAuthRepository) UpdateUser(ctx context.Context, db DBTX, userID string, name *string, username *string, leaderboardVisible *bool, profilePictureURL *string) (UserRow, error) {
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
	if profilePictureURL != nil {
		setClauses = append(setClauses, fmt.Sprintf("profile_picture_url = $%d", argIdx))
		args = append(args, *profilePictureURL)
		argIdx++
	}

	if len(setClauses) == 0 {
		return r.GetUserByID(ctx, db, userID)
	}

	setClauses = append(setClauses, "updated_at = now()")
	args = append(args, userID)

	query := fmt.Sprintf(
		"UPDATE users SET %s WHERE id = $%d RETURNING %s",
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

func (r *PgxAuthRepository) GetPushEnabled(ctx context.Context, db DBTX, userID string) (bool, error) {
	var pushEnabled bool
	err := db.QueryRow(ctx, `SELECT push_enabled FROM users WHERE id = $1`, userID).Scan(&pushEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("getting push preference: %w", err)
	}
	return pushEnabled, nil
}

func (r *PgxAuthRepository) UpdatePushEnabled(ctx context.Context, db DBTX, userID string, pushEnabled bool) (bool, error) {
	err := db.QueryRow(ctx, `
		UPDATE users
		SET push_enabled = $1,
		    updated_at = now()
		WHERE id = $2
		RETURNING push_enabled
	`, pushEnabled, userID).Scan(&pushEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("updating push preference: %w", err)
	}
	return pushEnabled, nil
}

func (r *PgxAuthRepository) IsUsernameTaken(ctx context.Context, db DBTX, username string, excludeUserID string) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE lower(username) = lower($1) AND id != $2)`,
		username, excludeUserID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking username availability: %w", err)
	}
	return exists, nil
}

func (r *PgxAuthRepository) DeleteUser(ctx context.Context, db DBTX, userID string) error {
	tag, err := db.Exec(ctx,
		`DELETE FROM users WHERE id = $1`,
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

func (r *PgxAuthRepository) InsertOTP(ctx context.Context, db DBTX, email string, userID *string, codeHash string, purpose mail.Purpose, expiresAt time.Time) (string, error) {
	var id string
	err := db.QueryRow(ctx,
		`INSERT INTO otp_codes (email, user_id, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		email, userID, codeHash, string(purpose), expiresAt,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("inserting otp: %w", err)
	}
	return id, nil
}

func (r *PgxAuthRepository) GetActiveOTPForUpdate(ctx context.Context, db DBTX, email string, purpose mail.Purpose) (OTPRow, error) {
	var o OTPRow
	err := db.QueryRow(ctx,
		`SELECT id, code_hash
		 FROM otp_codes
		 WHERE lower(email) = lower($1)
		   AND purpose = $2
		   AND used = false
		   AND expires_at > now()
		 ORDER BY created_at DESC
		 LIMIT 1
		 FOR UPDATE`,
		email, string(purpose),
	).Scan(&o.ID, &o.CodeHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return OTPRow{}, ErrNotFound
	}
	if err != nil {
		return OTPRow{}, fmt.Errorf("getting active otp for update: %w", err)
	}
	return o, nil
}

func (r *PgxAuthRepository) CountFailedOTPAttemptsSinceForUpdate(ctx context.Context, db DBTX, email string, purpose mail.Purpose, since time.Time) (int, error) {
	var attempts int
	err := db.QueryRow(ctx,
		`SELECT COUNT(*)
		 FROM (
		 	SELECT 1
		 	FROM otp_failed_attempts
		 	WHERE lower(email) = lower($1)
		 	  AND purpose = $2
		 	  AND attempted_at >= $3
		 	FOR UPDATE
		 ) AS locked_attempts`,
		email, string(purpose), since,
	).Scan(&attempts)
	if err != nil {
		return 0, fmt.Errorf("counting failed otp attempts since for update: %w", err)
	}
	return attempts, nil
}

func (r *PgxAuthRepository) GetOldestFailedOTPAttemptSinceForUpdate(ctx context.Context, db DBTX, email string, purpose mail.Purpose, since time.Time) (time.Time, error) {
	var attemptedAt time.Time
	err := db.QueryRow(ctx,
		`SELECT attempted_at
		 FROM otp_failed_attempts
		 WHERE lower(email) = lower($1)
		   AND purpose = $2
		   AND attempted_at >= $3
		 ORDER BY attempted_at ASC
		 LIMIT 1
		 FOR UPDATE`,
		email, string(purpose), since,
	).Scan(&attemptedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("getting oldest failed otp attempt since for update: %w", err)
	}
	return attemptedAt, nil
}

func (r *PgxAuthRepository) InsertFailedOTPAttempt(ctx context.Context, db DBTX, email string, userID *string, purpose mail.Purpose, attemptedAt time.Time) error {
	_, err := db.Exec(ctx,
		`INSERT INTO otp_failed_attempts (email, user_id, purpose, attempted_at)
		 VALUES ($1, $2, $3, $4)`,
		email, userID, string(purpose), attemptedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting failed otp attempt: %w", err)
	}
	return nil
}

func (r *PgxAuthRepository) MarkOTPUsed(ctx context.Context, db DBTX, otpID string) error {
	_, err := db.Exec(ctx,
		`UPDATE otp_codes SET used = true WHERE id = $1`,
		otpID,
	)
	if err != nil {
		return fmt.Errorf("marking otp used: %w", err)
	}
	return nil
}

func (r *PgxAuthRepository) DeleteOTPsByEmailAndPurpose(ctx context.Context, db DBTX, email string, purpose mail.Purpose) error {
	_, err := db.Exec(ctx,
		`DELETE FROM otp_codes WHERE lower(email) = lower($1) AND purpose = $2`,
		email, string(purpose),
	)
	if err != nil {
		return fmt.Errorf("deleting otps by email and purpose: %w", err)
	}
	return nil
}

func (r *PgxAuthRepository) DeleteOTPByID(ctx context.Context, db DBTX, otpID string) error {
	_, err := db.Exec(ctx,
		`DELETE FROM otp_codes WHERE id = $1`,
		otpID,
	)
	if err != nil {
		return fmt.Errorf("deleting otp by id: %w", err)
	}
	return nil
}

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
		`SELECT email FROM users WHERE id = $1`,
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

func (r *PgxAuthRepository) GetEntitlementSnapshot(ctx context.Context, db DBTX, userID string) (EntitlementRow, error) {
	var e EntitlementRow
	// Use two CTEs—one for the stored snapshot, one for the active admin
	// override—and combine them with a cross join so the result is produced
	// even when only one side exists. Precedence:
	//   active admin grant  (± snapshot) → is_active = true
	//   active admin revoke (± snapshot) → is_active = false
	//   no active override + snapshot    → snapshot is_active
	//   no active override + no snapshot → no row (ErrNotFound)
	err := db.QueryRow(ctx,
		`WITH snapshot AS (
		   SELECT is_active, current_period_end
		   FROM user_entitlements
		   WHERE user_id = $1 AND entitlement = 'premium'
		 ),
		 override AS (
		   SELECT action
		   FROM admin_entitlement_overrides
		   WHERE user_id = $1 AND entitlement = 'premium'
		     AND (expires_at IS NULL OR expires_at > now())
		 )
		 SELECT 'premium' AS entitlement,
		        CASE
		          WHEN o.action = 'grant'  THEN true
		          WHEN o.action = 'revoke' THEN false
		          ELSE s.is_active
		        END AS is_active,
		        s.current_period_end
		 FROM (SELECT 1) AS base
		 LEFT JOIN snapshot  s ON true
		 LEFT JOIN override  o ON true
		 WHERE s.is_active IS NOT NULL
		    OR o.action IS NOT NULL`,
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
