package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Row types
// ---------------------------------------------------------------------------

// AdminCredentialRow holds the columns returned by admin credential lookups.
type AdminCredentialRow struct {
	ID           string
	Username     string
	PasswordHash string
}

// AdminUserRow holds the columns returned by the paginated admin user listing.
type AdminUserRow struct {
	ID                string
	Email             string
	Name              string
	Username          string
	ProfilePictureURL *string
	CreatedAt         time.Time
	IsPremium         bool
}

// AdminUserDetailRow holds the columns returned by the admin user detail query.
type AdminUserDetailRow struct {
	ID                  string
	Email               string
	Name                string
	Username            string
	ProfilePictureURL   *string
	LeaderboardVisible  bool
	CreatedAt           time.Time
	IsPremium           bool
	CurrentPeriodEnd    *time.Time
	RevenueCatAppUserID *string
	FriendCount         int
	TotalSessions       int
	LastSessionTime     *int64
}

// PlatformStatsRow holds the columns returned by the platform stats query.
type PlatformStatsRow struct {
	TotalUsers          int
	ActiveUsers30d      int
	PremiumUsers        int
	TotalFriendships    int
	AvgStreakDays       float64
	AvgDailyFocusTimeMS float64
}

// OverrideRow holds the columns returned by an active entitlement override lookup.
type OverrideRow struct {
	ID          string
	UserID      string
	Entitlement Entitlement
	Action      AdminOverrideAction
	ExpiresAt   *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AdminEntitlementListRow holds the columns returned by the admin entitlement listing.
type AdminEntitlementListRow struct {
	UserID           string
	Email            string
	Username         string
	Entitlement      string
	IsActive         bool
	CurrentPeriodEnd *time.Time
	UpdatedAt        time.Time
}

// ---------------------------------------------------------------------------
// Param types
// ---------------------------------------------------------------------------

// ListUsersParams holds pagination and optional search for the admin user listing.
type ListUsersParams struct {
	Limit  int
	Offset int
	Search string // optional ILIKE search over email, username, name
}

// UpsertOverrideParams holds the fields for upserting an admin entitlement override.
type UpsertOverrideParams struct {
	UserID      string
	Entitlement Entitlement
	Action      AdminOverrideAction
	ExpiresAt   *time.Time
}

// ListEntitlementsParams holds pagination and optional filters for the admin entitlement listing.
type ListEntitlementsParams struct {
	Limit       int
	Offset      int
	Entitlement Entitlement // optional filter
	IsActive    *bool       // optional filter
}

// ---------------------------------------------------------------------------
// Interface
// ---------------------------------------------------------------------------

// AdminRepository defines the data-access operations used by admin endpoints.
// Every method accepts a DBTX so it can be called against the pool or inside
// an explicit transaction.
type AdminRepository interface {
	GetAdminByUsername(ctx context.Context, db DBTX, username string) (AdminCredentialRow, error)
	ListUsers(ctx context.Context, db DBTX, params ListUsersParams) ([]AdminUserRow, int, error)
	GetUserDetail(ctx context.Context, db DBTX, userID string) (AdminUserDetailRow, error)
	GetPlatformStats(ctx context.Context, db DBTX) (PlatformStatsRow, error)
	UpsertEntitlementOverride(ctx context.Context, db DBTX, params UpsertOverrideParams) error
	DeleteEntitlementOverride(ctx context.Context, db DBTX, userID string, entitlement Entitlement) error
	GetActiveOverride(ctx context.Context, db DBTX, userID string, entitlement Entitlement) (OverrideRow, error)
	ListEntitlements(ctx context.Context, db DBTX, params ListEntitlementsParams) ([]AdminEntitlementListRow, int, error)
	UserExists(ctx context.Context, db DBTX, userID string) (bool, error)
}

// Compile-time check: PgxAdminRepository satisfies AdminRepository.
var _ AdminRepository = (*PgxAdminRepository)(nil)

// PgxAdminRepository implements AdminRepository using pgx queries.
type PgxAdminRepository struct{}

// NewPgxAdminRepository returns a PgxAdminRepository.
func NewPgxAdminRepository() *PgxAdminRepository {
	return &PgxAdminRepository{}
}

// ---------------------------------------------------------------------------
// Implementations
// ---------------------------------------------------------------------------

func (r *PgxAdminRepository) GetAdminByUsername(ctx context.Context, db DBTX, username string) (AdminCredentialRow, error) {
	var a AdminCredentialRow
	err := db.QueryRow(ctx,
		`SELECT id, username, password_hash
		 FROM admin_credentials
		 WHERE username = $1`,
		username,
	).Scan(&a.ID, &a.Username, &a.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminCredentialRow{}, ErrNotFound
	}
	if err != nil {
		return AdminCredentialRow{}, fmt.Errorf("getting admin by username: %w", err)
	}
	return a, nil
}

func (r *PgxAdminRepository) ListUsers(ctx context.Context, db DBTX, params ListUsersParams) ([]AdminUserRow, int, error) {
	var (
		whereClauses []string
		args         []any
		argIdx       = 1
	)

	if params.Search != "" {
		pattern := "%" + params.Search + "%"
		whereClauses = append(whereClauses, fmt.Sprintf(
			`(u.email ILIKE $%d OR u.username ILIKE $%d OR u.name ILIKE $%d)`,
			argIdx, argIdx, argIdx,
		))
		args = append(args, pattern)
		argIdx++
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(
		`SELECT u.id, u.email, u.name, u.username, u.profile_picture_url, u.created_at,
		        CASE
		          WHEN o.action = 'grant'  THEN true
		          WHEN o.action = 'revoke' THEN false
		          ELSE COALESCE(e.is_active, false)
		        END AS is_premium,
		        COUNT(*) OVER() AS total_count
		 FROM users u
		 LEFT JOIN user_entitlements e
		   ON e.user_id = u.id AND e.entitlement = 'premium'
		 LEFT JOIN admin_entitlement_overrides o
		   ON o.user_id = u.id AND o.entitlement = 'premium'
		   AND (o.expires_at IS NULL OR o.expires_at > now())
		 %s
		 ORDER BY u.created_at DESC
		 LIMIT $%d OFFSET $%d`,
		whereSQL, argIdx, argIdx+1,
	)
	args = append(args, params.Limit, params.Offset)

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing admin users: %w", err)
	}
	defer rows.Close()

	var (
		users []AdminUserRow
		total int
	)
	for rows.Next() {
		var u AdminUserRow
		if err := rows.Scan(
			&u.ID, &u.Email, &u.Name, &u.Username, &u.ProfilePictureURL,
			&u.CreatedAt, &u.IsPremium, &total,
		); err != nil {
			return nil, 0, fmt.Errorf("scanning admin user row: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating admin user rows: %w", err)
	}
	return users, total, nil
}

func (r *PgxAdminRepository) GetUserDetail(ctx context.Context, db DBTX, userID string) (AdminUserDetailRow, error) {
	var d AdminUserDetailRow
	err := db.QueryRow(ctx,
		`SELECT u.id, u.email, u.name, u.username, u.profile_picture_url,
		        u.leaderboard_visible, u.created_at,
		        CASE
		          WHEN o.action = 'grant'  THEN true
		          WHEN o.action = 'revoke' THEN false
		          ELSE COALESCE(e.is_active, false)
		        END AS is_premium,
		        e.current_period_end,
		        e.revenuecat_app_user_id,
		        (SELECT COUNT(*)
		         FROM friendships f
		         WHERE f.status = 'accepted'
		           AND (f.requester_id = u.id OR f.addressee_id = u.id)) AS friend_count,
		        (SELECT COUNT(*)
		         FROM restriction_sessions rs
		         WHERE rs.user_id = u.id) AS total_sessions,
		        (SELECT MAX(rs2.started_at)
		         FROM restriction_sessions rs2
		         WHERE rs2.user_id = u.id) AS last_session_time
		 FROM users u
		 LEFT JOIN user_entitlements e
		   ON e.user_id = u.id AND e.entitlement = 'premium'
		 LEFT JOIN admin_entitlement_overrides o
		   ON o.user_id = u.id AND o.entitlement = 'premium'
		   AND (o.expires_at IS NULL OR o.expires_at > now())
		 WHERE u.id = $1`,
		userID,
	).Scan(
		&d.ID, &d.Email, &d.Name, &d.Username, &d.ProfilePictureURL,
		&d.LeaderboardVisible, &d.CreatedAt,
		&d.IsPremium, &d.CurrentPeriodEnd, &d.RevenueCatAppUserID,
		&d.FriendCount, &d.TotalSessions, &d.LastSessionTime,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminUserDetailRow{}, ErrNotFound
	}
	if err != nil {
		return AdminUserDetailRow{}, fmt.Errorf("getting admin user detail: %w", err)
	}
	return d, nil
}

func (r *PgxAdminRepository) GetPlatformStats(ctx context.Context, db DBTX) (PlatformStatsRow, error) {
	var s PlatformStatsRow
	err := db.QueryRow(ctx,
		`SELECT
		   (SELECT COUNT(*) FROM users) AS total_users,
		   (SELECT COUNT(DISTINCT rs.user_id)
		    FROM restriction_sessions rs
		    WHERE rs.started_at > EXTRACT(EPOCH FROM (now() - INTERVAL '30 days')) * 1000
		   ) AS active_users_30d,
		   (SELECT COUNT(*)
		    FROM users u
		    LEFT JOIN user_entitlements ue
		      ON ue.user_id = u.id AND ue.entitlement = 'premium'
		    LEFT JOIN admin_entitlement_overrides o
		      ON o.user_id = u.id AND o.entitlement = 'premium'
		      AND (o.expires_at IS NULL OR o.expires_at > now())
		    WHERE CASE
		            WHEN o.action = 'grant'  THEN true
		            WHEN o.action = 'revoke' THEN false
		            ELSE COALESCE(ue.is_active, false)
		          END = true
		   ) AS premium_users,
		   (SELECT COUNT(*)
		    FROM friendships f
		    WHERE f.status = 'accepted'
		   ) AS total_friendships,
		   COALESCE((
		     SELECT AVG(streak_days)
		     FROM (
		       SELECT COUNT(*) AS streak_days
		       FROM streak_daily_aggregates sda
		       WHERE sda.qualified = 1
		       GROUP BY sda.user_id
		     ) AS user_streaks
		   ), 0) AS avg_streak_days,
		   COALESCE((
		     SELECT AVG(daily_ms)
		     FROM (
		       SELECT AVG(sda2.effective_ms) AS daily_ms
		       FROM streak_daily_aggregates sda2
		       WHERE sda2.effective_ms > 0
		       GROUP BY sda2.user_id
		     ) AS user_daily_focus
		   ), 0) AS avg_daily_focus_time_ms`,
	).Scan(
		&s.TotalUsers, &s.ActiveUsers30d, &s.PremiumUsers,
		&s.TotalFriendships, &s.AvgStreakDays, &s.AvgDailyFocusTimeMS,
	)
	if err != nil {
		return PlatformStatsRow{}, fmt.Errorf("getting platform stats: %w", err)
	}
	return s, nil
}

func (r *PgxAdminRepository) UpsertEntitlementOverride(ctx context.Context, db DBTX, params UpsertOverrideParams) error {
	_, err := db.Exec(ctx,
		`INSERT INTO admin_entitlement_overrides (user_id, entitlement, action, expires_at, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, now(), now())
		 ON CONFLICT (user_id, entitlement) DO UPDATE SET
		   action     = EXCLUDED.action,
		   expires_at = EXCLUDED.expires_at,
		   updated_at = now()`,
		params.UserID, string(params.Entitlement), string(params.Action), params.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("upserting entitlement override: %w", err)
	}
	return nil
}

func (r *PgxAdminRepository) DeleteEntitlementOverride(ctx context.Context, db DBTX, userID string, entitlement Entitlement) error {
	tag, err := db.Exec(ctx,
		`DELETE FROM admin_entitlement_overrides
		 WHERE user_id = $1 AND entitlement = $2`,
		userID, string(entitlement),
	)
	if err != nil {
		return fmt.Errorf("deleting entitlement override: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PgxAdminRepository) GetActiveOverride(ctx context.Context, db DBTX, userID string, entitlement Entitlement) (OverrideRow, error) {
	var o OverrideRow
	err := db.QueryRow(ctx,
		`SELECT id, user_id, entitlement, action, expires_at, created_at, updated_at
		 FROM admin_entitlement_overrides
		 WHERE user_id = $1
		   AND entitlement = $2
		   AND (expires_at IS NULL OR expires_at > now())`,
		userID, string(entitlement),
	).Scan(&o.ID, &o.UserID, &o.Entitlement, &o.Action, &o.ExpiresAt, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return OverrideRow{}, ErrNotFound
	}
	if err != nil {
		return OverrideRow{}, fmt.Errorf("getting active entitlement override: %w", err)
	}
	return o, nil
}

func (r *PgxAdminRepository) ListEntitlements(ctx context.Context, db DBTX, params ListEntitlementsParams) ([]AdminEntitlementListRow, int, error) {
	// The CTE merges user_entitlements rows with active admin overrides so
	// that users who only have an override (no user_entitlements row) still
	// appear. Effective is_active uses override precedence:
	//   active grant  -> true
	//   active revoke -> false
	//   else          -> stored is_active
	const baseCTE = `WITH merged AS (
		-- Snapshot rows, with any active override applied.
		SELECT
		  e.user_id,
		  e.entitlement,
		  CASE
		    WHEN o.action = 'grant'  THEN true
		    WHEN o.action = 'revoke' THEN false
		    ELSE e.is_active
		  END                                       AS is_active,
		  e.current_period_end,
		  COALESCE(o.updated_at, e.updated_at)     AS updated_at
		FROM user_entitlements e
		LEFT JOIN admin_entitlement_overrides o
		  ON  o.user_id     = e.user_id
		  AND o.entitlement = e.entitlement
		  AND (o.expires_at IS NULL OR o.expires_at > now())

		UNION ALL

		-- Active override-only rows (no snapshot).
		SELECT
		  o2.user_id,
		  o2.entitlement,
		  (o2.action = 'grant')                    AS is_active,
		  NULL                                      AS current_period_end,
		  o2.updated_at
		FROM admin_entitlement_overrides o2
		WHERE (o2.expires_at IS NULL OR o2.expires_at > now())
		  AND NOT EXISTS (
		    SELECT 1 FROM user_entitlements e2
		    WHERE e2.user_id = o2.user_id AND e2.entitlement = o2.entitlement
		  )
	)`

	var (
		whereClauses []string
		args         []any
		argIdx       = 1
	)

	if params.Entitlement != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("m.entitlement = $%d", argIdx))
		args = append(args, string(params.Entitlement))
		argIdx++
	}
	if params.IsActive != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("m.is_active = $%d", argIdx))
		args = append(args, *params.IsActive)
		argIdx++
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(
		`%s
		 SELECT m.user_id, u.email, u.username, m.entitlement, m.is_active,
		        m.current_period_end, m.updated_at,
		        COUNT(*) OVER() AS total_count
		 FROM merged m
		 JOIN users u ON u.id = m.user_id
		 %s
		 ORDER BY m.updated_at DESC
		 LIMIT $%d OFFSET $%d`,
		baseCTE, whereSQL, argIdx, argIdx+1,
	)
	args = append(args, params.Limit, params.Offset)

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing admin entitlements: %w", err)
	}
	defer rows.Close()

	var (
		entitlements []AdminEntitlementListRow
		total        int
	)
	for rows.Next() {
		var e AdminEntitlementListRow
		if err := rows.Scan(
			&e.UserID, &e.Email, &e.Username, &e.Entitlement,
			&e.IsActive, &e.CurrentPeriodEnd, &e.UpdatedAt, &total,
		); err != nil {
			return nil, 0, fmt.Errorf("scanning admin entitlement row: %w", err)
		}
		entitlements = append(entitlements, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating admin entitlement rows: %w", err)
	}
	return entitlements, total, nil
}

func (r *PgxAdminRepository) UserExists(ctx context.Context, db DBTX, userID string) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`,
		userID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking user existence: %w", err)
	}
	return exists, nil
}
