package database

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestConnect_InvalidURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := Connect(ctx, "not-a-valid-url://")
	if err == nil {
		t.Fatal("expected error for invalid database URL, got nil")
	}
}

func TestConnect_UnreachableHost(t *testing.T) {
	// Use a well-formed URL pointing to a host/port that will not be reachable.
	// RFC 5737 reserves 192.0.2.0/24 (TEST-NET-1) for documentation; nothing
	// should be listening there, and the connection should fail or time out.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := Connect(ctx, "postgres://user:pass@192.0.2.1:5432/testdb?connect_timeout=1")
	if err == nil {
		t.Fatal("expected error for unreachable host, got nil")
	}
	if !strings.Contains(err.Error(), "pinging database") &&
		!strings.Contains(err.Error(), "creating connection pool") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestConnect_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := Connect(ctx, "postgres://user:pass@localhost:5432/testdb")
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}
