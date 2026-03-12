package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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

type syncErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type failingSyncResponseWriter struct {
	header     http.Header
	statuses   []int
	writeCalls int
	body       []byte
}

func (m *mockSyncService) Sync(ctx context.Context, in service.SyncInput) (service.SyncOutput, error) {
	if m.syncFn != nil {
		return m.syncFn(ctx, in)
	}
	return service.SyncOutput{}, nil
}

func (w *failingSyncResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingSyncResponseWriter) WriteHeader(statusCode int) {
	w.statuses = append(w.statuses, statusCode)
}

func (w *failingSyncResponseWriter) Write(p []byte) (int, error) {
	w.writeCalls++
	if len(p) > 0 {
		w.body = append(w.body, p[0])
		return 1, io.ErrClosedPipe
	}
	return 0, io.ErrClosedPipe
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
	var resp syncErrorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeValidationError {
		t.Fatalf("error.code = %q, want %q", resp.Error.Code, apperror.CodeValidationError)
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

func TestSync_EncodeFailureDoesNotWriteSecondResponse(t *testing.T) {
	h := NewSyncHandler(&mockSyncService{syncFn: func(_ context.Context, in service.SyncInput) (service.SyncOutput, error) {
		return syncmodel.Response{ServerTime: 1000, Tables: syncmodel.TableChangesByTable{}}, nil
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(validSyncPayload()))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-1"}))
	w := &failingSyncResponseWriter{}

	h.Sync(w, req)

	if len(w.statuses) != 1 {
		t.Fatalf("WriteHeader calls = %d, want 1", len(w.statuses))
	}
	if w.statuses[0] != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.statuses[0])
	}
	if w.writeCalls == 0 {
		t.Fatal("expected encoder to attempt writing response body")
	}
	if w.writeCalls != 1 {
		t.Fatalf("Write calls = %d, want 1", w.writeCalls)
	}
	if string(w.body) != "{" {
		t.Fatalf("body prefix = %q, want %q", string(w.body), "{")
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", got, "application/json")
	}
}

func TestSync_EncodeFailureLogsWithInjectedLogger(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))
	h := NewSyncHandlerWithLogger(&mockSyncService{syncFn: func(_ context.Context, in service.SyncInput) (service.SyncOutput, error) {
		return syncmodel.Response{ServerTime: 1000, Tables: syncmodel.TableChangesByTable{}}, nil
	}}, logger)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", strings.NewReader(validSyncPayload()))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: "user-1"}))
	w := &failingSyncResponseWriter{}

	h.Sync(w, req)

	logOutput := buf.String()
	if !strings.Contains(logOutput, `"msg":"encoding sync response"`) {
		t.Fatalf("expected injected logger output, got %q", logOutput)
	}
	if !strings.Contains(logOutput, `"err":"io: read/write on closed pipe"`) {
		t.Fatalf("expected encode error in injected logger output, got %q", logOutput)
	}
}
