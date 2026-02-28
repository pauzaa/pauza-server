# Pauza Backend — Global Implementation Plan

This document outlines the implementation roadmap for the Pauza backend, broken into sequential phases. Each phase builds on the previous one. Testing is expected within each phase before moving to the next.

---

## Phase 1: Project Skeleton & Configuration ✅ COMPLETED

**Goal:** Establish the Go project structure, configuration loading, and basic runnable server.

- Initialize Go module and define project directory layout (cmd, internal, migrations, etc.)
- Set up configuration management — load all environment variables defined in the spec
- Create the HTTP server entry point with a basic router
- Implement the `GET /health` endpoint (returns `{"status":"ok"}` without DB for now)
- Set up Docker Compose with the Go API service and PostgreSQL 16
- Verify the app starts, connects to nothing yet, and health endpoint responds
- **Testing:** Health endpoint returns 200; app starts and shuts down cleanly
- **Completed:** March 1, 2026 — Full implementation with Go 1.25, chi router, envconfig, slog logging, Docker Compose with PostgreSQL 16

---

## Phase 2: Database Foundation & Migrations

**Goal:** Set up PostgreSQL connectivity, migration tooling, and create all database tables.

- Integrate `golang-migrate` for migration management
- Write migration files for all backend-only tables: `users`, `otp_codes`, `refresh_tokens`, `admin_credentials`, `subscription_plans`, `subscription_plan_discounts`, `user_subscriptions`, `friendships`, `device_tokens`, `sync_tombstones`
- Write migration files for all synced tables: `modes`, `mode_blocked_apps`, `schedules`, `restriction_sessions`, `restriction_lifecycle_events`, `nfc_linked_chips`, `qr_linked_codes`, `streak_session_daily_rollups`, `streak_daily_aggregates`
- Run migrations automatically on application startup
- Update `GET /health` to check database connectivity (return 503 if DB unreachable)
- Implement admin seeding — create initial admin account on first startup when `admin_credentials` is empty
- **Testing:** Migrations run cleanly on a fresh DB; health endpoint reflects DB status; admin seed creates the account exactly once

---

## Phase 3: Error Handling & Middleware Foundation

**Goal:** Build the shared infrastructure that all future endpoints will depend on.

- Implement the standard error response format (`{ "error": { "code", "message", "details" } }`)
- Define error codes and helper functions for returning structured errors (VALIDATION_ERROR, UNAUTHORIZED, FORBIDDEN, NOT_FOUND, CONFLICT, RATE_LIMITED, SUBSCRIPTION_REQUIRED, INTERNAL_ERROR)
- Implement request validation utilities (email format, password strength, etc.)
- Implement structured logging with configurable log level
- Add a request ID middleware for tracing
- **Testing:** Error helpers produce correct JSON shapes and HTTP status codes; validation utilities catch invalid inputs

---

## Phase 4: Authentication — Registration & Login

**Goal:** Users can register (with OTP email verification) and log in.

- Implement `POST /auth/register` — validate input, hash password with bcrypt (cost 12+), store pending user, generate 6-digit OTP, send via SMTP
- Implement email sending (OTP delivery over SMTP)
- Implement `POST /auth/verify-otp` — verify OTP (10-min expiry, single-use, max 3 attempts), activate user, return JWT + refresh token
- Implement JWT access token generation (15-min lifetime, HS256, claims: sub, email, iat, exp)
- Implement opaque refresh token generation, store hashed (SHA-256) in `refresh_tokens`
- Implement `POST /auth/login` — verify credentials, return tokens; generic error message to prevent email enumeration
- Implement JWT authentication middleware — extract and validate Bearer token on protected routes
- **Testing:** Full registration flow (register → OTP → tokens); login with valid/invalid credentials; JWT middleware rejects expired/missing tokens; OTP expiry and attempt limits enforced

---

## Phase 5: Authentication — Token Refresh & Password Reset

**Goal:** Complete the authentication system with token lifecycle management.

- Implement `POST /auth/refresh` — validate refresh token, rotate (revoke old, issue new pair); if revoked token is reused, revoke all tokens for that user
- Implement `POST /auth/forgot-password` — generate OTP and send email; always return 200 regardless of email existence
- Implement `POST /auth/reset-password` — verify OTP, update password hash, revoke all refresh tokens for user
- **Testing:** Token refresh produces new valid pair; reuse of revoked token triggers full revocation; password reset invalidates all existing sessions

---

## Phase 6: User Profile

**Goal:** Users can view and update their profile, upload photos, check username availability, and delete their account.

- Implement `GET /api/v1/me` — return user profile with subscription status (subscription will be null for now)
- Implement `PATCH /api/v1/me` — partial update of name, username, leaderboard_visible with validation
- Implement `GET /api/v1/me/username-available` — check case-insensitive username uniqueness
- Implement `POST /api/v1/me/photo` — multipart upload, validate format (JPEG/PNG) and size (5MB max), store to object storage, update `profile_picture_url`
- Implement `DELETE /api/v1/me` — require password confirmation, hard-delete user and all cascading data
- **Testing:** Profile CRUD works correctly; username conflicts return 409; photo upload validates format/size; account deletion cascades properly

---

## Phase 7: Sync Protocol

**Goal:** Implement the bidirectional sync endpoint for all synced tables.

- Implement `POST /api/v1/sync` — parse the sync request payload with per-table `last_synced_at`, `upserts`, and `deletions`
- Implement sync processing within a single database transaction per request:
  - Process client upserts (insert if new, last-write-wins update if newer)
  - Process client deletions (delete record, insert tombstone)
  - Gather server changes since client's `last_synced_at` (excluding echoed records)
  - Query `sync_tombstones` for deletions since `last_synced_at`
- Support all 9 synced tables with their respective primary key structures (single and composite)
- Return `server_time` in the response for the client to use as next `last_synced_at`
- Handle first sync / full restore (`last_synced_at = 0`)
- **Testing:** Upsert new records; last-write-wins conflict resolution; deletions produce tombstones; server returns only changes since `last_synced_at`; full restore returns all data; composite PK tables work correctly

---

## Phase 8: Tombstone Garbage Collection & Background Jobs

**Goal:** Set up the background job infrastructure and tombstone cleanup.

- Create a background job runner / scheduler within the Go application
- Implement tombstone garbage collection — delete `sync_tombstones` older than 90 days, run daily
- **Testing:** Tombstones older than 90 days are cleaned up; tombstones newer than 90 days are preserved; job runs on schedule

---

## Phase 9: Subscription System

**Goal:** Subscription plans exist, RevenueCat webhook updates subscription state, and entitlement checks work.

- Implement `GET /api/v1/subscriptions/plans` — list active plans with current discounts (public endpoint)
- Implement `POST /api/v1/webhooks/revenuecat` — verify webhook secret, handle all event types (INITIAL_PURCHASE, RENEWAL, CANCELLATION, EXPIRATION, PRODUCT_CHANGE, BILLING_ISSUE), map `app_user_id` to internal `user_id`
- Implement subscription entitlement check middleware — verify active subscription for premium-gated endpoints, return 403 SUBSCRIPTION_REQUIRED when not subscribed
- Update `GET /api/v1/me` to include real subscription data and features from the plan's `features_json`
- Implement `POST /api/v1/subscriptions/verify-student` — create session with third-party provider, return verification URL
- **Testing:** Plans endpoint returns correct data with discount resolution; webhook creates/updates subscriptions correctly; entitlement middleware blocks free users from premium endpoints; student verification initiates properly

---

## Phase 10: Friendships

**Goal:** Premium users can send, accept, decline friend requests and view friend stats.

- Implement `POST /api/v1/friends/request` — find user by username or email, create pending friendship, prevent self-add and duplicates
- Implement `GET /api/v1/friends/requests/incoming` and `GET /api/v1/friends/requests/outgoing`
- Implement `POST /api/v1/friends/requests/:id/accept` — update status to accepted
- Implement `POST /api/v1/friends/requests/:id/decline` — hard-delete the friendship record
- Implement `GET /api/v1/friends` — list accepted friends with pagination
- Implement `DELETE /api/v1/friends/:id` — remove (hard-delete) an accepted friendship
- Implement `GET /api/v1/friends/:id/stats` — compute current streak, longest streak, total focus time, daily trends from synced data
- Implement `GET /api/v1/friends/search` — prefix match on username, exact match on email, max 20 results, exclude self
- All endpoints gated behind subscription check middleware
- **Testing:** Full friendship lifecycle (request → accept/decline → remove); stats computation correct; subscription enforcement works; search returns expected results; duplicate/self-add prevented

---

## Phase 11: Leaderboard

**Goal:** Users can view streak and focus-time leaderboards with their own rank.

- Implement `GET /api/v1/leaderboard/streaks` — rank by current streak (consecutive qualified days), respect `leaderboard_visible`, include `my_rank`, paginate
- Implement `GET /api/v1/leaderboard/focus-time` — rank by total cumulative `effective_ms`, same visibility/pagination rules
- Implement streak calculation logic (consecutive qualified days ending at today or yesterday)
- **Testing:** Rankings are correct and ordered; opted-out users excluded from list but see own rank; pagination works; edge cases in streak calculation (gaps, timezone boundaries)

---

## Phase 12: Push Notifications

**Goal:** The server can send push notifications via FCM for friendship events and schedule reminders.

- Integrate Firebase Admin SDK for sending FCM messages
- Implement `POST /api/v1/devices` — register/update FCM token
- Implement `DELETE /api/v1/devices/:token` — unregister token (idempotent)
- Send push notification on friendship request received and friendship accepted
- Implement schedule reminder background job — check upcoming schedules every minute, send reminder 15 minutes before start, prevent duplicate reminders
- Handle stale tokens — delete token from DB if Firebase returns `registration-token-not-registered`
- **Testing:** Device registration and unregistration work; notifications dispatched on friendship events; schedule reminders fire at correct time without duplicates; stale tokens cleaned up

---

## Phase 13: Rate Limiting

**Goal:** All endpoints are protected by rate limits as specified.

- Implement rate limiting middleware (sliding window or token bucket)
- Configure per-group limits: auth (5/min per IP), OTP verification (3/min per email), sync (30/min per user), general API (60/min per user), admin (30/min per admin), webhooks (100/min per IP)
- Include rate limit headers in all responses: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`
- Return 429 with `Retry-After` header when limit exceeded
- Start with in-memory storage (single-instance)
- **Testing:** Requests within limit succeed; requests exceeding limit get 429; headers are present and correct; different scopes (IP vs user) enforced independently

---

## Phase 14: Admin API

**Goal:** Admin panel API is functional for user management, subscription plan CRUD, and platform analytics.

- Implement `POST /api/v1/admin/login` — authenticate admin, return admin JWT (1-hour lifetime, `role: admin` claim)
- Implement admin JWT middleware — separate from user JWT, check `role: admin`
- Implement `GET /api/v1/admin/users` — list with search and pagination
- Implement `GET /api/v1/admin/users/:id` — detailed user info (subscription history, friend count, sync activity)
- Implement `GET /api/v1/admin/stats` — aggregate platform statistics
- Implement subscription plan CRUD: list all, create, get by ID, update, deactivate (soft-delete)
- Implement discount management: create, update, delete time-limited discounts for plans
- Implement `POST /api/v1/admin/users/:id/subscription` — manually grant/revoke subscription
- Implement `GET /api/v1/admin/subscriptions` — list subscriptions with status filter
- **Testing:** Admin login and JWT flow; all CRUD operations; stats aggregation returns sensible numbers; manual subscription grant/revoke works; admin middleware blocks non-admin tokens

---

## Phase 15: Deployment Hardening & Final Polish

**Goal:** Production-ready deployment setup and final quality pass.

- Finalize Dockerfile (multi-stage build, minimal image)
- Finalize `docker-compose.yml` with all environment variables, health checks, and volume mounts
- Review and harden all input validation across endpoints
- Ensure consistent logging across all request paths (success and error)
- Verify graceful shutdown (in-flight requests complete, DB connections closed)
- Review all cascade delete behavior for account deletion
- End-to-end smoke test of the full system running in Docker Compose
- **Testing:** Docker build succeeds; compose stack starts cleanly; health checks pass; end-to-end flows (register → sync → leaderboard) work through Docker

---

## Phase Dependency Summary

```
Phase 1  → Phase 2  → Phase 3  → Phase 4  → Phase 5
                                      ↓
                                  Phase 6
                                      ↓
                                  Phase 7  → Phase 8
                                      ↓
                                  Phase 9
                                    ↙   ↘
                              Phase 10   Phase 11
                                    ↘   ↙
                                  Phase 12
                                      ↓
                                  Phase 13
                                      ↓
                                  Phase 14
                                      ↓
                                  Phase 15
```

Phases 1–5 are strictly sequential (each builds on the last). After Phase 9, Phases 10 and 11 can be worked on in parallel. Phase 13 (rate limiting) can technically be added at any point after Phase 3 but is placed late to avoid slowing down development iteration.
