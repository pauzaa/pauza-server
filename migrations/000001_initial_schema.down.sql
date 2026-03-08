-- 000001_initial_schema.down.sql
-- Drops all 19 tables in reverse dependency order.

DROP INDEX IF EXISTS idx_streak_aggregates_qualified;
DROP INDEX IF EXISTS idx_lifecycle_events_session;
DROP INDEX IF EXISTS idx_restriction_sessions_started;
DROP INDEX IF EXISTS idx_restriction_sessions_mode;
DROP INDEX IF EXISTS idx_sync_tombstones_user_time;
DROP INDEX IF EXISTS idx_device_tokens_user;
DROP INDEX IF EXISTS idx_friendships_requester;
DROP INDEX IF EXISTS idx_friendships_addressee;
DROP INDEX IF EXISTS idx_user_subscriptions_revenuecat;
DROP INDEX IF EXISTS idx_user_subscriptions_user;
DROP INDEX IF EXISTS idx_plan_discounts_plan;
DROP INDEX IF EXISTS idx_refresh_tokens_revoked_created;
DROP INDEX IF EXISTS idx_refresh_tokens_expires_at;
DROP INDEX IF EXISTS idx_refresh_tokens_user;
DROP INDEX IF EXISTS idx_otp_codes_expires_at;
DROP INDEX IF EXISTS idx_otp_codes_user_purpose;
DROP INDEX IF EXISTS idx_users_username;
DROP INDEX IF EXISTS idx_users_email;

DROP TABLE IF EXISTS streak_daily_aggregates CASCADE;
DROP TABLE IF EXISTS streak_session_daily_rollups CASCADE;
DROP TABLE IF EXISTS qr_linked_codes CASCADE;
DROP TABLE IF EXISTS nfc_linked_chips CASCADE;
DROP TABLE IF EXISTS restriction_lifecycle_events CASCADE;
DROP TABLE IF EXISTS restriction_sessions CASCADE;
DROP TABLE IF EXISTS schedules CASCADE;
DROP TABLE IF EXISTS mode_blocked_apps CASCADE;
DROP TABLE IF EXISTS modes CASCADE;
DROP TABLE IF EXISTS sync_tombstones CASCADE;
DROP TABLE IF EXISTS device_tokens CASCADE;
DROP TABLE IF EXISTS friendships CASCADE;
DROP TABLE IF EXISTS user_subscriptions CASCADE;
DROP TABLE IF EXISTS subscription_plan_discounts CASCADE;
DROP TABLE IF EXISTS subscription_plans CASCADE;
DROP TABLE IF EXISTS admin_credentials CASCADE;
DROP TABLE IF EXISTS refresh_tokens CASCADE;
DROP TABLE IF EXISTS otp_codes CASCADE;
DROP TABLE IF EXISTS users CASCADE;
