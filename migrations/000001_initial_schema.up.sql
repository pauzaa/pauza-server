-- 000001_initial_schema.up.sql
-- Creates the full pre-release schema exactly as defined by the current BACKEND_SPEC.md.
-- Tables are ordered by FK dependency (parents before children).

-- =============================================================================
-- Backend-Only Tables (§3.1)
-- =============================================================================

-- 1. users (no FK deps)
CREATE TABLE users (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email               TEXT NOT NULL,
  name                TEXT NOT NULL DEFAULT '',
  username            TEXT NOT NULL UNIQUE,
  profile_picture_url TEXT,
  push_enabled        BOOLEAN NOT NULL DEFAULT TRUE,
  leaderboard_visible BOOLEAN NOT NULL DEFAULT TRUE,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Case-insensitive unique indexes for email and username.
CREATE UNIQUE INDEX idx_users_email    ON users (lower(email));
CREATE UNIQUE INDEX idx_users_username ON users (lower(username));

-- 2. otp_codes (FK -> users)
CREATE TABLE otp_codes (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email      TEXT NOT NULL,
  user_id    UUID REFERENCES users(id) ON DELETE CASCADE,
  code_hash  TEXT NOT NULL,
  purpose    TEXT NOT NULL CHECK (purpose IN ('auth_login', 'account_deletion')),
  expires_at TIMESTAMPTZ NOT NULL,
  used       BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_otp_codes_email_purpose ON otp_codes (lower(email), purpose, used, expires_at);
CREATE INDEX idx_otp_codes_expires_at   ON otp_codes (expires_at);

-- 3. otp_failed_attempts (FK -> users)
CREATE TABLE otp_failed_attempts (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email        TEXT NOT NULL,
  user_id      UUID REFERENCES users(id) ON DELETE CASCADE,
  purpose      TEXT NOT NULL CHECK (purpose IN ('auth_login', 'account_deletion')),
  attempted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_otp_failed_attempts_email_purpose_attempted_at
  ON otp_failed_attempts (lower(email), purpose, attempted_at);

-- 4. refresh_tokens (FK -> users)
CREATE TABLE refresh_tokens (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  revoked    BOOLEAN NOT NULL DEFAULT FALSE,
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_refresh_tokens_user       ON refresh_tokens (user_id, revoked);
CREATE INDEX idx_refresh_tokens_expires_at ON refresh_tokens (expires_at);
CREATE INDEX idx_refresh_tokens_revoked_at
    ON refresh_tokens (revoked_at) WHERE revoked = true;

-- 5. admin_credentials (no FK deps)
CREATE TABLE admin_credentials (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 6. user_entitlements (FK -> users)
CREATE TABLE user_entitlements (
  id                              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id                         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  entitlement                     TEXT NOT NULL,
  is_active                       BOOLEAN NOT NULL DEFAULT FALSE,
  revenuecat_app_user_id          TEXT,
  revenuecat_original_app_user_id TEXT,
  current_period_end              TIMESTAMPTZ,
  last_webhook_event_at           TIMESTAMPTZ,
  created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),

  CHECK (length(trim(entitlement)) > 0),
  UNIQUE (user_id, entitlement)
);

CREATE INDEX idx_user_entitlements_user_active ON user_entitlements (user_id, is_active);
CREATE INDEX idx_user_entitlements_entitlement_active ON user_entitlements (entitlement, is_active);
CREATE INDEX idx_user_entitlements_rc_app_user_id ON user_entitlements (revenuecat_app_user_id);
CREATE INDEX idx_user_entitlements_rc_original_app_user_id ON user_entitlements (revenuecat_original_app_user_id);

-- 7. admin_entitlement_overrides (FK -> users)
CREATE TABLE admin_entitlement_overrides (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  entitlement  TEXT NOT NULL,
  action       TEXT NOT NULL CHECK (action IN ('grant', 'revoke')),
  expires_at   TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

  CHECK (length(trim(entitlement)) > 0),
  UNIQUE (user_id, entitlement)
);

CREATE INDEX idx_admin_entitlement_overrides_user ON admin_entitlement_overrides (user_id);
CREATE INDEX idx_admin_entitlement_overrides_active ON admin_entitlement_overrides (entitlement, action, expires_at);

-- 8. friendships (FK -> users x2: requester_id, addressee_id)
CREATE TABLE friendships (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  requester_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  addressee_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status       TEXT NOT NULL CHECK (status IN ('pending', 'accepted')),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

  CHECK (requester_id != addressee_id)
);

CREATE INDEX idx_friendships_addressee ON friendships (addressee_id, status);
CREATE INDEX idx_friendships_requester ON friendships (requester_id, status);
CREATE UNIQUE INDEX idx_friendships_user_pair
  ON friendships (LEAST(requester_id, addressee_id), GREATEST(requester_id, addressee_id));

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
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  table_name        TEXT NOT NULL,
  record_id         TEXT NOT NULL,
  deleted_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  server_deleted_at BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX idx_sync_tombstones_user_time ON sync_tombstones (user_id, deleted_at);
CREATE INDEX idx_sync_tombstones_user_cursor ON sync_tombstones (user_id, table_name, server_deleted_at);

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
  server_updated_at      BIGINT NOT NULL DEFAULT 0,

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
  server_updated_at BIGINT NOT NULL DEFAULT 0,

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
  server_updated_at BIGINT NOT NULL DEFAULT 0,

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
  server_updated_at    BIGINT NOT NULL DEFAULT 0,

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
  server_created_at BIGINT NOT NULL DEFAULT 0,

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
  server_updated_at BIGINT NOT NULL DEFAULT 0,

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
  server_updated_at BIGINT NOT NULL DEFAULT 0,

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
  server_updated_at BIGINT NOT NULL DEFAULT 0,

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
  server_updated_at    BIGINT NOT NULL DEFAULT 0,

  PRIMARY KEY (user_id, local_day)
);

CREATE INDEX idx_streak_aggregates_qualified ON streak_daily_aggregates (user_id, qualified, local_day);

-- 20. leaderboard_metrics (FK -> users)
CREATE TABLE leaderboard_metrics (
  user_id               UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  current_streak_days   INTEGER NOT NULL DEFAULT 0 CHECK (current_streak_days >= 0),
  total_focus_time_ms   BIGINT NOT NULL DEFAULT 0 CHECK (total_focus_time_ms >= 0),
  updated_at            BIGINT NOT NULL DEFAULT 0
);

-- Server-managed replication cursors.
CREATE OR REPLACE FUNCTION set_server_updated_at_bigint()
RETURNS trigger AS $$
BEGIN
  NEW.server_updated_at := FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION set_server_created_at_bigint()
RETURNS trigger AS $$
BEGIN
  NEW.server_created_at := FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION set_server_deleted_at_bigint()
RETURNS trigger AS $$
BEGIN
  NEW.server_deleted_at := FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_modes_server_updated_at
  BEFORE INSERT OR UPDATE ON modes
  FOR EACH ROW EXECUTE FUNCTION set_server_updated_at_bigint();
CREATE TRIGGER trg_mode_blocked_apps_server_updated_at
  BEFORE INSERT OR UPDATE ON mode_blocked_apps
  FOR EACH ROW EXECUTE FUNCTION set_server_updated_at_bigint();
CREATE TRIGGER trg_schedules_server_updated_at
  BEFORE INSERT OR UPDATE ON schedules
  FOR EACH ROW EXECUTE FUNCTION set_server_updated_at_bigint();
CREATE TRIGGER trg_restriction_sessions_server_updated_at
  BEFORE INSERT OR UPDATE ON restriction_sessions
  FOR EACH ROW EXECUTE FUNCTION set_server_updated_at_bigint();
CREATE TRIGGER trg_restriction_lifecycle_events_server_created_at
  BEFORE INSERT ON restriction_lifecycle_events
  FOR EACH ROW EXECUTE FUNCTION set_server_created_at_bigint();
CREATE TRIGGER trg_nfc_linked_chips_server_updated_at
  BEFORE INSERT OR UPDATE ON nfc_linked_chips
  FOR EACH ROW EXECUTE FUNCTION set_server_updated_at_bigint();
CREATE TRIGGER trg_qr_linked_codes_server_updated_at
  BEFORE INSERT OR UPDATE ON qr_linked_codes
  FOR EACH ROW EXECUTE FUNCTION set_server_updated_at_bigint();
CREATE TRIGGER trg_streak_session_daily_rollups_server_updated_at
  BEFORE INSERT OR UPDATE ON streak_session_daily_rollups
  FOR EACH ROW EXECUTE FUNCTION set_server_updated_at_bigint();
CREATE TRIGGER trg_streak_daily_aggregates_server_updated_at
  BEFORE INSERT OR UPDATE ON streak_daily_aggregates
  FOR EACH ROW EXECUTE FUNCTION set_server_updated_at_bigint();
CREATE TRIGGER trg_sync_tombstones_server_deleted_at
  BEFORE INSERT ON sync_tombstones
  FOR EACH ROW EXECUTE FUNCTION set_server_deleted_at_bigint();
