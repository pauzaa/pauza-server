package push

import (
	"context"
	"log/slog"
)

type Sender interface {
	Send(ctx context.Context, userID string, payload map[string]string) error
}

type NoopSender struct {
	logger *slog.Logger
}

func NewNoopSender(logger *slog.Logger) *NoopSender {
	return &NoopSender{logger: logger}
}

func (s *NoopSender) Send(ctx context.Context, userID string, payload map[string]string) error {
	if s.logger != nil {
		s.logger.InfoContext(ctx, "push notifications disabled; skipping send", "user_id", userID, "payload", payload)
	}
	return nil
}
