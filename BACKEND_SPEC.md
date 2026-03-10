# Pauza Backend Specification

## Table of Contents

1. [Overview](#1-overview)
2. [Authentication & Account Management](#2-authentication--account-management)
3. [Database Schema](#3-database-schema)
4. [Replication Protocol](#4-replication-protocol)
5. [REST API Endpoints](#5-rest-api-endpoints)
6. [Subscription System](#6-subscription-system)
7. [Friendships](#7-friendships)
8. [Leaderboard](#8-leaderboard)
9. [Push Notifications](#9-push-notifications)
10. [Rate Limiting](#10-rate-limiting)
11. [Error Handling](#11-error-handling)
12. [Deployment](#12-deployment)
13. [Out of Scope](#13-out-of-scope)

---

## 1. Overview

### 1.1 Purpose

The Pauza backend provides server-side infrastructure for the Pauza digital wellbeing mobile application. Its primary responsibilities are:

This specification is a target design document for the intended backend
contract.

- **Passwordless user authentication** (email OTP challenge, verification, session refresh)
- **Client-server replication** between the client's local SQLite database and the server's PostgreSQL database
- **Subscription management** (RevenueCat webhook ingestion, entitlement enforcement)
- **Social features** (friendships, shared stats)
- **Leaderboards** (streak-based and focus-time-based rankings)
- **Push notifications** via Firebase Cloud Messaging
- **Admin panel API** for user management and platform analytics

### 1.2 Tech Stack

| Component | Technology |
|---|---|
| Language | Go |
| Database | PostgreSQL |
| Authentication | JWT (access + refresh tokens) |
| In-App Purchases | RevenueCat (webhook + API verification) |
| Push Notifications | Firebase Cloud Messaging (Admin SDK) |
| Containerization | Docker / Docker Compose |
| DB Migrations | golang-migrate |

### 1.3 Architecture Principles

- **Offline-first**: The mobile client's local SQLite database is the source of truth during normal operation. The backend stores a replica for restore/bootstrap and server-side features.
- **Incremental replication**: The client replicates per-table changes with `last_synced_at` cursors. `last_synced_at = 0` requests a restore/bootstrap snapshot for that table.
- **Timestamp-based record versioning**: Replicated records carry client timestamps so the backend can deterministically apply newer client state and return newer server-side changes.
- **Single-device model**: One account is expected to be active on one device at a time. Concurrent active use across devices is unsupported and may result in stale restores or overwritten backup state.
- **Subscription enforcement on both client and server**: The client hides premium UI features, and the server rejects requests to premium-only endpoints for non-subscribers.

---

## 2. Authentication & Account Management

### 2.1 Passwordless Authentication Flow

```
Client                          Server
  |                               |
  |  POST /auth/start             |
  |  { email }                    |
  |------------------------------>|
  |                               |  Generate 6-digit OTP and send email
  |                               |  Invalidate any older unused auth_login
  |                               |  OTPs for the same email
  |                               |  If email is new, account creation is
  |                               |  deferred until OTP verification
  |                               |  If email already exists, return the same
  |                               |  generic response
  |  200 { otp_required: true }   |    without confirming account existence
  |<------------------------------|
  |                               |
  |  POST /auth/verify            |
  |  { email, otp }               |
  |------------------------------>|
  |                               |  Verify latest valid auth_login OTP
  |                               |  If email is new: create account
  |                               |  Issue JWT access + refresh tokens
  |  200 { access_token,          |
  |        refresh_token, user }  |
  |<------------------------------|
```

- OTP codes are 6-digit numeric, valid for 10 minutes, single-use, and scoped by `(email, purpose)`.
- Issuing a new OTP for the same `(email, purpose)` invalidates all older unused OTPs for that pair.
- A maximum of 3 OTP verification attempts are allowed per email per 10-minute window.
- Auth entry points should not reveal whether the email already exists.
- Passwords are not used. Email possession is the primary authentication and
  recovery factor.

### 2.2 Account Provisioning

- A new `users` row is created only after successful OTP verification.
- Re-verifying the same email signs the user in rather than creating a second account.

### 2.3 Token Strategy

| Token | Lifetime | Storage |
|---|---|---|
| Access token (JWT) | 15 minutes | Client memory / secure storage |
| Refresh token (opaque) | 30 days | Server-side in `refresh_tokens` table |

**Access token JWT claims:**

```json
{
  "sub": "<user_id>",
  "email": "<email>",
  "iat": 1700000000,
  "exp": 1700000900
}
```

**Refresh token flow:**

```
Client                          Server
  |                               |
  |  POST /auth/refresh           |
  |  { refresh_token }            |
  |------------------------------>|
  |                               |  Validate token exists, not revoked,
  |                               |  not expired. Rotate: revoke old token,
  |                               |  issue new access + refresh tokens.
  |  200 { access_token,          |
  |        refresh_token }        |
  |<------------------------------|
```

- Refresh tokens are stored as **hashed values** (SHA-256) in the database.
- On each refresh, the old token is revoked and a new pair is issued (token rotation).
- If a revoked refresh token is reused, **all refresh tokens for that user are revoked** (indicates token theft).
- Refresh-token rows will grow over time; cleanup is a later operational concern. A periodic cleanup job should eventually delete expired rows and revoked rows based on revocation time.

### 2.4 Recovery Model

- Because authentication is passwordless, there is no password reset flow.
- Account recovery is equivalent to proving control of the email address.
- If a user can receive and verify a valid login OTP, they can regain access to
  their account.
- If a user loses access to their email inbox, they lose access to the account
  unless another recovery mechanism is introduced in a future revision.

### 2.5 Account Deletion

Account deletion is a two-step flow:

1. `POST /api/v1/me/delete/request` sends a one-time deletion OTP to the
   authenticated user's email address and invalidates any older unused
   `account_deletion` OTPs for that email.
2. `POST /api/v1/me/delete/confirm` verifies the OTP and permanently deletes the
   user account and all associated data.

`POST /api/v1/me/delete/request` does not revoke the current access token or
refresh tokens. `POST /api/v1/me/delete/confirm` deletes the account and, by
cascade, invalidates all active sessions and refresh tokens. This is
irreversible.

---

## 3. Database Schema

All timestamps are stored as `TIMESTAMPTZ` (PostgreSQL timestamp with time zone). UUIDs use `UUID` type with `gen_random_uuid()` as default.

### 3.1 Backend-Only Tables

#### `users`

```sql
CREATE TABLE users (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email               TEXT NOT NULL,
  name                TEXT NOT NULL DEFAULT '',
  username            TEXT NOT NULL UNIQUE,
  profile_picture_url TEXT,
  leaderboard_visible BOOLEAN NOT NULL DEFAULT TRUE,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_users_email ON users (lower(email));
CREATE UNIQUE INDEX idx_users_username ON users (lower(username));
```

#### `otp_codes`

```sql
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
CREATE INDEX idx_otp_codes_expires_at ON otp_codes (expires_at);
```

At most one unused OTP should remain active for a given `(email, purpose)`.
Issuing a new OTP for that pair invalidates older unused rows before the new OTP
is stored.

#### `otp_failed_attempts`

Tracks failed OTP submissions for rate limiting and lockout windows. Failed
attempts are modeled separately from `otp_codes` so the verification budget
applies across OTP re-issuance, not just a single code row.

```sql
CREATE TABLE otp_failed_attempts (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email        TEXT NOT NULL,
  user_id      UUID REFERENCES users(id) ON DELETE CASCADE,
  purpose      TEXT NOT NULL CHECK (purpose IN ('auth_login', 'account_deletion')),
  attempted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_otp_failed_attempts_email_purpose_attempted_at
  ON otp_failed_attempts (lower(email), purpose, attempted_at);
```

#### `refresh_tokens`

```sql
CREATE TABLE refresh_tokens (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  revoked    BOOLEAN NOT NULL DEFAULT FALSE,
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens (user_id, revoked);
CREATE INDEX idx_refresh_tokens_expires_at ON refresh_tokens (expires_at);
CREATE INDEX idx_refresh_tokens_revoked_at
    ON refresh_tokens (revoked_at) WHERE revoked = true;
```

#### `admin_credentials`

```sql
CREATE TABLE admin_credentials (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

#### `user_entitlements`

```sql
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
```

This table stores the backend's derived entitlement snapshot for authorization. The current product model uses a single entitlement value, `premium`, but the schema permits one row per user + entitlement if additional entitlements are introduced later. When RevenueCat-backed entitlements are reconciled, `revenuecat_app_user_id` and `revenuecat_original_app_user_id` store the canonical identifiers returned for the current customer record at the time of reconciliation. They are not intended to preserve every historical alias or transfer path; alias resolution happens during reconciliation against RevenueCat's current customer state.

#### `friendships`

```sql
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
```

#### `device_tokens`

```sql
CREATE TABLE device_tokens (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  fcm_token  TEXT NOT NULL UNIQUE,
  platform   TEXT NOT NULL CHECK (platform IN ('android', 'ios')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_device_tokens_user ON device_tokens (user_id);
```

### 3.2 Replicated Tables

These tables mirror the client's local SQLite schema. Each table gains a `user_id` column as a foreign key to `users`. Column types are adapted from SQLite to PostgreSQL:

- `INTEGER` timestamps (milliseconds since epoch in SQLite) become `BIGINT` in PostgreSQL (preserving the client's format for replication compatibility).
- `TEXT` remains `TEXT`.
- `INTEGER` flags (0/1) remain `INTEGER` (not `BOOLEAN`) to match client format.

#### `modes`

```sql
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
```

#### `mode_blocked_apps`

```sql
CREATE TABLE mode_blocked_apps (
  user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  mode_id        TEXT NOT NULL,
  platform       TEXT NOT NULL CHECK (platform IN ('android', 'ios')),
  app_identifier TEXT NOT NULL,
  created_at     BIGINT NOT NULL,
  updated_at     BIGINT NOT NULL,

  PRIMARY KEY (user_id, mode_id, platform, app_identifier)
);
```

#### `schedules`

```sql
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
  UNIQUE (user_id, mode_id)
);
```

#### `restriction_sessions`

```sql
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

  PRIMARY KEY (user_id, session_id)
);

CREATE INDEX idx_restriction_sessions_mode ON restriction_sessions (user_id, mode_id);
CREATE INDEX idx_restriction_sessions_started ON restriction_sessions (user_id, started_at DESC);
```

#### `restriction_lifecycle_events`

```sql
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

  PRIMARY KEY (user_id, id)
);

CREATE INDEX idx_lifecycle_events_session ON restriction_lifecycle_events (user_id, session_id);
```

#### `nfc_linked_chips`

```sql
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
```

#### `qr_linked_codes`

```sql
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
```

#### `streak_session_daily_rollups`

```sql
CREATE TABLE streak_session_daily_rollups (
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_id   TEXT NOT NULL,
  local_day    TEXT NOT NULL CHECK (length(local_day) = 10),
  effective_ms INTEGER NOT NULL DEFAULT 0 CHECK (effective_ms >= 0),
  updated_at   BIGINT NOT NULL,

  PRIMARY KEY (user_id, session_id, local_day)
);
```

#### `streak_daily_aggregates`

```sql
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
```

### 3.3 Tables NOT Replicated

| Client Table | Reason |
|---|---|
| `streak_rollup_state` | Client-only processing cursor. Tracks which sessions have been rolled up locally. Has no meaning on the server. |

---

## 4. Replication Protocol

### 4.1 Overview

The protocol uses a single endpoint (`POST /api/v1/sync`) for incremental
replication between the active client and the backend replica.

For each replicated table, the client sends:

- `last_synced_at`: the last server cursor the client has fully applied for that table
- `upserts`: records created or updated locally since that cursor
- `deletions`: primary keys deleted locally since that cursor

The server applies the client changes transactionally, then returns server
changes newer than `last_synced_at` for the same table. `last_synced_at = 0`
requests a restore/bootstrap snapshot for that table.

This is not a general-purpose multi-master sync engine. The product model is
still single-device, but the wire protocol is incremental in both directions so
the backend can serve restore/bootstrap and server-side features from the same
replicated state.

### 4.2 Replication Request

The client sends:

```json
{
  "tables": {
    "modes": {
      "last_synced_at": 1700000000000,
      "upserts": [
        {
          "id": "uuid-1",
          "title": "Focus Mode",
          "text_on_screen": "Stay focused!",
          "description": null,
          "allowed_pauses_count": 2,
          "minimum_duration_ms": 3600000,
          "ending_pausing_scenario": "manual",
          "icon_token": "ms:v1:work",
          "created_at": 1700000000000,
          "updated_at": 1700000500000
        }
      ],
      "deletions": ["uuid-3", "uuid-7"]
    },
    "mode_blocked_apps": {
      "last_synced_at": 1700000000000,
      "upserts": [ ... ],
      "deletions": [
        { "mode_id": "uuid-1", "platform": "android", "app_identifier": "com.example.app" }
      ]
    }
  }
}
```

**Field definitions:**

| Field | Description |
|---|---|
| `last_synced_at` | Milliseconds-since-epoch server cursor for that table. `0` requests restore/bootstrap for the table. The client advances this value only after a successful response has been fully applied locally. |
| `upserts` | Array of records created or updated locally since the client's current cursor for that table. |
| `deletions` | Array of primary key values for records deleted locally since the client's current cursor for that table. For single-column PKs this is an array of strings. For composite PKs this is an array of objects with all PK fields. |

If a request fails or the client does not receive the response, the client may
retry with the same `last_synced_at`, `upserts`, and `deletions`. Duplicate
upserts are safe because records are keyed by primary key and compared by record
version (`updated_at`, or `created_at` where no `updated_at` exists). Duplicate
deletions are safe because deleting a missing row is a no-op.

### 4.3 Replication Response

The server responds with:

```json
{
  "server_time": 1700001000000,
  "tables": {
    "modes": {
      "upserts": [
        {
          "id": "uuid-2",
          "title": "Sleep Mode",
          "..."
        }
      ],
      "deletions": []
    },
    "mode_blocked_apps": {
      "upserts": [ ... ],
      "deletions": []
    }
  }
}
```

**Field definitions:**

| Field | Description |
|---|---|
| `server_time` | The server's current time in milliseconds-since-epoch. The client stores this as the next cursor after it has successfully applied the full response. |
| `upserts` | Server records for that table whose server-side change time is newer than `last_synced_at`. When `last_synced_at = 0`, this is the full server snapshot for the table. |
| `deletions` | Server tombstones for that table whose deletion time is newer than `last_synced_at`. These are part of normal replication responses, including after bootstrap if the retention window includes matching tombstones. |

### 4.4 Replication Processing (Server-Side)

For each table in the request, the server performs the following steps **within
a single database transaction**:

1. **Process client upserts**: For each record in the client's `upserts` array:
   - Look up the existing server record by primary key.
   - If no server record exists: insert the client record.
   - If a server record exists and the client's `updated_at` > server's `updated_at`: update with client data.
   - If a server record exists and the client's `updated_at` <= server's `updated_at`: keep the server copy unchanged.

2. **Process client deletions**: For each primary key in the client's
   `deletions` array:
   - Delete the corresponding server record if it exists.
   - Record a tombstone so later clients or restore/bootstrap flows can observe
     that deletion until tombstone retention expires.

3. **Gather server data for response**:
   - If `last_synced_at = 0` for a table, return the full current server
     snapshot for that table.
   - Also return all tombstones for that table newer than `last_synced_at`.
   - Otherwise, return all server upserts and tombstones newer than
     `last_synced_at`.

4. **Return the response**.

The server response is the authoritative result of that replication cycle. The
client should update its local cursor for a table only after applying both
`upserts` and `deletions` from the response.

### 4.5 Replicated Tables Reference

| Table | Primary Key (for replication) | Notes |
|---|---|---|
| `modes` | `id` | Delete payload is the record ID string |
| `mode_blocked_apps` | `(mode_id, platform, app_identifier)` | Composite PK |
| `schedules` | `id` | Delete payload is the record ID string |
| `restriction_sessions` | `session_id` | Delete payload is the session ID string |
| `restriction_lifecycle_events` | `id` | No `updated_at` column; use `created_at` as the record version when comparing uploads |
| `nfc_linked_chips` | `id` | Delete payload is the record ID string |
| `qr_linked_codes` | `id` | Delete payload is the record ID string |
| `streak_session_daily_rollups` | `(session_id, local_day)` | Composite PK |
| `streak_daily_aggregates` | `local_day` | Delete payload is the local day string |

### 4.6 Restore Semantics

When `last_synced_at = 0` for a table:

- The client requests the server's stored snapshot for that table.
- The client may still include local `upserts` and `deletions`; the server
  applies them before building the response.
- The server returns all records it currently stores for that user and table,
  plus tombstones newer than the retained deletion horizon.

This enables restoring data on a new device: the client sends
`last_synced_at = 0` with empty `upserts` and empty `deletions`, then rebuilds
its local SQLite tables from the returned snapshot.

### 4.7 Ongoing Replication Semantics

After bootstrap, the client continues to use the same replication shape:

- the client sends `last_synced_at`, `upserts`, and `deletions`,
- the backend applies the client changes and returns any newer server upserts
  and tombstones,
- the client applies the response and then advances its cursor to `server_time`.

If there are no newer server changes for a table, the response arrays for that
table may be empty.

### 4.8 Failure Mode

If a client change is applied locally but never successfully replicated, the
backend replica will lag behind the device. A later restore/bootstrap on another
device may therefore reconstruct older state. The client should retry failed
replication requests with the same cursor and payload until one succeeds.

---

## 5. REST API Endpoints

All endpoints are prefixed with `/api/v1` unless otherwise noted.

**Common headers:**

| Header | Value | Required |
|---|---|---|
| `Content-Type` | `application/json` | All requests with a body |
| `Authorization` | `Bearer <access_token>` | All authenticated endpoints |

### 5.1 Authentication

#### `POST /api/v1/auth/start`

Start a passwordless authentication challenge.

**Request:**

```json
{
  "email": "user@example.com"
}
```

**Validation:**

- `email`: valid email format, max 255 characters, case-insensitive (stored lowercase).

**Responses:**

| Status | Body | Condition |
|---|---|---|
| `200` | `{ "otp_required": true }` | Request accepted; response does not confirm whether the email was newly registered or already exists |
| `422` | `{ "error": { "code": "VALIDATION_ERROR", ... } }` | Invalid input |

#### `POST /api/v1/auth/verify`

Verify the passwordless email OTP and sign the user in.

**Request:**

```json
{
  "email": "user@example.com",
  "otp": "123456"
}
```

**Responses:**

| Status | Body | Condition |
|---|---|---|
| `200` | `{ "access_token": "...", "refresh_token": "...", "user": { ... } }` | OTP valid; existing account signed in or new account created and signed in |
| `401` | `{ "error": { "code": "UNAUTHORIZED", "message": "Invalid or expired OTP" } }` | Wrong/expired OTP |
| `429` | `{ "error": { "code": "RATE_LIMITED", ... } }` | Too many attempts |

The `user` object in the response follows the profile format described in [Section 5.3](#53-profile).

#### `POST /api/v1/auth/refresh`

Exchange a refresh token for a new token pair.

**Request:**

```json
{
  "refresh_token": "opaque-token-string"
}
```

**Responses:**

| Status | Body | Condition |
|---|---|---|
| `200` | `{ "access_token": "...", "refresh_token": "..." }` | Token valid |
| `401` | `{ "error": { "code": "UNAUTHORIZED", ... } }` | Token invalid/revoked/expired |


### 5.2 Replication

#### `POST /api/v1/sync`

**Auth:** Required (JWT).

Replication endpoint. See [Section 4](#4-replication-protocol) for full protocol details.

**Request:** See [Section 4.2](#42-replication-request).

**Response:** See [Section 4.3](#43-replication-response).

| Status | Body | Condition |
|---|---|---|
| `200` | Replication response payload | Success |
| `401` | Error | Unauthorized |
| `422` | Error | Malformed replication payload |

### 5.3 Profile

#### `GET /api/v1/me`

Returns the authenticated user's profile and current subscription status.

**Auth:** Required (JWT).

**Response (`200`):**

```json
{
  "id": "uuid",
  "email": "user@example.com",
  "name": "John Doe",
  "username": "johndoe",
  "profile_picture_url": "https://api.example.com/photos/uuid.jpg",
  "leaderboard_visible": true,
  "created_at": "2024-01-15T10:30:00Z",
  "subscription": {
    "entitlement": "premium",
    "is_active": true,
    "current_period_end": "2024-02-15T10:30:00Z"
  }
}
```

If the user has no stored premium entitlement snapshot, `subscription` is `null`.

#### `PATCH /api/v1/me`

Update profile fields.

**Auth:** Required (JWT).

**Request:**

```json
{
  "name": "Jane Doe",
  "username": "janedoe",
  "leaderboard_visible": false
}
```

All fields are optional. Only provided fields are updated.

**Validation:**

- `name`: max 100 characters.
- `username`: 3-30 characters, alphanumeric + underscores only, case-insensitive unique.
- `leaderboard_visible`: boolean.

**Responses:**

| Status | Body | Condition |
|---|---|---|
| `200` | Updated user object (same format as `GET /me`) | Success |
| `409` | Error with code `CONFLICT` | Username already taken |
| `422` | Error with code `VALIDATION_ERROR` | Invalid input |

#### `POST /api/v1/me/photo`

Upload a profile photo.

**Auth:** Required (JWT).

**Request:** `multipart/form-data` with a single `photo` field. Accepted formats: JPEG, PNG. Max size: 5 MB.

**Response (`200`):**

```json
{
  "profile_picture_url": "https://api.example.com/photos/uuid.jpg"
}
```

#### `GET /api/v1/me/username-available`

Check if a username is available.

**Auth:** Required (JWT).

**Query parameters:** `username` (required).

**Response (`200`):**

```json
{
  "available": true
}
```

#### `POST /api/v1/me/delete/request`

Send an account-deletion OTP to the authenticated user's email address.

**Auth:** Required (JWT).

Issuing a new deletion OTP invalidates any older unused `account_deletion` OTPs
for the same email. This request does not revoke the current access token or any
refresh tokens.

**Response (`200`):**

```json
{
  "message": "If the account is eligible for deletion, a confirmation code has been sent."
}
```

#### `POST /api/v1/me/delete/confirm`

Permanently delete the user account and all associated data.

**Auth:** Required (JWT).

On success, the user row and all dependent data are deleted. All active sessions
and refresh tokens become invalid as a consequence of account deletion.

**Request:**

```json
{
  "otp": "123456"
}
```

**Responses:**

| Status | Body | Condition |
|---|---|---|
| `200` | `{ "message": "Account deleted" }` | Success |
| `401` | Error | Wrong/expired OTP |

### 5.4 Friendships

All friendship endpoints require an active premium subscription. Returns `403` with error code `SUBSCRIPTION_REQUIRED` if the user is on the free tier.

#### `GET /api/v1/friends`

List accepted friends.

**Auth:** Required (JWT). **Subscription:** Premium.

**Query parameters:** `page` (default 1), `limit` (default 20, max 100).

**Response (`200`):**

```json
{
  "friends": [
    {
      "friendship_id": "uuid",
      "user": {
        "id": "uuid",
        "name": "Jane Doe",
        "username": "janedoe",
        "profile_picture_url": "https://..."
      },
      "since": "2024-01-20T15:00:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 5
  }
}
```

#### `POST /api/v1/friends/request`

Send a friendship request.

**Auth:** Required (JWT). **Subscription:** Premium.

**Request:**

```json
{
  "username": "janedoe"
}
```

The `username` field is matched against username only (exact, case-insensitive).

**Responses:**

| Status | Body | Condition |
|---|---|---|
| `201` | `{ "friendship_id": "uuid", "status": "pending" }` | Request sent |
| `404` | Error | User not found |
| `409` | Error with code `CONFLICT` | Request already exists or already friends |
| `422` | Error | Trying to add self |

Triggers a push notification to the addressee (see [Section 9](#9-push-notifications)).

#### `GET /api/v1/friends/requests/incoming`

List pending friendship requests received by the current user.

**Auth:** Required (JWT). **Subscription:** Premium.

**Response (`200`):**

```json
{
  "requests": [
    {
      "friendship_id": "uuid",
      "from": {
        "id": "uuid",
        "name": "John Doe",
        "username": "johndoe",
        "profile_picture_url": "https://..."
      },
      "created_at": "2024-01-20T15:00:00Z"
    }
  ]
}
```

#### `GET /api/v1/friends/requests/outgoing`

List pending friendship requests sent by the current user.

**Auth:** Required (JWT). **Subscription:** Premium.

**Response (`200`):**

```json
{
  "requests": [
    {
      "friendship_id": "uuid",
      "to": {
        "id": "uuid",
        "name": "Jane Doe",
        "username": "janedoe",
        "profile_picture_url": "https://..."
      },
      "created_at": "2024-01-20T15:00:00Z"
    }
  ]
}
```

#### `POST /api/v1/friends/requests/:id/accept`

Accept a pending friendship request.

**Auth:** Required (JWT). **Subscription:** Premium.

**Responses:**

| Status | Body | Condition |
|---|---|---|
| `200` | `{ "friendship_id": "uuid", "status": "accepted" }` | Accepted |
| `404` | Error | Request not found or not addressed to current user |
| `409` | Error | Already accepted or no longer pending |

Triggers a push notification to the requester (see [Section 9](#9-push-notifications)).

#### `POST /api/v1/friends/requests/:id/decline`

Decline a pending friendship request. The friendship record is **hard-deleted**.

**Auth:** Required (JWT). **Subscription:** Premium.

**Responses:**

| Status | Body | Condition |
|---|---|---|
| `200` | `{ "message": "Request declined" }` | Declined and deleted |
| `404` | Error | Request not found |

#### `DELETE /api/v1/friends/:id`

Remove an accepted friend. The friendship record is **hard-deleted**.

**Auth:** Required (JWT). **Subscription:** Premium.

**Responses:**

| Status | Body | Condition |
|---|---|---|
| `200` | `{ "message": "Friend removed" }` | Removed |
| `404` | Error | Friendship not found |

#### `GET /api/v1/friends/:id/stats`

View a friend's stats. Only accessible if the two users are accepted friends.

**Auth:** Required (JWT). **Subscription:** Premium.

**Response (`200`):**

```json
{
  "user": {
    "id": "uuid",
    "name": "Jane Doe",
    "username": "janedoe",
    "profile_picture_url": "https://..."
  },
  "stats": {
    "current_streak_days": 12,
    "longest_streak_days": 30,
    "total_focus_time_ms": 86400000,
    "daily_trends": [
      {
        "local_day": "2024-01-20",
        "effective_ms": 7200000,
        "qualified": true,
        "session_count": 3
      }
    ]
  }
}
```

`daily_trends` returns the last 30 days by default. An optional `days` query parameter can be used to request a different range (max 90).

#### `GET /api/v1/friends/search`

Search for users to add as friends.

**Auth:** Required (JWT). **Subscription:** Premium.

**Query parameters:** `q` (required, min 3 characters).

Searches by username only (prefix match, case-insensitive). Does not return the current user.

**Response (`200`):**

```json
{
  "users": [
    {
      "id": "uuid",
      "name": "Jane Doe",
      "username": "janedoe",
      "profile_picture_url": "https://..."
    }
  ]
}
```

Results are capped at 20 entries.

### 5.5 Leaderboard

#### `GET /api/v1/leaderboard/streaks`

Leaderboard ranked by current streak length (consecutive qualified days up to today).

**Auth:** Required (JWT).

**Query parameters:** `page` (default 1), `limit` (default 20, max 100).

**Response (`200`):**

```json
{
  "entries": [
    {
      "rank": 1,
      "user": {
        "id": "uuid",
        "name": "Jane Doe",
        "username": "janedoe",
        "profile_picture_url": "https://..."
      },
      "current_streak_days": 45
    }
  ],
  "my_rank": {
    "rank": 23,
    "current_streak_days": 12
  },
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 150
  }
}
```

Only includes users where `leaderboard_visible = true`. The `my_rank` field is always included regardless of the user's visibility setting, so the user can see their own position.

#### `GET /api/v1/leaderboard/focus-time`

Leaderboard ranked by total cumulative focus time.

**Auth:** Required (JWT).

**Query parameters:** `page` (default 1), `limit` (default 20, max 100).

**Response (`200`):**

```json
{
  "entries": [
    {
      "rank": 1,
      "user": {
        "id": "uuid",
        "name": "Top Focuser",
        "username": "topfocuser",
        "profile_picture_url": "https://..."
      },
      "total_focus_time_ms": 360000000
    }
  ],
  "my_rank": {
    "rank": 42,
    "total_focus_time_ms": 86400000
  },
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 150
  }
}
```

### 5.6 Subscriptions (Client-Facing)

Client-facing purchase flows are handled through the RevenueCat SDK in the mobile app. The backend's role is limited to storing a reconciled entitlement snapshot for authorization and account-state display.

### 5.7 Push Notification Device Registration

#### `POST /api/v1/devices`

Register an FCM device token.

**Auth:** Required (JWT).

**Request:**

```json
{
  "fcm_token": "firebase-device-token-string",
  "platform": "android"
}
```

**Validation:**

- `platform`: must be `"android"` or `"ios"`.
- `fcm_token`: non-empty string.

If the token already exists for this user, its `updated_at` is refreshed. If the token exists for a different user, it is reassigned to the current user (device changed hands).

**Response:**

| Status | Body | Condition |
|---|---|---|
| `200` | `{ "message": "Device registered" }` | Success |

#### `POST /api/v1/devices/unregister`

Unregister an FCM device token (e.g., on logout) without placing the raw token in the URL.

**Auth:** Required (JWT).

**Request:**

```json
{
  "fcm_token": "firebase-device-token-string"
}
```

**Response:**

| Status | Body | Condition |
|---|---|---|
| `200` | `{ "message": "Device unregistered" }` | Success (also returned if token not found, for idempotency) |

### 5.8 RevenueCat Webhook

#### `POST /api/v1/webhooks/revenuecat`

Receives RevenueCat lifecycle notifications that trigger entitlement reconciliation.

**Auth:** Verified via a shared webhook secret in the `Authorization` header using the format `Authorization: Bearer <revenuecat_webhook_secret>` (configured in RevenueCat dashboard and the backend's environment variables).

Webhook handling requirements:

1. The webhook payload is treated as a **signal to reconcile**, not as the source of truth for entitlement state.
2. Handling must be **idempotent**. RevenueCat may deliver the same webhook more than once, and replayed deliveries must converge on the same final `user_entitlements` state.
3. Unknown event types, additional fields, and forward-compatible payload changes must be tolerated without failing the webhook.
4. After receiving a valid webhook, the backend fetches the current subscriber/customer state from the RevenueCat API and derives entitlement state from the customer's current entitlements rather than directly applying the raw event payload.
5. The backend should capture and reconcile both `app_user_id` and `original_app_user_id` when present. RevenueCat aliases, restores, and transfer scenarios can cause the active entitlement to move between identifiers, so reconciliation must consider the current canonical customer state rather than assuming a one-event/one-user mapping.
6. The backend persists the reconciled identifiers and entitlement snapshot in `user_entitlements`, including `revenuecat_app_user_id`, `revenuecat_original_app_user_id`, `is_active`, `current_period_end`, and `last_webhook_event_at`. Those identifier columns reflect the canonical customer identifiers from the latest successful reconciliation, not a full alias history.

Operational notes:

- Authenticated, well-formed webhook deliveries should be acknowledged with `200` as soon as they are accepted for reconciliation so RevenueCat does not keep retrying unnecessarily.
- Duplicate deliveries should still return `200`.
- Invalid or missing webhook authorization should return `401`.

**Response:**

Returns `200` for accepted webhook deliveries. RevenueCat retries on non-2xx responses.

### 5.9 Admin Endpoints

All admin endpoints are prefixed with `/api/v1/admin` and require admin authentication via a separate admin JWT obtained from the admin login endpoint.

#### `POST /api/v1/admin/login`

**Request:**

```json
{
  "username": "admin",
  "password": "adminPassword"
}
```

**Response (`200`):**

```json
{
  "access_token": "admin-jwt-token"
}
```

Admin JWT has a 1-hour lifetime. Claims include `"role": "admin"`.

#### `GET /api/v1/admin/users`

List users with search and pagination.

**Query parameters:** `page`, `limit`, `search` (searches by email, username, or name).

**Response (`200`):**

```json
{
  "users": [
    {
      "id": "uuid",
      "email": "user@example.com",
      "name": "John Doe",
      "username": "johndoe",
      "profile_picture_url": "https://...",
      "premium_entitlement_active": true,
      "created_at": "2024-01-15T10:30:00Z"
    }
  ],
  "pagination": { "page": 1, "limit": 20, "total": 500 }
}
```

#### `GET /api/v1/admin/users/:id`

Get detailed user info including entitlement state, friend count, and replication activity.

#### `GET /api/v1/admin/stats`

Aggregate platform statistics.

**Response (`200`):**

```json
{
  "total_users": 1500,
  "active_users_30d": 800,
  "users_with_premium_entitlement": 200,
  "active_premium_entitlements": 180,
  "total_friendships": 350,
  "avg_streak_days": 8.5,
  "avg_daily_focus_time_ms": 5400000
}
```

#### Manual Entitlement Management

**`POST /api/v1/admin/users/:id/entitlements`** — Manually grant or revoke an entitlement.

```json
{
  "action": "grant",
  "entitlement": "premium",
  "expires_at": "2024-12-31T23:59:59Z"
}
```

`action` must be `"grant"` or `"revoke"`. `expires_at` is optional and can be used for temporary grants. Admin grant/revoke actions are durable overrides of the stored entitlement snapshot for authorization: they remain in effect until an admin changes them again or a grant expires, and they are not automatically overwritten by the next RevenueCat webhook reconciliation.

**`GET /api/v1/admin/entitlements`** — List entitlement records with filters.

**Query parameters:** `entitlement`, `is_active`, `page`, `limit`.

This listing is oriented around stored entitlement state (for example, filtering active `premium` entitlements), not plan type.

### 5.10 Health Probes

Two health-check endpoints exist at the root level (not under `/api/v1`). They
share the same JSON response shape but serve different purposes in container
orchestration and load-balancer configuration.

#### `GET /live`

Liveness probe. Returns immediately with status `"ok"` and the current UTC
timestamp. No external dependencies are checked — if the process can serve HTTP,
this endpoint succeeds. Container runtimes use this to detect a hung or crashed
process (restart policy).

**Response (`200`):**

```json
{
  "status": "ok",
  "timestamp": "2024-01-15T10:30:00Z"
}
```

#### `GET /ready`

Readiness probe. Pings the database connection pool before responding. Returns
`200` with status `"ok"` when the database is reachable, or `503` with status
`"degraded"` when the pool is nil or the ping fails. Load balancers use this to
decide whether to route traffic to the instance.

**Response (`200` when ready, `503` when degraded):**

```json
{
  "status": "ok",
  "timestamp": "2024-01-15T10:30:00Z"
}
```

When the database is unreachable:

```json
{
  "status": "degraded",
  "timestamp": "2024-01-15T10:30:00Z"
}
```

---

## 6. Subscription System

### 6.1 Architecture

```
App Store / Google Play
        |
        v
    RevenueCat  ----webhook---->  Pauza Backend  ----API----> RevenueCat
        |                              |
        v                              v
  Client SDK                   user_entitlements table
  (purchase flow +             (backend entitlement snapshot
   customer info)               for authorization)
```

1. **Products, pricing, offerings, and discounts** are owned by RevenueCat and the underlying app stores, not by the Pauza backend.
2. **Payment processing** is handled by RevenueCat. The Flutter app uses the RevenueCat SDK to present purchase UI and read current customer info.
3. **Webhook-driven reconciliation**: RevenueCat sends lifecycle notifications to `POST /api/v1/webhooks/revenuecat`. The backend treats those notifications as triggers to fetch current subscriber state and reconcile `user_entitlements`.
4. **Backend authorization snapshot**: The backend stores the derived entitlement state it needs for server-side access control and may reconcile through the RevenueCat API if a webhook is missed or a state mismatch is detected.

### 6.2 Subscription Tiers

| Tier | Cost | Access |
|---|---|---|
| **Free** | $0 | Core features: modes (limited count), basic stats, basic streaks |
| **Premium** | Determined by the current RevenueCat offering and store pricing | All premium-gated features, represented server-side by the `premium` entitlement |

### 6.3 Enforcement

Subscription status is enforced on **both** client and server:

- **Client-side**: The Flutter app uses RevenueCat SDK customer info for purchase UX and may also read the backend `subscription` snapshot from `GET /api/v1/me` for account state display.
- **Server-side**: Endpoints that require premium access (e.g., all `/friends/*` endpoints) check whether the user has an active `premium` entitlement. If not, the server returns:

```json
{
  "error": {
    "code": "SUBSCRIPTION_REQUIRED",
    "message": "This feature requires a premium subscription"
  }
}
```

HTTP status: `403 Forbidden`.

Premium-gated endpoints are marked in their documentation with **Subscription: Premium**.

---

## 7. Friendships

### 7.1 Overview

Friendships allow premium users to connect with each other and view detailed
stats. The friendship system uses a request/accept model over an unordered user
pair: for any two users there may be at most one friendship row, regardless of
who initiated the request.

### 7.2 Lifecycle

```
 Requester                    Addressee
     |                            |
     |  send request              |
     |  (status: pending)         |
     |--------------------------->|
     |                            |  push notification received
     |                            |
     |                            |  accept / decline
     |                            |---> accept: status -> accepted
     |                            |     push notification to requester
     |  can view each other's     |
     |  stats                     |
     |<-------------------------->|
     |                            |---> decline: record hard-deleted
```

### 7.3 Data Access Rules

- Only **accepted** friends can view each other's stats.
- Stats include: current streak, longest streak, total focus time, and daily trends (last 30-90 days from `streak_daily_aggregates`).
- Friend stats are computed from the friend's replicated data on the server.

### 7.4 Subscription Dependency

- All friendship endpoints require an active premium subscription.
- If a user's subscription expires, they cannot access friendship features, but their existing friendship records are **preserved** (not deleted). If they re-subscribe, their friends are restored.
- If both users have expired subscriptions, neither can view the other's stats, but the friendship record remains.
- Declining a pending request deletes the pending row instead of transitioning it to a separate `declined` state.

---

## 8. Leaderboard

### 8.1 Overview

Two independent leaderboards rank users based on different metrics:

1. **Streak Leaderboard**: Ranked by current streak length (number of consecutive `qualified = 1` days in `streak_daily_aggregates` up to and including today).
2. **Focus Time Leaderboard**: Ranked by total cumulative `effective_ms` across all entries in `streak_daily_aggregates`.

### 8.2 Visibility

- Users are visible on leaderboards by default (`leaderboard_visible = true` in `users` table).
- Users can opt out by setting `leaderboard_visible = false` via `PATCH /api/v1/me`.
- Opted-out users do not appear in leaderboard listings but can still see their own rank via the `my_rank` field in the response.

### 8.3 Computation

Leaderboard rankings are computed on-demand from the replicated `streak_daily_aggregates` table. For performance at scale, consider:

- Materialized views or summary tables refreshed periodically (e.g., every 15 minutes).
- Caching leaderboard pages with a short TTL.

The initial implementation may compute rankings directly with SQL queries. Optimization should be applied when query latency exceeds acceptable thresholds.

### 8.4 Streak Calculation (Server-Side)

The current streak is calculated as the count of consecutive days with `qualified = 1` ending at the most recent qualified day, which must be either today or yesterday (to allow for timezone differences and partial days). If the most recent qualified day is older than yesterday, the current streak is 0.

```sql
-- Pseudocode for current streak calculation
WITH recent_days AS (
  SELECT local_day, qualified
  FROM streak_daily_aggregates
  WHERE user_id = $1
  ORDER BY local_day DESC
)
-- Count consecutive qualified = 1 days from the top
```

---

## 9. Push Notifications

### 9.1 Architecture

The Go backend sends push notifications directly via the **Firebase Admin SDK** (using a Firebase service account). The client registers its FCM device token with the backend.

```
Flutter App                    Pauza Backend                Firebase
    |                               |                          |
    |  POST /api/v1/devices         |                          |
    |  { fcm_token, platform }      |                          |
    |------------------------------>|                          |
    |                               |  store in device_tokens  |
    |                               |                          |
    |                               |  (event occurs)          |
    |                               |  send FCM message ------>|
    |                               |                          |
    |  push notification displayed  |<---- FCM delivery -------|
    |<------------------------------|                          |
```

### 9.2 Notification Triggers

| Event | Recipient | Payload |
|---|---|---|
| Friendship request received | Addressee | `{ "type": "friend_request", "from_username": "...", "friendship_id": "..." }` |
| Friendship request accepted | Requester | `{ "type": "friend_accepted", "by_username": "...", "friendship_id": "..." }` |
| Schedule reminder | User | `{ "type": "schedule_reminder", "mode_title": "...", "starts_in_minutes": 15 }` |

### 9.3 Schedule Reminders

The backend runs a periodic job (e.g., every minute) that checks replicated `schedules` for upcoming scheduled mode activations. If a schedule is due to start within 15 minutes, and a reminder has not already been sent for this occurrence, a push notification is sent to the user.

To avoid duplicate reminders, the backend tracks sent reminders in memory or in a lightweight table/cache with a TTL.

### 9.4 Token Lifecycle

- Tokens are registered via `POST /api/v1/devices`.
- Tokens are unregistered via `POST /api/v1/devices/unregister` with the `fcm_token` in the request body (e.g., on user logout).
- If Firebase returns a `messaging/registration-token-not-registered` error when sending a notification, the token is automatically deleted from `device_tokens`.
- A user may have multiple tokens (e.g., after app reinstall before old token is cleaned up). All valid tokens are targeted when sending a notification.

---

## 10. Rate Limiting

Rate limits are enforced per the following rules. Responses that exceed the
limit return HTTP `429 Too Many Requests` with a `Retry-After` header.

| Endpoint Group | Limit | Scope |
|---|---|---|
| Auth endpoints (`/auth/*`) | 5 requests / minute | Per IP address |
| OTP verification (`/auth/verify`) | 3 requests / minute | Per email address |
| Replication (`/sync`) | 30 requests / minute | Per authenticated user |
| General API (all other authenticated endpoints) | 60 requests / minute | Per authenticated user |
| Admin endpoints (`/admin/*`) | 30 requests / minute | Per admin |
| Webhooks (`/webhooks/*`) | 100 requests / minute | Per IP address |

`/auth/verify` is subject to both limits above: the shared auth-endpoint per-IP
limit and the endpoint-specific per-email OTP verification limit.

**Implementation notes:**

- Use a sliding window or token bucket algorithm.
- Rate limiting is Redis-backed in every deployment. There is no in-process fallback limiter.
- The limiter should fail open if the backend is unavailable: requests are allowed, the incident is logged/observed, and fabricated budget headers are not emitted.
- When the limiter has authoritative budget data, responses should include `X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `X-RateLimit-Reset`.

---

## 11. Error Handling

### 11.1 Standard Error Response Format

All error responses follow this structure:

```json
{
  "error": {
    "code": "ERROR_CODE",
    "message": "Human-readable description of what went wrong",
    "details": {}
  }
}
```

The `details` field is optional and may contain additional structured information (e.g., field-level validation errors).

### 11.2 Error Codes

| Code | HTTP Status | Description |
|---|---|---|
| `VALIDATION_ERROR` | `422` | Request body or query parameters failed validation |
| `UNAUTHORIZED` | `401` | Missing, invalid, or expired authentication |
| `FORBIDDEN` | `403` | Authenticated but not authorized for this action |
| `SUBSCRIPTION_REQUIRED` | `403` | Feature requires an active premium subscription |
| `NOT_FOUND` | `404` | Requested resource does not exist |
| `CONFLICT` | `409` | Resource already exists (e.g., duplicate email, username) |
| `RATE_LIMITED` | `429` | Too many requests |
| `INTERNAL_ERROR` | `500` | Unexpected server error |

### 11.3 Validation Error Details

For `VALIDATION_ERROR`, the `details` field contains per-field errors:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Invalid request body",
    "details": {
      "fields": {
        "email": "must be a valid email address",
        "otp": "must be a 6-digit numeric code"
      }
    }
  }
}
```

---

## 12. Deployment

### 12.1 Docker Compose

This repository uses a shared Compose base file plus environment-specific
overlays. Development runs the API behind Nginx with bundled PostgreSQL and
Redis. Production uses a single-host full-stack deployment that runs Nginx,
the API, PostgreSQL, and Redis on the same machine.

```yaml
# docker-compose.yml + docker-compose.dev.yml (reference structure)

services:
  api:
    build: .
    volumes:
      - photodata:/var/lib/pauza/photos
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8080/live"]
      interval: 30s
      timeout: 5s
      retries: 3
  nginx:
    image: nginx:1.27-alpine
    ports:
      - "8080:80"
    volumes:
      - ./deploy/nginx/development.conf:/etc/nginx/conf.d/default.conf:ro
      - photodata:/var/lib/pauza/photos:ro
    depends_on:
      api:
        condition: service_healthy

  db:
    image: postgres:16-alpine
    environment:
      - POSTGRES_USER=pauza
      - POSTGRES_PASSWORD=password
      - POSTGRES_DB=pauza
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U pauza"]
      interval: 10s
      timeout: 5s
      retries: 5

  redis:
    image: redis:7-alpine
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5

volumes:
  pgdata:
  photodata:
```

Production uses `docker-compose.yml` with `docker-compose.prod.yml`, keeps
Nginx as the public entrypoint, and loads the API environment from `.env.prod`
while also running local PostgreSQL and Redis services.

```yaml
# docker-compose.yml + docker-compose.prod.yml (reference structure)

services:
  api:
    env_file:
      - ./.env.prod
    depends_on:
      db:
        condition: service_healthy
      redis:
        condition: service_healthy
  nginx:
    ports:
      - "80:80"
    volumes:
      - ./deploy/nginx/production-compose.conf:/etc/nginx/conf.d/default.conf:ro
      - photodata:/var/lib/pauza/photos:ro
  db:
    image: postgres:16-alpine
    volumes:
      - pgdata-prod:/var/lib/postgresql/data
  redis:
    image: redis:7-alpine
    volumes:
      - redisdata-prod:/data
```

Migrations remain a separate release step, for example:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml run --rm api ./pauza-migrate
```

### 12.2 Environment Variables

| Variable | Description | Required |
|---|---|---|
| `DATABASE_URL` | PostgreSQL connection string | Yes |
| `JWT_SECRET` | HMAC secret for signing JWTs | Yes |
| `JWT_ACCESS_TOKEN_TTL` | Access token lifetime (e.g., `15m`) | Yes |
| `JWT_REFRESH_TOKEN_TTL` | Refresh token lifetime (e.g., `720h`) | Yes |
| `REVENUECAT_API_KEY` | RevenueCat REST API key | Yes |
| `REVENUECAT_WEBHOOK_SECRET` | Shared secret for webhook verification | Yes |
| `REDIS_URL` | Redis connection string for shared rate limiting | Yes |
| `FIREBASE_SERVICE_ACCOUNT_JSON` | Firebase service account JSON content or another deployment-specific representation of the credential | Yes when push notifications are enabled |
| `SMTP_HOST` | SMTP server hostname | Yes |
| `SMTP_PORT` | SMTP server port | Yes |
| `SMTP_USERNAME` | SMTP authentication username | Yes |
| `SMTP_PASSWORD` | SMTP authentication password | Yes |
| `SMTP_FROM` | Email sender address | Yes |
| `SMTP_TIMEOUT` | SMTP dial/send timeout | No |
| `SMTP_TLS_POLICY` | SMTP TLS behavior: `mandatory`, `opportunistic`, or `none` | No |
| `TRUSTED_PROXIES` | Comma-separated IPs/CIDRs allowed to supply forwarded client IP headers | No |
| `AUTH_RATE_LIMIT` | Auth endpoint request budget per window | No |
| `AUTH_RATE_WINDOW` | Window for auth endpoint request budget | No |
| `VERIFY_OTP_RATE_LIMIT` | OTP verification request budget per window | No |
| `VERIFY_OTP_RATE_WINDOW` | Window for OTP verification request budget | No |
| `GENERAL_API_RATE_LIMIT` | General authenticated API request budget per window | No |
| `GENERAL_API_RATE_WINDOW` | Window for general authenticated API budget | No |
| `SYNC_RATE_LIMIT` | Replication request budget per window | No |
| `SYNC_RATE_WINDOW` | Window for replication request budget | No |
| `CLEANUP_INTERVAL` | Interval for auth/replication cleanup jobs | No |
| `OTP_RETENTION_PERIOD` | How long expired/used OTP artifacts are kept before cleanup | No |
| `REFRESH_TOKEN_REVOKED_RETENTION` | How long revoked/expired refresh-token rows are retained before cleanup | No |
| `ADMIN_SEED_USERNAME` | Initial admin username (used by `cmd/seed-admin`) | For seed command |
| `ADMIN_SEED_PASSWORD` | Initial admin password (used by `cmd/seed-admin`) | For seed command |
| `PORT` | Server listen port (default `8080`) | No |
| `LOG_LEVEL` | Logging level: `debug`, `info`, `warn`, `error` (default `info`) | No |

### 12.3 Database Migrations

Migrations are managed using [golang-migrate](https://github.com/golang-migrate/migrate). Migration files are stored in a `migrations/` directory within the backend repository.

Migrations are applied via the dedicated `cmd/migrate` command (`go run ./cmd/migrate`), not at server startup. This keeps the serving binary free of DDL privileges and simplifies rollout reasoning in multi-instance environments. Migrations run within transactions and are rolled back on failure.

### 12.4 Admin Seeding

Admin bootstrap is handled by the dedicated `cmd/seed-admin` command (`go run ./cmd/seed-admin`), not at server startup. When the `admin_credentials` table is empty, this command creates an initial admin account using `ADMIN_SEED_USERNAME` and `ADMIN_SEED_PASSWORD`. The password is hashed with bcrypt before storage.

---

## 13. Out of Scope

The following items are intentionally excluded from this specification:

| Item | Reason |
|---|---|
| **Usage stats collection** | Device usage data comes from OS APIs (Android UsageStatsManager, iOS Screen Time). This data is collected natively on the device and is not sent to or processed by the backend. |
| **App blocking enforcement** | Blocking is enforced natively on the device via platform-specific APIs. The backend has no role in enforcement. |
| **Specific premium feature definitions** | Which exact product capabilities are included in free vs premium is a product decision outside this backend contract. This spec only defines entitlement enforcement boundaries. |
| **User blocking** | The ability to block other users (preventing friend requests, hiding from search) is deferred to a future iteration. |
| **Contact-based friend discovery** | Syncing phone contacts to find existing Pauza users is deferred to a future iteration. |
| **Push notification preferences** | Per-notification-type opt-in/opt-out settings are deferred. All notification types are sent to all users initially. |
| **File/photo storage infrastructure** | Profile photos are stored on local disk on the deployed machine. The deployment must expose `PHOTO_STORAGE_DIR` at the same public path configured by `PHOTO_PUBLIC_BASE_URL`, typically via Nginx. |
| **Web frontend for admin panel** | This spec covers the admin REST API only. The admin web UI is a separate project that consumes these endpoints. |

### 13.1 Possible Future Features

The following ideas may be revisited later, but they are not part of the current backend contract:

- Student verification and student-priced offers
- Additional entitlement tiers beyond `premium`
- More granular premium feature gating beyond the single `premium` entitlement
