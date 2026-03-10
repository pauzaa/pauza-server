# Database Runtime
> See `/AGENTS.md` for repo-wide DB rules and `internal/AGENTS.md` for package boundaries.

## OVERVIEW
`internal/database` owns process-level Postgres helpers: connect, migrate, seed, cleanup, and the destructive integration-test utilities. Feature SQL belongs in `internal/repository`.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| Connect and ping | `internal/database/database.go` | `pgxpool.New` + ping on startup |
| Apply migrations | `internal/database/migrate.go` | embedded FS + `postgres://` -> `pgx5://` rewrite |
| Seed admin | `internal/database/seed.go` | idempotent `admin_credentials` bootstrap |
| Cleanup job | `internal/database/cleanup.go` | immediate pass + ticker-based retention cleanup |
| Test helpers | `internal/database/testhelper_test.go` | `TEST_DATABASE_URL`, schema reset, `coreTables()` |
| Schema | `migrations/000001_initial_schema.up.sql` | flattened pre-release schema |

## CONVENTIONS
- Run migrations via `cmd/migrate`; server startup does not apply schema changes.
- Keep migration SQL embedded through `migrations.FS`.
- `SeedAdmin` must remain idempotent and race-safe.
- Cleanup helpers delete expired OTPs and revoked or expired refresh tokens; config lives in `internal/config`.
- Keep `coreTables()` aligned with the migration baseline when schema tables change.

## TESTING
- Integration tests use `//go:build integration` and require `TEST_DATABASE_URL`.
- `testhelper_test.go` drops and recreates the `public` schema; use a disposable database only.
- Migration tests validate both fresh apply and idempotent re-run behavior.

## ANTI-PATTERNS
- Do not move request-path SQL into this package.
- Do not auto-run migrations from the HTTP server binary.
- Do not point integration helpers at a shared or production-like database.
- Do not add migration files without both `up` and `down` SQL.
