package middleware_test

import (
	"context"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/middleware"
)

func TestUserFromContext_Empty(t *testing.T) {
	ctx := context.Background()
	user, ok := middleware.UserFromContext(ctx)
	if ok {
		t.Error("UserFromContext on empty context: got ok = true, want false")
	}
	if user != (middleware.AuthUser{}) {
		t.Errorf("UserFromContext on empty context: got %+v, want zero value", user)
	}
}

func TestWithUser_RoundTrip(t *testing.T) {
	want := middleware.AuthUser{
		UserID: "550e8400-e29b-41d4-a716-446655440000",
		Email:  "alice@example.com",
	}

	ctx := middleware.WithUser(context.Background(), want)
	got, ok := middleware.UserFromContext(ctx)

	if !ok {
		t.Fatal("UserFromContext returned ok = false after WithUser")
	}
	if got != want {
		t.Errorf("UserFromContext = %+v, want %+v", got, want)
	}
}

func TestWithUser_OverwritesPrevious(t *testing.T) {
	first := middleware.AuthUser{UserID: "aaa", Email: "a@example.com"}
	second := middleware.AuthUser{UserID: "bbb", Email: "b@example.com"}

	ctx := middleware.WithUser(context.Background(), first)
	ctx = middleware.WithUser(ctx, second)

	got, ok := middleware.UserFromContext(ctx)
	if !ok {
		t.Fatal("UserFromContext returned ok = false after second WithUser")
	}
	if got != second {
		t.Errorf("UserFromContext = %+v, want %+v (second value)", got, second)
	}
}

func TestUserFromContext_WrongType(t *testing.T) {
	// Storing a non-AuthUser value under a different key should not confuse
	// UserFromContext. This tests that the typed context key prevents
	// collisions with plain string keys.
	ctx := context.WithValue(context.Background(), "pauza.auth.user", "not-an-AuthUser")
	_, ok := middleware.UserFromContext(ctx)
	if ok {
		t.Error("UserFromContext should return false for wrong value type / key type")
	}
}
