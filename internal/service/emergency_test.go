package service

import (
	"context"
	"errors"
	"log/slog"
	"io"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/repository"
)

// ---------------------------------------------------------------------------
// Fake emergency stop repository
// ---------------------------------------------------------------------------

type fakeEmergencyRepo struct {
	getUsedFn          func(ctx context.Context, db repository.DBTX, userID string) (int, error)
	getUsedForUpdateFn func(ctx context.Context, db repository.DBTX, userID string) (int, error)
	incrementFn        func(ctx context.Context, db repository.DBTX, userID string) error
}

var _ repository.EmergencyStopRepository = (*fakeEmergencyRepo)(nil)

func (f *fakeEmergencyRepo) GetEmergencyStopsUsed(ctx context.Context, db repository.DBTX, userID string) (int, error) {
	if f.getUsedFn != nil {
		return f.getUsedFn(ctx, db, userID)
	}
	return 0, nil
}

func (f *fakeEmergencyRepo) GetEmergencyStopsUsedForUpdate(ctx context.Context, db repository.DBTX, userID string) (int, error) {
	if f.getUsedForUpdateFn != nil {
		return f.getUsedForUpdateFn(ctx, db, userID)
	}
	return 0, nil
}

func (f *fakeEmergencyRepo) IncrementEmergencyStopsUsed(ctx context.Context, db repository.DBTX, userID string) error {
	if f.incrementFn != nil {
		return f.incrementFn(ctx, db, userID)
	}
	return nil
}

func newTestEmergencyService(repo *fakeEmergencyRepo) *EmergencyStopService {
	return NewEmergencyStopService(&fakePool{}, repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// ---------------------------------------------------------------------------
// UseEmergencyStop tests
// ---------------------------------------------------------------------------

func TestUseEmergencyStop_Success(t *testing.T) {
	t.Parallel()
	var incrementCalled bool
	repo := &fakeEmergencyRepo{
		getUsedForUpdateFn: func(_ context.Context, _ repository.DBTX, _ string) (int, error) {
			return 0, nil
		},
		incrementFn: func(_ context.Context, _ repository.DBTX, _ string) error {
			incrementCalled = true
			return nil
		},
	}
	svc := newTestEmergencyService(repo)

	out, err := svc.UseEmergencyStop(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("UseEmergencyStop() error = %v", err)
	}
	if out.RemainingEmergencyStops != 2 {
		t.Errorf("remaining = %d, want 2", out.RemainingEmergencyStops)
	}
	if !incrementCalled {
		t.Error("expected IncrementEmergencyStopsUsed to be called")
	}
}

func TestUseEmergencyStop_SecondUse(t *testing.T) {
	t.Parallel()
	repo := &fakeEmergencyRepo{
		getUsedForUpdateFn: func(_ context.Context, _ repository.DBTX, _ string) (int, error) {
			return 2, nil
		},
		incrementFn: func(_ context.Context, _ repository.DBTX, _ string) error {
			return nil
		},
	}
	svc := newTestEmergencyService(repo)

	out, err := svc.UseEmergencyStop(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("UseEmergencyStop() error = %v", err)
	}
	if out.RemainingEmergencyStops != 0 {
		t.Errorf("remaining = %d, want 0", out.RemainingEmergencyStops)
	}
}

func TestUseEmergencyStop_NoStopsRemaining(t *testing.T) {
	t.Parallel()
	repo := &fakeEmergencyRepo{
		getUsedForUpdateFn: func(_ context.Context, _ repository.DBTX, _ string) (int, error) {
			return 3, nil
		},
	}
	svc := newTestEmergencyService(repo)

	_, err := svc.UseEmergencyStop(context.Background(), "user-1")
	if err == nil {
		t.Fatal("UseEmergencyStop() expected error, got nil")
	}
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("UseEmergencyStop() error = %v, want ErrForbidden", err)
	}
}

func TestUseEmergencyStop_RepoGetError(t *testing.T) {
	t.Parallel()
	repo := &fakeEmergencyRepo{
		getUsedForUpdateFn: func(_ context.Context, _ repository.DBTX, _ string) (int, error) {
			return 0, errors.New("db error")
		},
	}
	svc := newTestEmergencyService(repo)

	_, err := svc.UseEmergencyStop(context.Background(), "user-1")
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("UseEmergencyStop() error = %v, want ErrInternal", err)
	}
}

func TestUseEmergencyStop_RepoIncrementError(t *testing.T) {
	t.Parallel()
	repo := &fakeEmergencyRepo{
		getUsedForUpdateFn: func(_ context.Context, _ repository.DBTX, _ string) (int, error) {
			return 0, nil
		},
		incrementFn: func(_ context.Context, _ repository.DBTX, _ string) error {
			return errors.New("db error")
		},
	}
	svc := newTestEmergencyService(repo)

	_, err := svc.UseEmergencyStop(context.Background(), "user-1")
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("UseEmergencyStop() error = %v, want ErrInternal", err)
	}
}

// ---------------------------------------------------------------------------
// GetRemainingStops tests
// ---------------------------------------------------------------------------

func TestGetRemainingStops_Fresh(t *testing.T) {
	t.Parallel()
	repo := &fakeEmergencyRepo{
		getUsedFn: func(_ context.Context, _ repository.DBTX, _ string) (int, error) {
			return 0, nil
		},
	}
	svc := newTestEmergencyService(repo)

	out, err := svc.GetRemainingStops(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("GetRemainingStops() error = %v", err)
	}
	if out.RemainingEmergencyStops != 3 {
		t.Errorf("remaining = %d, want 3", out.RemainingEmergencyStops)
	}
}

func TestGetRemainingStops_PartiallyUsed(t *testing.T) {
	t.Parallel()
	repo := &fakeEmergencyRepo{
		getUsedFn: func(_ context.Context, _ repository.DBTX, _ string) (int, error) {
			return 2, nil
		},
	}
	svc := newTestEmergencyService(repo)

	out, err := svc.GetRemainingStops(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("GetRemainingStops() error = %v", err)
	}
	if out.RemainingEmergencyStops != 1 {
		t.Errorf("remaining = %d, want 1", out.RemainingEmergencyStops)
	}
}

func TestGetRemainingStops_AllUsed(t *testing.T) {
	t.Parallel()
	repo := &fakeEmergencyRepo{
		getUsedFn: func(_ context.Context, _ repository.DBTX, _ string) (int, error) {
			return 3, nil
		},
	}
	svc := newTestEmergencyService(repo)

	out, err := svc.GetRemainingStops(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("GetRemainingStops() error = %v", err)
	}
	if out.RemainingEmergencyStops != 0 {
		t.Errorf("remaining = %d, want 0", out.RemainingEmergencyStops)
	}
}

func TestGetRemainingStops_RepoError(t *testing.T) {
	t.Parallel()
	repo := &fakeEmergencyRepo{
		getUsedFn: func(_ context.Context, _ repository.DBTX, _ string) (int, error) {
			return 0, errors.New("db error")
		},
	}
	svc := newTestEmergencyService(repo)

	_, err := svc.GetRemainingStops(context.Background(), "user-1")
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("GetRemainingStops() error = %v, want ErrInternal", err)
	}
}
