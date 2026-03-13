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

func normalizeTableChanges[T any, D any](changes syncmodel.TableChanges[T, D]) syncmodel.TableChanges[T, D] {
	if changes.Upserts == nil {
		changes.Upserts = make([]T, 0)
	}
	if changes.Deletions == nil {
		changes.Deletions = make([]D, 0)
	}
	return changes
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

	if in.Tables.Modes != nil {
		changes, syncErr := s.repo.SyncModes(ctx, tx, in.UserID, *in.Tables.Modes)
		if syncErr != nil {
			return s.syncInternal("syncing modes", syncErr)
		}
		changes = normalizeTableChanges(changes)
		out.Tables.Modes = &changes
	}
	if in.Tables.ModeBlockedApps != nil {
		changes, syncErr := s.repo.SyncModeBlockedApps(ctx, tx, in.UserID, *in.Tables.ModeBlockedApps)
		if syncErr != nil {
			return s.syncInternal("syncing mode_blocked_apps", syncErr)
		}
		changes = normalizeTableChanges(changes)
		out.Tables.ModeBlockedApps = &changes
	}
	if in.Tables.Schedules != nil {
		changes, syncErr := s.repo.SyncSchedules(ctx, tx, in.UserID, *in.Tables.Schedules)
		if syncErr != nil {
			return s.syncInternal("syncing schedules", syncErr)
		}
		changes = normalizeTableChanges(changes)
		out.Tables.Schedules = &changes
	}
	if in.Tables.RestrictionSessions != nil {
		changes, syncErr := s.repo.SyncRestrictionSessions(ctx, tx, in.UserID, *in.Tables.RestrictionSessions)
		if syncErr != nil {
			return s.syncInternal("syncing restriction_sessions", syncErr)
		}
		changes = normalizeTableChanges(changes)
		out.Tables.RestrictionSessions = &changes
	}
	if in.Tables.RestrictionLifecycleEvents != nil {
		changes, syncErr := s.repo.SyncRestrictionLifecycleEvents(ctx, tx, in.UserID, *in.Tables.RestrictionLifecycleEvents)
		if syncErr != nil {
			return s.syncInternal("syncing restriction_lifecycle_events", syncErr)
		}
		changes = normalizeTableChanges(changes)
		out.Tables.RestrictionLifecycleEvents = &changes
	}
	if in.Tables.NFCLinkedChips != nil {
		changes, syncErr := s.repo.SyncNFCLinkedChips(ctx, tx, in.UserID, *in.Tables.NFCLinkedChips)
		if syncErr != nil {
			return s.syncInternal("syncing nfc_linked_chips", syncErr)
		}
		changes = normalizeTableChanges(changes)
		out.Tables.NFCLinkedChips = &changes
	}
	if in.Tables.QRLinkedCodes != nil {
		changes, syncErr := s.repo.SyncQRLinkedCodes(ctx, tx, in.UserID, *in.Tables.QRLinkedCodes)
		if syncErr != nil {
			return s.syncInternal("syncing qr_linked_codes", syncErr)
		}
		changes = normalizeTableChanges(changes)
		out.Tables.QRLinkedCodes = &changes
	}
	if in.Tables.StreakSessionDailyRollups != nil {
		changes, syncErr := s.repo.SyncStreakSessionDailyRollups(ctx, tx, in.UserID, *in.Tables.StreakSessionDailyRollups)
		if syncErr != nil {
			return s.syncInternal("syncing streak_session_daily_rollups", syncErr)
		}
		changes = normalizeTableChanges(changes)
		out.Tables.StreakSessionDailyRollups = &changes
	}
	if in.Tables.StreakDailyAggregates != nil {
		changes, syncErr := s.repo.SyncStreakDailyAggregates(ctx, tx, in.UserID, *in.Tables.StreakDailyAggregates)
		if syncErr != nil {
			return s.syncInternal("syncing streak_daily_aggregates", syncErr)
		}
		changes = normalizeTableChanges(changes)
		out.Tables.StreakDailyAggregates = &changes
	}

	serverTime, err := s.repo.ServerCursor(ctx, tx)
	if err != nil {
		s.logger.Error("loading sync server cursor", "err", err)
		return SyncOutput{}, ErrInternal
	}

	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("committing sync transaction", "err", err)
		return SyncOutput{}, ErrInternal
	}

	out.ServerTime = serverTime
	return out, nil
}

func (s *SyncService) syncInternal(stage string, err error) (SyncOutput, error) {
	s.logger.Error(stage, "err", err)
	return SyncOutput{}, ErrInternal
}
