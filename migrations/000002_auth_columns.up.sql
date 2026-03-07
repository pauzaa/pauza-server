-- 000002_auth_columns.up.sql
-- Adds columns needed for auth: email verification flag and OTP attempt tracking.

ALTER TABLE users ADD COLUMN email_verified BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE otp_codes ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
