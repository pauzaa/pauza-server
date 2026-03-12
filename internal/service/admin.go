package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/pagination"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

// ---------------------------------------------------------------------------
// Input / output types
// ---------------------------------------------------------------------------

// LoginInput holds the credentials for an admin login attempt.
type LoginInput struct {
	Username string
	Password string
}

// LoginOutput holds the token returned after a successful admin login.
type LoginOutput struct {
	Token string
}

// ListUsersInput holds pagination and optional search for the admin user listing.
type ListUsersInput struct {
	Page   int
	Limit  int
	Search string
}

// ListUsersOutput holds the paginated result for the admin user listing.
type ListUsersOutput struct {
	Users []AdminUserItem
	Total int
	Page  int
	Limit int
}

// AdminUserItem is the public representation of a user in the admin listing.
type AdminUserItem struct {
	ID                string
	Email             string
	Name              string
	Username          string
	ProfilePictureURL *string
	CreatedAt         time.Time
	IsPremium         bool
}

// GetUserDetailInput identifies a user for the admin detail endpoint.
type GetUserDetailInput struct {
	UserID string
}

// UserDetailOutput holds the full user detail returned to admin callers.
type UserDetailOutput struct {
	ID                  string
	Email               string
	Name                string
	Username            string
	ProfilePictureURL   *string
	LeaderboardVisible  bool
	CreatedAt           time.Time
	IsPremium           bool
	CurrentPeriodEnd    *time.Time
	RevenueCatAppUserID *string
	FriendCount         int
	TotalSessions       int
	LastSessionTime     *int64
}

// ManageEntitlementInput holds the fields for granting or revoking an entitlement.
type ManageEntitlementInput struct {
	UserID      string
	Entitlement repository.Entitlement
	Action      repository.AdminOverrideAction
	ExpiresAt   *time.Time
}

// ListEntitlementsInput holds pagination and optional filters for the admin entitlement listing.
type ListEntitlementsInput struct {
	Page        int
	Limit       int
	Entitlement repository.Entitlement
	IsActive    *bool
}

// ListEntitlementsOutput holds the paginated result for the admin entitlement listing.
type ListEntitlementsOutput struct {
	Entitlements []AdminEntitlementItem
	Total        int
	Page         int
	Limit        int
}

// AdminEntitlementItem is the public representation of an entitlement row in the admin listing.
type AdminEntitlementItem struct {
	UserID           string
	Email            string
	Username         string
	Entitlement      string
	IsActive         bool
	CurrentPeriodEnd *time.Time
	UpdatedAt        time.Time
}

// PlatformStatsOutput holds the aggregate statistics returned by the admin dashboard.
type PlatformStatsOutput struct {
	TotalUsers                int
	ActiveUsers30d            int
	PremiumUsers              int
	ActivePremiumEntitlements int
	TotalFriendships          int
	AvgStreakDays             float64
	AvgDailyFocusTimeMS       float64
}

// ---------------------------------------------------------------------------
// Pagination defaults
// ---------------------------------------------------------------------------

var (
	ErrInvalidAction      = errors.New("invalid action")
	ErrInvalidEntitlement = errors.New("invalid entitlement")
)

// ---------------------------------------------------------------------------
// Service
// ---------------------------------------------------------------------------

// AdminService encapsulates admin business logic.
type AdminService struct {
	pool            repository.Pool
	adminRepo       repository.AdminRepository
	entitlementRepo repository.EntitlementRepository
	jwtSecret       string
	adminTokenTTL   time.Duration
	logger          *slog.Logger
}

// NewAdminService creates an AdminService with its required dependencies.
func NewAdminService(
	pool repository.Pool,
	adminRepo repository.AdminRepository,
	entitlementRepo repository.EntitlementRepository,
	jwtSecret string,
	adminTokenTTL time.Duration,
	logger *slog.Logger,
) *AdminService {
	return &AdminService{
		pool:            pool,
		adminRepo:       adminRepo,
		entitlementRepo: entitlementRepo,
		jwtSecret:       jwtSecret,
		adminTokenTTL:   adminTokenTTL,
		logger:          logger,
	}
}

// Login authenticates an admin by username and password. It avoids leaking
// whether the username exists by performing a constant-time comparison even
// when the admin is not found.
func (s *AdminService) Login(ctx context.Context, in LoginInput) (LoginOutput, error) {
	cred, err := s.adminRepo.GetAdminByUsername(ctx, s.pool, in.Username)
	if errors.Is(err, repository.ErrNotFound) {
		// Perform a dummy password check so that the response time does not
		// reveal whether the username exists.
		_, _ = auth.CheckPassword("$2a$12$000000000000000000000uGbhMOb3FMvYXCaaDfibSaHOHfNqYpfi", in.Password)
		return LoginOutput{}, UnauthorizedError("Invalid credentials")
	}
	if err != nil {
		s.logger.Error("querying admin credentials", "err", err)
		return LoginOutput{}, ErrInternal
	}

	match, err := auth.CheckPassword(cred.PasswordHash, in.Password)
	if err != nil {
		s.logger.Error("checking admin password", "err", err)
		return LoginOutput{}, ErrInternal
	}
	if !match {
		return LoginOutput{}, UnauthorizedError("Invalid credentials")
	}

	token, err := auth.IssueAdminToken(cred.ID, s.jwtSecret, s.adminTokenTTL)
	if err != nil {
		s.logger.Error("issuing admin token", "err", err)
		return LoginOutput{}, ErrInternal
	}

	return LoginOutput{Token: token}, nil
}

// ListUsers returns a paginated list of users for the admin dashboard.
func (s *AdminService) ListUsers(ctx context.Context, in ListUsersInput) (ListUsersOutput, error) {
	page, limit := pagination.Normalize(in.Page, in.Limit)

	rows, total, err := s.adminRepo.ListUsers(ctx, s.pool, repository.ListUsersParams{
		Limit:  limit,
		Offset: (page - 1) * limit,
		Search: in.Search,
	})
	if err != nil {
		s.logger.Error("listing admin users", "err", err)
		return ListUsersOutput{}, ErrInternal
	}

	users := make([]AdminUserItem, len(rows))
	for i, r := range rows {
		users[i] = AdminUserItem{
			ID:                r.ID,
			Email:             r.Email,
			Name:              r.Name,
			Username:          r.Username,
			ProfilePictureURL: r.ProfilePictureURL,
			CreatedAt:         r.CreatedAt,
			IsPremium:         r.IsPremium,
		}
	}

	return ListUsersOutput{
		Users: users,
		Total: total,
		Page:  page,
		Limit: limit,
	}, nil
}

// GetUserDetail returns the full detail of a single user for the admin dashboard.
func (s *AdminService) GetUserDetail(ctx context.Context, in GetUserDetailInput) (UserDetailOutput, error) {
	row, err := s.adminRepo.GetUserDetail(ctx, s.pool, in.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return UserDetailOutput{}, NotFoundError("User not found")
	}
	if err != nil {
		s.logger.Error("getting admin user detail", "user_id", in.UserID, "err", err)
		return UserDetailOutput{}, ErrInternal
	}

	return UserDetailOutput{
		ID:                  row.ID,
		Email:               row.Email,
		Name:                row.Name,
		Username:            row.Username,
		ProfilePictureURL:   row.ProfilePictureURL,
		LeaderboardVisible:  row.LeaderboardVisible,
		CreatedAt:           row.CreatedAt,
		IsPremium:           row.IsPremium,
		CurrentPeriodEnd:    row.CurrentPeriodEnd,
		RevenueCatAppUserID: row.RevenueCatAppUserID,
		FriendCount:         row.FriendCount,
		TotalSessions:       row.TotalSessions,
		LastSessionTime:     row.LastSessionTime,
	}, nil
}

// GetPlatformStats returns aggregate platform statistics for the admin dashboard.
func (s *AdminService) GetPlatformStats(ctx context.Context) (PlatformStatsOutput, error) {
	row, err := s.adminRepo.GetPlatformStats(ctx, s.pool)
	if err != nil {
		s.logger.Error("getting platform stats", "err", err)
		return PlatformStatsOutput{}, ErrInternal
	}

	return PlatformStatsOutput{
		TotalUsers:                row.TotalUsers,
		ActiveUsers30d:            row.ActiveUsers30d,
		PremiumUsers:              row.PremiumUsers,
		ActivePremiumEntitlements: row.ActivePremiumEntitlements,
		TotalFriendships:          row.TotalFriendships,
		AvgStreakDays:             row.AvgStreakDays,
		AvgDailyFocusTimeMS:       row.AvgDailyFocusTimeMS,
	}, nil
}

// ManageEntitlement grants or revokes a user entitlement via admin override.
// It validates the action and entitlement, checks that the user exists, then
// upserts the admin override row. The override is the source of truth for
// authorization while active; user_entitlements (the RevenueCat snapshot) is
// not mutated so that temporary overrides expire cleanly.
func (s *AdminService) ManageEntitlement(ctx context.Context, in ManageEntitlementInput) (MessageOutput, error) {
	if in.Action != repository.AdminOverrideGrant && in.Action != repository.AdminOverrideRevoke {
		return MessageOutput{}, invalidAdminActionError()
	}
	if in.Entitlement != repository.EntitlementPremium {
		return MessageOutput{}, invalidEntitlementError(in.Entitlement)
	}

	// Check user existence before starting the transaction.
	exists, err := s.adminRepo.UserExists(ctx, s.pool, in.UserID)
	if err != nil {
		s.logger.Error("checking user existence for entitlement management", "user_id", in.UserID, "err", err)
		return MessageOutput{}, ErrInternal
	}
	if !exists {
		return MessageOutput{}, NotFoundError("User not found")
	}

	// Upsert the admin entitlement override. Authorization reads and admin
	// listing queries consult this table and apply the override while it is
	// active (unexpired). When the override expires the stored RevenueCat
	// snapshot in user_entitlements takes effect again automatically.
	if err := s.adminRepo.UpsertEntitlementOverride(ctx, s.pool, repository.UpsertOverrideParams{
		UserID:      in.UserID,
		Entitlement: in.Entitlement,
		Action:      in.Action,
		ExpiresAt:   in.ExpiresAt,
	}); err != nil {
		s.logger.Error("upserting entitlement override", "user_id", in.UserID, "err", err)
		return MessageOutput{}, ErrInternal
	}

	return MessageOutput{Message: fmt.Sprintf("Entitlement %s %sed for user", in.Entitlement, in.Action)}, nil
}

// ListEntitlements returns a paginated list of entitlements for the admin dashboard.
func (s *AdminService) ListEntitlements(ctx context.Context, in ListEntitlementsInput) (ListEntitlementsOutput, error) {
	page, limit := pagination.Normalize(in.Page, in.Limit)

	rows, total, err := s.adminRepo.ListEntitlements(ctx, s.pool, repository.ListEntitlementsParams{
		Limit:       limit,
		Offset:      (page - 1) * limit,
		Entitlement: in.Entitlement,
		IsActive:    in.IsActive,
	})
	if err != nil {
		s.logger.Error("listing admin entitlements", "err", err)
		return ListEntitlementsOutput{}, ErrInternal
	}

	entitlements := make([]AdminEntitlementItem, len(rows))
	for i, r := range rows {
		entitlements[i] = AdminEntitlementItem{
			UserID:           r.UserID,
			Email:            r.Email,
			Username:         r.Username,
			Entitlement:      r.Entitlement,
			IsActive:         r.IsActive,
			CurrentPeriodEnd: r.CurrentPeriodEnd,
			UpdatedAt:        r.UpdatedAt,
		}
	}

	return ListEntitlementsOutput{
		Entitlements: entitlements,
		Total:        total,
		Page:         page,
		Limit:        limit,
	}, nil
}
