package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type SocialRepository struct{}

func NewSocialRepository() *SocialRepository {
	return &SocialRepository{}
}

type BasicUserRow struct {
	ID                 string
	Name               string
	Username           string
	ProfilePictureURL  *string
	LeaderboardVisible bool
}

type FriendRow struct {
	FriendshipID string
	User         BasicUserRow
	Since        time.Time
}

type FriendRequestRow struct {
	FriendshipID string
	User         BasicUserRow
	CreatedAt    time.Time
}

type PaginationResult struct {
	Page  int
	Limit int
	Total int
}

type LeaderboardMetric string

const (
	LeaderboardMetricStreak    LeaderboardMetric = "streak"
	LeaderboardMetricFocusTime LeaderboardMetric = "focus_time"
)

type LeaderboardRow struct {
	Rank              int
	User              BasicUserRow
	CurrentStreakDays int
	TotalFocusTimeMS  int64
}

type DeviceTokenRow struct {
	FCMToken string
	Platform string
}

type EntitlementListRow struct {
	UserID      string
	Entitlement string
	IsActive    bool
	ExpiresAt   *time.Time
	UpdatedAt   time.Time
}

func (r *SocialRepository) EffectivePremiumActive(ctx context.Context, db DBTX, userID string) (bool, error) {
	var active bool
	err := db.QueryRow(ctx, `
		SELECT CASE
			WHEN o.action = 'grant' AND (o.expires_at IS NULL OR o.expires_at > now()) THEN true
			WHEN o.action = 'revoke' AND (o.expires_at IS NULL OR o.expires_at > now()) THEN false
			ELSE COALESCE(e.is_active, false)
		END
		FROM users u
		LEFT JOIN admin_entitlement_overrides o
		  ON o.user_id = u.id AND o.entitlement = 'premium'
		LEFT JOIN user_entitlements e
		  ON e.user_id = u.id AND e.entitlement = 'premium'
		WHERE u.id = $1
	`, userID).Scan(&active)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, ErrNotFound
		}
		return false, fmt.Errorf("loading effective premium entitlement: %w", err)
	}
	return active, nil
}

func (r *SocialRepository) RegisterDevice(ctx context.Context, db DBTX, userID, fcmToken, platform string) error {
	_, err := db.Exec(ctx, `
		INSERT INTO device_tokens (user_id, fcm_token, platform, created_at, updated_at)
		VALUES ($1, $2, $3, now(), now())
		ON CONFLICT (fcm_token) DO UPDATE
		SET user_id = EXCLUDED.user_id,
		    platform = EXCLUDED.platform,
		    updated_at = now()
	`, userID, fcmToken, platform)
	if err != nil {
		return fmt.Errorf("registering device: %w", err)
	}
	return nil
}

func (r *SocialRepository) UnregisterDevice(ctx context.Context, db DBTX, userID, fcmToken string) error {
	_, err := db.Exec(ctx, `DELETE FROM device_tokens WHERE user_id = $1 AND fcm_token = $2`, userID, fcmToken)
	if err != nil {
		return fmt.Errorf("unregistering device: %w", err)
	}
	return nil
}

func (r *SocialRepository) ListDeviceTokens(ctx context.Context, db DBTX, userID string) ([]DeviceTokenRow, error) {
	rows, err := db.Query(ctx, `
		SELECT fcm_token, platform
		FROM device_tokens
		WHERE user_id = $1
		ORDER BY updated_at DESC, created_at DESC, id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("listing device tokens: %w", err)
	}
	defer rows.Close()

	var out []DeviceTokenRow
	for rows.Next() {
		var item DeviceTokenRow
		if err := rows.Scan(&item.FCMToken, &item.Platform); err != nil {
			return nil, fmt.Errorf("scanning device token: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *SocialRepository) DeleteDeviceToken(ctx context.Context, db DBTX, fcmToken string) error {
	if _, err := db.Exec(ctx, `DELETE FROM device_tokens WHERE fcm_token = $1`, fcmToken); err != nil {
		return fmt.Errorf("deleting device token: %w", err)
	}
	return nil
}

func (r *SocialRepository) FindUserByExactUsername(ctx context.Context, db DBTX, username string) (BasicUserRow, error) {
	var out BasicUserRow
	err := db.QueryRow(ctx, `
		SELECT id, name, username, profile_picture_url, leaderboard_visible
		FROM users
		WHERE lower(username) = lower($1)
	`, username).Scan(&out.ID, &out.Name, &out.Username, &out.ProfilePictureURL, &out.LeaderboardVisible)
	if err != nil {
		return BasicUserRow{}, mapNoRows("finding user by username", err)
	}
	return out, nil
}

func (r *SocialRepository) GetBasicUserByID(ctx context.Context, db DBTX, userID string) (BasicUserRow, error) {
	var out BasicUserRow
	err := db.QueryRow(ctx, `
		SELECT id, name, username, profile_picture_url, leaderboard_visible
		FROM users
		WHERE id = $1
	`, userID).Scan(&out.ID, &out.Name, &out.Username, &out.ProfilePictureURL, &out.LeaderboardVisible)
	if err != nil {
		return BasicUserRow{}, mapNoRows("loading user by id", err)
	}
	return out, nil
}

func (r *SocialRepository) CreateFriendRequest(ctx context.Context, db DBTX, requesterID, addresseeID string) (string, error) {
	var id string
	err := db.QueryRow(ctx, `
		INSERT INTO friendships (requester_id, addressee_id, status, created_at, updated_at)
		VALUES ($1, $2, 'pending', now(), now())
		RETURNING id
	`, requesterID, addresseeID).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (r *SocialRepository) ListFriends(ctx context.Context, db DBTX, userID string, page, limit int) ([]FriendRow, PaginationResult, error) {
	offset := (page - 1) * limit
	var total int
	if err := db.QueryRow(ctx, `
		SELECT count(*)
		FROM friendships
		WHERE status = 'accepted'
		  AND (requester_id = $1 OR addressee_id = $1)
	`, userID).Scan(&total); err != nil {
		return nil, PaginationResult{}, fmt.Errorf("counting friends: %w", err)
	}

	rows, err := db.Query(ctx, `
		SELECT f.id,
		       u.id,
		       u.name,
		       u.username,
		       u.profile_picture_url,
		       f.updated_at
		FROM friendships f
		JOIN users u
		  ON u.id = CASE WHEN f.requester_id = $1 THEN f.addressee_id ELSE f.requester_id END
		WHERE f.status = 'accepted'
		  AND (f.requester_id = $1 OR f.addressee_id = $1)
		ORDER BY f.updated_at DESC, f.id
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, PaginationResult{}, fmt.Errorf("listing friends: %w", err)
	}
	defer rows.Close()

	var out []FriendRow
	for rows.Next() {
		var item FriendRow
		if err := rows.Scan(&item.FriendshipID, &item.User.ID, &item.User.Name, &item.User.Username, &item.User.ProfilePictureURL, &item.Since); err != nil {
			return nil, PaginationResult{}, fmt.Errorf("scanning friend: %w", err)
		}
		out = append(out, item)
	}
	return out, PaginationResult{Page: page, Limit: limit, Total: total}, rows.Err()
}

func (r *SocialRepository) ListFriendRequests(ctx context.Context, db DBTX, userID, direction string) ([]FriendRequestRow, error) {
	column := "requester_id"
	joinExpr := "f.addressee_id"
	if direction == "incoming" {
		column = "addressee_id"
		joinExpr = "f.requester_id"
	}

	rows, err := db.Query(ctx, fmt.Sprintf(`
		SELECT f.id, u.id, u.name, u.username, u.profile_picture_url, f.created_at
		FROM friendships f
		JOIN users u ON u.id = %s
		WHERE f.%s = $1 AND f.status = 'pending'
		ORDER BY f.created_at DESC, f.id
	`, joinExpr, column), userID)
	if err != nil {
		return nil, fmt.Errorf("listing friend requests: %w", err)
	}
	defer rows.Close()

	var out []FriendRequestRow
	for rows.Next() {
		var item FriendRequestRow
		if err := rows.Scan(&item.FriendshipID, &item.User.ID, &item.User.Name, &item.User.Username, &item.User.ProfilePictureURL, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning friend request: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *SocialRepository) GetFriendship(ctx context.Context, db DBTX, friendshipID string) (string, string, string, error) {
	var requesterID, addresseeID, status string
	err := db.QueryRow(ctx, `SELECT requester_id, addressee_id, status FROM friendships WHERE id = $1`, friendshipID).Scan(&requesterID, &addresseeID, &status)
	if err != nil {
		return "", "", "", mapNoRows("loading friendship", err)
	}
	return requesterID, addresseeID, status, nil
}

func (r *SocialRepository) AcceptFriendRequest(ctx context.Context, db DBTX, friendshipID, userID string) error {
	tag, err := db.Exec(ctx, `
		UPDATE friendships
		SET status = 'accepted', updated_at = now()
		WHERE id = $1 AND addressee_id = $2 AND status = 'pending'
	`, friendshipID, userID)
	if err != nil {
		return fmt.Errorf("accepting friendship: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *SocialRepository) DeletePendingRequest(ctx context.Context, db DBTX, friendshipID, userID string) error {
	tag, err := db.Exec(ctx, `DELETE FROM friendships WHERE id = $1 AND addressee_id = $2 AND status = 'pending'`, friendshipID, userID)
	if err != nil {
		return fmt.Errorf("declining friendship: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *SocialRepository) RemoveFriend(ctx context.Context, db DBTX, friendshipID, userID string) error {
	tag, err := db.Exec(ctx, `
		DELETE FROM friendships
		WHERE id = $1 AND status = 'accepted' AND (requester_id = $2 OR addressee_id = $2)
	`, friendshipID, userID)
	if err != nil {
		return fmt.Errorf("removing friend: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *SocialRepository) SearchUsers(ctx context.Context, db DBTX, prefix, excludeUserID string, limit int) ([]BasicUserRow, error) {
	rows, err := db.Query(ctx, `
		SELECT id, name, username, profile_picture_url, leaderboard_visible
		FROM users
		WHERE id <> $2
		  AND lower(username) LIKE lower($1) || '%'
		ORDER BY username
		LIMIT $3
	`, prefix, excludeUserID, limit)
	if err != nil {
		return nil, fmt.Errorf("searching users: %w", err)
	}
	defer rows.Close()

	var out []BasicUserRow
	for rows.Next() {
		var item BasicUserRow
		if err := rows.Scan(&item.ID, &item.Name, &item.Username, &item.ProfilePictureURL, &item.LeaderboardVisible); err != nil {
			return nil, fmt.Errorf("scanning user search row: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *SocialRepository) LoadRecentDailyAggregates(ctx context.Context, db DBTX, userID string, days int) ([]struct {
	LocalDay    string
	EffectiveMS int
	Qualified   bool
}, error) {
	rows, err := db.Query(ctx, `
		SELECT local_day, effective_ms, qualified
		FROM streak_daily_aggregates
		WHERE user_id = $1
		ORDER BY local_day DESC
		LIMIT $2
	`, userID, days)
	if err != nil {
		return nil, fmt.Errorf("loading daily aggregates: %w", err)
	}
	defer rows.Close()

	var out []struct {
		LocalDay    string
		EffectiveMS int
		Qualified   bool
	}
	for rows.Next() {
		var item struct {
			LocalDay    string
			EffectiveMS int
			Qualified   bool
		}
		var qualifiedInt int
		if err := rows.Scan(&item.LocalDay, &item.EffectiveMS, &qualifiedInt); err != nil {
			return nil, fmt.Errorf("scanning daily aggregate: %w", err)
		}
		item.Qualified = qualifiedInt == 1
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *SocialRepository) LoadTotalFocusTime(ctx context.Context, db DBTX, userID string) (int64, error) {
	var total int64
	if err := db.QueryRow(ctx, `SELECT COALESCE(sum(effective_ms), 0) FROM streak_daily_aggregates WHERE user_id = $1`, userID).Scan(&total); err != nil {
		return 0, fmt.Errorf("loading total focus time: %w", err)
	}
	return total, nil
}

func (r *SocialRepository) CountFriendships(ctx context.Context, db DBTX, userID string) (int, error) {
	var count int
	if err := db.QueryRow(ctx, `
		SELECT count(*)
		FROM friendships
		WHERE status = 'accepted' AND (requester_id = $1 OR addressee_id = $1)
	`, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting friendships: %w", err)
	}
	return count, nil
}

func (r *SocialRepository) RefreshLeaderboardMetrics(ctx context.Context, db DBTX, userID string) error {
	const query = `
		INSERT INTO leaderboard_metrics (user_id, current_streak_days, total_focus_time_ms, updated_at)
		WITH ordered AS (
			SELECT qualified,
			       ROW_NUMBER() OVER (ORDER BY local_day DESC) AS rn,
			       SUM(CASE WHEN qualified = 0 THEN 1 ELSE 0 END) OVER (
			           ORDER BY local_day DESC
			           ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
			       ) AS break_count
			FROM streak_daily_aggregates
			WHERE user_id = $1
		),
		computed AS (
			SELECT $1::uuid AS user_id,
			       COALESCE(SUM(CASE WHEN break_count = 0 AND qualified = 1 THEN 1 ELSE 0 END), 0)::integer AS current_streak_days,
			       COALESCE((SELECT SUM(effective_ms) FROM streak_daily_aggregates WHERE user_id = $1), 0)::bigint AS total_focus_time_ms
			FROM ordered
		)
		SELECT u.id, c.current_streak_days, c.total_focus_time_ms, EXTRACT(EPOCH FROM now())::bigint
		FROM users u
		LEFT JOIN computed c ON c.user_id = u.id
		WHERE u.id = $1
		ON CONFLICT (user_id) DO UPDATE
		SET current_streak_days = EXCLUDED.current_streak_days,
		    total_focus_time_ms = EXCLUDED.total_focus_time_ms,
		    updated_at = EXCLUDED.updated_at
	`
	if _, err := db.Exec(ctx, query, userID); err != nil {
		return fmt.Errorf("refreshing leaderboard metrics: %w", err)
	}
	return nil
}

func (r *SocialRepository) ListLeaderboardEntries(ctx context.Context, db DBTX, metric LeaderboardMetric, page, limit int) ([]LeaderboardRow, int, error) {
	offset := (page - 1) * limit
	var total int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM users WHERE leaderboard_visible = true`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting leaderboard users: %w", err)
	}

	rows, err := db.Query(ctx, leaderboardEntriesQuery(metric), limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing leaderboard entries: %w", err)
	}
	defer rows.Close()

	var out []LeaderboardRow
	for rows.Next() {
		var item LeaderboardRow
		if err := rows.Scan(
			&item.Rank,
			&item.User.ID,
			&item.User.Name,
			&item.User.Username,
			&item.User.ProfilePictureURL,
			&item.User.LeaderboardVisible,
			&item.CurrentStreakDays,
			&item.TotalFocusTimeMS,
		); err != nil {
			return nil, 0, fmt.Errorf("scanning leaderboard entry: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating leaderboard entries: %w", err)
	}
	return out, total, nil
}

func (r *SocialRepository) GetLeaderboardRank(ctx context.Context, db DBTX, metric LeaderboardMetric, userID string) (LeaderboardRow, error) {
	var out LeaderboardRow
	err := db.QueryRow(ctx, leaderboardRankQuery(metric), userID).Scan(
		&out.Rank,
		&out.User.ID,
		&out.User.Name,
		&out.User.Username,
		&out.User.ProfilePictureURL,
		&out.User.LeaderboardVisible,
		&out.CurrentStreakDays,
		&out.TotalFocusTimeMS,
	)
	if err != nil {
		return LeaderboardRow{}, mapNoRows("loading leaderboard rank", err)
	}
	return out, nil
}

func leaderboardEntriesQuery(metric LeaderboardMetric) string {
	return fmt.Sprintf(`
		WITH ranked AS (
			SELECT ROW_NUMBER() OVER (ORDER BY %s DESC, u.username ASC) AS rank,
			       u.id,
			       u.name,
			       u.username,
			       u.profile_picture_url,
			       u.leaderboard_visible,
			       COALESCE(m.current_streak_days, 0) AS current_streak_days,
			       COALESCE(m.total_focus_time_ms, 0) AS total_focus_time_ms
			FROM users u
			LEFT JOIN leaderboard_metrics m ON m.user_id = u.id
		)
		SELECT rank, id, name, username, profile_picture_url, leaderboard_visible, current_streak_days, total_focus_time_ms
		FROM ranked
		WHERE leaderboard_visible = true
		ORDER BY rank
		LIMIT $1 OFFSET $2
	`, leaderboardMetricOrderExpr(metric))
}

func leaderboardRankQuery(metric LeaderboardMetric) string {
	return fmt.Sprintf(`
		WITH ranked AS (
			SELECT ROW_NUMBER() OVER (ORDER BY %s DESC, u.username ASC) AS rank,
			       u.id,
			       u.name,
			       u.username,
			       u.profile_picture_url,
			       u.leaderboard_visible,
			       COALESCE(m.current_streak_days, 0) AS current_streak_days,
			       COALESCE(m.total_focus_time_ms, 0) AS total_focus_time_ms
			FROM users u
			LEFT JOIN leaderboard_metrics m ON m.user_id = u.id
		)
		SELECT rank, id, name, username, profile_picture_url, leaderboard_visible, current_streak_days, total_focus_time_ms
		FROM ranked
		WHERE id = $1
	`, leaderboardMetricOrderExpr(metric))
}

func leaderboardMetricOrderExpr(metric LeaderboardMetric) string {
	switch metric {
	case LeaderboardMetricStreak:
		return "COALESCE(m.current_streak_days, 0)"
	case LeaderboardMetricFocusTime:
		return "COALESCE(m.total_focus_time_ms, 0)"
	default:
		panic("unsupported leaderboard metric")
	}
}

func mapNoRows(op string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", op, err)
}
