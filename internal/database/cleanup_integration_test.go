//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"github.com/IsorilovA/pauza-server/internal/mail"
	"github.com/IsorilovA/pauza-server/migrations"
)

func TestCleanup_RemovesExpiredOTPAndFailedAttempts(t *testing.T) {
	pool, url := testPool(t)
	if err := RunMigrations(testLogger(), url, migrations.FS); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	codeHash, err := auth.HashOTP("123456")
	if err != nil {
		t.Fatalf("hashing otp: %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO otp_codes (email, code_hash, purpose, expires_at)
		 VALUES ($1, $2, $3, now() - interval '48 hours')`,
		"user@example.com", codeHash, mail.PurposeAuthLogin,
	)
	if err != nil {
		t.Fatalf("inserting expired otp: %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO otp_failed_attempts (email, purpose, attempted_at)
		 VALUES ($1, $2, now() - interval '48 hours')`,
		"user@example.com", mail.PurposeAuthLogin,
	)
	if err != nil {
		t.Fatalf("inserting old failed attempt: %v", err)
	}

	cfg := CleanupConfig{
		Interval:           time.Hour,
		OTPRetention:       24 * time.Hour,
		RefreshTokenMaxAge: 7 * 24 * time.Hour,
	}
	runCleanup(ctx, pool, testLogger(), cfg)

	if n := rowCount(t, pool, "otp_codes"); n != 0 {
		t.Fatalf("otp_codes rows = %d, want 0", n)
	}
	if n := rowCount(t, pool, "otp_failed_attempts"); n != 0 {
		t.Fatalf("otp_failed_attempts rows = %d, want 0", n)
	}
}
