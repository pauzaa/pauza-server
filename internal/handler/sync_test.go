package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/middleware"
	"github.com/IsorilovA/pauza-server/internal/service"
	"github.com/IsorilovA/pauza-server/internal/syncmodel"
)

type mockSyncService struct {
	syncFn func(ctx context.Context, in service.SyncInput) (service.SyncOutput, error)
}

func (m *mockSyncService) Sync(ctx context.Context, in service.SyncInput) (service.SyncOutput, error) {
	if m.syncFn != nil {
		return m.syncFn(ctx, in)
	}
	return service.SyncOutput{}, nil
}

func validSyncPayload() string {
	return `{"tables":{"modes":{"last_synced_at":0,"upserts":[],"deletions":[]},"mode_blocked_apps":{"last_synced_at":0,"upserts":[],"deletions":[]},"schedules":{"last_synced_at":0,"upserts":[],"deletions":[]},"restriction_sessions":{"last_synced_at":0,"upserts":[],"deletions":[]},"restriction_lifecycle_events":{"last_synced_at":0,"upserts":[],"deletions":[]},"nfc_linked_chips":{"last_synced_at":0,"upserts":[],"deletions":[]},"qr_linked_codes":{"last_synced_at":0,"upserts":[],"deletions":[]},"streak_session_daily_rollups":{"last_synced_at":0,"upserts":[],"deletions":[]},"streak_daily_aggregates":{"last_synced_at":0,"upserts":[],"deletions":[]}}}`
}

func minimalSyncPayload() string {
	return `{"tables":{"modes":{"last_synced_at":0,"upserts":[],"deletions":[]}}}`
}

func TestSync_MissingAuthContext(t *testing.T) {
	h := NewSyncHandler(&mockSyncService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(validSyncPayload()))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Sync(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestSync_InvalidJSON(t *testing.T) {
	h := NewSyncHandler(&mockSyncService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-1"}))
	rec := httptest.NewRecorder()

	h.Sync(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestSync_UnknownFieldRejected(t *testing.T) {
	h := NewSyncHandler(&mockSyncService{})
	body := `{"tables":{"modes":{"last_synced_at":0,"upserts":[],"deletions":[],"x":1},"mode_blocked_apps":{"last_synced_at":0,"upserts":[],"deletions":[]},"schedules":{"last_synced_at":0,"upserts":[],"deletions":[]},"restriction_sessions":{"last_synced_at":0,"upserts":[],"deletions":[]},"restriction_lifecycle_events":{"last_synced_at":0,"upserts":[],"deletions":[]},"nfc_linked_chips":{"last_synced_at":0,"upserts":[],"deletions":[]},"qr_linked_codes":{"last_synced_at":0,"upserts":[],"deletions":[]},"streak_session_daily_rollups":{"last_synced_at":0,"upserts":[],"deletions":[]},"streak_daily_aggregates":{"last_synced_at":0,"upserts":[],"deletions":[]}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-1"}))
	rec := httptest.NewRecorder()

	h.Sync(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestSync_MissingLastSyncedAt(t *testing.T) {
	h := NewSyncHandler(&mockSyncService{})
	body := `{"tables":{"modes":{"upserts":[],"deletions":[]},"mode_blocked_apps":{"last_synced_at":0,"upserts":[],"deletions":[]},"schedules":{"last_synced_at":0,"upserts":[],"deletions":[]},"restriction_sessions":{"last_synced_at":0,"upserts":[],"deletions":[]},"restriction_lifecycle_events":{"last_synced_at":0,"upserts":[],"deletions":[]},"nfc_linked_chips":{"last_synced_at":0,"upserts":[],"deletions":[]},"qr_linked_codes":{"last_synced_at":0,"upserts":[],"deletions":[]},"streak_session_daily_rollups":{"last_synced_at":0,"upserts":[],"deletions":[]},"streak_daily_aggregates":{"last_synced_at":0,"upserts":[],"deletions":[]}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-1"}))
	rec := httptest.NewRecorder()

	h.Sync(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	fields, _ := resp.Error.Details.(map[string]any)
	if fields == nil {
		return
	}
}

func TestSync_InvalidCompositeDeletion(t *testing.T) {
	h := NewSyncHandler(&mockSyncService{})
	body := `{"tables":{"modes":{"last_synced_at":0,"upserts":[],"deletions":[]},"mode_blocked_apps":{"last_synced_at":0,"upserts":[],"deletions":[{"mode_id":"","platform":"android","app_identifier":"app"}]},"schedules":{"last_synced_at":0,"upserts":[],"deletions":[]},"restriction_sessions":{"last_synced_at":0,"upserts":[],"deletions":[]},"restriction_lifecycle_events":{"last_synced_at":0,"upserts":[],"deletions":[]},"nfc_linked_chips":{"last_synced_at":0,"upserts":[],"deletions":[]},"qr_linked_codes":{"last_synced_at":0,"upserts":[],"deletions":[]},"streak_session_daily_rollups":{"last_synced_at":0,"upserts":[],"deletions":[]},"streak_daily_aggregates":{"last_synced_at":0,"upserts":[],"deletions":[]}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-1"}))
	rec := httptest.NewRecorder()

	h.Sync(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestSync_ServiceErrorMapping(t *testing.T) {
	h := NewSyncHandler(&mockSyncService{syncFn: func(context.Context, service.SyncInput) (service.SyncOutput, error) {
		return service.SyncOutput{}, fmt.Errorf("%w: bad auth", service.ErrUnauthorized)
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(validSyncPayload()))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-1"}))
	rec := httptest.NewRecorder()

	h.Sync(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestSync_Success(t *testing.T) {
	h := NewSyncHandler(&mockSyncService{syncFn: func(_ context.Context, in service.SyncInput) (service.SyncOutput, error) {
		if in.UserID != "user-1" {
			t.Fatalf("user id = %q", in.UserID)
		}
		return syncmodel.Response{ServerTime: 1000, Tables: syncmodel.TableChangesByTable{}}, nil
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(validSyncPayload()))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-1"}))
	rec := httptest.NewRecorder()

	h.Sync(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp syncmodel.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ServerTime != 1000 {
		t.Errorf("server_time = %d, want 1000", resp.ServerTime)
	}
}

func TestSync_SubsetOfTablesAllowed(t *testing.T) {
	h := NewSyncHandler(&mockSyncService{syncFn: func(_ context.Context, in service.SyncInput) (service.SyncOutput, error) {
		if in.Tables.Modes == nil {
			t.Fatal("expected modes table to be present")
		}
		if in.Tables.ModeBlockedApps != nil || in.Tables.Schedules != nil || in.Tables.RestrictionSessions != nil || in.Tables.RestrictionLifecycleEvents != nil || in.Tables.NFCLinkedChips != nil || in.Tables.QRLinkedCodes != nil || in.Tables.StreakSessionDailyRollups != nil || in.Tables.StreakDailyAggregates != nil {
			t.Fatal("expected unspecified tables to stay nil")
		}
		return service.SyncOutput{}, nil
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(minimalSyncPayload()))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-1"}))
	rec := httptest.NewRecorder()

	h.Sync(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
