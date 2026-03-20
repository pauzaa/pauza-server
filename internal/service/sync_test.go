package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/repository"
	"github.com/IsorilovA/pauza-server/internal/syncmodel"
)

type fakeSyncRepo struct {
	syncModesFn                      func(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.Mode, string]) (syncmodel.TableResult[syncmodel.Mode, string], []string, error)
	syncModeBlockedAppsFn            func(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]) (syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey], error)
	syncSchedulesFn                  func(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.Schedule, string]) (syncmodel.TableResult[syncmodel.Schedule, string], error)
	syncRestrictionSessionsFn        func(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.RestrictionSession, string]) (syncmodel.TableResult[syncmodel.RestrictionSession, string], []string, error)
	syncRestrictionLifecycleEventsFn func(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.RestrictionLifecycleEvent, string]) (syncmodel.TableResult[syncmodel.RestrictionLifecycleEvent, string], error)
	syncNFCLinkedChipsFn             func(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.NFCLinkedChip, string]) (syncmodel.TableResult[syncmodel.NFCLinkedChip, string], error)
	syncQRLinkedCodesFn              func(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.QRLinkedCode, string]) (syncmodel.TableResult[syncmodel.QRLinkedCode, string], error)
	syncStreakSessionDailyRollupsFn  func(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]) (syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey], error)
	listStreakDailyAggregateChangesFn func(ctx context.Context, db repository.DBTX, userID string, cursor int64) (syncmodel.TableResult[syncmodel.StreakDailyAggregate, string], error)
	recomputeStreakAggregatesFn       func(ctx context.Context, db repository.DBTX, userID string, affectedDays []string) error
}

var _ repository.SyncRepository = (*fakeSyncRepo)(nil)
var _ syncUserVerifier = (*fakeAuthRepo)(nil)
var _ syncUserVerifier = (*repository.PgxAuthRepository)(nil)

func (f *fakeSyncRepo) SyncModes(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.Mode, string]) (syncmodel.TableResult[syncmodel.Mode, string], []string, error) {
	if f.syncModesFn != nil {
		return f.syncModesFn(ctx, db, userID, in)
	}
	panic("fakeSyncRepo.SyncModes: not configured")
}

func (f *fakeSyncRepo) SyncModeBlockedApps(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey]) (syncmodel.TableResult[syncmodel.ModeBlockedApp, syncmodel.ModeBlockedAppKey], error) {
	if f.syncModeBlockedAppsFn != nil {
		return f.syncModeBlockedAppsFn(ctx, db, userID, in)
	}
	panic("fakeSyncRepo.SyncModeBlockedApps: not configured")
}

func (f *fakeSyncRepo) SyncSchedules(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.Schedule, string]) (syncmodel.TableResult[syncmodel.Schedule, string], error) {
	if f.syncSchedulesFn != nil {
		return f.syncSchedulesFn(ctx, db, userID, in)
	}
	panic("fakeSyncRepo.SyncSchedules: not configured")
}

func (f *fakeSyncRepo) SyncRestrictionSessions(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.RestrictionSession, string]) (syncmodel.TableResult[syncmodel.RestrictionSession, string], []string, error) {
	if f.syncRestrictionSessionsFn != nil {
		return f.syncRestrictionSessionsFn(ctx, db, userID, in)
	}
	panic("fakeSyncRepo.SyncRestrictionSessions: not configured")
}

func (f *fakeSyncRepo) SyncRestrictionLifecycleEvents(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.RestrictionLifecycleEvent, string]) (syncmodel.TableResult[syncmodel.RestrictionLifecycleEvent, string], error) {
	if f.syncRestrictionLifecycleEventsFn != nil {
		return f.syncRestrictionLifecycleEventsFn(ctx, db, userID, in)
	}
	panic("fakeSyncRepo.SyncRestrictionLifecycleEvents: not configured")
}

func (f *fakeSyncRepo) SyncNFCLinkedChips(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.NFCLinkedChip, string]) (syncmodel.TableResult[syncmodel.NFCLinkedChip, string], error) {
	if f.syncNFCLinkedChipsFn != nil {
		return f.syncNFCLinkedChipsFn(ctx, db, userID, in)
	}
	panic("fakeSyncRepo.SyncNFCLinkedChips: not configured")
}

func (f *fakeSyncRepo) SyncQRLinkedCodes(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.QRLinkedCode, string]) (syncmodel.TableResult[syncmodel.QRLinkedCode, string], error) {
	if f.syncQRLinkedCodesFn != nil {
		return f.syncQRLinkedCodesFn(ctx, db, userID, in)
	}
	panic("fakeSyncRepo.SyncQRLinkedCodes: not configured")
}

func (f *fakeSyncRepo) SyncStreakSessionDailyRollups(ctx context.Context, db repository.DBTX, userID string, in syncmodel.TableSync[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey]) (syncmodel.TableResult[syncmodel.StreakSessionDailyRollup, syncmodel.StreakSessionDailyRollupKey], error) {
	if f.syncStreakSessionDailyRollupsFn != nil {
		return f.syncStreakSessionDailyRollupsFn(ctx, db, userID, in)
	}
	panic("fakeSyncRepo.SyncStreakSessionDailyRollups: not configured")
}

func (f *fakeSyncRepo) ListStreakDailyAggregateChanges(ctx context.Context, db repository.DBTX, userID string, cursor int64) (syncmodel.TableResult[syncmodel.StreakDailyAggregate, string], error) {
	if f.listStreakDailyAggregateChangesFn != nil {
		return f.listStreakDailyAggregateChangesFn(ctx, db, userID, cursor)
	}
	return syncmodel.TableResult[syncmodel.StreakDailyAggregate, string]{}, nil
}

func (f *fakeSyncRepo) RecomputeStreakAggregates(ctx context.Context, db repository.DBTX, userID string, affectedDays []string) error {
	if f.recomputeStreakAggregatesFn != nil {
		return f.recomputeStreakAggregatesFn(ctx, db, userID, affectedDays)
	}
	return nil
}

func (f *fakeSyncRepo) RefreshLeaderboard(ctx context.Context, db repository.DBTX, userID string) error {
	return nil
}

func TestSync_MissingUser_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	userVerifier := &fakeAuthRepo{
		getUserByIDForUpdateFn: func(_ context.Context, db repository.DBTX, userID string) (repository.UserRow, error) {
			if userID != "user-001" {
				t.Errorf("userID = %q, want %q", userID, "user-001")
			}
			if _, ok := db.(*fakeTx); !ok {
				t.Fatalf("db type = %T, want *fakeTx", db)
			}
			return repository.UserRow{}, repository.ErrNotFound
		},
		getUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			t.Fatal("GetVerifiedUserByID should not be called when row-lock lookup is supported")
			return repository.UserRow{}, nil
		},
	}
	repo := &fakeSyncRepo{
		syncModesFn: func(context.Context, repository.DBTX, string, syncmodel.TableSync[syncmodel.Mode, string]) (syncmodel.TableResult[syncmodel.Mode, string], []string, error) {
			t.Fatal("SyncModes should not be called when authenticated user is missing")
			return syncmodel.TableResult[syncmodel.Mode, string]{}, nil, nil
		},
	}
	svc := NewSyncService(
		&fakePool{},
		repo,
		userVerifier,
		slog.New(slog.NewTextHandler(devNull{}, &slog.HandlerOptions{Level: slog.LevelError})),
	)

	_, err := svc.Sync(context.Background(), SyncInput{
		UserID: "user-001",
		Tables: syncmodel.Tables{
			Modes: &syncmodel.TableSync[syncmodel.Mode, string]{},
		},
	})

	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Sync() error = %v, want ErrUnauthorized", err)
	}
}
