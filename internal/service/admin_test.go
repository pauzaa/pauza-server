package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/pagination"
	"github.com/IsorilovA/pauza-server/internal/repository"
	"github.com/IsorilovA/pauza-server/internal/revenuecat"
)

// ---------------------------------------------------------------------------
// Fake RC metrics fetcher
// ---------------------------------------------------------------------------

type fakeRCMetricsFetcher struct {
	overviewFn func(ctx context.Context) (*revenuecat.OverviewMetrics, error)
	chartFn    func(ctx context.Context, params revenuecat.ChartParams) (*revenuecat.ChartResponse, error)
}

func (f *fakeRCMetricsFetcher) GetOverview(ctx context.Context) (*revenuecat.OverviewMetrics, error) {
	if f.overviewFn != nil {
		return f.overviewFn(ctx)
	}
	return &revenuecat.OverviewMetrics{}, nil
}

func (f *fakeRCMetricsFetcher) GetChart(ctx context.Context, params revenuecat.ChartParams) (*revenuecat.ChartResponse, error) {
	if f.chartFn != nil {
		return f.chartFn(ctx, params)
	}
	return &revenuecat.ChartResponse{}, nil
}

// ---------------------------------------------------------------------------
// Fake admin repository
// ---------------------------------------------------------------------------

type fakeAdminRepo struct {
	getAdminByUsernameFn        func(ctx context.Context, db repository.DBTX, username string) (repository.AdminCredentialRow, error)
	listUsersFn                 func(ctx context.Context, db repository.DBTX, params repository.ListUsersParams) ([]repository.AdminUserRow, int, error)
	getUserDetailFn             func(ctx context.Context, db repository.DBTX, userID string) (repository.AdminUserDetailRow, error)
	getPlatformStatsFn          func(ctx context.Context, db repository.DBTX) (repository.PlatformStatsRow, error)
	upsertEntitlementOverrideFn func(ctx context.Context, db repository.DBTX, params repository.UpsertOverrideParams) error
	deleteEntitlementOverrideFn func(ctx context.Context, db repository.DBTX, userID string, entitlement repository.Entitlement) error
	getActiveOverrideFn         func(ctx context.Context, db repository.DBTX, userID string, entitlement repository.Entitlement) (repository.OverrideRow, error)
	listEntitlementsFn          func(ctx context.Context, db repository.DBTX, params repository.ListEntitlementsParams) ([]repository.AdminEntitlementListRow, int, error)
	userExistsFn                func(ctx context.Context, db repository.DBTX, userID string) (bool, error)
}

var _ repository.AdminRepository = (*fakeAdminRepo)(nil)

func (f *fakeAdminRepo) GetAdminByUsername(ctx context.Context, db repository.DBTX, username string) (repository.AdminCredentialRow, error) {
	if f.getAdminByUsernameFn != nil {
		return f.getAdminByUsernameFn(ctx, db, username)
	}
	return repository.AdminCredentialRow{}, repository.ErrNotFound
}

func (f *fakeAdminRepo) ListUsers(ctx context.Context, db repository.DBTX, params repository.ListUsersParams) ([]repository.AdminUserRow, int, error) {
	if f.listUsersFn != nil {
		return f.listUsersFn(ctx, db, params)
	}
	return nil, 0, nil
}

func (f *fakeAdminRepo) GetUserDetail(ctx context.Context, db repository.DBTX, userID string) (repository.AdminUserDetailRow, error) {
	if f.getUserDetailFn != nil {
		return f.getUserDetailFn(ctx, db, userID)
	}
	return repository.AdminUserDetailRow{}, repository.ErrNotFound
}

func (f *fakeAdminRepo) GetPlatformStats(ctx context.Context, db repository.DBTX) (repository.PlatformStatsRow, error) {
	if f.getPlatformStatsFn != nil {
		return f.getPlatformStatsFn(ctx, db)
	}
	return repository.PlatformStatsRow{}, nil
}

func (f *fakeAdminRepo) UpsertEntitlementOverride(ctx context.Context, db repository.DBTX, params repository.UpsertOverrideParams) error {
	if f.upsertEntitlementOverrideFn != nil {
		return f.upsertEntitlementOverrideFn(ctx, db, params)
	}
	return nil
}

func (f *fakeAdminRepo) DeleteEntitlementOverride(ctx context.Context, db repository.DBTX, userID string, entitlement repository.Entitlement) error {
	if f.deleteEntitlementOverrideFn != nil {
		return f.deleteEntitlementOverrideFn(ctx, db, userID, entitlement)
	}
	return nil
}

func (f *fakeAdminRepo) GetActiveOverride(ctx context.Context, db repository.DBTX, userID string, entitlement repository.Entitlement) (repository.OverrideRow, error) {
	if f.getActiveOverrideFn != nil {
		return f.getActiveOverrideFn(ctx, db, userID, entitlement)
	}
	return repository.OverrideRow{}, repository.ErrNotFound
}

func (f *fakeAdminRepo) ListEntitlements(ctx context.Context, db repository.DBTX, params repository.ListEntitlementsParams) ([]repository.AdminEntitlementListRow, int, error) {
	if f.listEntitlementsFn != nil {
		return f.listEntitlementsFn(ctx, db, params)
	}
	return nil, 0, nil
}

func (f *fakeAdminRepo) UserExists(ctx context.Context, db repository.DBTX, userID string) (bool, error) {
	if f.userExistsFn != nil {
		return f.userExistsFn(ctx, db, userID)
	}
	return false, nil
}

func (f *fakeAdminRepo) GetUserGrowth(ctx context.Context, db repository.DBTX, params repository.TimeSeriesParams) ([]repository.TimeSeriesPoint, error) {
	return nil, nil
}

func (f *fakeAdminRepo) GetActiveUsers(ctx context.Context, db repository.DBTX, params repository.TimeSeriesParams) ([]repository.TimeSeriesPoint, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const testJWTSecret = "test-secret-at-least-32-bytes-long!"
const testAdminTokenTTL = 1 * time.Hour

func newTestAdminService(adminRepo *fakeAdminRepo, opts ...AdminServiceOption) *AdminService {
	return NewAdminService(
		&fakePool{},
		adminRepo,
		testJWTSecret,
		testAdminTokenTTL,
		slog.New(slog.NewTextHandler(devNull{}, &slog.HandlerOptions{Level: slog.LevelError})),
		opts...,
	)
}

func mustHashPassword(t *testing.T, password string) string {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword(%q) error: %v", password, err)
	}
	return hash
}

// =========================================================================
// Login tests
// =========================================================================

func TestLogin_ValidCredentials_ReturnsToken(t *testing.T) {
	t.Parallel()

	hash := mustHashPassword(t, "correctpassword")
	adminRepo := &fakeAdminRepo{
		getAdminByUsernameFn: func(_ context.Context, _ repository.DBTX, username string) (repository.AdminCredentialRow, error) {
			if username != "admin" {
				t.Errorf("username = %q, want %q", username, "admin")
			}
			return repository.AdminCredentialRow{
				ID:           "admin-001",
				Username:     "admin",
				PasswordHash: hash,
			}, nil
		},
	}

	svc := newTestAdminService(adminRepo)

	out, err := svc.Login(context.Background(), LoginInput{
		Username: "admin",
		Password: "correctpassword",
	})
	if err != nil {
		t.Fatalf("Login() error = %v, want nil", err)
	}
	if out.Token == "" {
		t.Error("Login() Token is empty, want non-empty JWT")
	}

	// Validate the token is a proper admin JWT.
	claims, err := auth.ValidateAccessToken(out.Token, testJWTSecret)
	if err != nil {
		t.Fatalf("ValidateAccessToken() error = %v", err)
	}
	if claims.Subject != "admin-001" {
		t.Errorf("token subject = %q, want %q", claims.Subject, "admin-001")
	}
	if claims.Role != "admin" {
		t.Errorf("token role = %q, want %q", claims.Role, "admin")
	}
}

func TestLogin_WrongPassword_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	hash := mustHashPassword(t, "correctpassword")
	adminRepo := &fakeAdminRepo{
		getAdminByUsernameFn: func(_ context.Context, _ repository.DBTX, _ string) (repository.AdminCredentialRow, error) {
			return repository.AdminCredentialRow{
				ID:           "admin-001",
				Username:     "admin",
				PasswordHash: hash,
			}, nil
		},
	}

	svc := newTestAdminService(adminRepo)

	_, err := svc.Login(context.Background(), LoginInput{
		Username: "admin",
		Password: "wrongpassword",
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Login() error = %v, want ErrUnauthorized", err)
	}
}

func TestLogin_UnknownUsername_ReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	adminRepo := &fakeAdminRepo{
		getAdminByUsernameFn: func(_ context.Context, _ repository.DBTX, _ string) (repository.AdminCredentialRow, error) {
			return repository.AdminCredentialRow{}, repository.ErrNotFound
		},
	}

	svc := newTestAdminService(adminRepo)

	_, err := svc.Login(context.Background(), LoginInput{
		Username: "nonexistent",
		Password: "anything",
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Login() error = %v, want ErrUnauthorized", err)
	}
}

func TestLogin_DBError_ReturnsInternal(t *testing.T) {
	t.Parallel()

	adminRepo := &fakeAdminRepo{
		getAdminByUsernameFn: func(_ context.Context, _ repository.DBTX, _ string) (repository.AdminCredentialRow, error) {
			return repository.AdminCredentialRow{}, errors.New("db down")
		},
	}

	svc := newTestAdminService(adminRepo)

	_, err := svc.Login(context.Background(), LoginInput{
		Username: "admin",
		Password: "pass",
	})
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("Login() error = %v, want ErrInternal", err)
	}
}

// =========================================================================
// ListUsers tests
// =========================================================================

func TestListUsers_HappyPath(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	adminRepo := &fakeAdminRepo{
		listUsersFn: func(_ context.Context, _ repository.DBTX, params repository.ListUsersParams) ([]repository.AdminUserRow, int, error) {
			if params.Limit != 20 || params.Offset != 0 {
				t.Errorf("params = {Limit:%d, Offset:%d}, want {20, 0}", params.Limit, params.Offset)
			}
			return []repository.AdminUserRow{
				{
					ID: "user-001", Email: "alice@example.com",
					Name: "Alice", Username: "alice",
					CreatedAt: now, IsPremium: true,
				},
			}, 1, nil
		},
	}

	svc := newTestAdminService(adminRepo)

	out, err := svc.ListUsers(context.Background(), ListUsersInput{})
	if err != nil {
		t.Fatalf("ListUsers() error = %v, want nil", err)
	}
	if out.Total != 1 {
		t.Errorf("Total = %d, want 1", out.Total)
	}
	if out.Page != pagination.DefaultPage {
		t.Errorf("Page = %d, want %d", out.Page, pagination.DefaultPage)
	}
	if out.Limit != pagination.DefaultLimit {
		t.Errorf("Limit = %d, want %d", out.Limit, pagination.DefaultLimit)
	}
	if len(out.Users) != 1 {
		t.Fatalf("len(Users) = %d, want 1", len(out.Users))
	}
	if out.Users[0].ID != "user-001" {
		t.Errorf("Users[0].ID = %q, want %q", out.Users[0].ID, "user-001")
	}
	if !out.Users[0].IsPremium {
		t.Error("Users[0].IsPremium = false, want true")
	}
}

func TestListUsers_WithSearchAndPagination(t *testing.T) {
	t.Parallel()

	adminRepo := &fakeAdminRepo{
		listUsersFn: func(_ context.Context, _ repository.DBTX, params repository.ListUsersParams) ([]repository.AdminUserRow, int, error) {
			if params.Limit != 10 {
				t.Errorf("params.Limit = %d, want 10", params.Limit)
			}
			if params.Offset != 10 {
				t.Errorf("params.Offset = %d, want 10 (page=2, limit=10)", params.Offset)
			}
			if params.Search != "alice" {
				t.Errorf("params.Search = %q, want %q", params.Search, "alice")
			}
			return nil, 0, nil
		},
	}

	svc := newTestAdminService(adminRepo)

	out, err := svc.ListUsers(context.Background(), ListUsersInput{
		Page:   2,
		Limit:  10,
		Search: "alice",
	})
	if err != nil {
		t.Fatalf("ListUsers() error = %v, want nil", err)
	}
	if out.Page != 2 || out.Limit != 10 {
		t.Errorf("Page/Limit = %d/%d, want 2/10", out.Page, out.Limit)
	}
}

func TestListUsers_DBError_ReturnsInternal(t *testing.T) {
	t.Parallel()

	adminRepo := &fakeAdminRepo{
		listUsersFn: func(context.Context, repository.DBTX, repository.ListUsersParams) ([]repository.AdminUserRow, int, error) {
			return nil, 0, errors.New("db error")
		},
	}

	svc := newTestAdminService(adminRepo)

	_, err := svc.ListUsers(context.Background(), ListUsersInput{})
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("ListUsers() error = %v, want ErrInternal", err)
	}
}

// =========================================================================
// GetUserDetail tests
// =========================================================================

func TestGetUserDetail_Found(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	rcID := "rc-user-id"
	adminRepo := &fakeAdminRepo{
		getUserDetailFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.AdminUserDetailRow, error) {
			if userID != "user-001" {
				t.Errorf("userID = %q, want %q", userID, "user-001")
			}
			return repository.AdminUserDetailRow{
				ID:                  "user-001",
				Email:               "alice@example.com",
				Name:                "Alice",
				Username:            "alice",
				LeaderboardVisible:  true,
				CreatedAt:           now,
				IsPremium:           true,
				RevenueCatAppUserID: &rcID,
				FriendCount:         5,
				TotalSessions:       42,
			}, nil
		},
	}

	svc := newTestAdminService(adminRepo)

	out, err := svc.GetUserDetail(context.Background(), GetUserDetailInput{UserID: "user-001"})
	if err != nil {
		t.Fatalf("GetUserDetail() error = %v, want nil", err)
	}
	if out.ID != "user-001" {
		t.Errorf("ID = %q, want %q", out.ID, "user-001")
	}
	if !out.IsPremium {
		t.Error("IsPremium = false, want true")
	}
	if out.RevenueCatAppUserID == nil || *out.RevenueCatAppUserID != rcID {
		t.Errorf("RevenueCatAppUserID = %v, want %q", out.RevenueCatAppUserID, rcID)
	}
	if out.FriendCount != 5 {
		t.Errorf("FriendCount = %d, want 5", out.FriendCount)
	}
}

func TestGetUserDetail_NotFound(t *testing.T) {
	t.Parallel()

	adminRepo := &fakeAdminRepo{
		getUserDetailFn: func(context.Context, repository.DBTX, string) (repository.AdminUserDetailRow, error) {
			return repository.AdminUserDetailRow{}, repository.ErrNotFound
		},
	}

	svc := newTestAdminService(adminRepo)

	_, err := svc.GetUserDetail(context.Background(), GetUserDetailInput{UserID: "nonexistent"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserDetail() error = %v, want ErrNotFound", err)
	}
}

func TestGetUserDetail_DBError_ReturnsInternal(t *testing.T) {
	t.Parallel()

	adminRepo := &fakeAdminRepo{
		getUserDetailFn: func(context.Context, repository.DBTX, string) (repository.AdminUserDetailRow, error) {
			return repository.AdminUserDetailRow{}, errors.New("db error")
		},
	}

	svc := newTestAdminService(adminRepo)

	_, err := svc.GetUserDetail(context.Background(), GetUserDetailInput{UserID: "user-001"})
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("GetUserDetail() error = %v, want ErrInternal", err)
	}
}

// =========================================================================
// GetPlatformStats tests
// =========================================================================

func TestGetPlatformStats_HappyPath(t *testing.T) {
	t.Parallel()

	adminRepo := &fakeAdminRepo{
		getPlatformStatsFn: func(context.Context, repository.DBTX) (repository.PlatformStatsRow, error) {
			return repository.PlatformStatsRow{
				TotalUsers:          100,
				ActiveUsers30d:      42,
				PremiumUsers:        10,
				TotalFriendships:    25,
				AvgStreakDays:       3.5,
				AvgDailyFocusTimeMS: 1800000,
			}, nil
		},
	}

	svc := newTestAdminService(adminRepo)

	out, err := svc.GetPlatformStats(context.Background())
	if err != nil {
		t.Fatalf("GetPlatformStats() error = %v, want nil", err)
	}
	if out.TotalUsers != 100 {
		t.Errorf("TotalUsers = %d, want 100", out.TotalUsers)
	}
	if out.PremiumUsers != 10 {
		t.Errorf("PremiumUsers = %d, want 10", out.PremiumUsers)
	}
}

func TestGetPlatformStats_DBError_ReturnsInternal(t *testing.T) {
	t.Parallel()

	adminRepo := &fakeAdminRepo{
		getPlatformStatsFn: func(context.Context, repository.DBTX) (repository.PlatformStatsRow, error) {
			return repository.PlatformStatsRow{}, errors.New("db error")
		},
	}

	svc := newTestAdminService(adminRepo)

	_, err := svc.GetPlatformStats(context.Background())
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("GetPlatformStats() error = %v, want ErrInternal", err)
	}
}

// =========================================================================
// ManageEntitlement tests
// =========================================================================

func TestManageEntitlement_Grant_HappyPath(t *testing.T) {
	t.Parallel()

	var overrideParams repository.UpsertOverrideParams

	adminRepo := &fakeAdminRepo{
		userExistsFn: func(_ context.Context, _ repository.DBTX, userID string) (bool, error) {
			if userID != "user-001" {
				t.Errorf("userID = %q, want %q", userID, "user-001")
			}
			return true, nil
		},
		upsertEntitlementOverrideFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertOverrideParams) error {
			overrideParams = params
			return nil
		},
	}

	svc := newTestAdminService(adminRepo)

	out, err := svc.ManageEntitlement(context.Background(), ManageEntitlementInput{
		UserID:      "user-001",
		Entitlement: repository.EntitlementPremium,
		Action:      repository.AdminOverrideGrant,
	})
	if err != nil {
		t.Fatalf("ManageEntitlement() error = %v, want nil", err)
	}
	if out.Message == "" {
		t.Error("Message is empty, want non-empty")
	}

	// Verify override params.
	if overrideParams.UserID != "user-001" {
		t.Errorf("override UserID = %q, want %q", overrideParams.UserID, "user-001")
	}
	if overrideParams.Action != repository.AdminOverrideGrant {
		t.Errorf("override Action = %q, want %q", overrideParams.Action, repository.AdminOverrideGrant)
	}
	if overrideParams.Entitlement != repository.EntitlementPremium {
		t.Errorf("override Entitlement = %q, want %q", overrideParams.Entitlement, repository.EntitlementPremium)
	}
}

func TestManageEntitlement_Revoke_SetsOverrideAction(t *testing.T) {
	t.Parallel()

	var overrideParams repository.UpsertOverrideParams

	adminRepo := &fakeAdminRepo{
		userExistsFn: func(context.Context, repository.DBTX, string) (bool, error) {
			return true, nil
		},
		upsertEntitlementOverrideFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertOverrideParams) error {
			overrideParams = params
			return nil
		},
	}

	svc := newTestAdminService(adminRepo)

	_, err := svc.ManageEntitlement(context.Background(), ManageEntitlementInput{
		UserID:      "user-001",
		Entitlement: repository.EntitlementPremium,
		Action:      repository.AdminOverrideRevoke,
	})
	if err != nil {
		t.Fatalf("ManageEntitlement() error = %v, want nil", err)
	}

	if overrideParams.Action != repository.AdminOverrideRevoke {
		t.Errorf("override Action = %q, want %q", overrideParams.Action, repository.AdminOverrideRevoke)
	}
}

func TestManageEntitlement_TemporaryGrant_SetsExpiry(t *testing.T) {
	t.Parallel()

	var overrideParams repository.UpsertOverrideParams
	expiresAt := time.Now().UTC().Add(24 * time.Hour)

	adminRepo := &fakeAdminRepo{
		userExistsFn: func(context.Context, repository.DBTX, string) (bool, error) {
			return true, nil
		},
		upsertEntitlementOverrideFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertOverrideParams) error {
			overrideParams = params
			return nil
		},
	}

	svc := newTestAdminService(adminRepo)

	_, err := svc.ManageEntitlement(context.Background(), ManageEntitlementInput{
		UserID:      "user-001",
		Entitlement: repository.EntitlementPremium,
		Action:      repository.AdminOverrideGrant,
		ExpiresAt:   &expiresAt,
	})
	if err != nil {
		t.Fatalf("ManageEntitlement() error = %v, want nil", err)
	}

	// The override should carry the expiry through to the repository.
	if overrideParams.ExpiresAt == nil {
		t.Fatal("override ExpiresAt = nil, want non-nil")
	}
	if !overrideParams.ExpiresAt.Equal(expiresAt) {
		t.Errorf("override ExpiresAt = %v, want %v", *overrideParams.ExpiresAt, expiresAt)
	}
	if overrideParams.Action != repository.AdminOverrideGrant {
		t.Errorf("override Action = %q, want %q", overrideParams.Action, repository.AdminOverrideGrant)
	}
}

func TestManageEntitlement_PermanentGrant_NilExpiry(t *testing.T) {
	t.Parallel()

	var overrideParams repository.UpsertOverrideParams

	adminRepo := &fakeAdminRepo{
		userExistsFn: func(context.Context, repository.DBTX, string) (bool, error) {
			return true, nil
		},
		upsertEntitlementOverrideFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertOverrideParams) error {
			overrideParams = params
			return nil
		},
	}

	svc := newTestAdminService(adminRepo)

	_, err := svc.ManageEntitlement(context.Background(), ManageEntitlementInput{
		UserID:      "user-001",
		Entitlement: repository.EntitlementPremium,
		Action:      repository.AdminOverrideGrant,
	})
	if err != nil {
		t.Fatalf("ManageEntitlement() error = %v, want nil", err)
	}

	// When no ExpiresAt is provided, the override should be permanent.
	if overrideParams.ExpiresAt != nil {
		t.Errorf("override ExpiresAt = %v, want nil (permanent)", overrideParams.ExpiresAt)
	}
}

func TestManageEntitlement_UserNotFound_ReturnsNotFound(t *testing.T) {
	t.Parallel()

	adminRepo := &fakeAdminRepo{
		userExistsFn: func(context.Context, repository.DBTX, string) (bool, error) {
			return false, nil
		},
	}

	svc := newTestAdminService(adminRepo)

	_, err := svc.ManageEntitlement(context.Background(), ManageEntitlementInput{
		UserID:      "nonexistent",
		Entitlement: repository.EntitlementPremium,
		Action:      repository.AdminOverrideGrant,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ManageEntitlement() error = %v, want ErrNotFound", err)
	}
}

func TestManageEntitlement_UserExistsDBError_ReturnsInternal(t *testing.T) {
	t.Parallel()

	adminRepo := &fakeAdminRepo{
		userExistsFn: func(context.Context, repository.DBTX, string) (bool, error) {
			return false, errors.New("db error")
		},
	}

	svc := newTestAdminService(adminRepo)

	_, err := svc.ManageEntitlement(context.Background(), ManageEntitlementInput{
		UserID:      "user-001",
		Entitlement: repository.EntitlementPremium,
		Action:      repository.AdminOverrideGrant,
	})
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("ManageEntitlement() error = %v, want ErrInternal", err)
	}
}

func TestManageEntitlement_OverrideUpsertError_ReturnsInternal(t *testing.T) {
	t.Parallel()

	adminRepo := &fakeAdminRepo{
		userExistsFn: func(context.Context, repository.DBTX, string) (bool, error) {
			return true, nil
		},
		upsertEntitlementOverrideFn: func(context.Context, repository.DBTX, repository.UpsertOverrideParams) error {
			return errors.New("override db error")
		},
	}

	svc := newTestAdminService(adminRepo)

	_, err := svc.ManageEntitlement(context.Background(), ManageEntitlementInput{
		UserID:      "user-001",
		Entitlement: repository.EntitlementPremium,
		Action:      repository.AdminOverrideGrant,
	})
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("ManageEntitlement() error = %v, want ErrInternal", err)
	}
}

func TestManageEntitlement_MessageFormat(t *testing.T) {
	t.Parallel()

	for _, action := range []repository.AdminOverrideAction{repository.AdminOverrideGrant, repository.AdminOverrideRevoke} {
		action := action
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()

			adminRepo := &fakeAdminRepo{
				userExistsFn: func(context.Context, repository.DBTX, string) (bool, error) {
					return true, nil
				},
				upsertEntitlementOverrideFn: func(context.Context, repository.DBTX, repository.UpsertOverrideParams) error {
					return nil
				},
			}

			svc := newTestAdminService(adminRepo)

			out, err := svc.ManageEntitlement(context.Background(), ManageEntitlementInput{
				UserID:      "user-001",
				Entitlement: repository.EntitlementPremium,
				Action:      action,
			})
			if err != nil {
				t.Fatalf("ManageEntitlement(%s) error = %v, want nil", action, err)
			}
			want := fmt.Sprintf("Entitlement premium %sed for user", action)
			if out.Message != want {
				t.Errorf("Message = %q, want %q", out.Message, want)
			}
		})
	}
}

// =========================================================================
// ListEntitlements tests
// =========================================================================

func TestListEntitlements_HappyPath(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	adminRepo := &fakeAdminRepo{
		listEntitlementsFn: func(_ context.Context, _ repository.DBTX, params repository.ListEntitlementsParams) ([]repository.AdminEntitlementListRow, int, error) {
			return []repository.AdminEntitlementListRow{
				{
					UserID: "user-001", Email: "alice@example.com",
					Username: "alice", Entitlement: "premium",
					IsActive: true, UpdatedAt: now,
				},
			}, 1, nil
		},
	}

	svc := newTestAdminService(adminRepo)

	out, err := svc.ListEntitlements(context.Background(), ListEntitlementsInput{})
	if err != nil {
		t.Fatalf("ListEntitlements() error = %v, want nil", err)
	}
	if out.Total != 1 {
		t.Errorf("Total = %d, want 1", out.Total)
	}
	if len(out.Entitlements) != 1 {
		t.Fatalf("len(Entitlements) = %d, want 1", len(out.Entitlements))
	}
	if out.Entitlements[0].UserID != "user-001" {
		t.Errorf("Entitlements[0].UserID = %q, want %q", out.Entitlements[0].UserID, "user-001")
	}
}

func TestListEntitlements_DBError_ReturnsInternal(t *testing.T) {
	t.Parallel()

	adminRepo := &fakeAdminRepo{
		listEntitlementsFn: func(context.Context, repository.DBTX, repository.ListEntitlementsParams) ([]repository.AdminEntitlementListRow, int, error) {
			return nil, 0, errors.New("db error")
		},
	}

	svc := newTestAdminService(adminRepo)

	_, err := svc.ListEntitlements(context.Background(), ListEntitlementsInput{})
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("ListEntitlements() error = %v, want ErrInternal", err)
	}
}

// =========================================================================
// pagination.Normalize tests
// =========================================================================

func TestNormalizePagination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		page      int
		limit     int
		wantPage  int
		wantLimit int
	}{
		{"zero values use defaults", 0, 0, pagination.DefaultPage, pagination.DefaultLimit},
		{"negative values use defaults", -1, -5, pagination.DefaultPage, pagination.DefaultLimit},
		{"valid values pass through", 3, 50, 3, 50},
		{"limit clamped to max", 1, 200, 1, pagination.MaxLimit},
		{"page=1 limit=1 edge case", 1, 1, 1, 1},
		{"page=0 limit=maxLimit", 0, pagination.MaxLimit, pagination.DefaultPage, pagination.MaxLimit},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotPage, gotLimit := pagination.Normalize(tt.page, tt.limit)
			if gotPage != tt.wantPage {
				t.Errorf("pagination.Normalize(%d, %d) page = %d, want %d",
					tt.page, tt.limit, gotPage, tt.wantPage)
			}
			if gotLimit != tt.wantLimit {
				t.Errorf("pagination.Normalize(%d, %d) limit = %d, want %d",
					tt.page, tt.limit, gotLimit, tt.wantLimit)
			}
		})
	}
}

// =========================================================================
// GetRCOverview tests
// =========================================================================

func TestGetRCOverview_HappyPath(t *testing.T) {
	t.Parallel()

	fetcher := &fakeRCMetricsFetcher{
		overviewFn: func(_ context.Context) (*revenuecat.OverviewMetrics, error) {
			return &revenuecat.OverviewMetrics{
				MRR:               4999,
				ARR:               59988,
				ActiveSubscribers: 42,
				ActiveTrials:      7,
			}, nil
		},
	}

	svc := newTestAdminService(&fakeAdminRepo{}, WithRCMetricsFetcher(fetcher))

	out, err := svc.GetRCOverview(context.Background())
	if err != nil {
		t.Fatalf("GetRCOverview() error = %v, want nil", err)
	}
	if out.MRR != 4999 {
		t.Errorf("MRR = %d, want 4999", out.MRR)
	}
	if out.ActiveSubscribers != 42 {
		t.Errorf("ActiveSubscribers = %d, want 42", out.ActiveSubscribers)
	}
}

func TestGetRCOverview_NilClient_ReturnsInternal(t *testing.T) {
	t.Parallel()

	svc := newTestAdminService(&fakeAdminRepo{})

	_, err := svc.GetRCOverview(context.Background())
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("GetRCOverview() error = %v, want ErrInternal", err)
	}
}

func TestGetRCOverview_FetchError_ReturnsInternal(t *testing.T) {
	t.Parallel()

	fetcher := &fakeRCMetricsFetcher{
		overviewFn: func(_ context.Context) (*revenuecat.OverviewMetrics, error) {
			return nil, errors.New("upstream error")
		},
	}

	svc := newTestAdminService(&fakeAdminRepo{}, WithRCMetricsFetcher(fetcher))

	_, err := svc.GetRCOverview(context.Background())
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("GetRCOverview() error = %v, want ErrInternal", err)
	}
}

// =========================================================================
// GetRCChart tests
// =========================================================================

func TestGetRCChart_HappyPath(t *testing.T) {
	t.Parallel()

	fetcher := &fakeRCMetricsFetcher{
		overviewFn: func(_ context.Context) (*revenuecat.OverviewMetrics, error) {
			return &revenuecat.OverviewMetrics{}, nil
		},
		chartFn: func(_ context.Context, params revenuecat.ChartParams) (*revenuecat.ChartResponse, error) {
			if params.ChartName != "revenue" {
				t.Errorf("ChartName = %q, want %q", params.ChartName, "revenue")
			}
			return &revenuecat.ChartResponse{
				Name: "revenue",
				Data: []revenuecat.ChartPoint{{Date: "2026-03-01", Value: 4999}},
			}, nil
		},
	}

	svc := newTestAdminService(&fakeAdminRepo{}, WithRCMetricsFetcher(fetcher))

	out, err := svc.GetRCChart(context.Background(), revenuecat.ChartParams{
		ChartName: "revenue",
		StartDate: "2026-02-18",
		EndDate:   "2026-03-20",
	})
	if err != nil {
		t.Fatalf("GetRCChart() error = %v, want nil", err)
	}
	if out.Name != "revenue" {
		t.Errorf("Name = %q, want %q", out.Name, "revenue")
	}
	if len(out.Data) != 1 {
		t.Fatalf("len(Data) = %d, want 1", len(out.Data))
	}
}

func TestGetRCChart_NilClient_ReturnsInternal(t *testing.T) {
	t.Parallel()

	svc := newTestAdminService(&fakeAdminRepo{})

	_, err := svc.GetRCChart(context.Background(), revenuecat.ChartParams{
		ChartName: "revenue",
		StartDate: "2026-02-18",
		EndDate:   "2026-03-20",
	})
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("GetRCChart() error = %v, want ErrInternal", err)
	}
}

func TestGetRCChart_FetchError_ReturnsInternal(t *testing.T) {
	t.Parallel()

	fetcher := &fakeRCMetricsFetcher{
		overviewFn: func(_ context.Context) (*revenuecat.OverviewMetrics, error) {
			return &revenuecat.OverviewMetrics{}, nil
		},
		chartFn: func(_ context.Context, _ revenuecat.ChartParams) (*revenuecat.ChartResponse, error) {
			return nil, errors.New("upstream error")
		},
	}

	svc := newTestAdminService(&fakeAdminRepo{}, WithRCMetricsFetcher(fetcher))

	_, err := svc.GetRCChart(context.Background(), revenuecat.ChartParams{
		ChartName: "revenue",
		StartDate: "2026-02-18",
		EndDate:   "2026-03-20",
	})
	if !errors.Is(err, ErrInternal) {
		t.Fatalf("GetRCChart() error = %v, want ErrInternal", err)
	}
}

// ---------------------------------------------------------------------------
// Fake RC subscriber fetcher (v1)
// ---------------------------------------------------------------------------

type fakeRCSubscriberFetcher struct {
	getSubscriberFn func(ctx context.Context, appUserID string) (*revenuecat.SubscriberResponse, error)
}

func (f *fakeRCSubscriberFetcher) GetSubscriber(ctx context.Context, appUserID string) (*revenuecat.SubscriberResponse, error) {
	if f.getSubscriberFn != nil {
		return f.getSubscriberFn(ctx, appUserID)
	}
	return &revenuecat.SubscriberResponse{}, nil
}

// ---------------------------------------------------------------------------
// GetUserRCSubscription tests
// ---------------------------------------------------------------------------

func TestGetUserRCSubscription_NoRCID(t *testing.T) {
	t.Parallel()
	repo := &fakeAdminRepo{
		getUserDetailFn: func(_ context.Context, _ repository.DBTX, _ string) (repository.AdminUserDetailRow, error) {
			return repository.AdminUserDetailRow{ID: "user-1", RevenueCatAppUserID: nil}, nil
		},
	}
	svc := newTestAdminService(repo, WithRCSubscriberFetcher(&fakeRCSubscriberFetcher{}))

	_, err := svc.GetUserRCSubscription(context.Background(), GetUserDetailInput{UserID: "user-1"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND error, got %v", err)
	}
}

func TestGetUserRCSubscription_HappyPath(t *testing.T) {
	t.Parallel()
	now := time.Now().Add(24 * time.Hour) // future expiry
	rcID := "rc_app_123"
	repo := &fakeAdminRepo{
		getUserDetailFn: func(_ context.Context, _ repository.DBTX, _ string) (repository.AdminUserDetailRow, error) {
			return repository.AdminUserDetailRow{ID: "user-1", RevenueCatAppUserID: &rcID}, nil
		},
	}
	fetcher := &fakeRCSubscriberFetcher{
		getSubscriberFn: func(_ context.Context, appUserID string) (*revenuecat.SubscriberResponse, error) {
			if appUserID != "rc_app_123" {
				t.Fatalf("appUserID = %q, want %q", appUserID, "rc_app_123")
			}
			return &revenuecat.SubscriberResponse{
				Subscriber: revenuecat.Subscriber{
					Entitlements: map[string]revenuecat.EntitlementObj{
						"premium": {
							ExpiresDate:       &now,
							PurchaseDate:      time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
							ProductIdentifier: "monthly_sub",
						},
					},
				},
			}, nil
		},
	}
	svc := newTestAdminService(repo, WithRCSubscriberFetcher(fetcher))

	out, err := svc.GetUserRCSubscription(context.Background(), GetUserDetailInput{UserID: "user-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.AppUserID != "rc_app_123" {
		t.Fatalf("app_user_id = %q, want %q", out.AppUserID, "rc_app_123")
	}
	if len(out.Entitlements) != 1 {
		t.Fatalf("entitlements count = %d, want 1", len(out.Entitlements))
	}
	if !out.Entitlements[0].IsActive {
		t.Fatal("expected entitlement to be active")
	}
	if out.Entitlements[0].ProductIdentifier != "monthly_sub" {
		t.Fatalf("product_identifier = %q, want %q", out.Entitlements[0].ProductIdentifier, "monthly_sub")
	}
}
