package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/middleware"
	"github.com/IsorilovA/pauza-server/internal/service"
)

// ---------------------------------------------------------------------------
// Mock emergency stop service
// ---------------------------------------------------------------------------

type mockEmergencyService struct {
	useEmergencyStopFn  func(ctx context.Context, userID string) (service.EmergencyStopOutput, error)
	getRemainingStopsFn func(ctx context.Context, userID string) (service.EmergencyStopOutput, error)
}

func (m *mockEmergencyService) UseEmergencyStop(ctx context.Context, userID string) (service.EmergencyStopOutput, error) {
	if m.useEmergencyStopFn != nil {
		return m.useEmergencyStopFn(ctx, userID)
	}
	return service.EmergencyStopOutput{}, nil
}

func (m *mockEmergencyService) GetRemainingStops(ctx context.Context, userID string) (service.EmergencyStopOutput, error) {
	if m.getRemainingStopsFn != nil {
		return m.getRemainingStopsFn(ctx, userID)
	}
	return service.EmergencyStopOutput{}, nil
}

func newTestEmergencyHandler(svc *mockEmergencyService) *EmergencyHandler {
	return NewEmergencyHandler(svc, noopLogger())
}

func authedRequest(method, target string, userID string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	return req.WithContext(middleware.WithUser(req.Context(), middleware.AuthUser{UserID: userID}))
}

// ---------------------------------------------------------------------------
// UseEmergencyStop handler tests
// ---------------------------------------------------------------------------

func TestUseEmergencyStop_NoAuth(t *testing.T) {
	t.Parallel()
	h := newTestEmergencyHandler(&mockEmergencyService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/emergency-stop", nil)
	rec := httptest.NewRecorder()
	h.UseEmergencyStop(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestUseEmergencyStop_Success(t *testing.T) {
	t.Parallel()
	h := newTestEmergencyHandler(&mockEmergencyService{
		useEmergencyStopFn: func(_ context.Context, userID string) (service.EmergencyStopOutput, error) {
			if userID != "user-1" {
				t.Errorf("userID = %q, want %q", userID, "user-1")
			}
			return service.EmergencyStopOutput{RemainingEmergencyStops: 2}, nil
		},
	})
	req := authedRequest(http.MethodPost, "/api/v1/emergency-stop", "user-1")
	rec := httptest.NewRecorder()
	h.UseEmergencyStop(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body service.EmergencyStopOutput
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.RemainingEmergencyStops != 2 {
		t.Errorf("remaining = %d, want 2", body.RemainingEmergencyStops)
	}
}

func TestUseEmergencyStop_Forbidden(t *testing.T) {
	t.Parallel()
	h := newTestEmergencyHandler(&mockEmergencyService{
		useEmergencyStopFn: func(_ context.Context, _ string) (service.EmergencyStopOutput, error) {
			return service.EmergencyStopOutput{}, service.ForbiddenError("No emergency stops remaining")
		},
	})
	req := authedRequest(http.MethodPost, "/api/v1/emergency-stop", "user-1")
	rec := httptest.NewRecorder()
	h.UseEmergencyStop(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestUseEmergencyStop_InternalError(t *testing.T) {
	t.Parallel()
	h := newTestEmergencyHandler(&mockEmergencyService{
		useEmergencyStopFn: func(_ context.Context, _ string) (service.EmergencyStopOutput, error) {
			return service.EmergencyStopOutput{}, errors.New("unexpected")
		},
	})
	req := authedRequest(http.MethodPost, "/api/v1/emergency-stop", "user-1")
	rec := httptest.NewRecorder()
	h.UseEmergencyStop(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// GetRemainingStops handler tests
// ---------------------------------------------------------------------------

func TestGetRemainingStops_NoAuth(t *testing.T) {
	t.Parallel()
	h := newTestEmergencyHandler(&mockEmergencyService{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/emergency-stops/remaining", nil)
	rec := httptest.NewRecorder()
	h.GetRemainingStops(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestGetRemainingStops_Success(t *testing.T) {
	t.Parallel()
	h := newTestEmergencyHandler(&mockEmergencyService{
		getRemainingStopsFn: func(_ context.Context, userID string) (service.EmergencyStopOutput, error) {
			if userID != "user-1" {
				t.Errorf("userID = %q, want %q", userID, "user-1")
			}
			return service.EmergencyStopOutput{RemainingEmergencyStops: 3}, nil
		},
	})
	req := authedRequest(http.MethodGet, "/api/v1/emergency-stops/remaining", "user-1")
	rec := httptest.NewRecorder()
	h.GetRemainingStops(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body service.EmergencyStopOutput
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.RemainingEmergencyStops != 3 {
		t.Errorf("remaining = %d, want 3", body.RemainingEmergencyStops)
	}
}

func TestGetRemainingStops_ServiceError(t *testing.T) {
	t.Parallel()
	h := newTestEmergencyHandler(&mockEmergencyService{
		getRemainingStopsFn: func(_ context.Context, _ string) (service.EmergencyStopOutput, error) {
			return service.EmergencyStopOutput{}, errors.New("unexpected")
		},
	})
	req := authedRequest(http.MethodGet, "/api/v1/emergency-stops/remaining", "user-1")
	rec := httptest.NewRecorder()
	h.GetRemainingStops(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
