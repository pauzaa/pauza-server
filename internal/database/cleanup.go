package database

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const syncTombstoneRetention = 90 * 24 * time.Hour

// CleanupConfig controls the periodic auth-data cleanup job.
type CleanupConfig struct {
	// Interval is the time between consecutive cleanup passes.
	Interval time.Duration

	// OTPRetention is how long past their expires_at timestamp expired
	// OTP rows are retained before deletion. For example, a value of 24h
	// means an OTP that expired at 12:00 will be deleted after 12:00 the
	// following day.
	OTPRetention time.Duration

	// RefreshTokenMaxAge is the retention window applied to both expired
	// and revoked refresh tokens. Expired tokens are deleted when
	// expires_at < now() - RefreshTokenMaxAge; revoked tokens are deleted
	// when revoked_at < now() - RefreshTokenMaxAge. A single duration
	// keeps both predicates aligned with the same cleanup indexes.
	RefreshTokenMaxAge time.Duration

	// SessionMaxAge is the retention window applied to both expired
	// and revoked auth sessions. Expired sessions are deleted when
	// expires_at < now() - SessionMaxAge; revoked sessions are deleted
	// when revoked_at < now() - SessionMaxAge.
	SessionMaxAge time.Duration
}

// StartCleanup launches a background goroutine that periodically removes
// stale auth data (expired OTP codes and old revoked/expired refresh tokens).
// It performs an immediate cleanup pass on start unless the context is already
// cancelled. The returned function stops the goroutine and waits for it to
// finish; it is safe to call more than once.
func StartCleanup(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, cfg CleanupConfig) func() {
	ctx, cancel := context.WithCancel(ctx)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()

		// If the context is already cancelled, exit without touching the
		// pool or creating a ticker. This avoids unnecessary error noise
		// when the caller passes a pre-cancelled context.
		if ctx.Err() != nil {
			return
		}

		runCleanup(ctx, pool, logger, cfg)

		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runCleanup(ctx, pool, logger, cfg)
			}
		}
	}()

	return func() {
		cancel()
		wg.Wait()
	}
}

// runCleanup performs a single cleanup pass, deleting expired OTP rows
// and old revoked/expired refresh tokens. It logs the number of deleted
// rows and any errors encountered.
func runCleanup(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, cfg CleanupConfig) {
	if ctx.Err() != nil {
		return
	}

	if pool == nil {
		logger.WarnContext(ctx, "auth cleanup: skipping, pool is nil")
		return
	}

	otpDeleted, err := cleanupOTPCodes(ctx, pool, cfg.OTPRetention)
	if err != nil {
		logger.ErrorContext(ctx, "auth cleanup: failed to delete old OTP codes", "err", err)
	} else if otpDeleted > 0 {
		logger.InfoContext(ctx, "auth cleanup: deleted old OTP codes", "count", otpDeleted)
	}

	otpFailedAttemptsDeleted, err := cleanupOTPFailedAttempts(ctx, pool, cfg.OTPRetention)
	if err != nil {
		logger.ErrorContext(ctx, "auth cleanup: failed to delete old OTP failed attempts", "err", err)
	} else if otpFailedAttemptsDeleted > 0 {
		logger.InfoContext(ctx, "auth cleanup: deleted old OTP failed attempts", "count", otpFailedAttemptsDeleted)
	}

	rtExpired, err := cleanupExpiredRefreshTokens(ctx, pool, cfg.RefreshTokenMaxAge)
	if err != nil {
		logger.ErrorContext(ctx, "auth cleanup: failed to delete expired refresh tokens", "err", err)
	} else if rtExpired > 0 {
		logger.InfoContext(ctx, "auth cleanup: deleted expired refresh tokens", "count", rtExpired)
	}

	rtRevoked, err := cleanupRevokedRefreshTokens(ctx, pool, cfg.RefreshTokenMaxAge)
	if err != nil {
		logger.ErrorContext(ctx, "auth cleanup: failed to delete revoked refresh tokens", "err", err)
	} else if rtRevoked > 0 {
		logger.InfoContext(ctx, "auth cleanup: deleted revoked refresh tokens", "count", rtRevoked)
	}

	sessExpired, err := cleanupExpiredSessions(ctx, pool, cfg.SessionMaxAge)
	if err != nil {
		logger.ErrorContext(ctx, "auth cleanup: failed to delete expired sessions", "err", err)
	} else if sessExpired > 0 {
		logger.InfoContext(ctx, "auth cleanup: deleted expired sessions", "count", sessExpired)
	}

	sessRevoked, err := cleanupRevokedSessions(ctx, pool, cfg.SessionMaxAge)
	if err != nil {
		logger.ErrorContext(ctx, "auth cleanup: failed to delete revoked sessions", "err", err)
	} else if sessRevoked > 0 {
		logger.InfoContext(ctx, "auth cleanup: deleted revoked sessions", "count", sessRevoked)
	}

	syncTombstonesDeleted, err := cleanupSyncTombstones(ctx, pool, syncTombstoneRetention)
	if err != nil {
		logger.ErrorContext(ctx, "auth cleanup: failed to delete old sync tombstones", "err", err)
	} else if syncTombstonesDeleted > 0 {
		logger.InfoContext(ctx, "auth cleanup: deleted old sync tombstones", "count", syncTombstonesDeleted)
	}
}

// cleanupOTPCodes deletes OTP rows whose expires_at is older than the
// retention window, i.e. rows where expires_at < now() - retention.
// Uses idx_otp_codes_expires_at.
func cleanupOTPCodes(ctx context.Context, pool *pgxpool.Pool, retention time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx,
		`DELETE FROM otp_codes WHERE expires_at < now() - $1::interval`,
		retention)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// cleanupOTPFailedAttempts deletes failed OTP-attempt rows whose attempted_at
// is older than the retention window, i.e. rows where
// attempted_at < now() - retention.
// Uses idx_otp_failed_attempts_user_purpose_attempted_at.
func cleanupOTPFailedAttempts(ctx context.Context, pool *pgxpool.Pool, retention time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx,
		`DELETE FROM otp_failed_attempts WHERE attempted_at < now() - $1::interval`,
		retention)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// cleanupExpiredRefreshTokens deletes non-revoked refresh tokens whose
// expires_at is older than the retention window, i.e. rows where
// revoked = false AND expires_at < now() - maxAge. Revoked rows are retained
// and pruned by cleanupRevokedRefreshTokens based on revoked_at.
func cleanupExpiredRefreshTokens(ctx context.Context, pool *pgxpool.Pool, maxAge time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx,
		`DELETE FROM refresh_tokens
		 WHERE revoked = false AND expires_at < now() - $1::interval`,
		maxAge)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// cleanupRevokedRefreshTokens deletes revoked refresh tokens whose
// revoked_at is older than the retention window, i.e. rows where
// revoked = true AND revoked_at < now() - maxAge.
// Uses idx_refresh_tokens_revoked_at (partial index on revoked = true).
func cleanupRevokedRefreshTokens(ctx context.Context, pool *pgxpool.Pool, maxAge time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx,
		`DELETE FROM refresh_tokens
		 WHERE revoked = true AND revoked_at < now() - $1::interval`,
		maxAge)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// cleanupExpiredSessions deletes non-revoked auth sessions whose
// expires_at is older than the retention window.
func cleanupExpiredSessions(ctx context.Context, pool *pgxpool.Pool, maxAge time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx,
		`DELETE FROM auth_sessions
		 WHERE revoked = false AND expires_at < now() - $1::interval`,
		maxAge)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// cleanupRevokedSessions deletes revoked auth sessions whose
// revoked_at is older than the retention window.
func cleanupRevokedSessions(ctx context.Context, pool *pgxpool.Pool, maxAge time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx,
		`DELETE FROM auth_sessions
		 WHERE revoked = true AND revoked_at < now() - $1::interval`,
		maxAge)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// cleanupSyncTombstones deletes sync tombstones older than the retention
// window, i.e. rows where deleted_at < now() - retention.
// Uses idx_sync_tombstones_user_time.
func cleanupSyncTombstones(ctx context.Context, pool *pgxpool.Pool, retention time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx,
		`DELETE FROM sync_tombstones WHERE deleted_at < now() - $1::interval`,
		retention)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
