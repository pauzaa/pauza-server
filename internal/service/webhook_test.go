package service

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/repository"
	"github.com/IsorilovA/pauza-server/internal/revenuecat"
)

// ---------------------------------------------------------------------------
// Fakes specific to webhook tests
// ---------------------------------------------------------------------------

// fakeEntitlementRepo implements repository.EntitlementRepository.
type fakeEntitlementRepo struct {
	upsertEntitlementFn      func(ctx context.Context, db repository.DBTX, params repository.UpsertEntitlementParams) error
	getEntitlementFn         func(ctx context.Context, db repository.DBTX, userID, entitlement string) (repository.EntitlementDetailRow, error)
	getUsersByRevenueCatIDFn func(ctx context.Context, db repository.DBTX, appUserID, originalAppUserID string) ([]repository.UserRow, error)
}

var _ repository.EntitlementRepository = (*fakeEntitlementRepo)(nil)

func (f *fakeEntitlementRepo) UpsertEntitlement(ctx context.Context, db repository.DBTX, params repository.UpsertEntitlementParams) error {
	if f.upsertEntitlementFn != nil {
		return f.upsertEntitlementFn(ctx, db, params)
	}
	return nil
}

func (f *fakeEntitlementRepo) GetEntitlement(ctx context.Context, db repository.DBTX, userID, entitlement string) (repository.EntitlementDetailRow, error) {
	if f.getEntitlementFn != nil {
		return f.getEntitlementFn(ctx, db, userID, entitlement)
	}
	return repository.EntitlementDetailRow{}, repository.ErrNotFound
}

func (f *fakeEntitlementRepo) GetUsersByRevenueCatID(ctx context.Context, db repository.DBTX, appUserID, originalAppUserID string) ([]repository.UserRow, error) {
	if f.getUsersByRevenueCatIDFn != nil {
		return f.getUsersByRevenueCatIDFn(ctx, db, appUserID, originalAppUserID)
	}
	return nil, repository.ErrNotFound
}

// fakeRCClient implements revenueCatSubscriberFetcher.
type fakeRCClient struct {
	getSubscriberFn func(ctx context.Context, appUserID string) (*revenuecat.SubscriberResponse, error)
}

var _ revenueCatSubscriberFetcher = (*fakeRCClient)(nil)

func (f *fakeRCClient) GetSubscriber(ctx context.Context, appUserID string) (*revenuecat.SubscriberResponse, error) {
	if f.getSubscriberFn != nil {
		return f.getSubscriberFn(ctx, appUserID)
	}
	return nil, errors.New("fakeRCClient.GetSubscriber: not configured")
}

// fakeWebhookUserLookup implements webhookUserLookup.
type fakeWebhookUserLookup struct {
	getUserByIDFn func(ctx context.Context, db repository.DBTX, userID string) (repository.UserRow, error)
}

var _ webhookUserLookup = (*fakeWebhookUserLookup)(nil)

func (f *fakeWebhookUserLookup) GetUserByID(ctx context.Context, db repository.DBTX, userID string) (repository.UserRow, error) {
	if f.getUserByIDFn != nil {
		return f.getUserByIDFn(ctx, db, userID)
	}
	return repository.UserRow{}, repository.ErrNotFound
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(devNull{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestWebhookService(
	entRepo *fakeEntitlementRepo,
	rcClient *fakeRCClient,
	userLookup *fakeWebhookUserLookup,
) *WebhookService {
	return NewWebhookService(
		&fakePool{},
		entRepo,
		rcClient,
		userLookup,
		discardLogger(),
	)
}

// validUUID is a canonical UUID v4 that passes the uuidRE check.
const validUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func activeSubscriberResponse(rcOriginalID string) *revenuecat.SubscriberResponse {
	future := time.Now().UTC().Add(30 * 24 * time.Hour)
	return &revenuecat.SubscriberResponse{
		Subscriber: revenuecat.Subscriber{
			OriginalAppUserID: rcOriginalID,
			Entitlements: map[string]revenuecat.EntitlementObj{
				"premium": {
					ExpiresDate:       &future,
					PurchaseDate:      time.Now().UTC().Add(-30 * 24 * time.Hour),
					ProductIdentifier: "monthly_premium",
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestWebhook_TESTEvent_NoReconciliation verifies that TEST events are
// acknowledged without any RC API call or DB upsert.
func TestWebhook_TESTEvent_NoReconciliation(t *testing.T) {
	t.Parallel()

	rcClient := &fakeRCClient{
		getSubscriberFn: func(context.Context, string) (*revenuecat.SubscriberResponse, error) {
			t.Fatal("GetSubscriber must not be called for TEST events")
			return nil, nil
		},
	}
	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(context.Context, repository.DBTX, repository.UpsertEntitlementParams) error {
			t.Fatal("UpsertEntitlement must not be called for TEST events")
			return nil
		},
	}
	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			t.Fatal("GetUserByID must not be called for TEST events")
			return repository.UserRow{}, nil
		},
	}

	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type: "TEST",
		ID:   "evt_test_1",
	})
	if err != nil {
		t.Fatalf("HandleWebhook(TEST) = %v, want nil", err)
	}
}

// TestWebhook_NormalEvent_ResolvesAndUpsertsEntitlement verifies the happy
// path: the service resolves the user by UUID, fetches the subscriber from
// RevenueCat, and upserts the entitlement.
func TestWebhook_NormalEvent_ResolvesAndUpsertsEntitlement(t *testing.T) {
	t.Parallel()

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			if userID != validUUID {
				t.Errorf("GetUserByID userID = %q, want %q", userID, validUUID)
			}
			return repository.UserRow{ID: validUUID, Email: "alice@example.com"}, nil
		},
	}

	subResp := activeSubscriberResponse(validUUID)

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, appUserID string) (*revenuecat.SubscriberResponse, error) {
			if appUserID != validUUID {
				t.Errorf("GetSubscriber appUserID = %q, want %q", appUserID, validUUID)
			}
			return subResp, nil
		},
	}

	var upserted repository.UpsertEntitlementParams
	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertEntitlementParams) error {
			upserted = params
			return nil
		},
	}

	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	event := revenuecat.WebhookEvent{
		Type:             "RENEWAL",
		ID:               "evt_renewal_1",
		AppUserID:        validUUID,
		EventTimestampMs: 1700000000000,
	}
	err := svc.HandleWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil", err)
	}

	// Verify the upserted params.
	if upserted.UserID != validUUID {
		t.Errorf("upserted.UserID = %q, want %q", upserted.UserID, validUUID)
	}
	if upserted.Entitlement != "premium" {
		t.Errorf("upserted.Entitlement = %q, want %q", upserted.Entitlement, "premium")
	}
	if !upserted.IsActive {
		t.Error("upserted.IsActive = false, want true")
	}
	if upserted.RevenueCatAppUserID == nil || *upserted.RevenueCatAppUserID != validUUID {
		t.Errorf("upserted.RevenueCatAppUserID = %v, want %q", upserted.RevenueCatAppUserID, validUUID)
	}
	if upserted.CurrentPeriodEnd == nil {
		t.Error("upserted.CurrentPeriodEnd = nil, want non-nil")
	}
	if upserted.LastWebhookEventAt == nil {
		t.Fatal("upserted.LastWebhookEventAt = nil, want non-nil")
	}
	wantEventTime := msToTime(1700000000000)
	if !upserted.LastWebhookEventAt.Equal(wantEventTime) {
		t.Errorf("upserted.LastWebhookEventAt = %v, want %v",
			upserted.LastWebhookEventAt, wantEventTime)
	}
}

// TestWebhook_UserNotFoundLocally_WarnsAndReturnsNil verifies that an
// unknown local user (neither direct UUID lookup nor entitlement-based
// lookup) produces a warning but no error.
func TestWebhook_UserNotFoundLocally_WarnsAndReturnsNil(t *testing.T) {
	t.Parallel()

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			return repository.UserRow{}, repository.ErrNotFound
		},
	}
	entRepo := &fakeEntitlementRepo{
		getUsersByRevenueCatIDFn: func(context.Context, repository.DBTX, string, string) ([]repository.UserRow, error) {
			return nil, repository.ErrNotFound
		},
		upsertEntitlementFn: func(context.Context, repository.DBTX, repository.UpsertEntitlementParams) error {
			t.Fatal("UpsertEntitlement must not be called when user is not found")
			return nil
		},
	}
	rcClient := &fakeRCClient{
		getSubscriberFn: func(context.Context, string) (*revenuecat.SubscriberResponse, error) {
			t.Fatal("GetSubscriber must not be called when user is not found locally")
			return nil, nil
		},
	}

	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "RENEWAL",
		ID:               "evt_renewal_2",
		AppUserID:        validUUID,
		EventTimestampMs: 1700000000000,
	})
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil (warning only)", err)
	}
}

// TestWebhook_RC404_UpsertsInactive verifies that when RevenueCat returns
// a 404 (subscriber no longer exists), the service upserts an inactive
// entitlement.
func TestWebhook_RC404_UpsertsInactive(t *testing.T) {
	t.Parallel()

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			return repository.UserRow{ID: userID, Email: "alice@example.com"}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, appUserID string) (*revenuecat.SubscriberResponse, error) {
			return nil, &revenuecat.APIError{StatusCode: 404, AppUserID: appUserID}
		},
	}

	var upserted repository.UpsertEntitlementParams
	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertEntitlementParams) error {
			upserted = params
			return nil
		},
	}

	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "EXPIRATION",
		ID:               "evt_exp_1",
		AppUserID:        validUUID,
		EventTimestampMs: 1700000000000,
	})
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil", err)
	}

	if upserted.UserID != validUUID {
		t.Errorf("upserted.UserID = %q, want %q", upserted.UserID, validUUID)
	}
	if upserted.IsActive {
		t.Error("upserted.IsActive = true, want false (RC 404 → inactive)")
	}
	if upserted.Entitlement != "premium" {
		t.Errorf("upserted.Entitlement = %q, want %q", upserted.Entitlement, "premium")
	}
	if upserted.CurrentPeriodEnd != nil {
		t.Errorf("upserted.CurrentPeriodEnd = %v, want nil for RC 404", upserted.CurrentPeriodEnd)
	}
}

// TestWebhook_TransientRCError_ReturnsError verifies that a transient
// RevenueCat error (e.g. 500) is propagated to the caller.
func TestWebhook_TransientRCError_ReturnsError(t *testing.T) {
	t.Parallel()

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			return repository.UserRow{ID: userID, Email: "alice@example.com"}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, appUserID string) (*revenuecat.SubscriberResponse, error) {
			return nil, &revenuecat.APIError{StatusCode: 500, AppUserID: appUserID}
		},
	}

	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(context.Context, repository.DBTX, repository.UpsertEntitlementParams) error {
			t.Fatal("UpsertEntitlement must not be called on transient RC error")
			return nil
		},
	}

	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "RENEWAL",
		ID:               "evt_renewal_3",
		AppUserID:        validUUID,
		EventTimestampMs: 1700000000000,
	})
	if err == nil {
		t.Fatal("HandleWebhook() = nil, want error for transient RC failure")
	}

	var apiErr *revenuecat.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error chain does not contain *revenuecat.APIError: %v", err)
	}
	if apiErr.StatusCode != 500 {
		t.Errorf("APIError.StatusCode = %d, want 500", apiErr.StatusCode)
	}
}

// TestWebhook_TransferEvent_ReconcilesBothUsers verifies that a TRANSFER
// event reconciles both the transferred_from and transferred_to user IDs.
func TestWebhook_TransferEvent_ReconcilesBothUsers(t *testing.T) {
	t.Parallel()

	const (
		fromUID = "11111111-1111-1111-1111-111111111111"
		toUID   = "22222222-2222-2222-2222-222222222222"
	)

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			switch userID {
			case fromUID, toUID:
				return repository.UserRow{ID: userID, Email: userID + "@example.com"}, nil
			default:
				return repository.UserRow{}, repository.ErrNotFound
			}
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, appUserID string) (*revenuecat.SubscriberResponse, error) {
			return activeSubscriberResponse(appUserID), nil
		},
	}

	var upsertCalls int64
	reconciledUsers := make(map[string]bool)
	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertEntitlementParams) error {
			atomic.AddInt64(&upsertCalls, 1)
			reconciledUsers[params.UserID] = true
			return nil
		},
	}

	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	event := revenuecat.WebhookEvent{
		Type:             "TRANSFER",
		ID:               "evt_transfer_1",
		AppUserID:        fromUID,
		EventTimestampMs: 1700000000000,
		TransferredFrom:  []string{fromUID},
		TransferredTo:    []string{toUID},
	}
	err := svc.HandleWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("HandleWebhook(TRANSFER) = %v, want nil", err)
	}

	if !reconciledUsers[fromUID] {
		t.Errorf("transferred_from user %q was not reconciled", fromUID)
	}
	if !reconciledUsers[toUID] {
		t.Errorf("transferred_to user %q was not reconciled", toUID)
	}
}

// TestWebhook_TransferEvent_DeduplicatesIDs verifies that if a user ID
// appears in multiple event fields, it is reconciled exactly once.
func TestWebhook_TransferEvent_DeduplicatesIDs(t *testing.T) {
	t.Parallel()

	const uid = "33333333-3333-3333-3333-333333333333"

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			return repository.UserRow{ID: userID, Email: "dup@example.com"}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, appUserID string) (*revenuecat.SubscriberResponse, error) {
			return activeSubscriberResponse(appUserID), nil
		},
	}

	var upsertCalls int64
	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertEntitlementParams) error {
			atomic.AddInt64(&upsertCalls, 1)
			return nil
		},
	}

	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	event := revenuecat.WebhookEvent{
		Type:              "TRANSFER",
		ID:                "evt_transfer_dup",
		AppUserID:         uid,
		OriginalAppUserID: uid,
		EventTimestampMs:  1700000000000,
		TransferredFrom:   []string{uid},
		TransferredTo:     []string{uid},
	}
	err := svc.HandleWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil", err)
	}

	// The same UUID appearing in all fields should be deduplicated to one call.
	if n := atomic.LoadInt64(&upsertCalls); n != 1 {
		t.Errorf("UpsertEntitlement called %d times, want 1 (dedup)", n)
	}
}

// TestWebhook_DuplicateReplay_IdempotentUpsert verifies that replaying
// the same event produces the same upsert (idempotent by design since
// UpsertEntitlement uses ON CONFLICT DO UPDATE).
func TestWebhook_DuplicateReplay_IdempotentUpsert(t *testing.T) {
	t.Parallel()

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			return repository.UserRow{ID: userID, Email: "alice@example.com"}, nil
		},
	}

	subResp := activeSubscriberResponse(validUUID)
	rcClient := &fakeRCClient{
		getSubscriberFn: func(context.Context, string) (*revenuecat.SubscriberResponse, error) {
			return subResp, nil
		},
	}

	var calls int64
	var lastParams repository.UpsertEntitlementParams
	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertEntitlementParams) error {
			atomic.AddInt64(&calls, 1)
			lastParams = params
			return nil
		},
	}

	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	event := revenuecat.WebhookEvent{
		Type:             "RENEWAL",
		ID:               "evt_renewal_dup",
		AppUserID:        validUUID,
		EventTimestampMs: 1700000000000,
	}

	// First delivery.
	if err := svc.HandleWebhook(context.Background(), event); err != nil {
		t.Fatalf("first HandleWebhook() = %v, want nil", err)
	}
	firstParams := lastParams

	// Second delivery (replay).
	if err := svc.HandleWebhook(context.Background(), event); err != nil {
		t.Fatalf("second HandleWebhook() = %v, want nil", err)
	}
	secondParams := lastParams

	// Both should result in the same upsert parameters.
	if firstParams.UserID != secondParams.UserID {
		t.Errorf("UserID changed: %q → %q", firstParams.UserID, secondParams.UserID)
	}
	if firstParams.IsActive != secondParams.IsActive {
		t.Errorf("IsActive changed: %v → %v", firstParams.IsActive, secondParams.IsActive)
	}
	if firstParams.Entitlement != secondParams.Entitlement {
		t.Errorf("Entitlement changed: %q → %q", firstParams.Entitlement, secondParams.Entitlement)
	}
	if atomic.LoadInt64(&calls) != 2 {
		t.Errorf("UpsertEntitlement called %d times, want 2 (one per delivery)", atomic.LoadInt64(&calls))
	}
}

// TestWebhook_NonUUID_FallsBackToEntitlementLookup verifies that when the
// RevenueCat app_user_id is not a UUID, the service falls back to the
// entitlement-based lookup and still reconciles.
func TestWebhook_NonUUID_FallsBackToEntitlementLookup(t *testing.T) {
	t.Parallel()

	const rcAnonymousID = "$RCAnonymousID:abc123"
	const localUserID = "44444444-4444-4444-4444-444444444444"

	// The UUID-based lookup should NOT be called for a non-UUID app_user_id.
	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			t.Fatal("GetUserByID should not be called for non-UUID app_user_id")
			return repository.UserRow{}, nil
		},
	}

	var upserted bool
	entRepo := &fakeEntitlementRepo{
		getUsersByRevenueCatIDFn: func(_ context.Context, _ repository.DBTX, appUserID, originalAppUserID string) ([]repository.UserRow, error) {
			if appUserID != rcAnonymousID {
				t.Errorf("appUserID = %q, want %q", appUserID, rcAnonymousID)
			}
			return []repository.UserRow{{ID: localUserID, Email: "bob@example.com"}}, nil
		},
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertEntitlementParams) error {
			if params.UserID != localUserID {
				t.Errorf("upserted UserID = %q, want %q", params.UserID, localUserID)
			}
			upserted = true
			return nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(context.Context, string) (*revenuecat.SubscriberResponse, error) {
			return activeSubscriberResponse(rcAnonymousID), nil
		},
	}

	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "INITIAL_PURCHASE",
		ID:               "evt_purchase_1",
		AppUserID:        rcAnonymousID,
		EventTimestampMs: 1700000000000,
	})
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil", err)
	}
	if !upserted {
		t.Error("UpsertEntitlement was not called")
	}
}

// TestWebhook_DBErrorOnUpsert_ReturnsError verifies that a database error
// during upsert is returned to the caller.
func TestWebhook_DBErrorOnUpsert_ReturnsError(t *testing.T) {
	t.Parallel()

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			return repository.UserRow{ID: userID, Email: "alice@example.com"}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(context.Context, string) (*revenuecat.SubscriberResponse, error) {
			return activeSubscriberResponse(validUUID), nil
		},
	}

	dbErr := errors.New("db connection lost")
	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(context.Context, repository.DBTX, repository.UpsertEntitlementParams) error {
			return dbErr
		},
	}

	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "RENEWAL",
		ID:               "evt_renewal_4",
		AppUserID:        validUUID,
		EventTimestampMs: 1700000000000,
	})
	if err == nil {
		t.Fatal("HandleWebhook() = nil, want error on DB failure")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("error chain does not contain original DB error: %v", err)
	}
}

// TestWebhook_MultipleMatchingUsers_ReconcilesAll verifies that when the
// entitlement-based RevenueCat ID lookup returns multiple local users, each
// one is reconciled with its own upsert.
func TestWebhook_MultipleMatchingUsers_ReconcilesAll(t *testing.T) {
	t.Parallel()

	const rcAnonymousID = "$RCAnonymousID:multi123"
	const userA = "aaaa1111-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const userB = "bbbb2222-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	// Non-UUID app_user_id — the UUID fast path must not be called.
	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			t.Fatal("GetUserByID should not be called for non-UUID app_user_id")
			return repository.UserRow{}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(context.Context, string) (*revenuecat.SubscriberResponse, error) {
			return activeSubscriberResponse(rcAnonymousID), nil
		},
	}

	reconciledUsers := make(map[string]bool)
	var upsertCalls int64
	entRepo := &fakeEntitlementRepo{
		getUsersByRevenueCatIDFn: func(_ context.Context, _ repository.DBTX, _, _ string) ([]repository.UserRow, error) {
			return []repository.UserRow{
				{ID: userA, Email: "a@example.com"},
				{ID: userB, Email: "b@example.com"},
			}, nil
		},
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertEntitlementParams) error {
			atomic.AddInt64(&upsertCalls, 1)
			reconciledUsers[params.UserID] = true
			return nil
		},
	}

	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "RENEWAL",
		ID:               "evt_multi_1",
		AppUserID:        rcAnonymousID,
		EventTimestampMs: 1700000000000,
	})
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil", err)
	}

	if n := atomic.LoadInt64(&upsertCalls); n != 2 {
		t.Errorf("UpsertEntitlement called %d times, want 2", n)
	}
	if !reconciledUsers[userA] {
		t.Errorf("user %q was not reconciled", userA)
	}
	if !reconciledUsers[userB] {
		t.Errorf("user %q was not reconciled", userB)
	}
}

// TestWebhook_MultipleMatchingUsers_RC404_MarksAllInactive verifies that
// when RevenueCat returns 404 and multiple local users match, all of them
// get an inactive entitlement upsert.
func TestWebhook_MultipleMatchingUsers_RC404_MarksAllInactive(t *testing.T) {
	t.Parallel()

	const rcAnonymousID = "$RCAnonymousID:multi404"
	const userA = "aaaa3333-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const userB = "bbbb4444-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			t.Fatal("GetUserByID should not be called for non-UUID app_user_id")
			return repository.UserRow{}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, appUserID string) (*revenuecat.SubscriberResponse, error) {
			return nil, &revenuecat.APIError{StatusCode: 404, AppUserID: appUserID}
		},
	}

	reconciledUsers := make(map[string]bool)
	var upsertCalls int64
	entRepo := &fakeEntitlementRepo{
		getUsersByRevenueCatIDFn: func(_ context.Context, _ repository.DBTX, _, _ string) ([]repository.UserRow, error) {
			return []repository.UserRow{
				{ID: userA, Email: "a@example.com"},
				{ID: userB, Email: "b@example.com"},
			}, nil
		},
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertEntitlementParams) error {
			atomic.AddInt64(&upsertCalls, 1)
			reconciledUsers[params.UserID] = true
			if params.IsActive {
				t.Errorf("user %q IsActive = true, want false for RC 404", params.UserID)
			}
			return nil
		},
	}

	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "EXPIRATION",
		ID:               "evt_multi_404",
		AppUserID:        rcAnonymousID,
		EventTimestampMs: 1700000000000,
	})
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil", err)
	}

	if n := atomic.LoadInt64(&upsertCalls); n != 2 {
		t.Errorf("UpsertEntitlement called %d times, want 2", n)
	}
	if !reconciledUsers[userA] {
		t.Errorf("user %q was not reconciled", userA)
	}
	if !reconciledUsers[userB] {
		t.Errorf("user %q was not reconciled", userB)
	}
}

// TestWebhook_UUIDFastPath_MergesEntitlementLookup verifies that when the
// RevenueCat app_user_id is a UUID that matches a local user AND the
// entitlement-based lookup returns additional users, all of them are
// reconciled. This is a regression test: previously the UUID fast-path
// short-circuited and prevented the entitlement lookup from running.
func TestWebhook_UUIDFastPath_MergesEntitlementLookup(t *testing.T) {
	t.Parallel()

	const (
		directUID     = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" // matches uuidRE, found by GetUserByID
		entitlementID = "55555555-5555-5555-5555-555555555555" // only found via entitlement lookup
	)

	// Direct UUID lookup succeeds for directUID.
	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			if userID == directUID {
				return repository.UserRow{ID: directUID, Email: "direct@example.com"}, nil
			}
			return repository.UserRow{}, repository.ErrNotFound
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, _ string) (*revenuecat.SubscriberResponse, error) {
			return activeSubscriberResponse(directUID), nil
		},
	}

	reconciledUsers := make(map[string]bool)
	var upsertCalls int64
	entRepo := &fakeEntitlementRepo{
		getUsersByRevenueCatIDFn: func(_ context.Context, _ repository.DBTX, _, _ string) ([]repository.UserRow, error) {
			// Return the direct user (already known) plus an additional user.
			return []repository.UserRow{
				{ID: directUID, Email: "direct@example.com"},
				{ID: entitlementID, Email: "extra@example.com"},
			}, nil
		},
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertEntitlementParams) error {
			atomic.AddInt64(&upsertCalls, 1)
			reconciledUsers[params.UserID] = true
			return nil
		},
	}

	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "RENEWAL",
		ID:               "evt_uuid_merge_1",
		AppUserID:        directUID,
		EventTimestampMs: 1700000000000,
	})
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil", err)
	}

	// Both users must be reconciled: the UUID fast-path user AND the
	// entitlement-lookup user, deduplicated.
	if n := atomic.LoadInt64(&upsertCalls); n != 2 {
		t.Errorf("UpsertEntitlement called %d times, want 2 (direct + entitlement user)", n)
	}
	if !reconciledUsers[directUID] {
		t.Errorf("direct UUID user %q was not reconciled", directUID)
	}
	if !reconciledUsers[entitlementID] {
		t.Errorf("entitlement-only user %q was not reconciled", entitlementID)
	}
}

// ---------------------------------------------------------------------------
// Override guard tests (Chunk 5)
// ---------------------------------------------------------------------------

// fakeOverrideChecker implements overrideChecker.
type fakeOverrideChecker struct {
	getActiveOverrideFn func(ctx context.Context, db repository.DBTX, userID, entitlement string) (repository.OverrideRow, error)
}

var _ overrideChecker = (*fakeOverrideChecker)(nil)

func (f *fakeOverrideChecker) GetActiveOverride(ctx context.Context, db repository.DBTX, userID, entitlement string) (repository.OverrideRow, error) {
	if f.getActiveOverrideFn != nil {
		return f.getActiveOverrideFn(ctx, db, userID, entitlement)
	}
	return repository.OverrideRow{}, repository.ErrNotFound
}

// newTestWebhookServiceWithOverride is like newTestWebhookService but injects
// the override checker via the WithOverrideChecker option.
func newTestWebhookServiceWithOverride(
	entRepo *fakeEntitlementRepo,
	rcClient *fakeRCClient,
	userLookup *fakeWebhookUserLookup,
	oc overrideChecker,
) *WebhookService {
	return NewWebhookService(
		&fakePool{},
		entRepo,
		rcClient,
		userLookup,
		discardLogger(),
		WithOverrideChecker(oc),
	)
}

// TestWebhook_ActiveGrantOverride_SkipsReconciliation verifies that when an
// active admin grant override exists for a user, the webhook reconciliation
// does NOT upsert the entitlement for that user.
func TestWebhook_ActiveGrantOverride_SkipsReconciliation(t *testing.T) {
	t.Parallel()

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			return repository.UserRow{ID: userID, Email: "alice@example.com"}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, _ string) (*revenuecat.SubscriberResponse, error) {
			return activeSubscriberResponse(validUUID), nil
		},
	}

	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, _ repository.UpsertEntitlementParams) error {
			t.Fatal("UpsertEntitlement must not be called when active grant override exists")
			return nil
		},
	}

	oc := &fakeOverrideChecker{
		getActiveOverrideFn: func(_ context.Context, _ repository.DBTX, userID, entitlement string) (repository.OverrideRow, error) {
			return repository.OverrideRow{
				UserID:      userID,
				Entitlement: entitlement,
				Action:      "grant",
			}, nil
		},
	}

	svc := newTestWebhookServiceWithOverride(entRepo, rcClient, userLookup, oc)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "EXPIRATION",
		ID:               "evt_exp_override_1",
		AppUserID:        validUUID,
		EventTimestampMs: 1700000000000,
	})
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil (skipped due to override)", err)
	}
}

// TestWebhook_ActiveRevokeOverride_SkipsReconciliation verifies that when an
// active admin revoke override exists, the webhook does NOT upsert.
func TestWebhook_ActiveRevokeOverride_SkipsReconciliation(t *testing.T) {
	t.Parallel()

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			return repository.UserRow{ID: userID, Email: "alice@example.com"}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, _ string) (*revenuecat.SubscriberResponse, error) {
			return activeSubscriberResponse(validUUID), nil
		},
	}

	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, _ repository.UpsertEntitlementParams) error {
			t.Fatal("UpsertEntitlement must not be called when active revoke override exists")
			return nil
		},
	}

	oc := &fakeOverrideChecker{
		getActiveOverrideFn: func(_ context.Context, _ repository.DBTX, userID, entitlement string) (repository.OverrideRow, error) {
			return repository.OverrideRow{
				UserID:      userID,
				Entitlement: entitlement,
				Action:      "revoke",
			}, nil
		},
	}

	svc := newTestWebhookServiceWithOverride(entRepo, rcClient, userLookup, oc)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "RENEWAL",
		ID:               "evt_renewal_override_1",
		AppUserID:        validUUID,
		EventTimestampMs: 1700000000000,
	})
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil (skipped due to override)", err)
	}
}

// TestWebhook_NoOverride_ReconcilesNormally verifies that when the override
// checker is configured but returns ErrNotFound, reconciliation proceeds.
func TestWebhook_NoOverride_ReconcilesNormally(t *testing.T) {
	t.Parallel()

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			return repository.UserRow{ID: userID, Email: "alice@example.com"}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, _ string) (*revenuecat.SubscriberResponse, error) {
			return activeSubscriberResponse(validUUID), nil
		},
	}

	var upserted bool
	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertEntitlementParams) error {
			upserted = true
			return nil
		},
	}

	oc := &fakeOverrideChecker{
		getActiveOverrideFn: func(_ context.Context, _ repository.DBTX, _, _ string) (repository.OverrideRow, error) {
			return repository.OverrideRow{}, repository.ErrNotFound
		},
	}

	svc := newTestWebhookServiceWithOverride(entRepo, rcClient, userLookup, oc)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "RENEWAL",
		ID:               "evt_renewal_no_override",
		AppUserID:        validUUID,
		EventTimestampMs: 1700000000000,
	})
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil", err)
	}
	if !upserted {
		t.Error("UpsertEntitlement was not called — expected normal reconciliation when no override exists")
	}
}

// TestWebhook_NilOverrideChecker_ReconcilesNormally verifies backward
// compatibility: when no override checker is set, reconciliation always proceeds.
func TestWebhook_NilOverrideChecker_ReconcilesNormally(t *testing.T) {
	t.Parallel()

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			return repository.UserRow{ID: userID, Email: "alice@example.com"}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, _ string) (*revenuecat.SubscriberResponse, error) {
			return activeSubscriberResponse(validUUID), nil
		},
	}

	var upserted bool
	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, _ repository.UpsertEntitlementParams) error {
			upserted = true
			return nil
		},
	}

	// No WithOverrideChecker option — nil overrides field.
	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "RENEWAL",
		ID:               "evt_renewal_nil_checker",
		AppUserID:        validUUID,
		EventTimestampMs: 1700000000000,
	})
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil", err)
	}
	if !upserted {
		t.Error("UpsertEntitlement was not called — expected reconciliation without override checker")
	}
}

// TestWebhook_OverrideCheckError_ReturnsError verifies that when the override
// checker returns a transient error (not ErrNotFound), reconciliation fails
// rather than silently proceeding and potentially overwriting an admin override.
func TestWebhook_OverrideCheckError_ReturnsError(t *testing.T) {
	t.Parallel()

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			return repository.UserRow{ID: userID, Email: "alice@example.com"}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, _ string) (*revenuecat.SubscriberResponse, error) {
			return activeSubscriberResponse(validUUID), nil
		},
	}

	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, _ repository.UpsertEntitlementParams) error {
			t.Fatal("UpsertEntitlement must not be called when override check fails")
			return nil
		},
	}

	overrideErr := errors.New("redis timeout")
	oc := &fakeOverrideChecker{
		getActiveOverrideFn: func(_ context.Context, _ repository.DBTX, _, _ string) (repository.OverrideRow, error) {
			return repository.OverrideRow{}, overrideErr
		},
	}

	svc := newTestWebhookServiceWithOverride(entRepo, rcClient, userLookup, oc)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "RENEWAL",
		ID:               "evt_renewal_override_err",
		AppUserID:        validUUID,
		EventTimestampMs: 1700000000000,
	})
	if err == nil {
		t.Fatal("HandleWebhook() = nil, want error when override check fails")
	}
	if !errors.Is(err, overrideErr) {
		t.Errorf("error chain does not contain override error: %v", err)
	}
}

// TestWebhook_RC404_ActiveOverride_SkipsInactiveUpsert verifies that when
// RevenueCat returns 404 but an active override exists, the service does NOT
// upsert an inactive entitlement for the overridden user.
func TestWebhook_RC404_ActiveOverride_SkipsInactiveUpsert(t *testing.T) {
	t.Parallel()

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			return repository.UserRow{ID: userID, Email: "alice@example.com"}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, appUserID string) (*revenuecat.SubscriberResponse, error) {
			return nil, &revenuecat.APIError{StatusCode: 404, AppUserID: appUserID}
		},
	}

	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, _ repository.UpsertEntitlementParams) error {
			t.Fatal("UpsertEntitlement must not be called for overridden user on RC 404")
			return nil
		},
	}

	oc := &fakeOverrideChecker{
		getActiveOverrideFn: func(_ context.Context, _ repository.DBTX, _, _ string) (repository.OverrideRow, error) {
			return repository.OverrideRow{Action: "grant"}, nil
		},
	}

	svc := newTestWebhookServiceWithOverride(entRepo, rcClient, userLookup, oc)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "EXPIRATION",
		ID:               "evt_exp_override_404",
		AppUserID:        validUUID,
		EventTimestampMs: 1700000000000,
	})
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil (overridden user skipped on RC 404)", err)
	}
}

// TestWebhook_RC404_OverrideCheckError_ReturnsError verifies that when
// RevenueCat returns 404 and the override checker returns a transient error,
// the error is propagated.
func TestWebhook_RC404_OverrideCheckError_ReturnsError(t *testing.T) {
	t.Parallel()

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			return repository.UserRow{ID: userID, Email: "alice@example.com"}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, appUserID string) (*revenuecat.SubscriberResponse, error) {
			return nil, &revenuecat.APIError{StatusCode: 404, AppUserID: appUserID}
		},
	}

	entRepo := &fakeEntitlementRepo{
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, _ repository.UpsertEntitlementParams) error {
			t.Fatal("UpsertEntitlement must not be called when override check fails")
			return nil
		},
	}

	overrideErr := errors.New("db timeout on override check")
	oc := &fakeOverrideChecker{
		getActiveOverrideFn: func(context.Context, repository.DBTX, string, string) (repository.OverrideRow, error) {
			return repository.OverrideRow{}, overrideErr
		},
	}

	svc := newTestWebhookServiceWithOverride(entRepo, rcClient, userLookup, oc)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "EXPIRATION",
		ID:               "evt_exp_override_err",
		AppUserID:        validUUID,
		EventTimestampMs: 1700000000000,
	})
	if err == nil {
		t.Fatal("HandleWebhook() = nil, want error when override check fails on RC 404")
	}
	if !errors.Is(err, overrideErr) {
		t.Errorf("error chain does not contain override error: %v", err)
	}
}

// TestWebhook_MultipleUsers_PartialOverride verifies that when one user has an
// active override and another does not, only the non-overridden user is
// reconciled.
func TestWebhook_MultipleUsers_PartialOverride(t *testing.T) {
	t.Parallel()

	const rcAnonymousID = "$RCAnonymousID:partial_override"
	const overriddenUID = "aaaa1111-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const normalUID = "bbbb2222-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(context.Context, repository.DBTX, string) (repository.UserRow, error) {
			t.Fatal("GetUserByID should not be called for non-UUID app_user_id")
			return repository.UserRow{}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(context.Context, string) (*revenuecat.SubscriberResponse, error) {
			return activeSubscriberResponse(rcAnonymousID), nil
		},
	}

	reconciledUsers := make(map[string]bool)
	entRepo := &fakeEntitlementRepo{
		getUsersByRevenueCatIDFn: func(_ context.Context, _ repository.DBTX, _, _ string) ([]repository.UserRow, error) {
			return []repository.UserRow{
				{ID: overriddenUID, Email: "overridden@example.com"},
				{ID: normalUID, Email: "normal@example.com"},
			}, nil
		},
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertEntitlementParams) error {
			reconciledUsers[params.UserID] = true
			return nil
		},
	}

	oc := &fakeOverrideChecker{
		getActiveOverrideFn: func(_ context.Context, _ repository.DBTX, userID, _ string) (repository.OverrideRow, error) {
			if userID == overriddenUID {
				return repository.OverrideRow{Action: "grant"}, nil
			}
			return repository.OverrideRow{}, repository.ErrNotFound
		},
	}

	svc := newTestWebhookServiceWithOverride(entRepo, rcClient, userLookup, oc)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "RENEWAL",
		ID:               "evt_partial_override",
		AppUserID:        rcAnonymousID,
		EventTimestampMs: 1700000000000,
	})
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil", err)
	}

	if reconciledUsers[overriddenUID] {
		t.Errorf("overridden user %q was reconciled — should have been skipped", overriddenUID)
	}
	if !reconciledUsers[normalUID] {
		t.Errorf("normal user %q was not reconciled — should have been", normalUID)
	}
}

// TestWebhook_UUIDFastPath_DeduplicatesWithEntitlement verifies that when the
// UUID fast-path user is also returned by the entitlement lookup, it is only
// reconciled once (deduplicated).
func TestWebhook_UUIDFastPath_DeduplicatesWithEntitlement(t *testing.T) {
	t.Parallel()

	const uid = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	userLookup := &fakeWebhookUserLookup{
		getUserByIDFn: func(_ context.Context, _ repository.DBTX, userID string) (repository.UserRow, error) {
			return repository.UserRow{ID: uid, Email: "alice@example.com"}, nil
		},
	}

	rcClient := &fakeRCClient{
		getSubscriberFn: func(_ context.Context, _ string) (*revenuecat.SubscriberResponse, error) {
			return activeSubscriberResponse(uid), nil
		},
	}

	var upsertCalls int64
	entRepo := &fakeEntitlementRepo{
		getUsersByRevenueCatIDFn: func(_ context.Context, _ repository.DBTX, _, _ string) ([]repository.UserRow, error) {
			// Entitlement lookup also returns the same user.
			return []repository.UserRow{
				{ID: uid, Email: "alice@example.com"},
			}, nil
		},
		upsertEntitlementFn: func(_ context.Context, _ repository.DBTX, params repository.UpsertEntitlementParams) error {
			atomic.AddInt64(&upsertCalls, 1)
			return nil
		},
	}

	svc := newTestWebhookService(entRepo, rcClient, userLookup)

	err := svc.HandleWebhook(context.Background(), revenuecat.WebhookEvent{
		Type:             "RENEWAL",
		ID:               "evt_uuid_dedup_1",
		AppUserID:        uid,
		EventTimestampMs: 1700000000000,
	})
	if err != nil {
		t.Fatalf("HandleWebhook() = %v, want nil", err)
	}

	// UUID fast-path and entitlement lookup return the same user — only one upsert.
	if n := atomic.LoadInt64(&upsertCalls); n != 1 {
		t.Errorf("UpsertEntitlement called %d times, want 1 (deduplicated)", n)
	}
}
