# Sync Feature Plan

## Goal

Implement `POST /api/v1/sync` from `BACKEND_SPEC.md` with JWT auth, per-user rate limiting, last-write-wins upserts, tombstone-based deletions, and response payloads for all synced tables already present in the baseline migration.

## Constraints

- Follow the existing `handler -> service -> repository` layering.
- Keep handlers responsible for decode/validate/respond only.
- Keep transactions in the service layer.
- Keep SQL in the repository layer with explicit column lists.
- Reuse existing `decodeJSONBody`, `writeServiceError`, `apperror`, and rate-limit patterns.
- Do not add commits or pushes.

## Scope

### In Scope

1. Add `Query` to `repository.DBTX` and update test fakes if compilation requires it.
2. Add sync endpoint config:
   - `SyncRateLimit`
   - `SyncRateWindow`
   - `.env.example` entries
   - config validation tests
3. Add `middleware.UserIDKey` for per-authenticated-user rate limiting.
4. Add shared sync domain/output types in a small feature package to avoid duplicating 9 table record shapes across layers.
5. Add `internal/repository/sync.go` with sync repository logic for all synced tables:
   - `modes`
   - `mode_blocked_apps`
   - `schedules`
   - `restriction_sessions`
   - `restriction_lifecycle_events`
   - `nfc_linked_chips`
   - `qr_linked_codes`
   - `streak_session_daily_rollups`
   - `streak_daily_aggregates`
   - plus `sync_tombstones`
6. Add `internal/service/sync.go` with one transaction per sync request and dependency-safe processing order.
7. Add `internal/handler/sync.go` with strict JSON decoding and per-table validation.
8. Wire `POST /api/v1/sync` in `internal/server/server.go` under JWT auth and sync-specific rate limiting.
9. Add tests for config, middleware key extraction, server wiring, handler validation, and DB-backed sync behavior.

### Deferred

1. Tombstone garbage collection job for rows older than 90 days.
2. Per-route request body size override for unusually large full-restore payloads.

## Key Design Decisions

### Request Modeling

- Use handler-local request structs with pointer fields for required numeric values so missing `last_synced_at` and missing numeric columns can be validated cleanly.
- Convert validated request structs into shared concrete sync domain types before calling the service.
- Reject unknown JSON fields through the existing `decodeJSONBody` behavior.

### Transaction Model

- Open one transaction for the full sync request in `SyncService`.
- Process tables in dependency-safe order so FK-backed inserts succeed.
- Keep echo suppression inside repository/service logic by tracking keys successfully written from the client and excluding them from server-change responses.

### Cursor Rules

- Use `updated_at` for normal synced tables.
- Use `created_at` for `restriction_lifecycle_events` because it is append-only and has no `updated_at`.
- Convert client `last_synced_at` from Unix milliseconds to `TIMESTAMPTZ` only for tombstone queries.

### Tombstones

- Insert tombstones for explicit deletions.
- Also account for FK-driven cascades that the migration introduces, especially from `modes` and `restriction_sessions`, so restore semantics are not broken by silent server-side cascades.
- Store composite deletion keys in a stable encoded form and decode them back into response objects.

### Rate Limiting

- Add a dedicated sync limiter using `UserIDKey` because the spec requires sync rate limiting per authenticated user.

## Files To Modify

- `internal/repository/auth.go`
- `internal/service/fake_test.go`
- `internal/config/config.go`
- `internal/config/config_test.go`
- `.env.example`
- `internal/middleware/ratelimit.go`
- `internal/middleware/ratelimit_test.go`
- `internal/server/server.go`
- `internal/server/server_test.go`

## Files To Add

- `internal/syncmodel/types.go`
- `internal/repository/sync.go`
- `internal/service/sync.go`
- `internal/handler/sync.go`
- `internal/handler/sync_test.go`
- `internal/handler/sync_integration_test.go`

## Execution Steps

1. Expand `DBTX` to support multi-row queries and keep tests compiling.
2. Add sync rate-limit config and `.env.example` coverage.
3. Add `UserIDKey` and tests.
4. Add shared sync domain/output types.
5. Implement repository sync helpers for all tables, including tombstone encode/decode and cursor queries.
6. Implement service orchestration with one transaction and dependency-safe table order.
7. Implement handler decode/validate/respond logic.
8. Wire route and sync limiter in the server.
9. Add/update tests and iterate until diagnostics and tests are clean.

## Task QA Scenarios

### 1. Expand `DBTX`

- Tool: `lsp_diagnostics`, `go test`
- Steps:
  1. Add `Query` to `repository.DBTX`.
  2. Update any fake pool/tx implementations that must satisfy the interface.
  3. Run `lsp_diagnostics` on `internal/repository/auth.go`, `internal/repository/tx.go`, and `internal/service/fake_test.go`.
  4. Run `go test ./internal/repository ./internal/service`.
- Expected: zero diagnostics; existing repository/service tests still compile and pass.

### 2. Add Sync Rate-Limit Config

- Tool: `go test`
- Steps:
  1. Add `SyncRateLimit` and `SyncRateWindow` to config with defaults and validation.
  2. Update `.env.example` and any test configs used by server/integration tests.
  3. Run `go test ./internal/config ./internal/server`.
- Expected: config tests pass; server tests compile with the new config fields.

### 3. Add `UserIDKey`

- Tool: `go test`
- Steps:
  1. Add `UserIDKey` to `internal/middleware/ratelimit.go`.
  2. Add unit tests for context user present and fallback behavior.
  3. Run `go test ./internal/middleware`.
- Expected: per-user key extraction is deterministic and middleware tests pass.

### 4. Add Shared Sync Domain Types

- Tool: `lsp_diagnostics`, `go test`
- Steps:
  1. Add the shared sync model package and types.
  2. Ensure they compile cleanly with repository/service imports.
  3. Run `lsp_diagnostics` on the new package and `go test ./internal/...` if the fan-out is small enough, otherwise the directly affected packages.
- Expected: no import cycles, no type errors, and clean compilation of dependent packages.

### 5. Implement Repository Sync Helpers

- Tool: DB-backed tests via `go test -tags=integration`
- Steps:
  1. Implement per-table repository logic for upserts, deletions, tombstones, and server-change queries.
  2. Add DB-backed tests for at least one single-key table, one composite-key table, and the append-only lifecycle-events table.
  3. Add a DB-backed cascade-tombstone scenario: delete a parent `mode` or `restriction_session`, verify child tombstones are created for cascaded rows.
  4. Run `go test -tags=integration ./internal/handler -run 'Sync|sync'` once the endpoint path exists, or a narrower DB-backed package command if repository integration tests are added elsewhere.
- Expected: repository behavior proves LWW, tombstone creation, composite deletion encoding, and cascade-tombstone propagation.

### 6. Implement Service Orchestration

- Tool: `go test`
- Steps:
  1. Implement one-transaction sync orchestration.
  2. Add unit or DB-backed tests proving first sync, older-client-loss, newer-client-win, and echo suppression.
  3. Run the affected test package command.
- Expected: service logic leaves the database and response state consistent with the sync spec.

### 7. Implement Handler Logic

- Tool: `go test`
- Steps:
  1. Implement strict request decoding and per-table validation.
  2. Add unit tests for missing auth context, malformed JSON, unknown fields, missing `last_synced_at`, invalid composite deletion objects, and service error mapping.
  3. Run `go test ./internal/handler`.
- Expected: handler returns standard `401`/`422` envelopes and encodes successful responses correctly.

### 8. Wire Route And Limiter

- Tool: `go test`, `go build`
- Steps:
  1. Register `POST /api/v1/sync` in the protected route group.
  2. Attach the sync limiter using `UserIDKey`.
  3. Add server tests for non-404 route existence, unauthorized requests returning `401`, and valid-JWT pass-through to the handler path.
  4. Run `go test ./internal/server` and `go build ./cmd/server`.
- Expected: the route is wired, protected, rate-limited per user, and the server binary builds.

### 9. End-To-End Sync Behavior

- Tool: `go test -tags=integration`
- Steps:
  1. Use the existing handler integration harness to create a user, obtain a JWT, and call `POST /api/v1/sync`.
  2. Verify first sync / empty sync behavior.
  3. Verify last-write-wins for a single-key table.
  4. Verify composite-key upsert + deletion behavior.
  5. Verify append-only lifecycle events use `created_at`.
  6. Verify parent-delete cascade tombstones are later returned to a restore/full-sync request.
  7. Run `go test -tags=integration ./internal/handler -run 'Sync|sync'`.
- Expected: the full request path works against the real router, middleware, and database.

## Verification

1. `lsp_diagnostics` on every changed Go file: zero errors.
2. `go test ./internal/config ./internal/middleware ./internal/server`
3. `go test ./internal/handler`
4. `go test -tags=integration ./internal/handler -run 'Sync|sync'` if the environment is available.
5. `go build ./cmd/server`

## Minimum Acceptance Criteria

- Authenticated `POST /api/v1/sync` is wired and non-404.
- Missing or invalid auth returns `401`.
- Malformed sync payload returns `422` with the standard error envelope.
- First sync can return server-side data.
- Client newer records overwrite server records.
- Server newer records are returned to client.
- Client-written records are not echoed back in the same response.
- Composite-key deletions round-trip correctly.
- `restriction_lifecycle_events` uses `created_at` as its cursor.
- Deleting a parent row that cascades in PostgreSQL also produces tombstones for the affected child rows, and a later restore/full-sync returns those child deletions.
