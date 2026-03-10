package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/IsorilovA/pauza-server/internal/push"
	"github.com/IsorilovA/pauza-server/internal/repository"
)

type SocialService struct {
	pool   repository.Pool
	repo   *repository.SocialRepository
	push   push.Sender
	logger *slog.Logger
}

func NewSocialService(pool repository.Pool, repo *repository.SocialRepository, pushSender push.Sender, logger *slog.Logger) *SocialService {
	return &SocialService{pool: pool, repo: repo, push: pushSender, logger: logger}
}

type DeviceInput struct {
	UserID   string
	FCMToken string
	Platform string
}

type FriendRequestInput struct {
	UserID   string
	Username string
}

type FriendMutationOutput struct {
	FriendshipID string
	Status       string
}

type FriendListOutput struct {
	Friends    []repository.FriendRow
	Pagination repository.PaginationResult
}

type FriendRequestsOutput struct {
	Requests []repository.FriendRequestRow
}

type FriendStatsOutput struct {
	User  repository.BasicUserRow
	Stats struct {
		CurrentStreakDays int   `json:"current_streak_days"`
		LongestStreakDays int   `json:"longest_streak_days"`
		TotalFocusTimeMS  int64 `json:"total_focus_time_ms"`
		DailyTrends       []struct {
			LocalDay     string `json:"local_day"`
			EffectiveMS  int    `json:"effective_ms"`
			Qualified    bool   `json:"qualified"`
			SessionCount int    `json:"session_count"`
		} `json:"daily_trends"`
	}
}

type LeaderboardEntry struct {
	Rank              int                     `json:"rank"`
	User              repository.BasicUserRow `json:"user"`
	CurrentStreakDays int                     `json:"current_streak_days,omitempty"`
	TotalFocusTimeMS  int64                   `json:"total_focus_time_ms,omitempty"`
}

type LeaderboardRank struct {
	Rank              int   `json:"rank"`
	CurrentStreakDays int   `json:"current_streak_days,omitempty"`
	TotalFocusTimeMS  int64 `json:"total_focus_time_ms,omitempty"`
}

type LeaderboardOutput struct {
	Entries    []LeaderboardEntry          `json:"entries"`
	MyRank     LeaderboardRank             `json:"my_rank"`
	Pagination repository.PaginationResult `json:"pagination"`
}

func (s *SocialService) RegisterDevice(ctx context.Context, in DeviceInput) (MessageOutput, error) {
	if err := s.repo.RegisterDevice(ctx, s.pool, in.UserID, in.FCMToken, in.Platform); err != nil {
		s.logger.Error("registering device", "err", err)
		return MessageOutput{}, ErrInternal
	}
	return MessageOutput{Message: "Device registered"}, nil
}

func (s *SocialService) UnregisterDevice(ctx context.Context, in DeviceInput) (MessageOutput, error) {
	if err := s.repo.UnregisterDevice(ctx, s.pool, in.UserID, in.FCMToken); err != nil {
		s.logger.Error("unregistering device", "err", err)
		return MessageOutput{}, ErrInternal
	}
	return MessageOutput{Message: "Device unregistered"}, nil
}

func (s *SocialService) RequestFriend(ctx context.Context, in FriendRequestInput) (FriendMutationOutput, error) {
	if err := s.requirePremium(ctx, in.UserID); err != nil {
		return FriendMutationOutput{}, err
	}
	target, err := s.repo.FindUserByExactUsername(ctx, s.pool, in.Username)
	if errors.Is(err, repository.ErrNotFound) {
		return FriendMutationOutput{}, fmt.Errorf("%w: user not found", ErrUnauthorized)
	}
	if err != nil {
		s.logger.Error("loading friend target", "err", err)
		return FriendMutationOutput{}, ErrInternal
	}
	if target.ID == in.UserID {
		return FriendMutationOutput{}, fmt.Errorf("%w: cannot add yourself", ErrConflict)
	}

	id, err := s.repo.CreateFriendRequest(ctx, s.pool, in.UserID, target.ID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
			return FriendMutationOutput{}, fmt.Errorf("%w: request already exists", ErrConflict)
		}
		s.logger.Error("creating friend request", "err", err)
		return FriendMutationOutput{}, ErrInternal
	}

	_ = s.push.Send(ctx, target.ID, map[string]string{
		"type":          "friend_request",
		"friendship_id": id,
	})
	return FriendMutationOutput{FriendshipID: id, Status: "pending"}, nil
}

func (s *SocialService) ListFriends(ctx context.Context, userID string, page, limit int) (FriendListOutput, error) {
	if err := s.requirePremium(ctx, userID); err != nil {
		return FriendListOutput{}, err
	}
	items, pagination, err := s.repo.ListFriends(ctx, s.pool, userID, page, limit)
	if err != nil {
		s.logger.Error("listing friends", "err", err)
		return FriendListOutput{}, ErrInternal
	}
	return FriendListOutput{Friends: items, Pagination: pagination}, nil
}

func (s *SocialService) ListFriendRequests(ctx context.Context, userID, direction string) (FriendRequestsOutput, error) {
	if err := s.requirePremium(ctx, userID); err != nil {
		return FriendRequestsOutput{}, err
	}
	items, err := s.repo.ListFriendRequests(ctx, s.pool, userID, direction)
	if err != nil {
		s.logger.Error("listing friend requests", "direction", direction, "err", err)
		return FriendRequestsOutput{}, ErrInternal
	}
	return FriendRequestsOutput{Requests: items}, nil
}

func (s *SocialService) AcceptFriend(ctx context.Context, userID, friendshipID string) (FriendMutationOutput, error) {
	if err := s.requirePremium(ctx, userID); err != nil {
		return FriendMutationOutput{}, err
	}
	requesterID, _, _, err := s.repo.GetFriendship(ctx, s.pool, friendshipID)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		s.logger.Error("loading friendship before accept", "err", err)
		return FriendMutationOutput{}, ErrInternal
	}
	if err := s.repo.AcceptFriendRequest(ctx, s.pool, friendshipID, userID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return FriendMutationOutput{}, fmt.Errorf("%w: request not found", ErrUnauthorized)
		}
		s.logger.Error("accepting friendship", "err", err)
		return FriendMutationOutput{}, ErrInternal
	}
	_ = s.push.Send(ctx, requesterID, map[string]string{
		"type":          "friend_accepted",
		"friendship_id": friendshipID,
	})
	return FriendMutationOutput{FriendshipID: friendshipID, Status: "accepted"}, nil
}

func (s *SocialService) DeclineFriend(ctx context.Context, userID, friendshipID string) (MessageOutput, error) {
	if err := s.requirePremium(ctx, userID); err != nil {
		return MessageOutput{}, err
	}
	if err := s.repo.DeletePendingRequest(ctx, s.pool, friendshipID, userID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return MessageOutput{}, fmt.Errorf("%w: request not found", ErrUnauthorized)
		}
		s.logger.Error("declining friendship", "err", err)
		return MessageOutput{}, ErrInternal
	}
	return MessageOutput{Message: "Request declined"}, nil
}

func (s *SocialService) RemoveFriend(ctx context.Context, userID, friendshipID string) (MessageOutput, error) {
	if err := s.requirePremium(ctx, userID); err != nil {
		return MessageOutput{}, err
	}
	if err := s.repo.RemoveFriend(ctx, s.pool, friendshipID, userID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return MessageOutput{}, fmt.Errorf("%w: friendship not found", ErrUnauthorized)
		}
		s.logger.Error("removing friend", "err", err)
		return MessageOutput{}, ErrInternal
	}
	return MessageOutput{Message: "Friend removed"}, nil
}

func (s *SocialService) SearchUsers(ctx context.Context, userID, prefix string) ([]repository.BasicUserRow, error) {
	if err := s.requirePremium(ctx, userID); err != nil {
		return nil, err
	}
	users, err := s.repo.SearchUsers(ctx, s.pool, prefix, userID, 20)
	if err != nil {
		s.logger.Error("searching users", "err", err)
		return nil, ErrInternal
	}
	return users, nil
}

func (s *SocialService) FriendStats(ctx context.Context, userID, friendshipID string, days int) (FriendStatsOutput, error) {
	if err := s.requirePremium(ctx, userID); err != nil {
		return FriendStatsOutput{}, err
	}
	requesterID, addresseeID, status, err := s.repo.GetFriendship(ctx, s.pool, friendshipID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return FriendStatsOutput{}, fmt.Errorf("%w: friendship not found", ErrUnauthorized)
		}
		s.logger.Error("loading friendship for stats", "err", err)
		return FriendStatsOutput{}, ErrInternal
	}
	if status != "accepted" || (requesterID != userID && addresseeID != userID) {
		return FriendStatsOutput{}, fmt.Errorf("%w: friendship not found", ErrUnauthorized)
	}
	friendID := requesterID
	if requesterID == userID {
		friendID = addresseeID
	}
	return s.loadStatsForUser(ctx, friendID, days)
}

func (s *SocialService) LeaderboardByStreak(ctx context.Context, userID string, page, limit int) (LeaderboardOutput, error) {
	return s.buildLeaderboard(ctx, userID, page, limit, true)
}

func (s *SocialService) LeaderboardByFocusTime(ctx context.Context, userID string, page, limit int) (LeaderboardOutput, error) {
	return s.buildLeaderboard(ctx, userID, page, limit, false)
}

func (s *SocialService) requirePremium(ctx context.Context, userID string) error {
	active, err := s.repo.EffectivePremiumActive(ctx, s.pool, userID)
	if err != nil {
		s.logger.Error("checking premium entitlement", "err", err)
		return ErrInternal
	}
	if !active {
		return fmt.Errorf("%w: subscription required", ErrSubscriptionRequired)
	}
	return nil
}

func (s *SocialService) buildLeaderboard(ctx context.Context, userID string, page, limit int, byStreak bool) (LeaderboardOutput, error) {
	users, err := s.repo.ListUsers(ctx, s.pool, false)
	if err != nil {
		s.logger.Error("listing users for leaderboard", "err", err)
		return LeaderboardOutput{}, ErrInternal
	}

	type ranked struct {
		user   repository.BasicUserRow
		streak int
		focus  int64
	}
	var rankedUsers []ranked
	for _, user := range users {
		stats, err := s.loadStatsForUser(ctx, user.ID, 30)
		if err != nil {
			return LeaderboardOutput{}, err
		}
		rankedUsers = append(rankedUsers, ranked{
			user:   user,
			streak: stats.Stats.CurrentStreakDays,
			focus:  stats.Stats.TotalFocusTimeMS,
		})
	}

	sort.SliceStable(rankedUsers, func(i, j int) bool {
		if byStreak {
			if rankedUsers[i].streak == rankedUsers[j].streak {
				return rankedUsers[i].user.Username < rankedUsers[j].user.Username
			}
			return rankedUsers[i].streak > rankedUsers[j].streak
		}
		if rankedUsers[i].focus == rankedUsers[j].focus {
			return rankedUsers[i].user.Username < rankedUsers[j].user.Username
		}
		return rankedUsers[i].focus > rankedUsers[j].focus
	})

	var visible []ranked
	out := LeaderboardOutput{Pagination: repository.PaginationResult{Page: page, Limit: limit}}
	for i, item := range rankedUsers {
		if item.user.ID == userID {
			out.MyRank.Rank = i + 1
			out.MyRank.CurrentStreakDays = item.streak
			out.MyRank.TotalFocusTimeMS = item.focus
		}
		if item.user.LeaderboardVisible {
			visible = append(visible, item)
		}
	}
	out.Pagination.Total = len(visible)
	start := (page - 1) * limit
	if start > len(visible) {
		start = len(visible)
	}
	end := start + limit
	if end > len(visible) {
		end = len(visible)
	}
	for i := start; i < end; i++ {
		entry := LeaderboardEntry{Rank: i + 1, User: visible[i].user, CurrentStreakDays: visible[i].streak, TotalFocusTimeMS: visible[i].focus}
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}

func (s *SocialService) loadStatsForUser(ctx context.Context, userID string, days int) (FriendStatsOutput, error) {
	user, err := s.repo.GetBasicUserByID(ctx, s.pool, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return FriendStatsOutput{}, fmt.Errorf("%w: user not found", ErrUnauthorized)
		}
		s.logger.Error("loading user for stats", "err", err)
		return FriendStatsOutput{}, ErrInternal
	}
	aggregates, err := s.repo.LoadRecentDailyAggregates(ctx, s.pool, userID, max(days, 90))
	if err != nil {
		s.logger.Error("loading aggregates for stats", "err", err)
		return FriendStatsOutput{}, ErrInternal
	}
	totalFocus, err := s.repo.LoadTotalFocusTime(ctx, s.pool, userID)
	if err != nil {
		s.logger.Error("loading total focus", "err", err)
		return FriendStatsOutput{}, ErrInternal
	}

	out := FriendStatsOutput{User: user}
	out.Stats.CurrentStreakDays = consecutiveQualified(aggregates)
	out.Stats.LongestStreakDays = longestQualified(aggregates)
	out.Stats.TotalFocusTimeMS = totalFocus
	for i, item := range aggregates {
		if i >= days {
			break
		}
		out.Stats.DailyTrends = append(out.Stats.DailyTrends, struct {
			LocalDay     string `json:"local_day"`
			EffectiveMS  int    `json:"effective_ms"`
			Qualified    bool   `json:"qualified"`
			SessionCount int    `json:"session_count"`
		}{
			LocalDay:     item.LocalDay,
			EffectiveMS:  item.EffectiveMS,
			Qualified:    item.Qualified,
			SessionCount: 0,
		})
	}
	return out, nil
}

func consecutiveQualified(days []struct {
	LocalDay    string
	EffectiveMS int
	Qualified   bool
}) int {
	count := 0
	for _, day := range days {
		if !day.Qualified {
			break
		}
		count++
	}
	return count
}

func longestQualified(days []struct {
	LocalDay    string
	EffectiveMS int
	Qualified   bool
}) int {
	longest := 0
	current := 0
	for i := len(days) - 1; i >= 0; i-- {
		if days[i].Qualified {
			current++
			if current > longest {
				longest = current
			}
			continue
		}
		current = 0
	}
	return longest
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
