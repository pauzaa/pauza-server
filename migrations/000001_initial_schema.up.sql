-- 000001_initial_schema.up.sql
-- Creates all 19 tables defined in BACKEND_SPEC.md §3.1 and §3.2
-- Tables are ordered by FK dependency (parents before children).

-- =============================================================================
-- Backend-Only Tables (§3.1)
-- =============================================================================

-- 1. users (no FK deps)
CREATE TABLE users (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email               TEXT NOT NULL,
  password_hash       TEXT NOT NULL,
  name                TEXT NOT NULL DEFAULT '',
  username            TEXT NOT NULL UNIQUE,
  profile_picture_url TEXT,
  leaderboard_visible BOOLEAN NOT NULL DEFAULT TRUE,
  email_verified      BOOLEAN NOT NULL DEFAULT FALSE,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Case-insensitive unique indexes for email and username.
CREATE UNIQUE INDEX idx_users_email    ON users (lower(email));
CREATE UNIQUE INDEX idx_users_username ON users (lower(username));

-- 2. otp_codes (FK -> users)
CREATE TABLE otp_codes (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash  TEXT NOT NULL,
  purpose    TEXT NOT NULL CHECK (purpose IN ('email_verification', 'password_reset')),
  expires_at TIMESTAMPTZ NOT NULL,
  used       BOOLEAN NOT NULL DEFAULT FALSE,
  attempts   INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_otp_codes_user_purpose ON otp_codes (user_id, purpose, used, expires_at);
CREATE INDEX idx_otp_codes_expires_at   ON otp_codes (expires_at);

-- 3. refresh_tokens (FK -> users)
CREATE TABLE refresh_tokens (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  revoked    BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_refresh_tokens_user       ON refresh_tokens (user_id, revoked);
CREATE INDEX idx_refresh_tokens_expires_at ON refresh_tokens (expires_at);
CREATE INDEX idx_refresh_tokens_revoked_created
    ON refresh_tokens (created_at) WHERE revoked = true;

-- 4. admin_credentials (no FK deps)
CREATE TABLE admin_credentials (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 5. subscription_plans (no FK deps)
CREATE TABLE subscription_plans (
  id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name                     TEXT NOT NULL,
  duration_type            TEXT NOT NULL CHECK (duration_type IN ('monthly', 'yearly', 'lifetime')),
  price_cents              INTEGER NOT NULL CHECK (price_cents >= 0),
  currency                 TEXT NOT NULL DEFAULT 'USD',
  features_json            JSONB NOT NULL DEFAULT '{}',
  is_active                BOOLEAN NOT NULL DEFAULT TRUE,
  student_discount_percent INTEGER NOT NULL DEFAULT 0 CHECK (student_discount_percent BETWEEN 0 AND 100),
  created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 6. subscription_plan_discounts (FK -> subscription_plans)
CREATE TABLE subscription_plan_discounts (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  plan_id          UUID NOT NULL REFERENCES subscription_plans(id) ON DELETE CASCADE,
  discount_percent INTEGER NOT NULL CHECK (discount_percent BETWEEN 1 AND 100),
  starts_at        TIMESTAMPTZ NOT NULL,
  ends_at          TIMESTAMPTZ NOT NULL,
  description      TEXT NOT NULL DEFAULT '',
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

  CHECK (ends_at > starts_at)
);

CREATE INDEX idx_plan_discounts_plan ON subscription_plan_discounts (plan_id, starts_at, ends_at);

-- 7. user_subscriptions (FK -> users, subscription_plans)
CREATE TABLE user_subscriptions (
  id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id                  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  plan_id                  UUID NOT NULL REFERENCES subscription_plans(id),
  revenuecat_subscription_id TEXT,
  status                   TEXT NOT NULL CHECK (status IN ('active', 'expired', 'cancelled', 'trial')),
  current_period_start     TIMESTAMPTZ,
  current_period_end       TIMESTAMPTZ,
  is_student               BOOLEAN NOT NULL DEFAULT FALSE,
  created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_user_subscriptions_user ON user_subscriptions (user_id, status);
CREATE INDEX idx_user_subscriptions_revenuecat ON user_subscriptions (revenuecat_subscription_id);

-- 8. friendships (FK -> users x2: requester_id, addressee_id)
CREATE TABLE friendships (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  requester_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  addressee_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status       TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'declined')),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

  CHECK (requester_id != addressee_id),
  UNIQUE (requester_id, addressee_id)
);

CREATE INDEX idx_friendships_addressee ON friendships (addressee_id, status);
CREATE INDEX idx_friendships_requester ON friendships (requester_id, status);

-- 9. device_tokens (FK -> users)
CREATE TABLE device_tokens (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  fcm_token  TEXT NOT NULL UNIQUE,
  platform   TEXT NOT NULL CHECK (platform IN ('android', 'ios')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_device_tokens_user ON device_tokens (user_id);

-- 10. sync_tombstones (FK -> users)
CREATE TABLE sync_tombstones (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  table_name TEXT NOT NULL,
  record_id  TEXT NOT NULL,
  deleted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sync_tombstones_user_time ON sync_tombstones (user_id, deleted_at);

-- =============================================================================
-- Synced Tables (§3.2)
-- These mirror the client's local SQLite schema.
-- BIGINT for timestamps, INTEGER for boolean flags (0/1).
-- Composite primary keys include user_id.
-- =============================================================================

-- 11. modes (FK -> users)
CREATE TABLE modes (
  user_id                UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  id                     TEXT NOT NULL,
  title                  TEXT NOT NULL,
  text_on_screen         TEXT NOT NULL,
  description            TEXT,
  allowed_pauses_count   INTEGER NOT NULL DEFAULT 0 CHECK (allowed_pauses_count >= 0),
  minimum_duration_ms    INTEGER CHECK (minimum_duration_ms IS NULL OR minimum_duration_ms >= 1000),
  ending_pausing_scenario TEXT NOT NULL CHECK (ending_pausing_scenario IN ('nfc', 'qr', 'manual')),
  icon_token             TEXT NOT NULL DEFAULT 'ms:v1:tune' CHECK (length(trim(icon_token)) > 0),
  created_at             BIGINT NOT NULL,
  updated_at             BIGINT NOT NULL,

  PRIMARY KEY (user_id, id)
);

-- 12. mode_blocked_apps (FK -> users, modes)
CREATE TABLE mode_blocked_apps (
  user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  mode_id        TEXT NOT NULL,
  platform       TEXT NOT NULL CHECK (platform IN ('android', 'ios')),
  app_identifier TEXT NOT NULL,
  created_at     BIGINT NOT NULL,
  updated_at     BIGINT NOT NULL,

  PRIMARY KEY (user_id, mode_id, platform, app_identifier),
  CONSTRAINT fk_mode_blocked_apps_mode
    FOREIGN KEY (user_id, mode_id) REFERENCES modes (user_id, id) ON DELETE CASCADE
);

-- 13. schedules (FK -> users, modes)
CREATE TABLE schedules (
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  id           TEXT NOT NULL,
  mode_id      TEXT NOT NULL,
  days         TEXT NOT NULL,
  start_minute INTEGER NOT NULL CHECK (start_minute BETWEEN 0 AND 1439),
  end_minute   INTEGER NOT NULL CHECK (end_minute BETWEEN 0 AND 1439),
  enabled      INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
  created_at   BIGINT NOT NULL,
  updated_at   BIGINT NOT NULL,

  PRIMARY KEY (user_id, id),
  UNIQUE (user_id, mode_id),
  CONSTRAINT fk_schedules_mode
    FOREIGN KEY (user_id, mode_id) REFERENCES modes (user_id, id) ON DELETE CASCADE
);

-- 14. restriction_sessions (FK -> users, modes)
CREATE TABLE restriction_sessions (
  user_id              UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_id           TEXT NOT NULL,
  mode_id              TEXT NOT NULL,
  source               TEXT NOT NULL CHECK (source IN ('manual', 'schedule')),
  started_at           BIGINT NOT NULL,
  ended_at             BIGINT,
  pause_count          INTEGER NOT NULL DEFAULT 0,
  total_paused_ms      INTEGER NOT NULL DEFAULT 0,
  last_paused_at       BIGINT,
  integrity_status     TEXT NOT NULL DEFAULT 'ok' CHECK (integrity_status IN ('ok', 'anomaly')),
  last_anomaly_reason  TEXT,
  last_event_id        TEXT NOT NULL,
  created_at           BIGINT NOT NULL,
  updated_at           BIGINT NOT NULL,

  PRIMARY KEY (user_id, session_id),
  CONSTRAINT fk_restriction_sessions_mode
    FOREIGN KEY (user_id, mode_id) REFERENCES modes (user_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_restriction_sessions_mode ON restriction_sessions (user_id, mode_id);
CREATE INDEX idx_restriction_sessions_started ON restriction_sessions (user_id, started_at DESC);

-- 15. restriction_lifecycle_events (FK -> users, modes, restriction_sessions)
-- NOTE: No updated_at column — this is an append-only events table.
CREATE TABLE restriction_lifecycle_events (
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  id          TEXT NOT NULL,
  session_id  TEXT NOT NULL,
  mode_id     TEXT NOT NULL,
  action      TEXT NOT NULL CHECK (action IN ('START', 'PAUSE', 'RESUME', 'END')),
  source      TEXT NOT NULL CHECK (source IN ('manual', 'schedule')),
  reason      TEXT NOT NULL,
  occurred_at BIGINT NOT NULL,
  created_at  BIGINT NOT NULL,

  PRIMARY KEY (user_id, id),
  CONSTRAINT fk_lifecycle_events_mode
    FOREIGN KEY (user_id, mode_id) REFERENCES modes (user_id, id) ON DELETE CASCADE,
  CONSTRAINT fk_lifecycle_events_session
    FOREIGN KEY (user_id, session_id) REFERENCES restriction_sessions (user_id, session_id) ON DELETE CASCADE
);

CREATE INDEX idx_lifecycle_events_session ON restriction_lifecycle_events (user_id, session_id);

-- 16. nfc_linked_chips (FK -> users)
CREATE TABLE nfc_linked_chips (
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  id              TEXT NOT NULL,
  chip_identifier TEXT NOT NULL CHECK (length(trim(chip_identifier)) > 0),
  name            TEXT NOT NULL,
  created_at      BIGINT NOT NULL,
  updated_at      BIGINT NOT NULL,

  PRIMARY KEY (user_id, id),
  UNIQUE (user_id, chip_identifier)
);

-- 17. qr_linked_codes (FK -> users)
CREATE TABLE qr_linked_codes (
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  id         TEXT NOT NULL,
  scan_value TEXT NOT NULL CHECK (length(trim(scan_value)) > 0),
  name       TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,

  PRIMARY KEY (user_id, id),
  UNIQUE (user_id, scan_value)
);

-- 18. streak_session_daily_rollups (FK -> users, restriction_sessions)
CREATE TABLE streak_session_daily_rollups (
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_id   TEXT NOT NULL,
  local_day    TEXT NOT NULL CHECK (length(local_day) = 10),
  effective_ms INTEGER NOT NULL DEFAULT 0 CHECK (effective_ms >= 0),
  updated_at   BIGINT NOT NULL,

  PRIMARY KEY (user_id, session_id, local_day),
  CONSTRAINT fk_streak_rollups_session
    FOREIGN KEY (user_id, session_id) REFERENCES restriction_sessions (user_id, session_id) ON DELETE CASCADE
);

-- 19. streak_daily_aggregates (FK -> users)
CREATE TABLE streak_daily_aggregates (
  user_id              UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  local_day            TEXT NOT NULL CHECK (length(local_day) = 10),
  effective_ms         INTEGER NOT NULL DEFAULT 0 CHECK (effective_ms >= 0),
  qualified            INTEGER NOT NULL CHECK (qualified IN (0, 1)),
  source_session_count INTEGER NOT NULL DEFAULT 0 CHECK (source_session_count >= 0),
  updated_at           BIGINT NOT NULL,

  PRIMARY KEY (user_id, local_day)
);

CREATE INDEX idx_streak_aggregates_qualified ON streak_daily_aggregates (user_id, qualified, local_day);
