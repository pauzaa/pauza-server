//go:build integration

package push

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"firebase.google.com/go/v4/messaging"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IsorilovA/pauza-server/internal/repository"
	"github.com/IsorilovA/pauza-server/internal/testdb"
)

func testIntegrationLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := testdb.New(t)
	return pool
}

func TestFirebaseSenderIntegrationDeletesUnregisteredToken(t *testing.T) {
	prev := isUnregisteredError
	t.Cleanup(func() { isUnregisteredError = prev })
	isUnregisteredError = func(err error) bool { return err != nil && err.Error() == "gone" }

	pool := testIntegrationPool(t)
	repo := repository.NewSocialRepository()

	ctx := context.Background()

	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, username)
		VALUES ('push-int@example.com', 'pushint')
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("inserting user: %v", err)
	}

	if err := repo.RegisterDevice(ctx, pool, userID, "token-live", "ios"); err != nil {
		t.Fatalf("registering token-live: %v", err)
	}
	if err := repo.RegisterDevice(ctx, pool, userID, "token-gone", "android"); err != nil {
		t.Fatalf("registering token-gone: %v", err)
	}

	client := &fakeMessagingClient{
		responses: []*messaging.BatchResponse{
			{
				Responses: []*messaging.SendResponse{
					{Success: false, Error: errors.New("gone")},
					{Success: true},
				},
			},
		},
	}
	sender := NewFirebaseSenderWithClient(pool, repo, client, testIntegrationLogger())

	err := sender.Send(ctx, userID, Notification{
		Type:  "friend_request",
		Title: "New friend request",
		Body:  "alice sent you a friend request",
		FriendMetadata: &FriendMetadata{
			FriendshipID: "friendship-1",
		},
	})
	if err != nil {
		t.Fatalf("Send error = %v, want nil", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM device_tokens WHERE user_id = $1`, userID).Scan(&count); err != nil {
		t.Fatalf("counting device tokens: %v", err)
	}
	if count != 1 {
		t.Fatalf("device token count = %d, want 1", count)
	}

	var remaining string
	if err := pool.QueryRow(ctx, `SELECT fcm_token FROM device_tokens WHERE user_id = $1`, userID).Scan(&remaining); err != nil {
		t.Fatalf("loading remaining token: %v", err)
	}
	if remaining != "token-live" {
		t.Fatalf("remaining token = %q, want token-live", remaining)
	}
}
