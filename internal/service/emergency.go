package service

import (
	"context"
	"log/slog"

	"github.com/IsorilovA/pauza-server/internal/repository"
)

const maxEmergencyStops = 3

type EmergencyStopService struct {
	pool   repository.Pool
	repo   repository.EmergencyStopRepository
	logger *slog.Logger
}

func NewEmergencyStopService(pool repository.Pool, repo repository.EmergencyStopRepository, logger *slog.Logger) *EmergencyStopService {
	if logger == nil {
		logger = slog.Default()
	}
	return &EmergencyStopService{pool: pool, repo: repo, logger: logger}
}

type EmergencyStopOutput struct {
	RemainingEmergencyStops int `json:"remaining_emergency_stops"`
}

func (s *EmergencyStopService) UseEmergencyStop(ctx context.Context, userID string) (EmergencyStopOutput, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning emergency stop transaction", "err", err)
		return EmergencyStopOutput{}, ErrInternal
	}
	defer tx.Rollback(ctx)

	used, err := s.repo.GetEmergencyStopsUsedForUpdate(ctx, tx, userID)
	if err != nil {
		s.logger.Error("getting emergency stops used", "err", err, "user_id", userID)
		return EmergencyStopOutput{}, ErrInternal
	}

	if used >= maxEmergencyStops {
		return EmergencyStopOutput{}, ForbiddenError("No emergency stops remaining")
	}

	if err := s.repo.IncrementEmergencyStopsUsed(ctx, tx, userID); err != nil {
		s.logger.Error("incrementing emergency stops", "err", err, "user_id", userID)
		return EmergencyStopOutput{}, ErrInternal
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("committing emergency stop transaction", "err", err, "user_id", userID)
		return EmergencyStopOutput{}, ErrInternal
	}

	return EmergencyStopOutput{
		RemainingEmergencyStops: maxEmergencyStops - (used + 1),
	}, nil
}

func (s *EmergencyStopService) GetRemainingStops(ctx context.Context, userID string) (EmergencyStopOutput, error) {
	used, err := s.repo.GetEmergencyStopsUsed(ctx, s.pool, userID)
	if err != nil {
		s.logger.Error("getting remaining emergency stops", "err", err, "user_id", userID)
		return EmergencyStopOutput{}, ErrInternal
	}

	remaining := maxEmergencyStops - used
	if remaining < 0 {
		remaining = 0
	}

	return EmergencyStopOutput{RemainingEmergencyStops: remaining}, nil
}
