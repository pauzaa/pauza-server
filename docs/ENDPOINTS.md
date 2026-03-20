# API Endpoints Reference

> Derived from source code. This document covers all **48 endpoints** currently
> wired in `internal/server/routes.go`.

---

## Conventions

| Item | Detail |
|---|---|
| **Base URL** | `https://<host>` — all `/api/v1/*` paths are relative to this. |
| **Content-Type** | `application/json` for all request and response bodies (except `POST /api/v1/me/photo` which uses `multipart/form-data`). |
| **Request ID** | Every response includes an `X-Request-Id` header echoed from the inbound request (or auto-generated). |
| **Body limit** | Request bodies are capped at **1 MiB** globally. `POST /api/v1/me/photo` and all `/api/v1/ai/*` endpoints use a **5 MiB** limit. Exceeding the limit returns a `VALIDATION_ERROR`. |
| **Timestamps** | All timestamps in JSON responses use **UTC RFC 3339** (e.g. `2025-01-15T08:30:00Z`). Sync-table timestamps are **Unix milliseconds** (`int64`). |
| **Pagination** | Endpoints that paginate accept `?page=` (default `1`) and `?limit=` (default `20`, max `100`). Invalid or out-of-range values are silently clamped to defaults rather than rejected. |
| **Authentication** | Protected endpoints require `Authorization: Bearer <access_token>`. Access tokens include a `sid` (session ID) claim that ties the token to a specific login session. Admin endpoints require an admin JWT. The webhook endpoint uses a separate Bearer secret. |
| **Unknown fields** | All JSON-body endpoints (except the webhook) use `DisallowUnknownFields()`, so unrecognised keys anywhere in the JSON payload are rejected with `VALIDATION_ERROR`. The webhook decoder does **not** enforce this (forward-compatible). |

### Rate-limit headers

Rate-limited responses normally include the headers below. In fail-open limiter degradation mode, the server may allow the request and omit the `X-RateLimit-*` headers rather than advertise fabricated budget data.

| Header | Description |
|---|---|
| `X-RateLimit-Limit` | Max requests allowed per window. |
| `X-RateLimit-Remaining` | Requests remaining in the current window. |
| `X-RateLimit-Reset` | UTC Unix timestamp when the window resets. |
| `Retry-After` | *(429 only)* Seconds until the next request may succeed. |

---

## Error envelope

All errors use a standard JSON envelope:

```json
{
  "error": {
    "code": "ERROR_CODE",
    "message": "Human-readable message"
  }
}
```

When present, `details` contains structured validation data.

Validation errors include per-field messages in `details`:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Invalid request body",
    "details": {
      "fields": {
        "email": "email is required"
      }
    }
  }
}
```

### Error code → HTTP status mapping

| Code | HTTP Status |
|---|---|
| `VALIDATION_ERROR` | 422 Unprocessable Entity |
| `UNAUTHORIZED` | 401 Unauthorized |
| `FORBIDDEN` | 403 Forbidden |
| `NOT_FOUND` | 404 Not Found |
| `CONFLICT` | 409 Conflict |
| `RATE_LIMITED` | 429 Too Many Requests |
| `SUBSCRIPTION_REQUIRED` | 403 Forbidden |
| `INTERNAL_ERROR` | 500 Internal Server Error |

---

## Health Probes

These live outside `/api/v1` and are used by container orchestration.

### `GET /live`

Liveness probe. Always succeeds if the process is running.

| Property | Value |
|---|---|
| **Auth** | None |
| **Rate limit** | None |

**Success — 200 OK**

```json
{
  "status": "ok",
  "timestamp": "2025-01-15T08:30:00Z"
}
```

---

### `GET /ready`

Readiness probe. Pings the database to verify connectivity.

| Property | Value |
|---|---|
| **Auth** | None |
| **Rate limit** | None |

**Success — 200 OK**

```json
{
  "status": "ok",
  "timestamp": "2025-01-15T08:30:00Z"
}
```

**Degraded — 503 Service Unavailable**

```json
{
  "status": "degraded",
  "timestamp": "2025-01-15T08:30:00Z"
}
```

---

### `GET /photos/*`

Serves static profile photo files from the configured `PHOTO_STORAGE_DIR` on
disk. The wildcard segment is the filename returned in `profile_picture_url`
fields elsewhere in the API.

| Property | Value |
|---|---|
| **Auth** | None |
| **Rate limit** | None |

**Success — 200 OK**: file content with appropriate `Content-Type`.

**Errors**: 404 if the file does not exist.

---

## Auth (`/api/v1/auth`)

Public passwordless authentication. No JWT required.

### `POST /api/v1/auth/start`

Initiate passwordless login by sending an OTP to the provided email.
The response does not leak whether the email is already registered.

| Property | Value |
|---|---|
| **Auth** | None |
| **Rate limit** | `auth` tier — per IP (default 5 req/min) |

**Request body**

| Field | Type | Required | Validation |
|---|---|---|---|
| `email` | string | yes | Valid email, max 255 chars, bare address only |

**Success — 200 OK**

```json
{
  "otp_required": true
}
```

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Invalid or missing email |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `POST /api/v1/auth/verify`

Verify the OTP and obtain access/refresh tokens.

| Property | Value |
|---|---|
| **Auth** | None |
| **Rate limit** | `auth` tier — per IP (default 5 req/min) **and** `verify-otp` tier — per email (default 3 req/min) |

**Request body**

| Field | Type | Required | Validation |
|---|---|---|---|
| `email` | string | yes | Bare email address only (`user@example.com`), max 255 chars |
| `otp` | string | yes | Exactly 6 digits |

**Success — 200 OK**

```json
{
  "access_token": "eyJ...",
  "refresh_token": "hex-string",
  "user": {
    "id": "uuid",
    "email": "user@example.com",
    "name": "",
    "username": "user123",
    "profile_picture_url": null,
    "push_enabled": true,
    "leaderboard_visible": true,
    "created_at": "2025-01-15T08:30:00Z",
    "subscription": null
  }
}
```

The `subscription` field, when present:

```json
{
  "entitlement": "premium",
  "is_active": true,
  "current_period_end": "2025-02-15T08:30:00Z"
}
```

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Invalid email or OTP format |
| `UNAUTHORIZED` (401) | Wrong OTP or expired |
| `RATE_LIMITED` (429) | Too many attempts |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `POST /api/v1/auth/refresh`

Exchange a refresh token for new access and refresh tokens.

| Property | Value |
|---|---|
| **Auth** | None |
| **Rate limit** | `auth` tier — per IP (default 5 req/min) |

**Request body**

| Field | Type | Required | Validation |
|---|---|---|---|
| `refresh_token` | string | yes | Non-empty; whitespace-only values are rejected |

**Success — 200 OK**

```json
{
  "access_token": "eyJ...",
  "refresh_token": "new-hex-string"
}
```

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Missing refresh_token |
| `UNAUTHORIZED` (401) | Invalid, expired, or reused token (reuse triggers revocation of the associated session) |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `POST /api/v1/auth/logout`

Revoke the current login session. The session is identified by the
`sid` claim in the caller's access token. Only the current session is revoked;
other sessions (if any) remain active.

| Property | Value |
|---|---|
| **Auth** | Required — `Authorization: Bearer <JWT>` |
| **Rate limit** | `general-api` tier — per user ID |

**Request body**

None.

**Success — 204 No Content**

_(empty body)_

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

## Webhook (`/api/v1/webhooks`)

### `POST /api/v1/webhooks/revenuecat`

Receives RevenueCat subscription lifecycle events. Authenticated via a static
Bearer secret (not a JWT).

| Property | Value |
|---|---|
| **Auth** | `Authorization: Bearer <webhook_secret>` |
| **Rate limit** | `webhook` tier — per IP (default 100 req/min) |

**Request body**

RevenueCat webhook payload. Unknown fields are tolerated (forward-compatible).

| Field | Type | Notes |
|---|---|---|
| `api_version` | string | API version string |
| `event.type` | string | Event type (e.g. `INITIAL_PURCHASE`, `RENEWAL`, `CANCELLATION`) |
| `event.id` | string | Unique event ID |
| `event.app_user_id` | string | RevenueCat app user ID |
| `event.original_app_user_id` | string | Original app user ID (before alias) |
| `event.product_id` | string | Product identifier |
| `event.entitlement_ids` | string[] | Entitlement identifiers |
| `event.event_timestamp_ms` | int64 | When the event occurred (Unix ms) |
| `event.expiration_at_ms` | int64 \| null | Expiration timestamp in ms |
| `event.environment` | string | `SANDBOX` or `PRODUCTION` |
| `event.period_type` | string | e.g. `NORMAL`, `TRIAL`, `INTRO` |
| `event.store` | string | e.g. `APP_STORE`, `PLAY_STORE` |
| `event.transferred_from` | string[] | App user IDs transferred from (TRANSFER events) |
| `event.transferred_to` | string[] | App user IDs transferred to (TRANSFER events) |
| *(additional fields)* | — | Silently ignored |

**Success — 200 OK**

```json
{}
```

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid Bearer secret |
| `VALIDATION_ERROR` (422) | Malformed JSON body |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient processing failure (RevenueCat will retry) |

---

## Admin (`/api/v1/admin`)

### `POST /api/v1/admin/login`

Authenticate as an admin user with username/password credentials.

| Property | Value |
|---|---|
| **Auth** | None |
| **Rate limit** | `admin` tier — per IP (default 30 req/min) |

**Request body**

| Field | Type | Required | Validation |
|---|---|---|---|
| `username` | string | yes | Non-empty; whitespace-only values are rejected |
| `password` | string | yes | Non-empty |

**Success — 200 OK**

```json
{
  "access_token": "eyJ..."
}
```

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Missing username or password |
| `UNAUTHORIZED` (401) | Invalid credentials |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `GET /api/v1/admin/users`

List all users with pagination and optional search.

| Property | Value |
|---|---|
| **Auth** | Admin JWT |
| **Rate limit** | `admin` tier — per admin user ID (default 30 req/min) |

**Query parameters**

| Param | Type | Default | Notes |
|---|---|---|---|
| `page` | int | 1 | Invalid values silently default to `1` |
| `limit` | int | 20 | Invalid or out-of-range values silently default to `20` (max `100`) |
| `search` | string | — | Optional search filter |

**Success — 200 OK**

```json
{
  "users": [
    {
      "id": "uuid",
      "email": "user@example.com",
      "name": "Jane",
      "username": "jane42",
      "profile_picture_url": null,
      "premium_entitlement_active": true,
      "created_at": "2025-01-15T08:30:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 150
  }
}
```

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid admin JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `GET /api/v1/admin/users/{id}`

Get detailed information about a single user.

| Property | Value |
|---|---|
| **Auth** | Admin JWT |
| **Rate limit** | `admin` tier — per admin user ID (default 30 req/min) |

**Path parameters**

| Param | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes | Must be a valid UUID — `parseUUIDParam` returns 422 on invalid format |

**Success — 200 OK**

```json
{
  "id": "uuid",
  "email": "user@example.com",
  "name": "Jane",
  "username": "jane42",
  "profile_picture_url": null,
  "leaderboard_visible": true,
  "created_at": "2025-01-15T08:30:00Z",
  "is_premium": true,
  "current_period_end": "2025-02-15T08:30:00Z",
  "revenuecat_app_user_id": "rc_abc123",
  "friend_count": 5,
  "total_sessions": 42,
  "last_session_time": 1705312200000
}
```

`current_period_end`, `revenuecat_app_user_id`, and `last_session_time` may be `null`.

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | `id` is not a valid UUID |
| `UNAUTHORIZED` (401) | Missing or invalid admin JWT |
| `NOT_FOUND` (404) | User does not exist |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `GET /api/v1/admin/stats`

Get platform-wide aggregate statistics.

| Property | Value |
|---|---|
| **Auth** | Admin JWT |
| **Rate limit** | `admin` tier — per admin user ID (default 30 req/min) |

**Success — 200 OK**

```json
{
  "total_users": 1500,
  "active_users_30d": 800,
  "users_with_premium_entitlement": 200,
  "total_friendships": 3000,
  "avg_streak_days": 12.5,
  "avg_daily_focus_time_ms": 3600000.0
}
```

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid admin JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `POST /api/v1/admin/users/{id}/entitlements`

Grant or revoke an entitlement override for a user.

| Property | Value |
|---|---|
| **Auth** | Admin JWT |
| **Rate limit** | `admin` tier — per admin user ID (default 30 req/min) |

**Path parameters**

| Param | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes | Must be a valid UUID — `parseUUIDParam` returns 422 on invalid format |

**Request body**

| Field | Type | Required | Validation |
|---|---|---|---|
| `action` | string | yes | `"grant"` or `"revoke"` |
| `entitlement` | string | yes | Must be `"premium"` (the only accepted value) |
| `expires_at` | string \| null | no | RFC 3339 timestamp, must be in the future |

**Success — 200 OK**

```json
{
  "message": "Entitlement premium granted for user"
}
```

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | `id` is not a valid UUID, invalid action, missing entitlement, or bad expires_at |
| `UNAUTHORIZED` (401) | Missing or invalid admin JWT |
| `NOT_FOUND` (404) | User does not exist |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `GET /api/v1/admin/entitlements`

List entitlement records with optional filtering.

| Property | Value |
|---|---|
| **Auth** | Admin JWT |
| **Rate limit** | `admin` tier — per admin user ID (default 30 req/min) |

**Query parameters**

| Param | Type | Default | Notes |
|---|---|---|---|
| `page` | int | 1 | Invalid values silently default to `1` |
| `limit` | int | 20 | Invalid or out-of-range values silently default to `20` (max `100`) |
| `entitlement` | string | — | Filter by entitlement name |
| `is_active` | bool | — | Filter by active status |

**Success — 200 OK**

```json
{
  "entitlements": [
    {
      "user_id": "uuid",
      "email": "user@example.com",
      "username": "jane42",
      "entitlement": "premium",
      "is_active": true,
      "current_period_end": "2025-02-15T08:30:00Z",
      "updated_at": "2025-01-15T08:30:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 50
  }
}
```

`current_period_end` may be `null`.

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Invalid `is_active` value |
| `UNAUTHORIZED` (401) | Missing or invalid admin JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `GET /api/v1/admin/stats/user-growth`

Return a time-series of new user registrations (cumulative new accounts per
bucket).

| Property | Value |
|---|---|
| **Auth** | Admin JWT |
| **Rate limit** | `admin` tier — per admin user ID (default 30 req/min) |

**Query parameters**

| Param | Type | Default | Notes |
|---|---|---|---|
| `range` | string | `30d` | One of `30d`, `90d`, `1y`, `all`; invalid values silently default to `30d` |
| `granularity` | string | auto | One of `day`, `week`, `month`; when omitted the service auto-selects (`day` for 30d, `week` for 90d, `month` for 1y/all). Invalid explicit values return 422. |

**Success — 200 OK**

```json
{
  "data": [
    { "date": "2025-01-15", "value": 12 }
  ],
  "granularity": "day"
}
```

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | `granularity` is an explicit invalid value (not `day`, `week`, or `month`) |
| `UNAUTHORIZED` (401) | Missing or invalid admin JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `GET /api/v1/admin/stats/active-users`

Return a time-series of distinct active users per bucket (users who performed
at least one sync in the period).

| Property | Value |
|---|---|
| **Auth** | Admin JWT |
| **Rate limit** | `admin` tier — per admin user ID (default 30 req/min) |

**Query parameters**

Same as `GET /api/v1/admin/stats/user-growth`.

**Success — 200 OK**

Same shape as `GET /api/v1/admin/stats/user-growth`.

**Errors**

Same as `GET /api/v1/admin/stats/user-growth`.

---

### `GET /api/v1/admin/revenuecat/overview`

Return high-level RevenueCat subscription metrics pulled live from the
RevenueCat v2 API. Requires the server to be configured with a RevenueCat v2
secret key; returns 500 if the key is absent or the upstream call fails.

| Property | Value |
|---|---|
| **Auth** | Admin JWT |
| **Rate limit** | `admin` tier — per admin user ID (default 30 req/min) |

**Success — 200 OK**

```json
{
  "mrr": 99900,
  "arr": 1198800,
  "active_subscribers": 250,
  "active_trials": 30
}
```

`mrr` and `arr` are in **cents** (dollars × 100, rounded).

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid admin JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | RevenueCat v2 not configured or upstream call failed |

---

### `GET /api/v1/admin/revenuecat/charts/{chart_name}`

Return a time-series chart from the RevenueCat v2 API for the given chart name
and optional date range.

| Property | Value |
|---|---|
| **Auth** | Admin JWT |
| **Rate limit** | `admin` tier — per admin user ID (default 30 req/min) |

**Path parameters**

| Param | Type | Required | Notes |
|---|---|---|---|
| `chart_name` | string | yes | One of `revenue`, `customers_new`, `customers_active`, `churn`; 422 on other values |

**Query parameters**

| Param | Type | Default | Notes |
|---|---|---|---|
| `range` | string | `30d` | One of `30d`, `90d`, `1y`, `all`; invalid values silently default to `30d` |

**Success — 200 OK**

```json
{
  "name": "revenue",
  "data": [
    { "date": "2025-01-15", "value": 4900 }
  ]
}
```

For `revenue` charts, `value` is in **cents**. For count-based charts (`customers_new`, `customers_active`, `churn`), `value` is an integer count.

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | `chart_name` is not one of the allowed values |
| `UNAUTHORIZED` (401) | Missing or invalid admin JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | RevenueCat v2 not configured or upstream call failed |

---

### `GET /api/v1/admin/users/{id}/revenuecat`

Return the live RevenueCat subscriber record for a user, showing their
entitlement state as reported directly by RevenueCat (v1 API). The user must
have a non-empty `revenuecat_app_user_id` in the database.

| Property | Value |
|---|---|
| **Auth** | Admin JWT |
| **Rate limit** | `admin` tier — per admin user ID (default 30 req/min) |

**Path parameters**

| Param | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes | Must be a valid UUID — `parseUUIDParam` returns 422 on invalid format |

**Success — 200 OK**

```json
{
  "app_user_id": "rc_abc123",
  "entitlements": [
    {
      "entitlement_id": "premium",
      "is_active": true,
      "product_identifier": "annual_premium",
      "purchase_date": "2025-01-15T08:30:00Z",
      "expires_date": "2026-01-15T08:30:00Z",
      "grace_period_expires_date": null
    }
  ]
}
```

`expires_date` and `grace_period_expires_date` may be `null`.

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | `id` is not a valid UUID |
| `UNAUTHORIZED` (401) | Missing or invalid admin JWT |
| `NOT_FOUND` (404) | User does not exist or has no RevenueCat subscription |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | RevenueCat v1 not configured or upstream call failed |

---

## User Profile (`/api/v1/me`)

All endpoints require a valid user JWT.

### `GET /api/v1/me`

Get the authenticated user's profile.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Success — 200 OK**

```json
{
  "id": "uuid",
  "email": "user@example.com",
  "name": "Jane",
  "username": "jane42",
  "profile_picture_url": null,
  "push_enabled": true,
  "leaderboard_visible": true,
  "created_at": "2025-01-15T08:30:00Z",
  "subscription": null
}
```

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid JWT, or user no longer exists |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `PATCH /api/v1/me`

Update the authenticated user's profile fields. Only provided fields are
updated.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Request body**

| Field | Type | Required | Validation |
|---|---|---|---|
| `name` | string | no | Max 100 characters |
| `username` | string | no | 3–30 chars, alphanumeric + underscore |

**Success — 200 OK**

Returns the full updated user profile (same shape as `GET /api/v1/me`).

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Invalid name or username |
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `CONFLICT` (409) | Username already taken |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `GET /api/v1/me/notification-preferences`

Get the authenticated user's notification preferences.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Success — 200 OK**

```json
{
  "push_enabled": true
}
```

---

### `PATCH /api/v1/me/notification-preferences`

Update the authenticated user's notification preferences.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Request body**

| Field | Type | Required | Validation |
|---|---|---|---|
| `push_enabled` | bool | no | — |

**Success — 200 OK**

```json
{
  "push_enabled": false
}
```

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Invalid JSON body |
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `GET /api/v1/me/privacy`

Get the authenticated user's privacy preferences.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Success — 200 OK**

```json
{
  "leaderboard_visible": true
}
```

---

### `PATCH /api/v1/me/privacy`

Update the authenticated user's privacy preferences.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Request body**

| Field | Type | Required | Validation |
|---|---|---|---|
| `leaderboard_visible` | bool | no | — |

**Success — 200 OK**

```json
{
  "leaderboard_visible": false
}
```

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Invalid JSON body |
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `GET /api/v1/me/username-available`

Check whether a username is available.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Query parameters**

| Param | Type | Required | Validation |
|---|---|---|---|
| `username` | string | yes | 3–30 chars, alphanumeric + underscore |

**Success — 200 OK**

```json
{
  "available": true
}
```

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Invalid username format |
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `POST /api/v1/me/delete/request`

Request account deletion. Sends a confirmation OTP to the user's email.
The response message is intentionally vague (anti-enumeration).

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Request body** — None. This endpoint ignores the request body.

**Success — 200 OK**

```json
{
  "message": "If the account is eligible for deletion, a confirmation code has been sent."
}
```

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `POST /api/v1/me/delete/confirm`

Confirm account deletion with the OTP.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Request body**

| Field | Type | Required | Validation |
|---|---|---|---|
| `otp` | string | yes | Exactly 6 digits |

**Success — 200 OK**

```json
{
  "message": "Account deleted"
}
```

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Invalid OTP format |
| `UNAUTHORIZED` (401) | Missing or invalid JWT, or wrong/expired OTP |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `POST /api/v1/me/photo`

Upload a profile photo. Uses `multipart/form-data` instead of JSON.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Request (multipart/form-data)**

| Field | Type | Required | Validation |
|---|---|---|---|
| `photo` | file | yes | JPEG or PNG, max **5 MiB** (this endpoint overrides the global 1 MiB body cap). |

**Success — 200 OK**

```json
{
  "profile_picture_url": "https://api.example.com/photos/abc123.jpg"
}
```

Uploaded files are written to `PHOTO_STORAGE_DIR` on the deployed machine. A
reverse proxy such as Nginx must expose that directory at the same public path
configured by `PHOTO_PUBLIC_BASE_URL`.

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Missing photo, body exceeds 5 MiB, or invalid image format |
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Photo storage failure |

---

## Devices (`/api/v1/devices`)

Push-notification device registration. Requires a user JWT.

### `POST /api/v1/devices`

Register a device for push notifications.

The backend sends Firebase notifications for friend-request events. If Firebase
later reports that a registered token is no longer valid, that token is
automatically removed from the server.
User-level delivery is additionally controlled by `push_enabled`; disabled
users keep their registered tokens, but the backend skips sends for them.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Request body**

| Field | Type | Required | Validation |
|---|---|---|---|
| `fcm_token` | string | yes | Non-empty |
| `platform` | string | yes | `"android"` or `"ios"` |

**Success — 200 OK**

```json
{
  "message": "Device registered"
}
```

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Missing or invalid fields |
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `POST /api/v1/devices/unregister`

Unregister a device from push notifications.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Request body**

| Field | Type | Required | Validation |
|---|---|---|---|
| `fcm_token` | string | yes | Non-empty |

**Success — 200 OK**

```json
{
  "message": "Device unregistered"
}
```

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Missing fcm_token |
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

## Friends (`/api/v1/friends`)

Social features. All endpoints require a user JWT. Most also require an active
premium subscription, except decline, cancel, and remove which allow users to
manage their friend list without premium.

### `GET /api/v1/friends`

List the authenticated user's accepted friends.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |
| **Subscription** | Premium required |

**Query parameters**

| Param | Type | Default | Notes |
|---|---|---|---|
| `page` | int | 1 | Invalid values silently default to `1` |
| `limit` | int | 20 | Invalid or out-of-range values silently default to `20` (max `100`) |

**Success — 200 OK**

```json
{
  "friends": [
    {
      "friendship_id": "uuid",
      "user": {
        "id": "uuid",
        "name": "Jane",
        "username": "jane42",
        "profile_picture_url": null
      },
      "since": "2025-01-15T08:30:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 5
  }
}
```

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `SUBSCRIPTION_REQUIRED` (403) | No active premium subscription |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `POST /api/v1/friends/request`

Send a friend request to another user by username.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |
| **Subscription** | Premium required |

**Request body**

| Field | Type | Required | Validation |
|---|---|---|---|
| `username` | string | yes | 3–30 chars, alphanumeric + underscore |

**Success — 201 Created**

```json
{
  "friendship_id": "uuid",
  "status": "pending"
}
```

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Invalid username format |
| `UNAUTHORIZED` (401) | Missing or invalid JWT, or target user not found |
| `CONFLICT` (409) | Request already exists or cannot add yourself |
| `SUBSCRIPTION_REQUIRED` (403) | No active premium subscription |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `GET /api/v1/friends/requests/incoming`

List pending friend requests received by the authenticated user.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |
| **Subscription** | Premium required |

**Success — 200 OK**

```json
{
  "requests": [
    {
      "friendship_id": "uuid",
      "user": {
        "id": "uuid",
        "name": "Bob",
        "username": "bob99",
        "profile_picture_url": null
      },
      "created_at": "2025-01-15T08:30:00Z"
    }
  ]
}
```

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `SUBSCRIPTION_REQUIRED` (403) | No active premium subscription |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `GET /api/v1/friends/requests/outgoing`

List pending friend requests sent by the authenticated user.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |
| **Subscription** | Premium required |

**Success — 200 OK**

Same shape as `GET /api/v1/friends/requests/incoming`.

**Errors**

Same as `GET /api/v1/friends/requests/incoming`.

---

### `POST /api/v1/friends/requests/{id}/accept`

Accept an incoming friend request.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |
| **Subscription** | Premium required |

**Path parameters**

| Param | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes | Friendship ID (not validated as UUID by the handler) |

**Success — 200 OK**

```json
{
  "friendship_id": "uuid",
  "status": "accepted"
}
```

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `NOT_FOUND` (404) | Request not found |
| `SUBSCRIPTION_REQUIRED` (403) | No active premium subscription |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `POST /api/v1/friends/requests/{id}/decline`

Decline an incoming friend request.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Path parameters**

| Param | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes | Friendship ID (not validated as UUID by the handler) |

**Success — 200 OK**

```json
{
  "message": "Request declined"
}
```

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `NOT_FOUND` (404) | Request not found |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `POST /api/v1/friends/requests/{id}/cancel`

Cancel an outgoing friend request.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Path parameters**

| Param | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes | Friendship ID (not validated as UUID by the handler) |

**Success — 200 OK**

```json
{
  "message": "Request cancelled"
}
```

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `NOT_FOUND` (404) | Request not found |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `DELETE /api/v1/friends/{id}`

Remove an accepted friend.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Path parameters**

| Param | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes | Friendship ID (not validated as UUID by the handler) |

**Success — 200 OK**

```json
{
  "message": "Friend removed"
}
```

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `NOT_FOUND` (404) | Friendship not found |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `GET /api/v1/friends/{id}/stats`

Get streak and focus-time statistics for a friend.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |
| **Subscription** | Premium required |

**Path parameters**

| Param | Type | Required | Notes |
|---|---|---|---|
| `id` | string | yes | Friendship ID (not validated as UUID by the handler) |

**Query parameters**

| Param | Type | Default | Notes |
|---|---|---|---|
| `days` | int | 7 | Invalid or out-of-range values silently default to `7` (max `90`) |

**Success — 200 OK**

```json
{
  "user": {
    "id": "uuid",
    "name": "Jane",
    "username": "jane42",
    "profile_picture_url": null
  },
  "stats": {
    "current_streak_days": 7,
    "longest_streak_days": 14,
    "total_focus_time_ms": 86400000,
    "focus_time_today_ms": 3600000,
    "daily_trends": [
      {
        "local_day": "2025-01-15",
        "effective_ms": 3600000,
        "qualified": true,
        "session_count": 0
      }
    ]
  }
}
```

`daily_trends` may be `null` (not `[]`) when there are no records — Go nil slice serialisation.

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid JWT, or friendship not found / not accepted |
| `SUBSCRIPTION_REQUIRED` (403) | No active premium subscription |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `GET /api/v1/friends/search`

Search for users by username prefix.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |
| **Subscription** | Premium required |

**Query parameters**

| Param | Type | Required | Validation |
|---|---|---|---|
| `q` | string | yes | At least 3 characters, max 50 characters |

**Success — 200 OK**

```json
{
  "users": [
    {
      "id": "uuid",
      "name": "Jane",
      "username": "jane42",
      "profile_picture_url": null
    }
  ]
}
```

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Query `q` is less than 3 characters or exceeds 50 characters |
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `SUBSCRIPTION_REQUIRED` (403) | No active premium subscription |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

## Leaderboard (`/api/v1/leaderboard`)

### `GET /api/v1/leaderboard/streaks`

Get the leaderboard ranked by current streak. Only users with
`leaderboard_visible = true` appear in the `entries` list, and ranks are
contiguous (no gaps from hidden users). `my_rank` reflects the caller's
position among visible users (the caller is included in the ranking even if
they have opted out of visibility).

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Query parameters**

| Param | Type | Default | Notes |
|---|---|---|---|
| `page` | int | 1 | Invalid values silently default to `1` |
| `limit` | int | 20 | Invalid or out-of-range values silently default to `20` (max `100`) |

**Success — 200 OK**

`entries` may be `null` (not `[]`) when empty (Go nil slice serialisation).

```json
{
  "entries": [
    {
      "rank": 1,
      "user": {
        "id": "uuid",
        "name": "Jane",
        "username": "jane42",
        "profile_picture_url": null
      },
      "current_streak_days": 14,
      "total_focus_time_ms": 0
    }
  ],
  "my_rank": {
    "rank": 3,
    "current_streak_days": 7,
    "total_focus_time_ms": 0
  },
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 10
  }
}
```

`current_streak_days` and `total_focus_time_ms` use `omitempty` — zero values
are omitted from the JSON output.

**Errors**

| Code | When |
|---|---|
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

### `GET /api/v1/leaderboard/focus-time`

Get the leaderboard ranked by total focus time. Same ranking logic as
`/streaks` — only visible users appear in `entries` with contiguous ranks,
and `my_rank` is computed among visible users plus the caller.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `general-api` tier — per user ID (default 60 req/min) |

**Query parameters**

| Param | Type | Default | Notes |
|---|---|---|---|
| `page` | int | 1 | Invalid values silently default to `1` |
| `limit` | int | 20 | Invalid or out-of-range values silently default to `20` (max `100`) |

**Success — 200 OK**

Same shape as `GET /api/v1/leaderboard/streaks`.

**Errors**

Same as `GET /api/v1/leaderboard/streaks`.

---

## Sync (`/api/v1/sync`)

### `POST /api/v1/sync`

Bidirectional data synchronization for client-side tables. The client sends
local changes since its last sync cursor; the server applies them and returns
server-side changes since that cursor.

| Property | Value |
|---|---|
| **Auth** | User JWT |
| **Rate limit** | `sync` tier — per user ID (default 30 req/min) |

**Request body**

Top-level structure (the `tables` object is optional; omitting it means no tables are synced):

```json
{
  "tables": {
    "modes": { ... },
    "mode_blocked_apps": { ... },
    "schedules": { ... },
    "restriction_sessions": { ... },
    "restriction_lifecycle_events": { ... },
    "nfc_linked_chips": { ... },
    "qr_linked_codes": { ... },
    "streak_session_daily_rollups": { ... },
    "streak_daily_aggregates": { ... }
  }
}
```

Each table key is **optional**. When present, it must contain:

| Field | Type | Required | Validation |
|---|---|---|---|
| `cursor` | int64 | yes | Monotonic sync cursor (`0` for first sync); use `next_cursor` from the previous response |
| `upserts` | array | no | Records to create/update; max **5000** items per table per request |
| `deletions` | array | no | Keys of records to delete; max **5000** items per table per request; each key is validated (see per-table rules below) |

#### Table: `modes`

**Upsert fields:**

| Field | Type | Required | Validation |
|---|---|---|---|
| `id` | string | yes | Non-empty |
| `title` | string | yes | Non-empty |
| `text_on_screen` | string | yes | Non-empty |
| `description` | string \| null | no | — |
| `allowed_pauses_count` | int | yes | ≥ 0 |
| `minimum_duration_ms` | int \| null | no | ≥ 1000 when present |
| `ending_pausing_scenario` | string | yes | `"nfc"`, `"qr"`, or `"manual"` |
| `icon_token` | string | yes | Non-empty |
| `created_at` | int64 | yes | Unix ms |
| `updated_at` | int64 | yes | Unix ms |

**Deletion key:** `string` (mode ID) — non-empty, whitespace-only values are rejected.

#### Table: `mode_blocked_apps`

**Upsert fields:**

| Field | Type | Required | Validation |
|---|---|---|---|
| `mode_id` | string | yes | Non-empty |
| `platform` | string | yes | `"android"` or `"ios"` |
| `app_identifier` | string | yes | Non-empty |
| `created_at` | int64 | yes | Unix ms |
| `updated_at` | int64 | yes | Unix ms |

**Deletion key:** object with `mode_id`, `platform`, `app_identifier` (same validation as upserts).

#### Table: `schedules`

**Upsert fields:**

| Field | Type | Required | Validation |
|---|---|---|---|
| `id` | string | yes | Non-empty |
| `mode_id` | string | yes | Non-empty |
| `days` | string | yes | Comma-separated values from `{mon,tue,wed,thu,fri,sat,sun}` |
| `start_minute` | int | yes | 0–1439 |
| `end_minute` | int | yes | 0–1439 |
| `enabled` | int | yes | `0` or `1` |
| `created_at` | int64 | yes | Unix ms |
| `updated_at` | int64 | yes | Unix ms |

**Deletion key:** `string` (schedule ID) — non-empty, whitespace-only values are rejected.

#### Table: `restriction_sessions`

**Upsert fields:**

| Field | Type | Required | Validation |
|---|---|---|---|
| `session_id` | string | yes | Non-empty |
| `mode_id` | string | yes | Non-empty |
| `source` | string | yes | `"manual"` or `"schedule"` |
| `started_at` | int64 | yes | Unix ms |
| `ended_at` | int64 \| null | no | Unix ms; must be ≥ `started_at` when present |
| `pause_count` | int | yes | ≥ 0 |
| `total_paused_ms` | int | yes | ≥ 0 |
| `last_paused_at` | int64 \| null | no | Unix ms |
| `integrity_status` | string | yes | `"ok"` or `"anomaly"` |
| `last_anomaly_reason` | string \| null | no | — |
| `last_event_id` | string | yes | Non-empty |
| `created_at` | int64 | yes | Unix ms |
| `updated_at` | int64 | yes | Unix ms |

**Deletion key:** `string` (session ID) — non-empty, whitespace-only values are rejected.

#### Table: `restriction_lifecycle_events`

**Upsert fields:**

| Field | Type | Required | Validation |
|---|---|---|---|
| `id` | string | yes | Non-empty |
| `session_id` | string | yes | Non-empty |
| `mode_id` | string | yes | Non-empty |
| `action` | string | yes | `"START"`, `"PAUSE"`, `"RESUME"`, or `"END"` |
| `source` | string | yes | `"manual"` or `"schedule"` |
| `reason` | string | yes | `"manual"`, `"nfc"`, `"qr"`, or `"timer"` (enforced by `UnmarshalJSON`) |
| `occurred_at` | int64 | yes | Unix ms |
| `created_at` | int64 | yes | Unix ms |

**Deletions:** Not supported for this table.

#### Table: `nfc_linked_chips`

**Upsert fields:**

| Field | Type | Required | Validation |
|---|---|---|---|
| `id` | string | yes | Non-empty |
| `chip_identifier` | string | yes | Non-empty |
| `name` | string | yes | Non-empty |
| `created_at` | int64 | yes | Unix ms |
| `updated_at` | int64 | yes | Unix ms |

**Deletion key:** `string` (chip ID) — non-empty, whitespace-only values are rejected.

#### Table: `qr_linked_codes`

**Upsert fields:**

| Field | Type | Required | Validation |
|---|---|---|---|
| `id` | string | yes | Non-empty |
| `scan_value` | string | yes | Non-empty |
| `name` | string | yes | Non-empty |
| `created_at` | int64 | yes | Unix ms |
| `updated_at` | int64 | yes | Unix ms |

**Deletion key:** `string` (code ID) — non-empty, whitespace-only values are rejected.

#### Table: `streak_session_daily_rollups`

**Upsert fields:**

| Field | Type | Required | Validation |
|---|---|---|---|
| `session_id` | string | yes | Non-empty |
| `local_day` | string | yes | `YYYY-MM-DD` |
| `effective_ms` | int | yes | ≥ 0 |
| `updated_at` | int64 | yes | Unix ms |

**Deletion key:** object with `session_id` and `local_day` (same validation as upserts).

#### Table: `streak_daily_aggregates`

**Read-only table.** The client may only supply `cursor` to receive server-computed
aggregates. Upserts and deletions are not accepted for this table.

---

**Success — 200 OK**

The response mirrors the request structure, returning server-side changes for
each table the client included. Tables not requested are omitted.

```json
{
  "tables": {
    "modes": {
      "upserts": [ ... ],
      "deletions": [ ... ],
      "next_cursor": 42
    }
  }
}
```

Each table entry in the response contains `upserts` (full records to apply
locally), `deletions` (keys to remove locally), and `next_cursor` (the cursor
the client should send in the next sync request for that table). The field
schemas match the upsert/deletion types described above.

**Errors**

| Code | When |
|---|---|
| `VALIDATION_ERROR` (422) | Missing `cursor`, invalid field values, blank deletion IDs, `upserts` or `deletions` exceeding 5000 items, duplicate keys within a batch, same key in both upserts and deletions, or unknown JSON fields anywhere in the payload |
| `UNAUTHORIZED` (401) | Missing or invalid JWT, or user not found |
| `RATE_LIMITED` (429) | Too many requests |
| `INTERNAL_ERROR` (500) | Transient server error |

---

## AI Analysis (`/api/v1/ai`)

All AI endpoints require a user JWT and an active **premium subscription**.
Rate limited to **10 requests / hour** per user (configurable via `AI_RATE_LIMIT` / `AI_RATE_WINDOW`).
Request body limit is **5 MiB**. Endpoints are only mounted when `AI_PROVIDER` is configured.

Usage data is sent by the client per-request and is **not** persisted on the server.
The server composes a system prompt, forwards the data to the configured AI provider
(OpenAI or Gemini), and returns the response.

**Common response shape (200 OK):**

```json
{
  "analysis": "AI-generated markdown text..."
}
```

**Common errors:**

| Code | When |
|---|---|
| `SUBSCRIPTION_REQUIRED` (403) | No active premium subscription |
| `VALIDATION_ERROR` (422) | Missing required fields or invalid values |
| `UNAUTHORIZED` (401) | Missing or invalid JWT |
| `RATE_LIMITED` (429) | Exceeded AI rate limit |
| `INTERNAL_ERROR` (500) | AI provider error or transient failure |

### `POST /api/v1/ai/usage-analysis`

Analyze phone usage patterns for a daily or weekly period.

| Detail | Value |
|---|---|
| **Auth** | User JWT |
| **Premium** | Required |
| **Rate limit** | `ai` tier (10 req/hour per user) |
| **Body limit** | 5 MiB |

**Request body:**

| Field | Type | Required | Validation |
|---|---|---|---|
| `period` | string | yes | `daily` or `weekly` |
| `app_usage` | array | yes | 1–500 items |
| `app_usage[].app_identifier` | string | yes | Non-empty, max 256 chars |
| `app_usage[].app_name` | string | yes | Non-empty, max 256 chars |
| `app_usage[].total_time_ms` | int64 | yes | ≥ 0; max 86400000 (daily) / 604800000 (weekly) |
| `app_usage[].launch_count` | int | yes | ≥ 0, max 100000 |
| `app_usage[].category` | string | no | e.g. `social`, `entertainment` |
| `total_screen_time_ms` | int64 | no | ≥ 0; max 86400000 (daily) / 604800000 (weekly) |
| `total_unlocks` | int | no | ≥ 0, max 100000 |

### `POST /api/v1/ai/focus-schedule`

Suggest optimal focus schedules based on app usage and existing schedules.

| Detail | Value |
|---|---|
| **Auth** | User JWT |
| **Premium** | Required |
| **Rate limit** | `ai` tier (10 req/hour per user) |
| **Body limit** | 5 MiB |

**Request body:**

| Field | Type | Required | Validation |
|---|---|---|---|
| `app_usage` | array | yes | 1–500 items (same field rules as usage-analysis) |
| `current_schedules` | array | no | Existing schedule slots |
| `current_schedules[].days` | int[] | — | Day-of-week numbers (0-6) |
| `current_schedules[].start_minute` | int | — | 0-1439 |
| `current_schedules[].end_minute` | int | — | 0-1439, must be > start_minute |
| `preferred_focus_hours` | int | yes | 1-16 |
| `timezone` | string | yes | Valid IANA timezone (e.g. `Asia/Almaty`); validated via `time.LoadLocation` |

### `POST /api/v1/ai/daily-report`

Generate a daily screen time report with highlights, wins, and improvement areas.

| Detail | Value |
|---|---|
| **Auth** | User JWT |
| **Premium** | Required |
| **Rate limit** | `ai` tier (10 req/hour per user) |
| **Body limit** | 5 MiB |

**Request body:**

| Field | Type | Required | Validation |
|---|---|---|---|
| `date` | string | yes | `YYYY-MM-DD` |
| `app_usage` | array | yes | 1–500 items (same field rules as usage-analysis, daily caps) |
| `focus_sessions` | array | no | Max 100 items |
| `focus_sessions[].started_at` | int64 | — | Must be > 0 |
| `focus_sessions[].ended_at` | int64 | — | Must be > 0 and > started_at |
| `focus_sessions[].pause_count` | int | — | ≥ 0 |
| `focus_sessions[].effective_ms` | int64 | — | ≥ 0 |
| `total_screen_time_ms` | int64 | no | ≥ 0, max 86400000 |
| `total_unlocks` | int | no | ≥ 0, max 100000 |
| `streak_days` | int | no | ≥ 0 |

### `POST /api/v1/ai/addiction-check`

Analyze multi-day usage history for addictive behavior patterns.

| Detail | Value |
|---|---|
| **Auth** | User JWT |
| **Premium** | Required |
| **Rate limit** | `ai` tier (10 req/hour per user) |
| **Body limit** | 5 MiB |

**Request body:**

| Field | Type | Required | Validation |
|---|---|---|---|
| `app_usage_history` | array | yes | 1–365 items |
| `app_usage_history[].date` | string | yes | `YYYY-MM-DD` |
| `app_usage_history[].apps` | array | yes | Max 500 items; same field rules as usage-analysis (daily caps) |
| `daily_screen_time_history` | array | yes | 1–365 items |
| `daily_screen_time_history[].date` | string | yes | `YYYY-MM-DD` |
| `daily_screen_time_history[].total_screen_time_ms` | int64 | yes | ≥ 0, max 86400000 |
| `daily_screen_time_history[].total_unlocks` | int | yes | ≥ 0, max 100000 |
| `first_unlock_times` | array | no | Max 365 items |
| `first_unlock_times[].date` | string | — | `YYYY-MM-DD` |
| `first_unlock_times[].time_of_day_minute` | int | — | 0-1439 |
