-- 000003_otp_identity_schema.up.sql
-- 1. Case-insensitive email uniqueness for users.
-- 2. Replace email-based OTP linkage with user_id FK.
-- 3. Rename otp_codes.code to code_hash (stop storing plaintext OTPs).

-- ===========================================================================
-- 1. Normalize users.email to lowercase and enforce case-insensitive
--    uniqueness via an expression index instead of a plain UNIQUE constraint.
-- ===========================================================================

-- Normalize existing data to lowercase.
UPDATE users SET email = lower(email) WHERE email != lower(email);

-- Drop the old case-sensitive UNIQUE constraint.
ALTER TABLE users DROP CONSTRAINT users_email_key;

-- Create a case-insensitive unique index (matches idx_users_username pattern).
CREATE UNIQUE INDEX idx_users_email ON users (lower(email));

-- ===========================================================================
-- 2. Link otp_codes to users via user_id instead of user_email.
-- ===========================================================================

-- Add the user_id column (nullable initially so we can backfill).
ALTER TABLE otp_codes ADD COLUMN user_id UUID;

-- Backfill user_id from users via case-insensitive email join.
UPDATE otp_codes
SET user_id = u.id
FROM users u
WHERE lower(otp_codes.user_email) = lower(u.email);

-- Delete orphan OTP rows that have no matching user.
DELETE FROM otp_codes WHERE user_id IS NULL;

-- Now make user_id NOT NULL and add FK.
ALTER TABLE otp_codes ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE otp_codes
  ADD CONSTRAINT fk_otp_codes_user
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

-- Drop the old user_email column.
ALTER TABLE otp_codes DROP COLUMN user_email;

-- Replace the old email-based index with a user_id-based one.
DROP INDEX IF EXISTS idx_otp_codes_email_purpose;
CREATE INDEX idx_otp_codes_user_purpose ON otp_codes (user_id, purpose, used, expires_at);

-- ===========================================================================
-- 3. Stop storing plaintext OTPs: rename code -> code_hash.
--    Mark all existing unused OTP rows as used so stale plaintext codes
--    cannot be verified after the migration.
-- ===========================================================================

UPDATE otp_codes SET used = true WHERE used = false;
ALTER TABLE otp_codes RENAME COLUMN code TO code_hash;
