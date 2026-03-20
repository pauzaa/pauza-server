package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/IsorilovA/pauza-server/internal/apperror"
	"github.com/IsorilovA/pauza-server/internal/service"
)

// ---------------------------------------------------------------------------
// Mock admin service
// ---------------------------------------------------------------------------

type mockAdminService struct {
	loginFn             func(ctx context.Context, in service.LoginInput) (service.LoginOutput, error)
	listUsersFn         func(ctx context.Context, in service.ListUsersInput) (service.ListUsersOutput, error)
	getUserDetailFn     func(ctx context.Context, in service.GetUserDetailInput) (service.UserDetailOutput, error)
	getPlatformStatsFn  func(ctx context.Context) (service.PlatformStatsOutput, error)
	manageEntitlementFn func(ctx context.Context, in service.ManageEntitlementInput) (service.MessageOutput, error)
	listEntitlementsFn  func(ctx context.Context, in service.ListEntitlementsInput) (service.ListEntitlementsOutput, error)
}

func (m *mockAdminService) Login(ctx context.Context, in service.LoginInput) (service.LoginOutput, error) {
	if m.loginFn != nil {
		return m.loginFn(ctx, in)
	}
	return service.LoginOutput{}, nil
}

func (m *mockAdminService) ListUsers(ctx context.Context, in service.ListUsersInput) (service.ListUsersOutput, error) {
	if m.listUsersFn != nil {
		return m.listUsersFn(ctx, in)
	}
	return service.ListUsersOutput{}, nil
}

func (m *mockAdminService) GetUserDetail(ctx context.Context, in service.GetUserDetailInput) (service.UserDetailOutput, error) {
	if m.getUserDetailFn != nil {
		return m.getUserDetailFn(ctx, in)
	}
	return service.UserDetailOutput{}, nil
}

func (m *mockAdminService) GetPlatformStats(ctx context.Context) (service.PlatformStatsOutput, error) {
	if m.getPlatformStatsFn != nil {
		return m.getPlatformStatsFn(ctx)
	}
	return service.PlatformStatsOutput{}, nil
}

func (m *mockAdminService) ManageEntitlement(ctx context.Context, in service.ManageEntitlementInput) (service.MessageOutput, error) {
	if m.manageEntitlementFn != nil {
		return m.manageEntitlementFn(ctx, in)
	}
	return service.MessageOutput{}, nil
}

func (m *mockAdminService) ListEntitlements(ctx context.Context, in service.ListEntitlementsInput) (service.ListEntitlementsOutput, error) {
	if m.listEntitlementsFn != nil {
		return m.listEntitlementsFn(ctx, in)
	}
	return service.ListEntitlementsOutput{}, nil
}

func (m *mockAdminService) GetUserGrowth(ctx context.Context, in service.TimeSeriesInput) ([]service.TimeSeriesPoint, error) {
	return nil, nil
}

func (m *mockAdminService) GetActiveUsers(ctx context.Context, in service.TimeSeriesInput) ([]service.TimeSeriesPoint, error) {
	return nil, nil
}

func newTestAdminHandler(svc *mockAdminService) *AdminHandler {
	return NewAdminHandler(svc, noopLogger())
}

// withChiURLParam injects chi URL params into the request context.
func withChiURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// =========================================================================
// Login handler tests
// =========================================================================

func TestAdminLogin_MissingUsername(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login",
		strings.NewReader(`{"username":"","password":"secret"}`))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	assertValidationEnvelope(t, rec, []string{"username"})
}

func TestAdminLogin_MissingPassword(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login",
		strings.NewReader(`{"username":"admin","password":""}`))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	assertValidationEnvelope(t, rec, []string{"password"})
}

func TestAdminLogin_MissingBothFields(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login",
		strings.NewReader(`{"username":"","password":""}`))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	assertValidationEnvelope(t, rec, []string{"username", "password"})
}

func TestAdminLogin_WhitespaceOnlyUsername(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login",
		strings.NewReader(`{"username":"   ","password":"secret"}`))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	assertValidationEnvelope(t, rec, []string{"username"})
}

func TestAdminLogin_Success(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{
		loginFn: func(_ context.Context, in service.LoginInput) (service.LoginOutput, error) {
			if in.Username != "admin" {
				t.Errorf("username = %q, want %q", in.Username, "admin")
			}
			if in.Password != "secretpass" {
				t.Errorf("password = %q, want %q", in.Password, "secretpass")
			}
			return service.LoginOutput{Token: "admin-jwt-token"}, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login",
		strings.NewReader(`{"username":"admin","password":"secretpass"}`))
	rec := httptest.NewRecorder()
	h.Login(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	var body adminLoginResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.AccessToken != "admin-jwt-token" {
		t.Errorf("access_token = %q, want %q", body.AccessToken, "admin-jwt-token")
	}
}

func TestAdminLogin_ServiceUnauthorized(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{
		loginFn: func(_ context.Context, _ service.LoginInput) (service.LoginOutput, error) {
			return service.LoginOutput{}, fmt.Errorf("%w: invalid credentials", service.ErrUnauthorized)
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login",
		strings.NewReader(`{"username":"admin","password":"wrong"}`))
	rec := httptest.NewRecorder()
	h.Login(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeUnauthorized {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, apperror.CodeUnauthorized)
	}
}

func TestAdminLogin_InvalidJSON(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login",
		strings.NewReader(`{not json`))
	rec := httptest.NewRecorder()
	h.Login(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestAdminLogin_UnknownFieldRejected(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login",
		strings.NewReader(`{"username":"admin","password":"pass","extra":"field"}`))
	rec := httptest.NewRecorder()
	h.Login(rec, req)

	// decodeJSONBody uses DisallowUnknownFields, so unknown fields → 422.
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

// =========================================================================
// ListUsers handler tests
// =========================================================================

func TestAdminListUsers_HappyPath(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)
	h := newTestAdminHandler(&mockAdminService{
		listUsersFn: func(_ context.Context, in service.ListUsersInput) (service.ListUsersOutput, error) {
			return service.ListUsersOutput{
				Users: []service.AdminUserItem{
					{
						ID: "00000000-0000-0000-0000-000000000001", Email: "alice@example.com",
						Name: "Alice", Username: "alice",
						IsPremium: true, CreatedAt: now,
					},
				},
				Total: 1, Page: 1, Limit: 20,
			}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	rec := httptest.NewRecorder()
	h.ListUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body adminListUsersResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Users) != 1 {
		t.Fatalf("len(users) = %d, want 1", len(body.Users))
	}
	if body.Users[0].ID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("users[0].id = %q, want %q", body.Users[0].ID, "00000000-0000-0000-0000-000000000001")
	}
	if !body.Users[0].PremiumEntitlementActive {
		t.Error("users[0].premium_entitlement_active = false, want true")
	}
	if body.Users[0].CreatedAt != "2026-03-10T12:00:00Z" {
		t.Errorf("users[0].created_at = %q, want %q", body.Users[0].CreatedAt, "2026-03-10T12:00:00Z")
	}
	if body.Pagination.Total != 1 || body.Pagination.Page != 1 || body.Pagination.Limit != 20 {
		t.Errorf("pagination = %+v", body.Pagination)
	}
}

func TestAdminListUsers_PaginationAndSearch(t *testing.T) {
	t.Parallel()

	var gotInput service.ListUsersInput
	h := newTestAdminHandler(&mockAdminService{
		listUsersFn: func(_ context.Context, in service.ListUsersInput) (service.ListUsersOutput, error) {
			gotInput = in
			return service.ListUsersOutput{Page: in.Page, Limit: in.Limit}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users?page=3&limit=10&search=alice", nil)
	rec := httptest.NewRecorder()
	h.ListUsers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotInput.Page != 3 {
		t.Errorf("page = %d, want 3", gotInput.Page)
	}
	if gotInput.Limit != 10 {
		t.Errorf("limit = %d, want 10", gotInput.Limit)
	}
	if gotInput.Search != "alice" {
		t.Errorf("search = %q, want %q", gotInput.Search, "alice")
	}
}

func TestAdminListUsers_ServiceError(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{
		listUsersFn: func(_ context.Context, _ service.ListUsersInput) (service.ListUsersOutput, error) {
			return service.ListUsersOutput{}, service.ErrInternal
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	rec := httptest.NewRecorder()
	h.ListUsers(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// =========================================================================
// GetUserDetail handler tests
// =========================================================================

func TestAdminGetUserDetail_HappyPath(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, time.April, 10, 12, 0, 0, 0, time.UTC)
	rcID := "rc-app-user-id"
	var lastSession int64 = 1710000000
	picURL := "https://cdn.example.com/pic.jpg"

	h := newTestAdminHandler(&mockAdminService{
		getUserDetailFn: func(_ context.Context, in service.GetUserDetailInput) (service.UserDetailOutput, error) {
			if in.UserID != "00000000-0000-0000-0000-000000000001" {
				t.Errorf("userID = %q, want %q", in.UserID, "00000000-0000-0000-0000-000000000001")
			}
			return service.UserDetailOutput{
				ID: "00000000-0000-0000-0000-000000000001", Email: "alice@example.com",
				Name: "Alice", Username: "alice",
				ProfilePictureURL: &picURL, LeaderboardVisible: true,
				CreatedAt: now, IsPremium: true,
				CurrentPeriodEnd: &periodEnd, RevenueCatAppUserID: &rcID,
				FriendCount: 5, TotalSessions: 42, LastSessionTime: &lastSession,
			}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001", nil)
	req = withChiURLParam(req, "id", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.GetUserDetail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body adminUserDetailResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("id = %q, want %q", body.ID, "00000000-0000-0000-0000-000000000001")
	}
	if !body.IsPremium {
		t.Error("is_premium = false, want true")
	}
	if body.CurrentPeriodEnd == nil || *body.CurrentPeriodEnd != "2026-04-10T12:00:00Z" {
		t.Errorf("current_period_end = %v, want %q", body.CurrentPeriodEnd, "2026-04-10T12:00:00Z")
	}
	if body.RevenueCatAppUserID == nil || *body.RevenueCatAppUserID != rcID {
		t.Errorf("revenuecat_app_user_id = %v, want %q", body.RevenueCatAppUserID, rcID)
	}
	if body.FriendCount != 5 {
		t.Errorf("friend_count = %d, want 5", body.FriendCount)
	}
	if body.TotalSessions != 42 {
		t.Errorf("total_sessions = %d, want 42", body.TotalSessions)
	}
	if body.LastSessionTime == nil || *body.LastSessionTime != lastSession {
		t.Errorf("last_session_time = %v, want %d", body.LastSessionTime, lastSession)
	}
	if body.ProfilePictureURL == nil || *body.ProfilePictureURL != picURL {
		t.Errorf("profile_picture_url = %v, want %q", body.ProfilePictureURL, picURL)
	}
}

func TestAdminGetUserDetail_NotFound(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{
		getUserDetailFn: func(_ context.Context, _ service.GetUserDetailInput) (service.UserDetailOutput, error) {
			return service.UserDetailOutput{}, service.ErrNotFound
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users/00000000-0000-0000-0000-000000000099", nil)
	req = withChiURLParam(req, "id", "00000000-0000-0000-0000-000000000099")
	rec := httptest.NewRecorder()
	h.GetUserDetail(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var resp apperror.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != apperror.CodeNotFound {
		t.Errorf("error.code = %q, want %q", resp.Error.Code, apperror.CodeNotFound)
	}
}

// =========================================================================
// GetPlatformStats handler tests
// =========================================================================

func TestAdminGetPlatformStats_HappyPath(t *testing.T) {
	t.Parallel()

	h := newTestAdminHandler(&mockAdminService{
		getPlatformStatsFn: func(_ context.Context) (service.PlatformStatsOutput, error) {
			return service.PlatformStatsOutput{
				TotalUsers: 100, ActiveUsers30d: 42,
				PremiumUsers: 10,
				TotalFriendships: 25, AvgStreakDays: 3.5,
				AvgDailyFocusTimeMS: 1800000,
			}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/stats", nil)
	rec := httptest.NewRecorder()
	h.GetPlatformStats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body adminStatsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TotalUsers != 100 {
		t.Errorf("total_users = %d, want 100", body.TotalUsers)
	}
	if body.PremiumUsers != 10 {
		t.Errorf("users_with_premium_entitlement = %d, want 10", body.PremiumUsers)
	}
	if body.AvgStreakDays != 3.5 {
		t.Errorf("avg_streak_days = %f, want 3.5", body.AvgStreakDays)
	}
}

func TestAdminGetPlatformStats_ServiceError(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{
		getPlatformStatsFn: func(_ context.Context) (service.PlatformStatsOutput, error) {
			return service.PlatformStatsOutput{}, service.ErrInternal
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/stats", nil)
	rec := httptest.NewRecorder()
	h.GetPlatformStats(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// =========================================================================
// ManageEntitlement handler tests
// =========================================================================

func TestAdminManageEntitlement_InvalidAction(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001/entitlements",
		strings.NewReader(`{"action":"suspend","entitlement":"premium"}`))
	req = withChiURLParam(req, "id", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.ManageEntitlement(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestAdminManageEntitlement_MissingEntitlement(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001/entitlements",
		strings.NewReader(`{"action":"grant"}`))
	req = withChiURLParam(req, "id", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.ManageEntitlement(rec, req)

	assertValidationEnvelope(t, rec, []string{"entitlement"})
}

func TestAdminManageEntitlement_InvalidExpiresAt(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001/entitlements",
		strings.NewReader(`{"action":"grant","entitlement":"premium","expires_at":"not-a-date"}`))
	req = withChiURLParam(req, "id", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.ManageEntitlement(rec, req)

	assertValidationEnvelope(t, rec, []string{"expires_at"})
}

func TestAdminManageEntitlement_PastExpiresAt(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{})
	past := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"action":"grant","entitlement":"premium","expires_at":"%s"}`, past)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001/entitlements",
		strings.NewReader(body))
	req = withChiURLParam(req, "id", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.ManageEntitlement(rec, req)

	assertValidationEnvelope(t, rec, []string{"expires_at"})
}

func TestAdminManageEntitlement_ValidExpiresAtPassedToService(t *testing.T) {
	t.Parallel()

	var gotInput service.ManageEntitlementInput
	h := newTestAdminHandler(&mockAdminService{
		manageEntitlementFn: func(_ context.Context, in service.ManageEntitlementInput) (service.MessageOutput, error) {
			gotInput = in
			return service.MessageOutput{Message: "done"}, nil
		},
	})

	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"action":"grant","entitlement":"premium","expires_at":"%s"}`, future)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001/entitlements",
		strings.NewReader(body))
	req = withChiURLParam(req, "id", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.ManageEntitlement(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotInput.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt to be non-nil")
	}
	if gotInput.UserID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("userID = %q, want %q", gotInput.UserID, "00000000-0000-0000-0000-000000000001")
	}
}

func TestAdminManageEntitlement_ServiceNotFound(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{
		manageEntitlementFn: func(_ context.Context, _ service.ManageEntitlementInput) (service.MessageOutput, error) {
			return service.MessageOutput{}, service.ErrNotFound
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/00000000-0000-0000-0000-000000000099/entitlements",
		strings.NewReader(`{"action":"grant","entitlement":"premium"}`))
	req = withChiURLParam(req, "id", "00000000-0000-0000-0000-000000000099")
	rec := httptest.NewRecorder()
	h.ManageEntitlement(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAdminManageEntitlement_ServiceInvalidAction(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{
		manageEntitlementFn: func(_ context.Context, _ service.ManageEntitlementInput) (service.MessageOutput, error) {
			return service.MessageOutput{}, service.ValidationError("Invalid request body", apperror.FieldErrors{
				"action": "action must be grant or revoke",
			})
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001/entitlements",
		strings.NewReader(`{"action":"grant","entitlement":"premium"}`))
	req = withChiURLParam(req, "id", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.ManageEntitlement(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestAdminManageEntitlement_ServiceInvalidEntitlement(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{
		manageEntitlementFn: func(_ context.Context, _ service.ManageEntitlementInput) (service.MessageOutput, error) {
			return service.MessageOutput{}, service.ValidationError("Invalid request body", apperror.FieldErrors{
				"entitlement": "entitlement must be premium",
			})
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001/entitlements",
		strings.NewReader(`{"action":"grant","entitlement":"premium"}`))
	req = withChiURLParam(req, "id", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.ManageEntitlement(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestAdminManageEntitlement_Success(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{
		manageEntitlementFn: func(_ context.Context, in service.ManageEntitlementInput) (service.MessageOutput, error) {
			return service.MessageOutput{Message: "Entitlement premium granted for user"}, nil
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/00000000-0000-0000-0000-000000000001/entitlements",
		strings.NewReader(`{"action":"grant","entitlement":"premium"}`))
	req = withChiURLParam(req, "id", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	h.ManageEntitlement(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body messageResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Message != "Entitlement premium granted for user" {
		t.Errorf("message = %q", body.Message)
	}
}

// =========================================================================
// ListEntitlements handler tests
// =========================================================================

func TestAdminListEntitlements_HappyPath(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, time.April, 10, 12, 0, 0, 0, time.UTC)
	h := newTestAdminHandler(&mockAdminService{
		listEntitlementsFn: func(_ context.Context, _ service.ListEntitlementsInput) (service.ListEntitlementsOutput, error) {
			return service.ListEntitlementsOutput{
				Entitlements: []service.AdminEntitlementItem{
					{
						UserID: "00000000-0000-0000-0000-000000000001", Email: "alice@example.com",
						Username: "alice", Entitlement: "premium",
						IsActive: true, CurrentPeriodEnd: &periodEnd,
						UpdatedAt: now,
					},
				},
				Total: 1, Page: 1, Limit: 20,
			}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/entitlements", nil)
	rec := httptest.NewRecorder()
	h.ListEntitlements(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body adminListEntitlementsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Entitlements) != 1 {
		t.Fatalf("len(entitlements) = %d, want 1", len(body.Entitlements))
	}
	if body.Entitlements[0].UserID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("entitlements[0].user_id = %q, want %q", body.Entitlements[0].UserID, "00000000-0000-0000-0000-000000000001")
	}
	if body.Entitlements[0].CurrentPeriodEnd == nil || *body.Entitlements[0].CurrentPeriodEnd != "2026-04-10T12:00:00Z" {
		t.Errorf("current_period_end = %v, want %q", body.Entitlements[0].CurrentPeriodEnd, "2026-04-10T12:00:00Z")
	}
	if body.Pagination.Total != 1 {
		t.Errorf("pagination.total = %d, want 1", body.Pagination.Total)
	}
}

func TestAdminListEntitlements_InvalidIsActive(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/entitlements?is_active=maybe", nil)
	rec := httptest.NewRecorder()
	h.ListEntitlements(rec, req)

	assertValidationEnvelope(t, rec, []string{"is_active"})
}

func TestAdminListEntitlements_ValidIsActiveFilter(t *testing.T) {
	t.Parallel()

	var gotInput service.ListEntitlementsInput
	h := newTestAdminHandler(&mockAdminService{
		listEntitlementsFn: func(_ context.Context, in service.ListEntitlementsInput) (service.ListEntitlementsOutput, error) {
			gotInput = in
			return service.ListEntitlementsOutput{}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/entitlements?is_active=true&entitlement=premium", nil)
	rec := httptest.NewRecorder()
	h.ListEntitlements(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotInput.IsActive == nil {
		t.Fatal("expected IsActive to be non-nil")
	}
	if !*gotInput.IsActive {
		t.Error("IsActive = false, want true")
	}
	if gotInput.Entitlement != "premium" {
		t.Errorf("Entitlement = %q, want %q", gotInput.Entitlement, "premium")
	}
}

func TestAdminListEntitlements_IsActiveFalse(t *testing.T) {
	t.Parallel()

	var gotInput service.ListEntitlementsInput
	h := newTestAdminHandler(&mockAdminService{
		listEntitlementsFn: func(_ context.Context, in service.ListEntitlementsInput) (service.ListEntitlementsOutput, error) {
			gotInput = in
			return service.ListEntitlementsOutput{}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/entitlements?is_active=false", nil)
	rec := httptest.NewRecorder()
	h.ListEntitlements(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotInput.IsActive == nil {
		t.Fatal("expected IsActive to be non-nil")
	}
	if *gotInput.IsActive {
		t.Error("IsActive = true, want false")
	}
}

func TestAdminListEntitlements_OmittedIsActive(t *testing.T) {
	t.Parallel()

	var gotInput service.ListEntitlementsInput
	h := newTestAdminHandler(&mockAdminService{
		listEntitlementsFn: func(_ context.Context, in service.ListEntitlementsInput) (service.ListEntitlementsOutput, error) {
			gotInput = in
			return service.ListEntitlementsOutput{}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/entitlements", nil)
	rec := httptest.NewRecorder()
	h.ListEntitlements(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotInput.IsActive != nil {
		t.Errorf("expected IsActive to be nil, got %v", *gotInput.IsActive)
	}
}

func TestAdminListEntitlements_ServiceError(t *testing.T) {
	t.Parallel()
	h := newTestAdminHandler(&mockAdminService{
		listEntitlementsFn: func(_ context.Context, _ service.ListEntitlementsInput) (service.ListEntitlementsOutput, error) {
			return service.ListEntitlementsOutput{}, service.ErrInternal
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/entitlements", nil)
	rec := httptest.NewRecorder()
	h.ListEntitlements(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
