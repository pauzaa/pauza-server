package database

import (
	"strings"
	"testing"
)

func TestMigrateDSN_PostgresScheme(t *testing.T) {
	input := "postgres://user:pass@localhost:5432/mydb?sslmode=disable"
	got, err := migrateDSN(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "pgx5://user:pass@localhost:5432/mydb?sslmode=disable"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMigrateDSN_PostgresqlScheme(t *testing.T) {
	input := "postgresql://user:pass@localhost:5432/mydb?sslmode=disable"
	got, err := migrateDSN(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "pgx5://user:pass@localhost:5432/mydb?sslmode=disable"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMigrateDSN_PreservesCredentialsAndPath(t *testing.T) {
	input := "postgres://admin:s3cret%21@db.example.com:5433/app_prod?sslmode=require&connect_timeout=10"
	got, err := migrateDSN(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, "pgx5://") {
		t.Errorf("expected pgx5:// prefix, got %q", got)
	}
	if !strings.Contains(got, "db.example.com:5433") {
		t.Errorf("expected host preserved, got %q", got)
	}
	if !strings.Contains(got, "/app_prod") {
		t.Errorf("expected database path preserved, got %q", got)
	}
}

func TestMigrateDSN_UnsupportedScheme(t *testing.T) {
	_, err := migrateDSN("mysql://user:pass@localhost/mydb")
	if err == nil {
		t.Fatal("expected error for unsupported scheme, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported database URL scheme") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestMigrateDSN_InvalidURL(t *testing.T) {
	_, err := migrateDSN("://broken")
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
	if !strings.Contains(err.Error(), "parsing database URL") {
		t.Errorf("unexpected error message: %v", err)
	}
}
