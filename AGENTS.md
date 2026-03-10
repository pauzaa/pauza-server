# PROJECT KNOWLEDGE BASE

Generated: 2026-03-08
Commit: 57b85a1
Branch: main

## OVERVIEW
Pauza backend HTTP API in Go. chi router, pgx/Postgres, slog JSON logging, and Redis-backed auth rate limiting that fails open on backend errors.

## STRUCTURE
```text
pauza-server/
├── cmd/                  # binaries: server, migrate, seed-admin
├── internal/             # app packages; see internal/AGENTS.md
├── migrations/           # embedded SQL migrations
├── BACKEND_SPEC.md       # API and schema contract
├── .env.example          # canonical env template
├── docker-compose.yml    # shared Compose base for app-facing services
├── docker-compose.dev.yml # local dev overlay (Nginx + Postgres + Redis)
└── docker-compose.prod.yml # production overlay (single-host Nginx + API + DB + Redis)
```

Child docs:
- `cmd/AGENTS.md`
- `internal/AGENTS.md`
- `internal/auth/AGENTS.md`
- `internal/database/AGENTS.md`
- `internal/handler/AGENTS.md`
- `internal/ratelimit/AGENTS.md`

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| Startup and shutdown | `cmd/server/main.go` | config -> DB -> mail -> Redis -> server.New -> cleanup |
| Router and middleware wiring | `internal/server/server.go` | probes, auth routes, rate-limit attachment |
| Auth policy | `internal/service/auth.go` | passwordless start, verify, refresh, anti-enumeration |
| Auth SQL | `internal/repository/auth.go` | user, OTP, refresh-token, entitlement queries and locks |
| JWT / OTP / password | `internal/auth/AGENTS.md` | token and credential primitives |
| DB runtime helpers | `internal/database/AGENTS.md` | connect, migrate, seed, cleanup, destructive test helpers |
| HTTP boundary | `internal/handler/AGENTS.md` | request decoding, validation, response mapping |
| Rate limiting | `internal/ratelimit/AGENTS.md` | memory vs Redis limiter, fail-open behavior |
| Env and validation | `internal/config/config.go` | envconfig tags, defaults, semantic checks |
| API contract | `BACKEND_SPEC.md` | stable response and error semantics |

## CODE MAP
| Symbol | Type | Location | Role |
| --- | --- | --- | --- |
| `main` | func | `cmd/server/main.go` | process startup, signals, shutdown order |
| `New` | func | `internal/server/server.go` | builds router, middleware, services, limiters |
| `AuthService` | type | `internal/service/auth.go` | business rules and transaction boundaries |
| `PgxAuthRepository` | type | `internal/repository/auth.go` | auth-related SQL access |
| `JWTAuth` | func | `internal/middleware/auth.go` | access-token enforcement on protected routes |
| `RunMigrations` | func | `internal/database/migrate.go` | embedded migration runner |
| `StartCleanup` | func | `internal/database/cleanup.go` | background auth-data retention job |

## CONVENTIONS
- Env is loaded with `envconfig`; `.env.example` is the canonical list and `internal/config/config.go` owns validation.
- Migrations are applied with `cmd/migrate`, not at server startup.
- Health probes stay outside `/api/v1`: `/live` is process-only, `/ready` pings Postgres.
- `internal/handler` validates and serializes only; `internal/service` owns business rules; `internal/repository` owns SQL.
- Rate limiting is per auth endpoint class and should use Redis + fail-open semantics in multi-instance deployments.
- Integration tests use `//go:build integration`; DB- and Redis-backed suites require dedicated `TEST_DATABASE_URL` / `TEST_REDIS_URL`.

## ANTI-PATTERNS
- Never log or commit secrets (`JWT_SECRET`, DB URLs, SMTP creds, webhook secrets, `.env`).
- Do not widen `TRUSTED_PROXIES` casually; a broad allowlist lets clients spoof IPs and bypass per-IP limits.
- Do not use `SELECT *`; keep explicit column lists and parameterized SQL.
- Do not move business logic into handlers or HTTP concerns into repositories.
- Do not point integration helpers at a shared database; `internal/database/testhelper_test.go` drops and recreates `public`.
- Do not use process-wide env wipes like `os.Clearenv()` in tests; prefer `t.Setenv` and save/restore.

## UNIQUE STYLES
- Request IDs are echoed back in `X-Request-Id`, even on recovered failures.
- Request bodies are capped at 1 MiB before handlers run.
- Auth start/verify flows avoid leaking account-existence signals.
- Refresh-token reuse detection revokes all refresh tokens for that user.
- Mail logging uses redaction and never logs OTP values.

## COMMANDS
```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build

cp .env.example .env
set -a; source .env; set +a
go run ./cmd/migrate
go run ./cmd/server
go run ./cmd/seed-admin

go build ./cmd/server
go build ./cmd/migrate
go build -o pauza-server ./cmd/server
go vet ./...

go test ./...
go test -race -count=1 ./...
go test -tags=integration ./...
go test ./internal/handler -run '^TestLive_TimestampIsRecent$'
```

## NOTES
- Use `docker-compose.yml` with `docker-compose.dev.yml` for local development and with `docker-compose.prod.yml` for single-host production-style deployments; the overlays load `.env.dev` and `.env.prod` via `env_file`.
- Compose does not apply migrations automatically; run the migrate binary as a separate release step.
- Build commands leave gitignored local binaries in the repo root (`server`, `migrate`, `seed-admin`, `pauza-server`).
- The current pre-release schema is flattened into `migrations/000001_initial_schema.up.sql`.
- For RevenueCat integration work, prefer local code/spec context first, but web-search current RevenueCat official documentation when API, webhook, or customer-state details may have changed.
