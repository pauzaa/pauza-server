# Command Binaries
> See `/AGENTS.md` for repo-wide commands, env setup, and CI expectations.

## OVERVIEW
`cmd/` holds process entrypoints only. Each subdirectory builds one binary and owns startup/shutdown concerns, not business logic.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| HTTP server process | `cmd/server/main.go` | config, logger, DB, Redis, cleanup, signals |
| Apply migrations | `cmd/migrate/main.go` | minimal config loader + embedded migrations |
| Seed admin | `cmd/seed-admin/main.go` | idempotent bootstrap against `admin_credentials` |

## CONVENTIONS
- Keep `main.go` thin: parse config, assemble dependencies, hand off to `internal/...`.
- Use the smallest config loader that fits the binary (`Load`, `LoadMigrate`, `LoadSeedAdmin`).
- `cmd/server` owns graceful shutdown order: stop HTTP, cancel cleanup context, stop limiter cleanup, then close Redis and Postgres.
- `cmd/migrate` is the only supported migration entrypoint; schema changes are explicit pre-start operations.
- Binary names follow directory names.

## ANTI-PATTERNS
- Do not put request-path business logic in `cmd/`.
- Do not auto-run migrations from `cmd/server`.
- Do not make `cmd/migrate` or `cmd/seed-admin` depend on full server-only env.
- Do not forget cleanup functions or resource closes when changing startup code.
