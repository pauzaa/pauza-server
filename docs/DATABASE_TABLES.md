# Database Tables & Sync Architecture

This document describes every table in the Pauza database, how data flows between clients and the server, and how server-derived tables are computed.

---

## Table of Contents

- [Sync Mechanism Overview](#sync-mechanism-overview)
- [Synced Tables (Client ↔ Server)](#synced-tables-client--server)
  - [modes](#modes)
  - [mode_blocked_apps](#mode_blocked_apps)
  - [schedules](#schedules)
  - [restriction_sessions](#restriction_sessions)
  - [restriction_lifecycle_events](#restriction_lifecycle_events)
  - [nfc_linked_chips](#nfc_linked_chips)
  - [qr_linked_codes](#qr_linked_codes)
  - [streak_session_daily_rollups](#streak_session_daily_rollups)
- [Server-Derived Tables (Computed from Synced Data)](#server-derived-tables-computed-from-synced-data)
  - [streak_daily_aggregates](#streak_daily_aggregates)
  - [leaderboard_metrics](#leaderboard_metrics)
- [Backend-Only Tables](#backend-only-tables)
  - [users](#users)
  - [otp_codes](#otp_codes)
  - [otp_failed_attempts](#otp_failed_attempts)
  - [auth_sessions](#auth_sessions)
  - [refresh_tokens](#refresh_tokens)
  - [admin_credentials](#admin_credentials)
  - [user_entitlements](#user_entitlements)
  - [admin_entitlement_overrides](#admin_entitlement_overrides)
  - [friendships](#friendships)
  - [device_tokens](#device_tokens)
  - [sync_tombstones](#sync_tombstones)
- [Replication Responsibility Matrix](#replication-responsibility-matrix)

---

## Sync Mechanism Overview

Pauza uses an **offline-first** architecture. The client SQLite database is the source of truth during use; the server stores a replica for cross-device restore and server-side features (leaderboards, streaks).

### Global Sync Version Sequence

A shared Postgres sequence (`sync_version_seq`) provides monotonically increasing, gap-tolerant version numbers across all replicated tables. Every replicated table has a `sync_version` column updated by a `BEFORE INSERT OR UPDATE` trigger:

```sql
CREATE OR REPLACE FUNCTION set_sync_version()
RETURNS trigger AS $$
BEGIN
  NEW.sync_version := nextval('sync_version_seq');
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
```

### Cursor-Based Incremental Replication

Sync happens via `POST /api/v1/sync`. The client sends per-table cursors (the last `sync_version` it received) along with any local changes:

**Request (Client → Server):**
```json
{
  "tables": {
    "<table_name>": {
      "cursor": 42,
      "upserts": [{ ... }],
      "deletions": ["id1", "id2"]
    }
  }
}
```

**Response (Server → Client):**
```json
{
  "tables": {
    "<table_name>": {
      "next_cursor": 58,
      "upserts": [{ ... }],
      "deletions": ["id3"]
    }
  }
}
```

- `cursor = 0` triggers a **full snapshot** (bootstrap/restore).
- Conflict resolution uses `updated_at` — the later write wins.
- The entire sync request executes within a single Postgres transaction.

### Tombstones

When a synced record is deleted, a row is written to `sync_tombstones` so that future incremental syncs can inform the client of the deletion. If a record is re-created (resurrected), its tombstone is cleared.

### Cascade Deletions

Deleting a `mode` cascades tombstones to all dependent tables: `mode_blocked_apps`, `schedules`, `restriction_sessions`, `restriction_lifecycle_events`, and `streak_session_daily_rollups`. Deleting a `restriction_session` cascades to `restriction_lifecycle_events` and `streak_session_daily_rollups`.

### Post-Sync Recomputation

After applying client changes, the server:
1. Recomputes `streak_daily_aggregates` for any days whose rollup data changed.
2. Refreshes `leaderboard_metrics` for the affected user.

---

## Synced Tables (Client ↔ Server)

All synced tables share these conventions:
- Partitioned by `user_id` (UUID FK → `users ON DELETE CASCADE`)
- `sync_version` (BIGINT) column with trigger-assigned values from `sync_version_seq`
- Timestamps stored as **BIGINT** (milliseconds since epoch) for client SQLite compatibility
- Booleans stored as **INTEGER** (0/1) for client SQLite compatibility
- Indexed on `(user_id, sync_version)` for efficient cursor queries

---

### modes

Focus/restriction mode templates that define how a blocking session behaves.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `user_id` | UUID | PK, FK → users ON DELETE CASCADE | Owner |
| `id` | TEXT | PK | Client-generated identifier |
| `title` | TEXT | NOT NULL | Display name |
| `text_on_screen` | TEXT | NOT NULL | Message shown during active session |
| `description` | TEXT | | Optional longer description |
| `allowed_pauses_count` | INTEGER | DEFAULT 0, >= 0 | How many pauses allowed per session |
| `minimum_duration_ms` | INTEGER | NULL or >= 1000 | Minimum session length; NULL = no minimum |
| `ending_pausing_scenario` | TEXT | CHECK IN ('nfc', 'qr', 'manual') | How the user ends/pauses the session |
| `icon_token` | TEXT | DEFAULT 'ms:v1:tune', non-empty | Icon identifier for the UI |
| `created_at` | BIGINT | | Epoch ms |
| `updated_at` | BIGINT | | Epoch ms |
| `sync_version` | BIGINT | DEFAULT nextval('sync_version_seq') | Replication cursor |

**Primary Key:** `(user_id, id)`
**Trigger:** `trg_modes_sync_version` (BEFORE INSERT OR UPDATE)
**Cascade:** Deleting a mode cascades to `mode_blocked_apps`, `schedules`, `restriction_sessions` (and transitively to `restriction_lifecycle_events`, `streak_session_daily_rollups`).

---

### mode_blocked_apps

Apps that are blocked when a specific mode is active. Platform-specific since app identifiers differ between Android and iOS.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `user_id` | UUID | PK, FK → users ON DELETE CASCADE | Owner |
| `mode_id` | TEXT | PK, FK → modes(user_id, id) ON DELETE CASCADE | Parent mode |
| `platform` | TEXT | PK, CHECK IN ('android', 'ios') | Target platform |
| `app_identifier` | TEXT | PK, non-empty | Bundle ID (iOS) or package name (Android) |
| `created_at` | BIGINT | | Epoch ms |
| `updated_at` | BIGINT | | Epoch ms |
| `sync_version` | BIGINT | DEFAULT nextval('sync_version_seq') | Replication cursor |

**Primary Key:** `(user_id, mode_id, platform, app_identifier)`
**Trigger:** `trg_mode_blocked_apps_sync_version` (BEFORE INSERT OR UPDATE)
**Note:** Composite primary key — sync deletions use JSON objects with all PK fields.

---

### schedules

Time-based triggers that automatically activate a mode on specific days and times.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `user_id` | UUID | PK, FK → users ON DELETE CASCADE | Owner |
| `id` | TEXT | PK | Client-generated identifier |
| `mode_id` | TEXT | FK → modes(user_id, id) ON DELETE CASCADE | Mode to activate |
| `days` | TEXT | non-empty | Comma-separated day codes: mon,tue,wed,thu,fri,sat,sun |
| `start_minute` | INTEGER | 0–1439 | Minute of day when mode activates |
| `end_minute` | INTEGER | 0–1439 | Minute of day when mode deactivates |
| `enabled` | INTEGER | 0 or 1 | Whether the schedule is active |
| `created_at` | BIGINT | | Epoch ms |
| `updated_at` | BIGINT | | Epoch ms |
| `sync_version` | BIGINT | DEFAULT nextval('sync_version_seq') | Replication cursor |

**Primary Key:** `(user_id, id)`
**Unique Constraint:** `(user_id, mode_id)` — one schedule per mode
**Trigger:** `trg_schedules_sync_version` (BEFORE INSERT OR UPDATE)

---

### restriction_sessions

Records of active or completed blocking sessions. A session ties a mode to a time window and tracks pauses and integrity.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `user_id` | UUID | PK, FK → users ON DELETE CASCADE | Owner |
| `session_id` | TEXT | PK | Client-generated identifier |
| `mode_id` | TEXT | FK → modes(user_id, id) ON DELETE CASCADE | Mode used |
| `source` | TEXT | CHECK IN ('manual', 'schedule') | How the session was started |
| `started_at` | BIGINT | NOT NULL | Epoch ms when session began |
| `ended_at` | BIGINT | NULL if ongoing, >= started_at | Epoch ms when session ended |
| `pause_count` | INTEGER | >= 0 | Number of times the user paused |
| `total_paused_ms` | INTEGER | >= 0 | Total time spent paused |
| `last_paused_at` | BIGINT | NULL | Epoch ms of most recent pause |
| `integrity_status` | TEXT | CHECK IN ('ok', 'anomaly') | Whether the session ran cleanly |
| `last_anomaly_reason` | TEXT | | Description of the anomaly if any |
| `last_event_id` | TEXT | | ID of the most recent lifecycle event |
| `created_at` | BIGINT | | Epoch ms |
| `updated_at` | BIGINT | | Epoch ms |
| `sync_version` | BIGINT | DEFAULT nextval('sync_version_seq') | Replication cursor |

**Primary Key:** `(user_id, session_id)`
**Indexes:** `idx_restriction_sessions_mode (user_id, mode_id)`, `idx_restriction_sessions_started (user_id, started_at DESC)`
**Trigger:** `trg_restriction_sessions_sync_version` (BEFORE INSERT OR UPDATE)
**Cascade:** Deleting a session cascades to `restriction_lifecycle_events` and `streak_session_daily_rollups`.

---

### restriction_lifecycle_events

Immutable audit trail of session state transitions. **Append-only** — client deletions are silently ignored by the server.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `user_id` | UUID | PK, FK → users ON DELETE CASCADE | Owner |
| `id` | TEXT | PK | Client-generated identifier |
| `session_id` | TEXT | FK → restriction_sessions(user_id, session_id) | Parent session |
| `mode_id` | TEXT | FK → modes(user_id, id) | Associated mode |
| `action` | TEXT | CHECK IN ('START', 'PAUSE', 'RESUME', 'END') | State transition type |
| `source` | TEXT | CHECK IN ('manual', 'schedule') | Trigger source |
| `reason` | TEXT | non-empty | Human-readable reason for the action |
| `occurred_at` | BIGINT | | Epoch ms when the action happened |
| `created_at` | BIGINT | | Epoch ms (used as version for replication) |
| `sync_version` | BIGINT | DEFAULT nextval('sync_version_seq') | Replication cursor |

**Primary Key:** `(user_id, id)`
**Indexes:** `idx_lifecycle_events_session (user_id, session_id)`, `idx_lifecycle_events_sync_version (user_id, sync_version)`
**Trigger:** `trg_restriction_lifecycle_events_sync_version` (**BEFORE INSERT ONLY** — never updated)
**Special:** No `updated_at` column. Conflict resolution uses `created_at`. Client deletions are ignored to preserve the audit trail.

---

### nfc_linked_chips

Physical NFC cards linked to a user for ending/pausing restriction sessions.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `user_id` | UUID | PK, FK → users ON DELETE CASCADE | Owner |
| `id` | TEXT | PK | Client-generated identifier |
| `chip_identifier` | TEXT | non-empty | Hardware identifier of the NFC chip |
| `name` | TEXT | | User-assigned name |
| `created_at` | BIGINT | | Epoch ms |
| `updated_at` | BIGINT | | Epoch ms |
| `sync_version` | BIGINT | DEFAULT nextval('sync_version_seq') | Replication cursor |

**Primary Key:** `(user_id, id)`
**Unique Constraint:** `(user_id, chip_identifier)`
**Trigger:** `trg_nfc_linked_chips_sync_version` (BEFORE INSERT OR UPDATE)

---

### qr_linked_codes

QR codes linked to a user for ending/pausing restriction sessions.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `user_id` | UUID | PK, FK → users ON DELETE CASCADE | Owner |
| `id` | TEXT | PK | Client-generated identifier |
| `scan_value` | TEXT | non-empty | The value encoded in the QR code |
| `name` | TEXT | | User-assigned name |
| `created_at` | BIGINT | | Epoch ms |
| `updated_at` | BIGINT | | Epoch ms |
| `sync_version` | BIGINT | DEFAULT nextval('sync_version_seq') | Replication cursor |

**Primary Key:** `(user_id, id)`
**Unique Constraint:** `(user_id, scan_value)`
**Trigger:** `trg_qr_linked_codes_sync_version` (BEFORE INSERT OR UPDATE)

---

### streak_session_daily_rollups

Per-day breakdown of effective focus time for each restriction session. A single session spanning midnight produces multiple rollup rows. Clients compute and upload these.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `user_id` | UUID | PK, FK → users ON DELETE CASCADE | Owner |
| `session_id` | TEXT | PK, FK → restriction_sessions(user_id, session_id) ON DELETE CASCADE | Parent session |
| `local_day` | TEXT | PK, exactly 10 chars (YYYY-MM-DD) | Calendar day in user's local timezone |
| `effective_ms` | INTEGER | >= 0 | Milliseconds of effective (non-paused) focus time |
| `updated_at` | BIGINT | | Epoch ms |
| `sync_version` | BIGINT | DEFAULT nextval('sync_version_seq') | Replication cursor |

**Primary Key:** `(user_id, session_id, local_day)`
**Trigger:** `trg_streak_session_daily_rollups_sync_version` (BEFORE INSERT OR UPDATE)
**Note:** Composite primary key — sync deletions use JSON objects with all PK fields. Changes to this table trigger recomputation of `streak_daily_aggregates`.

---

## Server-Derived Tables (Computed from Synced Data)

These tables are **populated exclusively by the server**. Client upserts and deletions are rejected or ignored. Clients receive them via the sync response as read-only data.

---

### streak_daily_aggregates

Aggregated daily focus metrics computed from `streak_session_daily_rollups`. Determines whether a day "qualifies" for a streak (>= 30 minutes of effective focus time).

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `user_id` | UUID | PK, FK → users ON DELETE CASCADE | Owner |
| `local_day` | TEXT | PK, exactly 10 chars (YYYY-MM-DD) | Calendar day |
| `effective_ms` | INTEGER | >= 0 | Total effective focus time across all sessions |
| `qualified` | INTEGER | 0 or 1 | 1 if `effective_ms >= 1,800,000` (30 minutes) |
| `source_session_count` | INTEGER | >= 0 | Number of distinct sessions contributing to this day |
| `updated_at` | BIGINT | | Epoch ms |
| `sync_version` | BIGINT | DEFAULT nextval('sync_version_seq') | Replication cursor |

**Primary Key:** `(user_id, local_day)`
**Indexes:** `idx_streak_aggregates_qualified (user_id, qualified, local_day)`
**Trigger:** `trg_streak_daily_aggregates_sync_version` (BEFORE INSERT OR UPDATE)

**How it's computed:**

After each sync request that modifies `streak_session_daily_rollups`, the server calls `RecomputeStreakAggregates()` for all affected days. For each day:

1. `SELECT SUM(effective_ms), COUNT(DISTINCT session_id) FROM streak_session_daily_rollups WHERE user_id = $1 AND local_day = $2`
2. If rows exist: `INSERT ... ON CONFLICT UPDATE` into `streak_daily_aggregates` with:
   - `effective_ms` = sum of all rollups for that day
   - `source_session_count` = count of distinct sessions
   - `qualified` = 1 if `effective_ms >= 1,800,000`, else 0
3. If no rollup rows exist for a day (all sessions deleted): delete the aggregate row and create a tombstone in `sync_tombstones`

**Sync behavior:** Included in the sync response like other tables but client writes are rejected. The server returns computed state only.

---

### leaderboard_metrics

Denormalized user statistics for leaderboard queries. **Not synced** to clients — used only by server-side leaderboard endpoints.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `user_id` | UUID | PK, FK → users ON DELETE CASCADE | Owner |
| `current_streak_days` | INTEGER | DEFAULT 0 | Consecutive qualified days up to today |
| `total_focus_time_ms` | BIGINT | DEFAULT 0 | Lifetime effective focus time |
| `updated_at` | BIGINT | DEFAULT 0 | Epoch ms of last refresh |

**Primary Key:** `user_id`

**How it's computed:**

After each sync, the server calls `RefreshLeaderboardMetrics()` for the affected user:

1. **current_streak_days**: Count consecutive `qualified = 1` days in `streak_daily_aggregates` going backwards from yesterday (today is not counted since it may still be in progress).
2. **total_focus_time_ms**: `SUM(effective_ms)` across all rows in `streak_daily_aggregates` for the user.

Used by the friends leaderboard endpoints — only users with `leaderboard_visible = TRUE` appear.

---

## Backend-Only Tables

These tables are never included in the sync protocol. They support authentication, authorization, social features, push notifications, and sync infrastructure.

---

### users

Core user accounts. Every other table references `users` via `user_id`.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PK | Auto-generated |
| `email` | TEXT | NOT NULL, UNIQUE (case-insensitive) | Login identifier |
| `name` | TEXT | DEFAULT '' | Display name |
| `username` | TEXT | UNIQUE (case-insensitive) | Public handle |
| `profile_picture_url` | TEXT | | Avatar URL |
| `push_enabled` | BOOLEAN | DEFAULT TRUE | Whether to send push notifications |
| `leaderboard_visible` | BOOLEAN | DEFAULT TRUE | Whether to show on friends' leaderboards |
| `created_at` | TIMESTAMPTZ | DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | DEFAULT now() | |

**Indexes:** `idx_users_email (lower(email))`, `idx_users_username (lower(username))`

---

### otp_codes

One-time passwords for passwordless authentication and account deletion confirmation.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PK | Auto-generated |
| `email` | TEXT | NOT NULL | Target email address |
| `user_id` | UUID | FK → users ON DELETE CASCADE | Associated user (NULL for first-time login) |
| `code_hash` | TEXT | NOT NULL | Bcrypt hash of the OTP code |
| `purpose` | TEXT | CHECK IN ('auth_login', 'account_deletion') | What the OTP is for |
| `expires_at` | TIMESTAMPTZ | NOT NULL | When the code becomes invalid |
| `used` | BOOLEAN | DEFAULT FALSE | Whether the code has been consumed |
| `created_at` | TIMESTAMPTZ | DEFAULT now() | |

**Indexes:** `idx_otp_codes_email_purpose (lower(email), purpose, used, expires_at)`, `idx_otp_codes_expires_at`
**Constraint:** At most one unused OTP per `(email, purpose)` pair — issuing a new one invalidates the previous.

---

### otp_failed_attempts

Rate-limiting tracker for OTP verification attempts. Enforces a maximum of 3 attempts per 10-minute window per email.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PK | Auto-generated |
| `email` | TEXT | NOT NULL | Attempted email |
| `user_id` | UUID | FK → users ON DELETE CASCADE | Associated user |
| `purpose` | TEXT | CHECK IN ('auth_login', 'account_deletion') | OTP purpose |
| `attempted_at` | TIMESTAMPTZ | DEFAULT now() | When the failed attempt occurred |

**Indexes:** `idx_otp_failed_attempts_email_purpose_attempted_at (lower(email), purpose, attempted_at)`
**Note:** Separate from `otp_codes` so that rate limits apply across OTP re-issuance.

---

### auth_sessions

Groups related refresh tokens into a single session. Revoking a session invalidates all its tokens (token theft detection).

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PK | Auto-generated |
| `user_id` | UUID | FK → users ON DELETE CASCADE, NOT NULL | Session owner |
| `revoked` | BOOLEAN | DEFAULT FALSE | Whether the session has been revoked |
| `revoked_at` | TIMESTAMPTZ | | When revocation occurred |
| `expires_at` | TIMESTAMPTZ | NOT NULL | Session expiry (30 days) |
| `created_at` | TIMESTAMPTZ | DEFAULT now() | |

**Indexes:** `idx_auth_sessions_user (user_id, revoked)`, `idx_auth_sessions_expires_at`, `idx_auth_sessions_revoked_at (WHERE revoked = true)`
**Lifetime:** 30 days, matching refresh token lifetime.

---

### refresh_tokens

Opaque refresh tokens for JWT access token rotation. Tokens are hashed before storage.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PK | Auto-generated |
| `user_id` | UUID | FK → users ON DELETE CASCADE, NOT NULL | Token owner |
| `session_id` | UUID | FK → auth_sessions ON DELETE CASCADE, NOT NULL | Parent session |
| `token_hash` | TEXT | NOT NULL, UNIQUE | SHA-256 hash of the token value |
| `expires_at` | TIMESTAMPTZ | NOT NULL | Token expiry (720 hours) |
| `revoked` | BOOLEAN | DEFAULT FALSE | Whether the token has been revoked |
| `revoked_at` | TIMESTAMPTZ | | When revocation occurred |
| `created_at` | TIMESTAMPTZ | DEFAULT now() | |

**Indexes:** `idx_refresh_tokens_user (user_id, revoked)`, `idx_refresh_tokens_expires_at`, `idx_refresh_tokens_revoked_at (WHERE revoked = true)`, `idx_refresh_tokens_session (session_id)`
**Security:** Reusing a revoked refresh token triggers revocation of the entire parent session (token theft detection).

---

### admin_credentials

Admin panel authentication. Passwords are bcrypt-hashed. Seeded via `cmd/seed-admin`.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PK | Auto-generated |
| `username` | TEXT | NOT NULL, UNIQUE | Admin login name |
| `password_hash` | TEXT | NOT NULL | Bcrypt hash |
| `created_at` | TIMESTAMPTZ | DEFAULT now() | |

---

### user_entitlements

Subscription state snapshot populated by RevenueCat webhooks. Used for server-side authorization checks.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PK | Auto-generated |
| `user_id` | UUID | FK → users ON DELETE CASCADE, NOT NULL | Subscriber |
| `entitlement` | TEXT | NOT NULL, non-empty | Entitlement name (e.g., "premium") |
| `is_active` | BOOLEAN | DEFAULT FALSE | Whether the entitlement is currently active |
| `revenuecat_app_user_id` | TEXT | | Current RC app user ID |
| `revenuecat_original_app_user_id` | TEXT | | Original RC app user ID (for transfers) |
| `current_period_end` | TIMESTAMPTZ | | End of current billing period |
| `last_webhook_event_at` | TIMESTAMPTZ | | Timestamp of last webhook update |
| `created_at` | TIMESTAMPTZ | DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | DEFAULT now() | |

**Indexes:** `idx_user_entitlements_user_active`, `idx_user_entitlements_entitlement_active`, `idx_user_entitlements_rc_app_user_id`, `idx_user_entitlements_rc_original_app_user_id`
**Unique Constraint:** `(user_id, entitlement)`
**Note:** Currently only the "premium" entitlement exists. The schema supports future tiers.

---

### admin_entitlement_overrides

Manual admin grants/revokes that take precedence over RevenueCat state during authorization checks.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PK | Auto-generated |
| `user_id` | UUID | FK → users ON DELETE CASCADE, NOT NULL | Target user |
| `entitlement` | TEXT | NOT NULL, non-empty | Entitlement name |
| `action` | TEXT | CHECK IN ('grant', 'revoke') | Override action |
| `expires_at` | TIMESTAMPTZ | | NULL for permanent overrides |
| `created_at` | TIMESTAMPTZ | DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | DEFAULT now() | |

**Indexes:** `idx_admin_entitlement_overrides_user`, `idx_admin_entitlement_overrides_active (entitlement, action, expires_at)`
**Unique Constraint:** `(user_id, entitlement)`
**Precedence:** `admin_entitlement_overrides.action/expires_at` → `user_entitlements.is_active`. An admin override always wins.

---

### friendships

Social connections between users. Supports pending requests and accepted friendships.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PK | Auto-generated |
| `requester_id` | UUID | FK → users ON DELETE CASCADE, NOT NULL | User who sent the request |
| `addressee_id` | UUID | FK → users ON DELETE CASCADE, NOT NULL | User who received the request |
| `status` | TEXT | CHECK IN ('pending', 'accepted') | Friendship state |
| `created_at` | TIMESTAMPTZ | DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | DEFAULT now() | |

**Indexes:** `idx_friendships_requester (requester_id, status)`, `idx_friendships_addressee (addressee_id, status)`
**Unique Constraint:** `UNIQUE (LEAST(requester_id, addressee_id), GREATEST(requester_id, addressee_id))` — one friendship per unordered user pair
**Constraint:** `requester_id != addressee_id`
**Lifecycle:** Hard-deleted on decline, cancel, or remove. Most friendship endpoints require premium.

---

### device_tokens

FCM device registrations for push notifications.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PK | Auto-generated |
| `user_id` | UUID | FK → users ON DELETE CASCADE, NOT NULL | Device owner |
| `fcm_token` | TEXT | NOT NULL, UNIQUE | Firebase Cloud Messaging token |
| `platform` | TEXT | CHECK IN ('android', 'ios') | Device platform |
| `created_at` | TIMESTAMPTZ | DEFAULT now() | |
| `updated_at` | TIMESTAMPTZ | DEFAULT now() | |

**Indexes:** `idx_device_tokens_user`
**Note:** If the same FCM token is registered by a different user, it's reassigned to the new user.

---

### sync_tombstones

Deletion history for incremental sync replication. When a synced record is deleted, a tombstone is created so that future sync responses can inform clients.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PK | Auto-generated |
| `user_id` | UUID | FK → users ON DELETE CASCADE, NOT NULL | Record owner |
| `table_name` | TEXT | NOT NULL | Which synced table the deleted record belonged to |
| `record_id` | TEXT | NOT NULL | Primary key of the deleted record (JSON for composites) |
| `deleted_at` | TIMESTAMPTZ | DEFAULT now() | When the deletion occurred |
| `sync_version` | BIGINT | DEFAULT nextval('sync_version_seq') | Replication cursor |

**Indexes:** `idx_sync_tombstones_user_time (user_id, deleted_at)`, `idx_sync_tombstones_user_cursor (user_id, table_name, sync_version)`
**Unique Constraint:** `(user_id, table_name, record_id)`
**Trigger:** `trg_sync_tombstones_sync_version` (BEFORE INSERT)
**Lifecycle:** Subject to a configurable retention window. Tombstones are cleared if the record is resurrected (re-created).

---

## Replication Responsibility Matrix

| Table | In Sync Protocol | Client Can Write | Server-Derived | Notes |
|-------|:-:|:-:|:-:|-------|
| `modes` | Yes | Yes | No | PK: (user_id, id) |
| `mode_blocked_apps` | Yes | Yes | No | Composite PK |
| `schedules` | Yes | Yes | No | One per mode |
| `restriction_sessions` | Yes | Yes | No | PK: (user_id, session_id) |
| `restriction_lifecycle_events` | Yes | Insert only | No | Append-only; deletions ignored |
| `nfc_linked_chips` | Yes | Yes | No | PK: (user_id, id) |
| `qr_linked_codes` | Yes | Yes | No | PK: (user_id, id) |
| `streak_session_daily_rollups` | Yes | Yes | No | Composite PK; triggers aggregate recomputation |
| `streak_daily_aggregates` | Yes | **No** (read-only) | **Yes** | Recomputed from rollups |
| `leaderboard_metrics` | No | No | **Yes** | Refreshed after sync; server-only |
| `sync_tombstones` | Internal | No | No | Deletion history for sync protocol |
| `users` | No | No | No | Account metadata |
| `otp_codes` | No | No | No | Auth flow |
| `otp_failed_attempts` | No | No | No | Rate limiting |
| `auth_sessions` | No | No | No | Session management |
| `refresh_tokens` | No | No | No | Token rotation |
| `admin_credentials` | No | No | No | Admin auth |
| `user_entitlements` | No | No | No | RevenueCat webhooks |
| `admin_entitlement_overrides` | No | No | No | Admin manual overrides |
| `friendships` | No | No | No | Social feature |
| `device_tokens` | No | No | No | Push notifications |
