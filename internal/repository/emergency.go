package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// EmergencyStopRepository defines operations for the emergency stop feature.
type EmergencyStopRepository interface {
	GetEmergencyStopsUsed(ctx context.Context, db DBTX, userID string) (int, error)
	GetEmergencyStopsUsedForUpdate(ctx context.Context, db DBTX, userID string) (int, error)
	IncrementEmergencyStopsUsed(ctx context.Context, db DBTX, userID string) error
}

type PgxEmergencyStopRepository struct{}

func NewPgxEmergencyStopRepository() *PgxEmergencyStopRepository {
	return &PgxEmergencyStopRepository{}
}

var _ EmergencyStopRepository = (*PgxEmergencyStopRepository)(nil)

func (r *PgxEmergencyStopRepository) GetEmergencyStopsUsed(ctx context.Context, db DBTX, userID string) (int, error) {
	var used int
	err := db.QueryRow(ctx,
		`SELECT emergency_stops_used FROM users WHERE id = $1`,
		userID,
	).Scan(&used)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("getting emergency stops used: %w", err)
	}
	return used, nil
}

func (r *PgxEmergencyStopRepository) GetEmergencyStopsUsedForUpdate(ctx context.Context, db DBTX, userID string) (int, error) {
	var used int
	err := db.QueryRow(ctx,
		`SELECT emergency_stops_used FROM users WHERE id = $1 FOR UPDATE`,
		userID,
	).Scan(&used)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("getting emergency stops used for update: %w", err)
	}
	return used, nil
}

func (r *PgxEmergencyStopRepository) IncrementEmergencyStopsUsed(ctx context.Context, db DBTX, userID string) error {
	_, err := db.Exec(ctx,
		`UPDATE users SET emergency_stops_used = emergency_stops_used + 1, updated_at = now() WHERE id = $1`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("incrementing emergency stops used: %w", err)
	}
	return nil
}
