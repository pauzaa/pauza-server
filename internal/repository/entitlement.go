package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// EntitlementRepository defines the data-access operations for user
// entitlements managed by RevenueCat webhooks. Every method accepts a DBTX
// so it can be called against the pool or inside an explicit transaction.
type EntitlementRepository interface {
	UpsertEntitlement(ctx context.Context, db DBTX, params UpsertEntitlementParams) error
	GetEntitlement(ctx context.Context, db DBTX, userID, entitlement string) (EntitlementDetailRow, error)
	GetUsersByRevenueCatID(ctx context.Context, db DBTX, appUserID, originalAppUserID string) ([]UserRow, error)
}

// EntitlementDetailRow holds all user_entitlements columns needed when
// preserving existing RevenueCat/snapshot data during an admin override.
type EntitlementDetailRow struct {
	UserID                      string
	Entitlement                 string
	IsActive                    bool
	RevenueCatAppUserID         *string
	RevenueCatOriginalAppUserID *string
	CurrentPeriodEnd            *time.Time
	LastWebhookEventAt          *time.Time
}

// UpsertEntitlementParams holds the fields written by the entitlement upsert.
type UpsertEntitlementParams struct {
	UserID                      string
	Entitlement                 string
	IsActive                    bool
	RevenueCatAppUserID         *string
	RevenueCatOriginalAppUserID *string
	CurrentPeriodEnd            *time.Time
	LastWebhookEventAt          *time.Time
}

// Compile-time check: PgxEntitlementRepository satisfies EntitlementRepository.
var _ EntitlementRepository = (*PgxEntitlementRepository)(nil)

// PgxEntitlementRepository implements EntitlementRepository using pgx queries.
type PgxEntitlementRepository struct{}

// NewPgxEntitlementRepository returns a PgxEntitlementRepository.
func NewPgxEntitlementRepository() *PgxEntitlementRepository {
	return &PgxEntitlementRepository{}
}

func (r *PgxEntitlementRepository) UpsertEntitlement(ctx context.Context, db DBTX, params UpsertEntitlementParams) error {
	_, err := db.Exec(ctx,
		`INSERT INTO user_entitlements (
			user_id, entitlement, is_active,
			revenuecat_app_user_id, revenuecat_original_app_user_id,
			current_period_end, last_webhook_event_at,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())
		ON CONFLICT (user_id, entitlement) DO UPDATE SET
			is_active                       = EXCLUDED.is_active,
			revenuecat_app_user_id          = EXCLUDED.revenuecat_app_user_id,
			revenuecat_original_app_user_id = EXCLUDED.revenuecat_original_app_user_id,
			current_period_end              = EXCLUDED.current_period_end,
			last_webhook_event_at           = EXCLUDED.last_webhook_event_at,
			updated_at                      = now()`,
		params.UserID,
		params.Entitlement,
		params.IsActive,
		params.RevenueCatAppUserID,
		params.RevenueCatOriginalAppUserID,
		params.CurrentPeriodEnd,
		params.LastWebhookEventAt,
	)
	if err != nil {
		return fmt.Errorf("upserting entitlement: %w", err)
	}
	return nil
}

func (r *PgxEntitlementRepository) GetEntitlement(ctx context.Context, db DBTX, userID, entitlement string) (EntitlementDetailRow, error) {
	var e EntitlementDetailRow
	err := db.QueryRow(ctx,
		`SELECT user_id, entitlement, is_active,
		        revenuecat_app_user_id, revenuecat_original_app_user_id,
		        current_period_end, last_webhook_event_at
		 FROM user_entitlements
		 WHERE user_id = $1 AND entitlement = $2`,
		userID, entitlement,
	).Scan(
		&e.UserID, &e.Entitlement, &e.IsActive,
		&e.RevenueCatAppUserID, &e.RevenueCatOriginalAppUserID,
		&e.CurrentPeriodEnd, &e.LastWebhookEventAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return EntitlementDetailRow{}, ErrNotFound
	}
	if err != nil {
		return EntitlementDetailRow{}, fmt.Errorf("getting entitlement: %w", err)
	}
	return e, nil
}

func (r *PgxEntitlementRepository) GetUsersByRevenueCatID(ctx context.Context, db DBTX, appUserID, originalAppUserID string) ([]UserRow, error) {
	rows, err := db.Query(ctx,
		`SELECT `+userColumns+`
		 FROM users
		 WHERE id IN (
			SELECT DISTINCT user_id
			FROM user_entitlements
			WHERE revenuecat_app_user_id         IN ($1, $2)
			   OR revenuecat_original_app_user_id IN ($1, $2)
		 )`,
		appUserID, originalAppUserID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying users by revenuecat id: %w", err)
	}
	defer rows.Close()

	var users []UserRow
	for rows.Next() {
		var u UserRow
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Username, &u.ProfilePictureURL,
			&u.LeaderboardVisible, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning user by revenuecat id: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating users by revenuecat id: %w", err)
	}
	if len(users) == 0 {
		return nil, ErrNotFound
	}
	return users, nil
}
