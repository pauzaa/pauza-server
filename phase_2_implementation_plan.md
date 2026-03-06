# Phase 2: Database Foundation & Migrations — Implementation Plan

## Overview

**Goal:** Establish PostgreSQL connectivity, run the initial schema migration on startup, update the health endpoint to check DB status, and seed the initial admin account.

**New dependencies:** `pgx/v5`, `golang-migrate/v4`, `golang.org/x/crypto/bcrypt`

---

## Architecture Changes

### New package: `internal/database`

```
internal/database/
  database.go     — Connect(), Close(), Pool accessor
  migrate.go      — RunMigrations() using golang-migrate
  seed.go         — SeedAdmin() for first-startup admin creation
```

### Migration files

```
migrations/
  000001_initial_schema.up.sql      — All 19 tables + indexes + constraints
  000001_initial_schema.down.sql    — Drop all tables in reverse dependency order
```

One migration because this is a greenfield project — all tables are part of the initial schema. Future schema changes (e.g., adding a column for a new feature) will each get their own numbered migration.

---

## Step-by-Step Implementation

### Step 1: Add Go Dependencies

```
go get github.com/jackc/pgx/v5
go get github.com/golang-migrate/migrate/v4
go get golang.org/x/crypto/bcrypt
```

Plus the migrate sub-packages: `database/pgx/v5` and `source/file`.

### Step 2: Create `internal/database/database.go`

- `Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error)` — creates pool via `pgxpool.New()`, pings to verify, returns pool
- Fail fast on bad config (caller exits if error)
- Log connection success at INFO level

### Step 3: Create `internal/database/migrate.go`

- `RunMigrations(databaseURL string, migrationsPath string) error`
- Uses `golang-migrate` with `file://` source and `pgx5` database driver
- Calls `m.Up()` — treats `migrate.ErrNoChange` as success
- Any other error prevents app startup

### Step 4: Write `migrations/000001_initial_schema.up.sql`

Single file containing all `CREATE TABLE` and `CREATE INDEX` statements from BACKEND_SPEC.md Section 3, ordered by foreign key dependencies:

**Table creation order:**

1. `users` — no FK deps
2. `otp_codes` — no FK deps (references email by value, not FK)
3. `refresh_tokens` — FK → `users`
4. `admin_credentials` — no FK deps
5. `subscription_plans` — no FK deps
6. `subscription_plan_discounts` — FK → `subscription_plans`
7. `user_subscriptions` — FK → `users`, `subscription_plans`
8. `friendships` — FK → `users` (×2)
9. `device_tokens` — FK → `users`
10. `sync_tombstones` — FK → `users`
11. `modes` — FK → `users`
12. `mode_blocked_apps` — FK → `users`
13. `schedules` — FK → `users`
14. `restriction_sessions` — FK → `users`
15. `restriction_lifecycle_events` — FK → `users`
16. `nfc_linked_chips` — FK → `users`
17. `qr_linked_codes` — FK → `users`
18. `streak_session_daily_rollups` — FK → `users`
19. `streak_daily_aggregates` — FK → `users`

Each table's SQL is taken verbatim from the spec, including all `CHECK` constraints, `UNIQUE` constraints, `DEFAULT` values, and indexes.

### Step 5: Write `migrations/000001_initial_schema.down.sql`

Drops all tables in reverse dependency order (children first, parents last):

```sql
DROP TABLE IF EXISTS streak_daily_aggregates CASCADE;
DROP TABLE IF EXISTS streak_session_daily_rollups CASCADE;
DROP TABLE IF EXISTS qr_linked_codes CASCADE;
DROP TABLE IF EXISTS nfc_linked_chips CASCADE;
DROP TABLE IF EXISTS restriction_lifecycle_events CASCADE;
DROP TABLE IF EXISTS restriction_sessions CASCADE;
DROP TABLE IF EXISTS schedules CASCADE;
DROP TABLE IF EXISTS mode_blocked_apps CASCADE;
DROP TABLE IF EXISTS modes CASCADE;
DROP TABLE IF EXISTS sync_tombstones CASCADE;
DROP TABLE IF EXISTS device_tokens CASCADE;
DROP TABLE IF EXISTS friendships CASCADE;
DROP TABLE IF EXISTS user_subscriptions CASCADE;
DROP TABLE IF EXISTS subscription_plan_discounts CASCADE;
DROP TABLE IF EXISTS subscription_plans CASCADE;
DROP TABLE IF EXISTS admin_credentials CASCADE;
DROP TABLE IF EXISTS refresh_tokens CASCADE;
DROP TABLE IF EXISTS otp_codes CASCADE;
DROP TABLE IF EXISTS users CASCADE;
```

### Step 6: Create `internal/database/seed.go`

- `SeedAdmin(ctx context.Context, pool *pgxpool.Pool, username, password string) error`
- `SELECT count(*) FROM admin_credentials` — if 0, hash password with bcrypt (cost 12), insert row
- If count > 0, log "admin already exists, skipping seed" at INFO and return nil
- Idempotent — safe on every startup

### Step 7: Update `cmd/server/main.go`

New startup sequence:

```
1. Load configuration           (existing)
2. Set up logger                (existing)
3. Connect to database          (NEW)
4. Run migrations               (NEW)
5. Seed admin                   (NEW)
6. Create HTTP server           (modified — pass pool)
7. Start server                 (existing)
8. Wait for shutdown signal     (existing)
9. Graceful shutdown            (modified — close pool after server)
```

On graceful shutdown: call `pool.Close()` after `srv.Shutdown(ctx)`.

### Step 8: Update `internal/server/server.go`

- Change signature: `New(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool) *http.Server`
- Pass pool to `handler.Health(pool)`

### Step 9: Update `internal/handler/health.go`

- `Health(pool *pgxpool.Pool) http.HandlerFunc`
- Call `pool.Ping(r.Context())`
- Success → `200 {"status":"ok","timestamp":"..."}`
- Failure → `503 {"status":"degraded","timestamp":"..."}`

### Step 10: Update Tests

**`internal/handler/health_test.go`:**
- Update `Health()` calls to pass `nil` for unit tests where DB isn't needed, or refactor to handle nil pool gracefully (return 503 if pool is nil — defensive)
- Add `TestHealth_NilPool_Returns503` — verifies behavior when pool is nil
- Existing tests that check `status:"ok"` need a working pool or need to accept `"degraded"` with 503

**`internal/server/server_test.go`:**
- Update `New()` calls to pass nil pool
- Existing route/middleware tests remain functionally the same

**New integration tests** (require running Postgres, gated with build tags or skipped):
- `internal/database/` — test Connect, RunMigrations, SeedAdmin against a real DB

---

## File Change Summary

| Action | Path | Purpose |
|---|---|---|
| **Create** | `internal/database/database.go` | DB connection pool management |
| **Create** | `internal/database/migrate.go` | golang-migrate runner |
| **Create** | `internal/database/seed.go` | Admin seeding logic |
| **Create** | `migrations/000001_initial_schema.up.sql` | All 19 tables + indexes |
| **Create** | `migrations/000001_initial_schema.down.sql` | Drop all tables |
| **Modify** | `cmd/server/main.go` | Add DB connect, migrate, seed, pool shutdown |
| **Modify** | `internal/server/server.go` | Accept pool param, pass to handler |
| **Modify** | `internal/handler/health.go` | Accept pool, ping DB, 503 on failure |
| **Modify** | `internal/handler/health_test.go` | Update for new signature |
| **Modify** | `internal/server/server_test.go` | Update for new `New()` signature |
| **Modify** | `go.mod` / `go.sum` | Add pgx, migrate, bcrypt deps |

**Total: 5 new files, 6 modified files**

---

## Verification Checklist

1. `go build ./...` compiles cleanly
2. `go test ./...` passes (unit tests, no DB required)
3. `docker compose up` — API starts, runs migration, seeds admin
4. `GET /health` → `200 {"status":"ok"}` when DB is up
5. `GET /health` → `503 {"status":"degraded"}` when DB is stopped
6. All 19 tables exist (`\dt` in psql)
7. All indexes exist as specified
8. `admin_credentials` has exactly 1 row with bcrypt-hashed password
9. Restart app — no duplicate admin, no migration errors
10. `docker compose down -v && docker compose up` — fresh DB works end-to-end