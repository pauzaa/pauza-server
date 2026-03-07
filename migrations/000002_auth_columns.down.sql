-- 000002_auth_columns.down.sql
-- Reverses 000002_auth_columns.up.sql.

ALTER TABLE otp_codes DROP COLUMN attempts;
ALTER TABLE users DROP COLUMN email_verified;
