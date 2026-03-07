-- 000003_otp_identity_schema.down.sql
-- Reverses 000003_otp_identity_schema.up.sql.

-- 3. Rename code_hash back to code.
ALTER TABLE otp_codes RENAME COLUMN code_hash TO code;

-- 2. Re-add user_email, backfill from users, drop user_id FK/column.
ALTER TABLE otp_codes ADD COLUMN user_email TEXT;

UPDATE otp_codes
SET user_email = u.email
FROM users u
WHERE otp_codes.user_id = u.id;

-- Delete any rows that cannot be backfilled (shouldn't happen with CASCADE,
-- but guard against edge cases).
DELETE FROM otp_codes WHERE user_email IS NULL;

ALTER TABLE otp_codes ALTER COLUMN user_email SET NOT NULL;

ALTER TABLE otp_codes DROP CONSTRAINT IF EXISTS fk_otp_codes_user;
ALTER TABLE otp_codes DROP COLUMN user_id;

-- Restore old index.
DROP INDEX IF EXISTS idx_otp_codes_user_purpose;
CREATE INDEX idx_otp_codes_email_purpose ON otp_codes (user_email, purpose, used, expires_at);

-- 1. Restore case-sensitive UNIQUE constraint on users.email.
DROP INDEX IF EXISTS idx_users_email;
ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);
