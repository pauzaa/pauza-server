package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/push"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

type fakeSocialRepo struct {
	effectivePremiumActiveFn    func(ctx context.Context, db repository.DBTX, userID string) (bool, error)
	registerDeviceFn            func(ctx context.Context, db repository.DBTX, userID, fcmToken string, platform repository.DevicePlatform) error
	unregisterDeviceFn          func(ctx context.Context, db repository.DBTX, userID, fcmToken string) error
	findUserByExactUsernameFn   func(ctx context.Context, db repository.DBTX, username string) (repository.BasicUserRow, error)
	getBasicUserByIDFn          func(ctx context.Context, db repository.DBTX, userID string) (repository.BasicUserRow, error)
	createFriendRequestFn       func(ctx context.Context, db repository.DBTX, requesterID, addresseeID string) (string, error)
	listFriendsFn               func(ctx context.Context, db repository.DBTX, userID string, page, limit int) ([]repository.FriendRow, repository.PaginationResult, error)
	listFriendRequestsFn        func(ctx context.Context, db repository.DBTX, userID string, direction repository.FriendRequestDirection) ([]repository.FriendRequestRow, error)
	getFriendshipFn             func(ctx context.Context, db repository.DBTX, friendshipID string) (string, string, repository.FriendshipStatus, error)
	acceptFriendRequestFn       func(ctx context.Context, db repository.DBTX, friendshipID, userID string) error
	deletePendingRequestFn      func(ctx context.Context, db repository.DBTX, friendshipID, userID string) error
	removeFriendFn              func(ctx context.Context, db repository.DBTX, friendshipID, userID string) error
	searchUsersFn               func(ctx context.Context, db repository.DBTX, prefix, excludeUserID string, limit int) ([]repository.BasicUserRow, error)
	loadRecentDailyAggregatesFn func(ctx context.Context, db repository.DBTX, userID string, days int) ([]struct {
		LocalDay    string
		EffectiveMS int
		Qualified   bool
	}, error)
	loadTotalFocusTimeFn     func(ctx context.Context, db repository.DBTX, userID string) (int64, error)
	listLeaderboardEntriesFn func(ctx context.Context, db repository.DBTX, metric repository.LeaderboardMetric, page, limit int) ([]repository.LeaderboardRow, int, error)
	getLeaderboardRankFn     func(ctx context.Context, db repository.DBTX, metric repository.LeaderboardMetric, userID string) (repository.LeaderboardRow, error)
}

func (f *fakeSocialRepo) EffectivePremiumActive(ctx context.Context, db repository.DBTX, userID string) (bool, error) {
	return f.effectivePremiumActiveFn(ctx, db, userID)
}
func (f *fakeSocialRepo) RegisterDevice(ctx context.Context, db repository.DBTX, userID, fcmToken string, platform repository.DevicePlatform) error {
	return f.registerDeviceFn(ctx, db, userID, fcmToken, platform)
}
func (f *fakeSocialRepo) UnregisterDevice(ctx context.Context, db repository.DBTX, userID, fcmToken string) error {
	return f.unregisterDeviceFn(ctx, db, userID, fcmToken)
}
func (f *fakeSocialRepo) FindUserByExactUsername(ctx context.Context, db repository.DBTX, username string) (repository.BasicUserRow, error) {
	return f.findUserByExactUsernameFn(ctx, db, username)
}
func (f *fakeSocialRepo) GetBasicUserByID(ctx context.Context, db repository.DBTX, userID string) (repository.BasicUserRow, error) {
	return f.getBasicUserByIDFn(ctx, db, userID)
}
func (f *fakeSocialRepo) CreateFriendRequest(ctx context.Context, db repository.DBTX, requesterID, addresseeID string) (string, error) {
	return f.createFriendRequestFn(ctx, db, requesterID, addresseeID)
}
func (f *fakeSocialRepo) ListFriends(ctx context.Context, db repository.DBTX, userID string, page, limit int) ([]repository.FriendRow, repository.PaginationResult, error) {
	return f.listFriendsFn(ctx, db, userID, page, limit)
}
func (f *fakeSocialRepo) ListFriendRequests(ctx context.Context, db repository.DBTX, userID string, direction repository.FriendRequestDirection) ([]repository.FriendRequestRow, error) {
	return f.listFriendRequestsFn(ctx, db, userID, direction)
}
func (f *fakeSocialRepo) GetFriendship(ctx context.Context, db repository.DBTX, friendshipID string) (string, string, repository.FriendshipStatus, error) {
	return f.getFriendshipFn(ctx, db, friendshipID)
}
func (f *fakeSocialRepo) AcceptFriendRequest(ctx context.Context, db repository.DBTX, friendshipID, userID string) error {
	return f.acceptFriendRequestFn(ctx, db, friendshipID, userID)
}
func (f *fakeSocialRepo) DeletePendingRequest(ctx context.Context, db repository.DBTX, friendshipID, userID string) error {
	return f.deletePendingRequestFn(ctx, db, friendshipID, userID)
}
func (f *fakeSocialRepo) RemoveFriend(ctx context.Context, db repository.DBTX, friendshipID, userID string) error {
	return f.removeFriendFn(ctx, db, friendshipID, userID)
}
func (f *fakeSocialRepo) SearchUsers(ctx context.Context, db repository.DBTX, prefix, excludeUserID string, limit int) ([]repository.BasicUserRow, error) {
	return f.searchUsersFn(ctx, db, prefix, excludeUserID, limit)
}
func (f *fakeSocialRepo) LoadRecentDailyAggregates(ctx context.Context, db repository.DBTX, userID string, days int) ([]struct {
	LocalDay    string
	EffectiveMS int
	Qualified   bool
}, error) {
	return f.loadRecentDailyAggregatesFn(ctx, db, userID, days)
}
func (f *fakeSocialRepo) LoadTotalFocusTime(ctx context.Context, db repository.DBTX, userID string) (int64, error) {
	return f.loadTotalFocusTimeFn(ctx, db, userID)
}
func (f *fakeSocialRepo) ListLeaderboardEntries(ctx context.Context, db repository.DBTX, metric repository.LeaderboardMetric, page, limit int) ([]repository.LeaderboardRow, int, error) {
	return f.listLeaderboardEntriesFn(ctx, db, metric, page, limit)
}
func (f *fakeSocialRepo) GetLeaderboardRank(ctx context.Context, db repository.DBTX, metric repository.LeaderboardMetric, userID string) (repository.LeaderboardRow, error) {
	return f.getLeaderboardRankFn(ctx, db, metric, userID)
}

type capturePushSender struct {
	calls []struct {
		userID       string
		notification push.Notification
	}
	sendErr error
}

func (c *capturePushSender) Send(_ context.Context, userID string, notification push.Notification) error {
	c.calls = append(c.calls, struct {
		userID       string
		notification push.Notification
	}{userID: userID, notification: notification})
	return c.sendErr
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newFakeSocialRepo() *fakeSocialRepo {
	return &fakeSocialRepo{
		effectivePremiumActiveFn: func(context.Context, repository.DBTX, string) (bool, error) { return true, nil },
		registerDeviceFn:         func(context.Context, repository.DBTX, string, string, repository.DevicePlatform) error { return nil },
		unregisterDeviceFn:       func(context.Context, repository.DBTX, string, string) error { return nil },
		findUserByExactUsernameFn: func(context.Context, repository.DBTX, string) (repository.BasicUserRow, error) {
			return repository.BasicUserRow{}, nil
		},
		getBasicUserByIDFn: func(context.Context, repository.DBTX, string) (repository.BasicUserRow, error) {
			return repository.BasicUserRow{}, nil
		},
		createFriendRequestFn: func(context.Context, repository.DBTX, string, string) (string, error) { return "", nil },
		listFriendsFn: func(context.Context, repository.DBTX, string, int, int) ([]repository.FriendRow, repository.PaginationResult, error) {
			return nil, repository.PaginationResult{}, nil
		},
		listFriendRequestsFn: func(context.Context, repository.DBTX, string, repository.FriendRequestDirection) ([]repository.FriendRequestRow, error) {
			return nil, nil
		},
		getFriendshipFn: func(context.Context, repository.DBTX, string) (string, string, repository.FriendshipStatus, error) {
			return "", "", repository.FriendshipStatusUnknown, nil
		},
		acceptFriendRequestFn: func(context.Context, repository.DBTX, string, string) error { return nil },
		deletePendingRequestFn: func(context.Context, repository.DBTX, string, string) error {
			return nil
		},
		removeFriendFn: func(context.Context, repository.DBTX, string, string) error { return nil },
		searchUsersFn: func(context.Context, repository.DBTX, string, string, int) ([]repository.BasicUserRow, error) {
			return nil, nil
		},
		loadRecentDailyAggregatesFn: func(context.Context, repository.DBTX, string, int) ([]struct {
			LocalDay    string
			EffectiveMS int
			Qualified   bool
		}, error) {
			return nil, nil
		},
		loadTotalFocusTimeFn: func(context.Context, repository.DBTX, string) (int64, error) { return 0, nil },
		listLeaderboardEntriesFn: func(context.Context, repository.DBTX, repository.LeaderboardMetric, int, int) ([]repository.LeaderboardRow, int, error) {
			return nil, 0, nil
		},
		getLeaderboardRankFn: func(context.Context, repository.DBTX, repository.LeaderboardMetric, string) (repository.LeaderboardRow, error) {
			return repository.LeaderboardRow{}, nil
		},
	}
}

func TestSocialService_RequestFriendSendsNotificationMetadata(t *testing.T) {
	repo := newFakeSocialRepo()
	repo.findUserByExactUsernameFn = func(_ context.Context, _ repository.DBTX, username string) (repository.BasicUserRow, error) {
		return repository.BasicUserRow{ID: "target-1", Username: username}, nil
	}
	repo.createFriendRequestFn = func(_ context.Context, _ repository.DBTX, requesterID, addresseeID string) (string, error) {
		if requesterID != "actor-1" || addresseeID != "target-1" {
			t.Fatalf("unexpected friend request ids: requester=%q addressee=%q", requesterID, addresseeID)
		}
		return "friendship-1", nil
	}
	repo.getBasicUserByIDFn = func(_ context.Context, _ repository.DBTX, userID string) (repository.BasicUserRow, error) {
		if userID != "actor-1" {
			t.Fatalf("actor lookup userID = %q, want actor-1", userID)
		}
		return repository.BasicUserRow{ID: "actor-1", Username: "alice"}, nil
	}

	pushSender := &capturePushSender{}
	svc := NewSocialService(&fakePool{}, repo, pushSender, silentLogger())

	out, err := svc.RequestFriend(context.Background(), FriendRequestInput{UserID: "actor-1", Username: "bob"})
	if err != nil {
		t.Fatalf("RequestFriend error = %v, want nil", err)
	}
	if out.FriendshipID != "friendship-1" || out.Status != repository.FriendshipStatusPending {
		t.Fatalf("unexpected output: %#v", out)
	}
	if len(pushSender.calls) != 1 {
		t.Fatalf("push calls = %d, want 1", len(pushSender.calls))
	}

	got := pushSender.calls[0]
	if got.notification.FriendMetadata == nil {
		t.Fatal("expected friend metadata")
	}
	if got.userID != "target-1" {
		t.Fatalf("push recipient = %q, want target-1", got.userID)
	}
	if got.notification.Type != "friend_request" {
		t.Fatalf("notification type = %q, want friend_request", got.notification.Type)
	}
	if got.notification.Title != "New friend request" {
		t.Fatalf("notification title = %q, want %q", got.notification.Title, "New friend request")
	}
	if got.notification.Body != "alice sent you a friend request" {
		t.Fatalf("notification body = %q", got.notification.Body)
	}
	if got.notification.FriendMetadata.FriendshipID != "friendship-1" {
		t.Fatalf("friendship_id = %q, want friendship-1", got.notification.FriendMetadata.FriendshipID)
	}
	if got.notification.FriendMetadata.ActorUserID != "actor-1" {
		t.Fatalf("actor_user_id = %q, want actor-1", got.notification.FriendMetadata.ActorUserID)
	}
	if got.notification.FriendMetadata.ActorUsername != "alice" {
		t.Fatalf("actor_username = %q, want alice", got.notification.FriendMetadata.ActorUsername)
	}
}

func TestSocialService_AcceptFriendSendsNotificationMetadata(t *testing.T) {
	repo := newFakeSocialRepo()
	repo.getFriendshipFn = func(_ context.Context, _ repository.DBTX, friendshipID string) (string, string, repository.FriendshipStatus, error) {
		if friendshipID != "friendship-1" {
			t.Fatalf("friendshipID = %q, want friendship-1", friendshipID)
		}
		return "requester-1", "actor-1", repository.FriendshipStatusPending, nil
	}
	repo.acceptFriendRequestFn = func(_ context.Context, _ repository.DBTX, friendshipID, userID string) error {
		if friendshipID != "friendship-1" || userID != "actor-1" {
			t.Fatalf("unexpected accept args: friendshipID=%q userID=%q", friendshipID, userID)
		}
		return nil
	}
	repo.getBasicUserByIDFn = func(_ context.Context, _ repository.DBTX, userID string) (repository.BasicUserRow, error) {
		if userID != "actor-1" {
			t.Fatalf("actor lookup userID = %q, want actor-1", userID)
		}
		return repository.BasicUserRow{ID: "actor-1", Username: "alice"}, nil
	}

	pushSender := &capturePushSender{}
	svc := NewSocialService(&fakePool{}, repo, pushSender, silentLogger())

	out, err := svc.AcceptFriend(context.Background(), "actor-1", "friendship-1")
	if err != nil {
		t.Fatalf("AcceptFriend error = %v, want nil", err)
	}
	if out.FriendshipID != "friendship-1" || out.Status != repository.FriendshipStatusAccepted {
		t.Fatalf("unexpected output: %#v", out)
	}
	if len(pushSender.calls) != 1 {
		t.Fatalf("push calls = %d, want 1", len(pushSender.calls))
	}

	got := pushSender.calls[0]
	if got.notification.FriendMetadata == nil {
		t.Fatal("expected friend metadata")
	}
	if got.userID != "requester-1" {
		t.Fatalf("push recipient = %q, want requester-1", got.userID)
	}
	if got.notification.Type != "friend_accepted" {
		t.Fatalf("notification type = %q, want friend_accepted", got.notification.Type)
	}
	if got.notification.Title != "Friend request accepted" {
		t.Fatalf("notification title = %q, want %q", got.notification.Title, "Friend request accepted")
	}
	if got.notification.Body != "alice accepted your friend request" {
		t.Fatalf("notification body = %q", got.notification.Body)
	}
	if got.notification.FriendMetadata.FriendshipID != "friendship-1" {
		t.Fatalf("friendship_id = %q, want friendship-1", got.notification.FriendMetadata.FriendshipID)
	}
}

func TestSocialService_RequestFriendPushFailureDoesNotFailRequest(t *testing.T) {
	repo := newFakeSocialRepo()
	repo.findUserByExactUsernameFn = func(_ context.Context, _ repository.DBTX, username string) (repository.BasicUserRow, error) {
		return repository.BasicUserRow{ID: "target-1", Username: username}, nil
	}
	repo.createFriendRequestFn = func(_ context.Context, _ repository.DBTX, _, _ string) (string, error) {
		return "friendship-1", nil
	}
	repo.getBasicUserByIDFn = func(_ context.Context, _ repository.DBTX, _ string) (repository.BasicUserRow, error) {
		return repository.BasicUserRow{ID: "actor-1", Username: "alice"}, nil
	}

	pushSender := &capturePushSender{sendErr: errors.New("fcm unavailable")}
	svc := NewSocialService(&fakePool{}, repo, pushSender, silentLogger())

	out, err := svc.RequestFriend(context.Background(), FriendRequestInput{UserID: "actor-1", Username: "bob"})
	if err != nil {
		t.Fatalf("RequestFriend error = %v, want nil", err)
	}
	if out.FriendshipID != "friendship-1" {
		t.Fatalf("friendship_id = %q, want friendship-1", out.FriendshipID)
	}
	if len(pushSender.calls) != 1 {
		t.Fatalf("push calls = %d, want 1", len(pushSender.calls))
	}
}

func TestSocialService_LeaderboardByStreak_UsesRepositoryRanking(t *testing.T) {
	repo := newFakeSocialRepo()
	repo.listLeaderboardEntriesFn = func(_ context.Context, _ repository.DBTX, metric repository.LeaderboardMetric, page, limit int) ([]repository.LeaderboardRow, int, error) {
		if metric != repository.LeaderboardMetricStreak {
			t.Fatalf("metric = %q, want %q", metric, repository.LeaderboardMetricStreak)
		}
		if page != 2 || limit != 5 {
			t.Fatalf("page=%d limit=%d, want 2/5", page, limit)
		}
		return []repository.LeaderboardRow{
			{
				Rank:              4,
				User:              repository.BasicUserRow{ID: "user-4", Username: "dave", LeaderboardVisible: true},
				CurrentStreakDays: 7,
				TotalFocusTimeMS:  900,
			},
		}, 3, nil
	}
	repo.getLeaderboardRankFn = func(_ context.Context, _ repository.DBTX, metric repository.LeaderboardMetric, userID string) (repository.LeaderboardRow, error) {
		if metric != repository.LeaderboardMetricStreak {
			t.Fatalf("metric = %q, want %q", metric, repository.LeaderboardMetricStreak)
		}
		if userID != "me" {
			t.Fatalf("userID = %q, want me", userID)
		}
		return repository.LeaderboardRow{
			Rank:              8,
			User:              repository.BasicUserRow{ID: "me", Username: "alice", LeaderboardVisible: false},
			CurrentStreakDays: 3,
			TotalFocusTimeMS:  400,
		}, nil
	}

	svc := NewSocialService(&fakePool{}, repo, &capturePushSender{}, silentLogger())

	out, err := svc.LeaderboardByStreak(context.Background(), "me", 2, 5)
	if err != nil {
		t.Fatalf("LeaderboardByStreak error = %v, want nil", err)
	}
	if out.Pagination.Page != 2 || out.Pagination.Limit != 5 || out.Pagination.Total != 3 {
		t.Fatalf("unexpected pagination: %#v", out.Pagination)
	}
	if out.MyRank.Rank != 8 || out.MyRank.CurrentStreakDays != 3 || out.MyRank.TotalFocusTimeMS != 400 {
		t.Fatalf("unexpected my_rank: %#v", out.MyRank)
	}
	if len(out.Entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(out.Entries))
	}
	if out.Entries[0].Rank != 4 || out.Entries[0].User.Username != "dave" || out.Entries[0].CurrentStreakDays != 7 {
		t.Fatalf("unexpected entry: %#v", out.Entries[0])
	}
}

func TestSocialService_LeaderboardByFocusTime_UserNotFound(t *testing.T) {
	repo := newFakeSocialRepo()
	repo.getLeaderboardRankFn = func(context.Context, repository.DBTX, repository.LeaderboardMetric, string) (repository.LeaderboardRow, error) {
		return repository.LeaderboardRow{}, repository.ErrNotFound
	}

	svc := NewSocialService(&fakePool{}, repo, &capturePushSender{}, silentLogger())

	_, err := svc.LeaderboardByFocusTime(context.Background(), "missing", 1, 20)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("LeaderboardByFocusTime error = %v, want ErrUnauthorized", err)
	}
}
