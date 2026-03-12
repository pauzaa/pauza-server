package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/IsorilovA/pauza-server/internal/repository"
	"github.com/IsorilovA/pauza-server/internal/revenuecat"
)

// uuidRE matches canonical UUID v4 strings used as local user IDs.
var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// premiumEntitlement is the entitlement identifier we track locally.
const premiumEntitlement = repository.EntitlementPremium

// webhookUserLookup is the subset of AuthRepository that the webhook service
// needs to resolve a RevenueCat app_user_id to a local user row.
type webhookUserLookup interface {
	GetUserByID(ctx context.Context, db repository.DBTX, userID string) (repository.UserRow, error)
}

// revenueCatSubscriberFetcher is the subset of the RevenueCat client that the
// webhook service depends on.
type revenueCatSubscriberFetcher interface {
	GetSubscriber(ctx context.Context, appUserID string) (*revenuecat.SubscriberResponse, error)
}

// overrideChecker is the narrow interface the webhook service uses to detect
// active admin entitlement overrides. It avoids coupling to the full
// AdminRepository. The dependency is optional: when nil the webhook service
// reconciles unconditionally (backwards-compatible).
type overrideChecker interface {
	GetActiveOverride(ctx context.Context, db repository.DBTX, userID string, entitlement repository.Entitlement) (repository.OverrideRow, error)
}

// WebhookService handles RevenueCat webhook reconciliation logic.
type WebhookService struct {
	pool            repository.Pool
	entitlementRepo repository.EntitlementRepository
	rcClient        revenueCatSubscriberFetcher
	userLookup      webhookUserLookup
	overrides       overrideChecker // nil → no override guard (backwards-compatible)
	logger          *slog.Logger
}

// WebhookServiceOption configures optional WebhookService dependencies.
type WebhookServiceOption func(*WebhookService)

// WithOverrideChecker sets the override checker used to skip reconciliation
// when an active admin entitlement override exists for a user. When not
// provided the service reconciles unconditionally.
func WithOverrideChecker(oc overrideChecker) WebhookServiceOption {
	return func(s *WebhookService) {
		s.overrides = oc
	}
}

// NewWebhookService creates a WebhookService with its required dependencies.
func NewWebhookService(
	pool repository.Pool,
	entitlementRepo repository.EntitlementRepository,
	rcClient revenueCatSubscriberFetcher,
	userLookup webhookUserLookup,
	logger *slog.Logger,
	opts ...WebhookServiceOption,
) *WebhookService {
	s := &WebhookService{
		pool:            pool,
		entitlementRepo: entitlementRepo,
		rcClient:        rcClient,
		userLookup:      userLookup,
		logger:          logger,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// HandleWebhook processes a single RevenueCat webhook event. TEST events are
// logged and ignored. For all other event types the service determines which
// RevenueCat user IDs need reconciliation (including transfer participants),
// deduplicates them, and reconciles each one. Unknown local users produce a
// warning but are not treated as errors. Transient RevenueCat API or DB
// failures are returned to the caller.
func (s *WebhookService) HandleWebhook(ctx context.Context, event revenuecat.WebhookEvent) error {
	if event.Type == revenuecat.WebhookEventTypeTest {
		s.logger.Info("received TEST webhook event", "event_id", event.ID)
		return nil
	}

	// Collect the RevenueCat user IDs that should be reconciled.
	rcUserIDs := s.collectReconcileIDs(event)

	var firstErr error
	for _, rcUID := range rcUserIDs {
		if err := s.reconcileUser(ctx, rcUID, event); err != nil {
			s.logger.Error("reconciling user", "rc_user_id", rcUID, "event_type", event.Type, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// collectReconcileIDs returns a deduplicated list of RevenueCat user IDs that
// should be reconciled for the given event. For transfer events this includes
// the transferred-from and transferred-to participants in addition to the
// event's own user IDs.
func (s *WebhookService) collectReconcileIDs(event revenuecat.WebhookEvent) []string {
	seen := make(map[string]struct{})
	var ids []string

	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	add(event.AppUserID)
	add(event.OriginalAppUserID)

	// Transfer events include participants that may have changed entitlements.
	for _, id := range event.TransferredFrom {
		add(id)
	}
	for _, id := range event.TransferredTo {
		add(id)
	}

	return ids
}

// hasActiveOverride returns true when an active admin entitlement override
// exists for the given user and entitlement. It returns false when no override
// checker is configured (backwards-compatible). Transient errors from the
// checker are returned to the caller so that reconciliation does not silently
// overwrite an admin override when the lookup fails.
func (s *WebhookService) hasActiveOverride(ctx context.Context, userID string, entitlement repository.Entitlement) (bool, error) {
	if s.overrides == nil {
		return false, nil
	}
	override, err := s.overrides.GetActiveOverride(ctx, s.pool, userID, entitlement)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return false, nil
		}
		s.logger.Error("checking active override", "user_id", userID, "entitlement", entitlement, "err", err)
		return false, fmt.Errorf("checking active override for user %s: %w", userID, err)
	}
	// An active grant or revoke override means the admin has explicitly set
	// the entitlement state — skip RevenueCat reconciliation.
	if override.Action == repository.AdminOverrideGrant || override.Action == repository.AdminOverrideRevoke {
		s.logger.Info("skipping reconciliation due to active admin override",
			"user_id", userID,
			"entitlement", entitlement,
			"override_action", override.Action,
		)
		return true, nil
	}
	return false, nil
}

// reconcileUser fetches the current subscriber state from RevenueCat for
// a single RevenueCat user ID, resolves all matching local users, derives
// the premium entitlement state, and upserts the local user_entitlements
// row for each of them.
func (s *WebhookService) reconcileUser(ctx context.Context, rcUserID string, event revenuecat.WebhookEvent) error {
	// Step 1: Resolve the RevenueCat user ID to all matching local user IDs.
	localUserIDs, err := s.resolveLocalUsers(ctx, rcUserID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			s.logger.Warn("unknown local user for revenuecat id, skipping",
				"rc_user_id", rcUserID, "event_type", event.Type)
			return nil
		}
		return fmt.Errorf("resolving local users for rc_user_id %s: %w", rcUserID, err)
	}

	// Step 2: Fetch current subscriber state from RevenueCat.
	subResp, err := s.rcClient.GetSubscriber(ctx, rcUserID)
	if err != nil {
		var apiErr *revenuecat.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			// Subscriber no longer exists at RevenueCat — mark inactive for all.
			var firstErr error
			for _, uid := range localUserIDs {
				overridden, oErr := s.hasActiveOverride(ctx, uid, premiumEntitlement)
				if oErr != nil {
					if firstErr == nil {
						firstErr = oErr
					}
					continue
				}
				if overridden {
					continue
				}
				if uErr := s.upsertInactive(ctx, uid, rcUserID, event); uErr != nil {
					if firstErr == nil {
						firstErr = uErr
					}
				}
			}
			return firstErr
		}
		return fmt.Errorf("fetching subscriber %s from revenuecat: %w", rcUserID, err)
	}

	// Step 3: Derive the premium entitlement state.
	now := time.Now().UTC()
	reconciled := revenuecat.DeriveEntitlement(&subResp.Subscriber, string(premiumEntitlement), now)

	// Step 4: Upsert user_entitlements for every matching local user.
	eventTime := msToTime(event.EventTimestampMs)
	rcOriginal := subResp.Subscriber.OriginalAppUserID
	var firstErr error
	for _, localUserID := range localUserIDs {
		overridden, oErr := s.hasActiveOverride(ctx, localUserID, premiumEntitlement)
		if oErr != nil {
			s.logger.Error("override check failed, skipping reconciliation for user",
				"user_id", localUserID, "rc_user_id", rcUserID, "err", oErr)
			if firstErr == nil {
				firstErr = oErr
			}
			continue
		}
		if overridden {
			continue
		}

		params := repository.UpsertEntitlementParams{
			UserID:                      localUserID,
			Entitlement:                 string(premiumEntitlement),
			IsActive:                    reconciled.IsActive,
			RevenueCatAppUserID:         strPtr(rcUserID),
			RevenueCatOriginalAppUserID: strPtrNonEmpty(rcOriginal),
			CurrentPeriodEnd:            reconciled.CurrentPeriodEnd,
			LastWebhookEventAt:          &eventTime,
		}
		if uErr := s.entitlementRepo.UpsertEntitlement(ctx, s.pool, params); uErr != nil {
			s.logger.Error("upserting entitlement", "user_id", localUserID, "rc_user_id", rcUserID, "err", uErr)
			if firstErr == nil {
				firstErr = fmt.Errorf("upserting entitlement for user %s: %w", localUserID, uErr)
			}
			continue
		}

		s.logger.Info("reconciled entitlement",
			"user_id", localUserID,
			"rc_user_id", rcUserID,
			"entitlement", premiumEntitlement,
			"is_active", reconciled.IsActive,
			"event_type", event.Type,
		)
	}
	return firstErr
}

// resolveLocalUsers maps a RevenueCat user ID to all matching local user IDs.
// It combines a direct user lookup (when the ID is a valid UUID) with the
// entitlement-based lookup that may return multiple previously-associated
// users. Results from both paths are merged and deduplicated so that all
// matching local users are reconciled, even when the RevenueCat ID happens
// to be a UUID.
func (s *WebhookService) resolveLocalUsers(ctx context.Context, rcUserID string) ([]string, error) {
	seen := make(map[string]struct{})
	var ids []string

	add := func(id string) {
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	// Fast path: if the RevenueCat app_user_id looks like one of our UUIDs,
	// try a direct user lookup and include that user in the result set.
	if uuidRE.MatchString(rcUserID) {
		user, err := s.userLookup.GetUserByID(ctx, s.pool, rcUserID)
		if err == nil {
			add(user.ID)
		} else if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}

	// Always check the entitlement-based lookup for additional matching users.
	users, err := s.entitlementRepo.GetUsersByRevenueCatID(ctx, s.pool, rcUserID, rcUserID)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	for _, u := range users {
		add(u.ID)
	}

	if len(ids) == 0 {
		return nil, repository.ErrNotFound
	}
	return ids, nil
}

// upsertInactive records an inactive entitlement when the RevenueCat subscriber
// is no longer found (404).
func (s *WebhookService) upsertInactive(ctx context.Context, localUserID, rcUserID string, event revenuecat.WebhookEvent) error {
	eventTime := msToTime(event.EventTimestampMs)
	params := repository.UpsertEntitlementParams{
		UserID:              localUserID,
		Entitlement:         string(premiumEntitlement),
		IsActive:            false,
		RevenueCatAppUserID: strPtr(rcUserID),
		LastWebhookEventAt:  &eventTime,
	}
	if err := s.entitlementRepo.UpsertEntitlement(ctx, s.pool, params); err != nil {
		return fmt.Errorf("upserting inactive entitlement for user %s: %w", localUserID, err)
	}

	s.logger.Info("reconciled entitlement (subscriber not found)",
		"user_id", localUserID,
		"rc_user_id", rcUserID,
		"entitlement", premiumEntitlement,
		"is_active", false,
		"event_type", event.Type,
	)
	return nil
}

// msToTime converts a Unix-millisecond timestamp to time.Time.
func msToTime(ms int64) time.Time {
	return time.Unix(0, ms*int64(time.Millisecond)).UTC()
}

func strPtr(s string) *string {
	return &s
}

func strPtrNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
