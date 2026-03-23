package push

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"

	"github.com/IsorilovA/pauza-server/internal/repository"
)

const multicastBatchSize = 500

type DeviceTokenStore interface {
	ListDeviceTokens(ctx context.Context, db repository.DBTX, userID string) ([]repository.DeviceTokenRow, error)
	DeleteDeviceToken(ctx context.Context, db repository.DBTX, fcmToken string) error
}

type messagingClient interface {
	SendEachForMulticast(ctx context.Context, message *messaging.MulticastMessage) (*messaging.BatchResponse, error)
}

var newMessagingClient = func(ctx context.Context, serviceAccountJSON string) (messagingClient, error) {
	var opts []option.ClientOption
	if strings.TrimSpace(serviceAccountJSON) != "" {
		opts = append(opts, option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(serviceAccountJSON)))
	}

	app, err := firebase.NewApp(ctx, nil, opts...)
	if err != nil {
		return nil, fmt.Errorf("creating firebase app: %w", err)
	}

	client, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating firebase messaging client: %w", err)
	}

	return client, nil
}

var isUnregisteredError = messaging.IsUnregistered

type FirebaseSender struct {
	pool   repository.Pool
	store  DeviceTokenStore
	client messagingClient
	logger *slog.Logger
}

func NewFirebaseSender(
	ctx context.Context,
	pool repository.Pool,
	store DeviceTokenStore,
	logger *slog.Logger,
	serviceAccountJSON string,
) (*FirebaseSender, error) {
	client, err := newMessagingClient(ctx, serviceAccountJSON)
	if err != nil {
		return nil, err
	}

	return &FirebaseSender{
		pool:   pool,
		store:  store,
		client: client,
		logger: logger,
	}, nil
}

func NewFirebaseSenderWithClient(
	pool repository.Pool,
	store DeviceTokenStore,
	client messagingClient,
	logger *slog.Logger,
) *FirebaseSender {
	return &FirebaseSender{
		pool:   pool,
		store:  store,
		client: client,
		logger: logger,
	}
}

func (s *FirebaseSender) Send(ctx context.Context, userID string, notification Notification) error {
	tokens, err := s.store.ListDeviceTokens(ctx, s.pool, userID)
	if err != nil {
		return fmt.Errorf("listing device tokens: %w", err)
	}
	if len(tokens) == 0 {
		return nil
	}

	data := make(map[string]string, 1)
	if notification.FriendMetadata != nil {
		for key, value := range friendMetadataFields(*notification.FriendMetadata) {
			data[key] = value
		}
	}
	if notification.Type != "" {
		data["type"] = notification.Type
	}

	var firstErr error

	for start := 0; start < len(tokens); start += multicastBatchSize {
		end := min(start+multicastBatchSize, len(tokens))
		batch := tokens[start:end]
		batchTokens := make([]string, 0, len(batch))
		for _, token := range batch {
			batchTokens = append(batchTokens, token.FCMToken)
		}

		msg := &messaging.MulticastMessage{
			Tokens: batchTokens,
			Data:   data,
			Notification: &messaging.Notification{
				Title: notification.Title,
				Body:  notification.Body,
			},
		}

		resp, err := s.client.SendEachForMulticast(ctx, msg)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("sending multicast notification: %w", err)
			}
			s.logger.ErrorContext(ctx, "sending multicast notification",
				"user_id", userID,
				"type", notification.Type,
				"err", err,
			)
			continue
		}

		for i, result := range resp.Responses {
			if result == nil || result.Success || result.Error == nil {
				continue
			}

			if isUnregisteredError(result.Error) {
				if err := s.store.DeleteDeviceToken(ctx, s.pool, batch[i].FCMToken); err != nil {
					if firstErr == nil {
						firstErr = fmt.Errorf("deleting unregistered device token: %w", err)
					}
					s.logger.ErrorContext(ctx, "deleting unregistered device token",
						"user_id", userID,
						"platform", batch[i].Platform,
						"err", err,
					)
				} else {
					s.logger.InfoContext(ctx, "deleted unregistered device token",
						"user_id", userID,
						"platform", batch[i].Platform,
					)
				}
				continue
			}

			if firstErr == nil {
				firstErr = fmt.Errorf("sending device notification: %w", result.Error)
			}
			s.logger.WarnContext(ctx, "device notification failed",
				"user_id", userID,
				"platform", batch[i].Platform,
				"type", notification.Type,
				"err", result.Error,
			)
		}
	}

	return firstErr
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func friendMetadataFields(metadata FriendMetadata) map[string]string {
	return map[string]string{
		"friendship_id":  metadata.FriendshipID,
		"actor_user_id":  metadata.ActorUserID,
		"actor_username": metadata.ActorUsername,
	}
}
