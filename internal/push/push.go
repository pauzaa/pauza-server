package push

import (
	"context"
	"errors"
	"log/slog"

	"github.com/IsorilovA/pauza-server/internal/repository"
)

type Notification struct {
	Type           string
	Title          string
	Body           string
	FriendMetadata *FriendMetadata
}

type FriendMetadata struct {
	FriendshipID  string
	ActorUserID   string
	ActorUsername string
}

type Sender interface {
	Send(ctx context.Context, userID string, notification Notification) error
}

type PreferenceStore interface {
	GetPushEnabled(ctx context.Context, db repository.DBTX, userID string) (bool, error)
}

type NoopSender struct {
	logger *slog.Logger
}

func NewNoopSender(logger *slog.Logger) *NoopSender {
	return &NoopSender{logger: logger}
}

func (s *NoopSender) Send(ctx context.Context, userID string, notification Notification) error {
	if s.logger != nil {
		s.logger.InfoContext(ctx, "push notifications disabled; skipping send",
			"user_id", userID,
			"type", notification.Type,
		)
	}
	return nil
}

type PreferenceSender struct {
	pool   repository.Pool
	store  PreferenceStore
	next   Sender
	logger *slog.Logger
}

func NewPreferenceSender(pool repository.Pool, store PreferenceStore, next Sender, logger *slog.Logger) *PreferenceSender {
	return &PreferenceSender{
		pool:   pool,
		store:  store,
		next:   next,
		logger: logger,
	}
}

func (s *PreferenceSender) Send(ctx context.Context, userID string, notification Notification) error {
	pushEnabled, err := s.store.GetPushEnabled(ctx, s.pool, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !pushEnabled {
		if s.logger != nil {
			s.logger.InfoContext(ctx, "push disabled for user; skipping send",
				"user_id", userID,
				"type", notification.Type,
			)
		}
		return nil
	}
	return s.next.Send(ctx, userID, notification)
}
