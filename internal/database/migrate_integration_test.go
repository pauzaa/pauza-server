//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/migrations"
)

func TestRunMigrations_CreatesPasswordlessAuthSchema(t *testing.T) {
	pool, url := testPool(t)
	if err := RunMigrations(testLogger(), url, migrations.FS); err != nil {
		t.Fatalf("RunMigrations() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var (
		otpCodesCount          int
		otpFailedAttemptsCount int
		passwordHashCount      int
	)

	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'otp_codes' AND column_name = 'email'`,
	).Scan(&otpCodesCount); err != nil {
		t.Fatalf("querying otp_codes.email: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'otp_failed_attempts' AND column_name = 'email'`,
	).Scan(&otpFailedAttemptsCount); err != nil {
		t.Fatalf("querying otp_failed_attempts.email: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'users' AND column_name = 'password_hash'`,
	).Scan(&passwordHashCount); err != nil {
		t.Fatalf("querying users.password_hash: %v", err)
	}

	if otpCodesCount != 1 || otpFailedAttemptsCount != 1 || passwordHashCount != 0 {
		t.Fatalf("schema counts = otp_codes.email:%d otp_failed_attempts.email:%d users.password_hash:%d",
			otpCodesCount, otpFailedAttemptsCount, passwordHashCount)
	}
}
