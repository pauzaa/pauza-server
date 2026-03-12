package push

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"firebase.google.com/go/v4/messaging"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/IsorilovA/pauza-server/internal/repository"
)

type fakeDeviceTokenStore struct {
	tokens        []repository.DeviceTokenRow
	deletedTokens []string
	listErr       error
	deleteErr     error
}

func (f *fakeDeviceTokenStore) ListDeviceTokens(context.Context, repository.DBTX, string) ([]repository.DeviceTokenRow, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]repository.DeviceTokenRow(nil), f.tokens...), nil
}

func (f *fakeDeviceTokenStore) DeleteDeviceToken(_ context.Context, _ repository.DBTX, fcmToken string) error {
	f.deletedTokens = append(f.deletedTokens, fcmToken)
	return f.deleteErr
}

type fakeMessagingClient struct {
	requests  []*messaging.MulticastMessage
	responses []*messaging.BatchResponse
	errors    []error
	callCount int
}

func (f *fakeMessagingClient) SendEachForMulticast(_ context.Context, message *messaging.MulticastMessage) (*messaging.BatchResponse, error) {
	cloned := *message
	cloned.Tokens = append([]string(nil), message.Tokens...)
	cloned.Data = map[string]string{}
	for k, v := range message.Data {
		cloned.Data[k] = v
	}
	if message.Notification != nil {
		notification := *message.Notification
		cloned.Notification = &notification
	}
	f.requests = append(f.requests, &cloned)

	index := f.callCount
	f.callCount++

	var resp *messaging.BatchResponse
	if index < len(f.responses) {
		resp = f.responses[index]
	}
	var err error
	if index < len(f.errors) {
		err = f.errors[index]
	}
	return resp, err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakePool struct{}

func (fakePool) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("unexpected Begin call")
}
func (fakePool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec call")
}
func (fakePool) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("unexpected QueryRow call")
}
func (fakePool) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query call")
}

type fakePreferenceStore struct {
	pushEnabled bool
	err         error
}

func (f *fakePreferenceStore) GetPushEnabled(context.Context, repository.DBTX, string) (bool, error) {
	return f.pushEnabled, f.err
}

type captureSender struct {
	calls []struct {
		userID       string
		notification Notification
	}
	err error
}

func (c *captureSender) Send(_ context.Context, userID string, notification Notification) error {
	c.calls = append(c.calls, struct {
		userID       string
		notification Notification
	}{userID: userID, notification: notification})
	return c.err
}

func TestNewFirebaseSenderUsesInjectedMessagingClient(t *testing.T) {
	prev := newMessagingClient
	t.Cleanup(func() { newMessagingClient = prev })

	expectedClient := &fakeMessagingClient{}
	newMessagingClient = func(_ context.Context, serviceAccountJSON string) (messagingClient, error) {
		if serviceAccountJSON != "{\"project_id\":\"test\"}" {
			t.Fatalf("serviceAccountJSON = %q", serviceAccountJSON)
		}
		return expectedClient, nil
	}

	sender, err := NewFirebaseSender(context.Background(), fakePool{}, &fakeDeviceTokenStore{}, testLogger(), "{\"project_id\":\"test\"}")
	if err != nil {
		t.Fatalf("NewFirebaseSender error = %v, want nil", err)
	}
	if sender.client != expectedClient {
		t.Fatal("expected injected client to be used")
	}
}

func TestFirebaseSenderBatchesNotificationsAndIncludesPayload(t *testing.T) {
	store := &fakeDeviceTokenStore{
		tokens: make([]repository.DeviceTokenRow, 501),
	}
	for i := range store.tokens {
		store.tokens[i] = repository.DeviceTokenRow{
			FCMToken: string(rune('a' + (i % 26))),
			Platform: "ios",
		}
	}
	store.tokens[0].FCMToken = "token-0"
	store.tokens[500].FCMToken = "token-500"

	client := &fakeMessagingClient{
		responses: []*messaging.BatchResponse{
			{Responses: make([]*messaging.SendResponse, 500)},
			{Responses: make([]*messaging.SendResponse, 1)},
		},
	}
	sender := NewFirebaseSenderWithClient(fakePool{}, store, client, testLogger())

	err := sender.Send(context.Background(), "user-1", Notification{
		Type:  "friend_request",
		Title: "New friend request",
		Body:  "alice sent you a friend request",
		Data: map[string]string{
			"friendship_id":  "friendship-1",
			"actor_user_id":  "actor-1",
			"actor_username": "alice",
		},
	})
	if err != nil {
		t.Fatalf("Send error = %v, want nil", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(client.requests))
	}
	if len(client.requests[0].Tokens) != 500 {
		t.Fatalf("first batch size = %d, want 500", len(client.requests[0].Tokens))
	}
	if len(client.requests[1].Tokens) != 1 {
		t.Fatalf("second batch size = %d, want 1", len(client.requests[1].Tokens))
	}
	if client.requests[0].Notification == nil || client.requests[0].Notification.Title != "New friend request" {
		t.Fatalf("unexpected notification payload: %#v", client.requests[0].Notification)
	}
	if client.requests[0].Data["type"] != "friend_request" {
		t.Fatalf("data.type = %q, want friend_request", client.requests[0].Data["type"])
	}
	if client.requests[0].Data["actor_username"] != "alice" {
		t.Fatalf("data.actor_username = %q, want alice", client.requests[0].Data["actor_username"])
	}
}

func TestFirebaseSenderDeletesUnregisteredTokens(t *testing.T) {
	prev := isUnregisteredError
	t.Cleanup(func() { isUnregisteredError = prev })
	isUnregisteredError = func(err error) bool { return err != nil && err.Error() == "gone" }

	store := &fakeDeviceTokenStore{
		tokens: []repository.DeviceTokenRow{
			{FCMToken: "token-1", Platform: "ios"},
			{FCMToken: "token-2", Platform: "android"},
		},
	}
	client := &fakeMessagingClient{
		responses: []*messaging.BatchResponse{
			{
				Responses: []*messaging.SendResponse{
					{Success: true},
					{Success: false, Error: errors.New("gone")},
				},
			},
		},
	}
	sender := NewFirebaseSenderWithClient(fakePool{}, store, client, testLogger())

	err := sender.Send(context.Background(), "user-1", Notification{Type: "friend_request"})
	if err != nil {
		t.Fatalf("Send error = %v, want nil", err)
	}
	if len(store.deletedTokens) != 1 || store.deletedTokens[0] != "token-2" {
		t.Fatalf("deleted tokens = %#v, want [token-2]", store.deletedTokens)
	}
}

func TestFirebaseSenderKeepsTransientFailures(t *testing.T) {
	prev := isUnregisteredError
	t.Cleanup(func() { isUnregisteredError = prev })
	isUnregisteredError = func(error) bool { return false }

	store := &fakeDeviceTokenStore{
		tokens: []repository.DeviceTokenRow{
			{FCMToken: "token-1", Platform: "ios"},
		},
	}
	client := &fakeMessagingClient{
		responses: []*messaging.BatchResponse{
			{
				Responses: []*messaging.SendResponse{
					{Success: false, Error: errors.New("temporary")},
				},
			},
		},
	}
	sender := NewFirebaseSenderWithClient(fakePool{}, store, client, testLogger())

	err := sender.Send(context.Background(), "user-1", Notification{Type: "friend_request"})
	if err == nil {
		t.Fatal("expected Send to return first delivery error")
	}
	if len(store.deletedTokens) != 0 {
		t.Fatalf("deleted tokens = %#v, want none", store.deletedTokens)
	}
}

func TestPreferenceSenderSkipsDisabledUsers(t *testing.T) {
	next := &captureSender{}
	sender := NewPreferenceSender(fakePool{}, &fakePreferenceStore{pushEnabled: false}, next, testLogger())

	err := sender.Send(context.Background(), "user-1", Notification{Type: "friend_request"})
	if err != nil {
		t.Fatalf("Send error = %v, want nil", err)
	}
	if len(next.calls) != 0 {
		t.Fatalf("next sender calls = %d, want 0", len(next.calls))
	}
}

func TestPreferenceSenderDelegatesEnabledUsers(t *testing.T) {
	next := &captureSender{}
	sender := NewPreferenceSender(fakePool{}, &fakePreferenceStore{pushEnabled: true}, next, testLogger())

	err := sender.Send(context.Background(), "user-1", Notification{Type: "friend_request"})
	if err != nil {
		t.Fatalf("Send error = %v, want nil", err)
	}
	if len(next.calls) != 1 {
		t.Fatalf("next sender calls = %d, want 1", len(next.calls))
	}
	if next.calls[0].userID != "user-1" {
		t.Fatalf("user_id = %q, want user-1", next.calls[0].userID)
	}
}
