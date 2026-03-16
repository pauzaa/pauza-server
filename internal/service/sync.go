package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/IsorilovA/pauza-server/internal/repository"
	"github.com/IsorilovA/pauza-server/internal/syncmodel"
)

type SyncInput struct {
	UserID string
	Tables syncmodel.Tables
}

type SyncOutput = syncmodel.Response

type syncUserVerifier interface {
	GetUserByIDForUpdate(ctx context.Context, db repository.DBTX, userID string) (repository.UserRow, error)
}

type SyncService struct {
	pool         repository.Pool
	repo         repository.SyncRepository
	userVerifier syncUserVerifier
	logger       *slog.Logger
}

func normalizeTableResult[T any, D any](result syncmodel.TableResult[T, D]) syncmodel.TableResult[T, D] {
	if result.Upserts == nil {
		result.Upserts = make([]T, 0)
	}
	if result.Deletions == nil {
		result.Deletions = make([]D, 0)
	}
	return result
}

func NewSyncService(pool repository.Pool, repo repository.SyncRepository, userVerifier syncUserVerifier, logger *slog.Logger) *SyncService {
	return &SyncService{pool: pool, repo: repo, userVerifier: userVerifier, logger: logger}
}

func (s *SyncService) Sync(ctx context.Context, in SyncInput) (SyncOutput, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("beginning sync transaction", "err", err)
		return SyncOutput{}, ErrInternal
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	_, errUser := s.userVerifier.GetUserByIDForUpdate(ctx, tx, in.UserID)
	if errUser != nil {
		if errors.Is(errUser, repository.ErrNotFound) {
			return SyncOutput{}, UnauthorizedError("Missing or invalid authentication")
		}
		s.logger.Error("querying user for sync", "err", errUser)
		return SyncOutput{}, ErrInternal
	}

	out := SyncOutput{}
	var affectedStreakDays []string

	if in.Tables.Modes != nil {
		result, cascadeDays, syncErr := s.repo.SyncModes(ctx, tx, in.UserID, *in.Tables.Modes)
		if syncErr != nil {
			return s.syncInternal("syncing modes", syncErr)
		}
		result = normalizeTableResult(result)
		out.Tables.Modes = &result
		affectedStreakDays = append(affectedStreakDays, cascadeDays...)
	}
	if in.Tables.ModeBlockedApps != nil {
		result, syncErr := s.repo.SyncModeBlockedApps(ctx, tx, in.UserID, *in.Tables.ModeBlockedApps)
		if syncErr != nil {
			return s.syncInternal("syncing mode_blocked_apps", syncErr)
		}
		result = normalizeTableResult(result)
		out.Tables.ModeBlockedApps = &result
	}
	if in.Tables.Schedules != nil {
		result, syncErr := s.repo.SyncSchedules(ctx, tx, in.UserID, *in.Tables.Schedules)
		if syncErr != nil {
			return s.syncInternal("syncing schedules", syncErr)
		}
		result = normalizeTableResult(result)
		out.Tables.Schedules = &result
	}
	if in.Tables.RestrictionSessions != nil {
		result, cascadeDays, syncErr := s.repo.SyncRestrictionSessions(ctx, tx, in.UserID, *in.Tables.RestrictionSessions)
		if syncErr != nil {
			return s.syncInternal("syncing restriction_sessions", syncErr)
		}
		result = normalizeTableResult(result)
		out.Tables.RestrictionSessions = &result
		affectedStreakDays = append(affectedStreakDays, cascadeDays...)
	}
	if in.Tables.RestrictionLifecycleEvents != nil {
		result, syncErr := s.repo.SyncRestrictionLifecycleEvents(ctx, tx, in.UserID, *in.Tables.RestrictionLifecycleEvents)
		if syncErr != nil {
			return s.syncInternal("syncing restriction_lifecycle_events", syncErr)
		}
		result = normalizeTableResult(result)
		out.Tables.RestrictionLifecycleEvents = &result
	}
	if in.Tables.NFCLinkedChips != nil {
		result, syncErr := s.repo.SyncNFCLinkedChips(ctx, tx, in.UserID, *in.Tables.NFCLinkedChips)
		if syncErr != nil {
			return s.syncInternal("syncing nfc_linked_chips", syncErr)
		}
		result = normalizeTableResult(result)
		out.Tables.NFCLinkedChips = &result
	}
	if in.Tables.QRLinkedCodes != nil {
		result, syncErr := s.repo.SyncQRLinkedCodes(ctx, tx, in.UserID, *in.Tables.QRLinkedCodes)
		if syncErr != nil {
			return s.syncInternal("syncing qr_linked_codes", syncErr)
		}
		result = normalizeTableResult(result)
		out.Tables.QRLinkedCodes = &result
	}

	// Process streak rollups (may trigger recomputation)
	if in.Tables.StreakSessionDailyRollups != nil {
		for _, u := range in.Tables.StreakSessionDailyRollups.Upserts {
			affectedStreakDays = append(affectedStreakDays, u.LocalDay)
		}
		for _, d := range in.Tables.StreakSessionDailyRollups.Deletions {
			affectedStreakDays = append(affectedStreakDays, d.LocalDay)
		}
		result, syncErr := s.repo.SyncStreakSessionDailyRollups(ctx, tx, in.UserID, *in.Tables.StreakSessionDailyRollups)
		if syncErr != nil {
			return s.syncInternal("syncing streak_session_daily_rollups", syncErr)
		}
		result = normalizeTableResult(result)
		out.Tables.StreakSessionDailyRollups = &result
	}

	// Recompute streaks for affected days
	if len(affectedStreakDays) > 0 {
		// Deduplicate days
		seen := make(map[string]struct{}, len(affectedStreakDays))
		unique := make([]string, 0, len(affectedStreakDays))
		for _, day := range affectedStreakDays {
			if _, ok := seen[day]; !ok {
				seen[day] = struct{}{}
				unique = append(unique, day)
			}
		}
		if err := s.repo.RecomputeStreakAggregates(ctx, tx, in.UserID, unique); err != nil {
			return s.syncInternal("recomputing streak aggregates", err)
		}
	}

	// Streak daily aggregates - read-only pull
	if in.Tables.StreakDailyAggregates != nil {
		result, syncErr := s.repo.ListStreakDailyAggregateChanges(ctx, tx, in.UserID, in.Tables.StreakDailyAggregates.Cursor)
		if syncErr != nil {
			return s.syncInternal("listing streak_daily_aggregates", syncErr)
		}
		result = normalizeTableResult(result)
		out.Tables.StreakDailyAggregates = &result
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("committing sync transaction", "err", err)
		return SyncOutput{}, ErrInternal
	}

	return out, nil
}

func (s *SyncService) syncInternal(stage string, err error) (SyncOutput, error) {
	s.logger.Error(stage, "err", err)
	return SyncOutput{}, ErrInternal
}
