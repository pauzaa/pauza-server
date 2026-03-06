-- 000001_initial_schema.down.sql
-- Drops all 19 tables in reverse dependency order.

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
