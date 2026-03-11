package push

import (
	"context"
	"log/slog"
)

type Notification struct {
	Type  string
	Title string
	Body  string
	Data  map[string]string
}

type Sender interface {
	Send(ctx context.Context, userID string, notification Notification) error
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
