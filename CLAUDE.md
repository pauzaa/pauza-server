# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run Commands

```bash
# Local development (Docker)
docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build

# Local development (native)
cp .env.dev.example .env.dev
set -a; source .env.dev; set +a
go run ./cmd/migrate        # apply migrations
go run ./cmd/server         # start API server
go run ./cmd/seed-admin     # seed admin user

# Build
go build ./cmd/server
go build ./cmd/migrate
go vet ./...

# Tests
go test ./...                        # unit tests only
go test -race -count=1 ./...         # with race detector
go test -tags=integration ./...      # integration tests (needs TEST_DATABASE_URL, TEST_REDIS_URL)
go test -run TestFunctionName ./internal/service/  # single test
```

## Architecture

Go backend API using **chi** router, **pgx/v5** (Postgres), **Redis** rate limiting, structured **slog** logging.

### Layering: Handler → Service → Repository

- **Handlers** (`internal/handler/`): HTTP request decoding, validation, response/error mapping. No business logic.
- **Services** (`internal/service/`): Business rules, transaction boundaries, returns `*apperror.APIError`.
- **Repositories** (`internal/repository/`): Raw SQL via pgx, row scanning, `ErrNotFound` sentinel. Accept `DBTX` interface (pool or transaction).

Support packages (`auth`, `middleware`, `ratelimit`, `mail`, `push`, `revenuecat`, `ai`) are narrowly scoped and don't cross layers.

### Entry Points

- `cmd/server/main.go` — startup orchestration: config → DB → mail → Redis → push → `server.New()` → background cleanup → graceful shutdown
- `internal/server/server.go` + `routes.go` — HTTP server, middleware stack, route mounting
- `internal/config/config.go` — all env vars (95+) with defaults and validation via `envconfig`

### Key Patterns

- **Passwordless auth**: Email OTP → JWT access (15min) + refresh (720h) tokens. Single-active-session per user.
- **Offline-first sync**: Client SQLite is source of truth; server stores replica. Per-table `sync_version` cursors via shared Postgres sequence. Cursor=0 means full snapshot.
- **Rate limiting**: Redis sliding-window counter (Lua script). Wrapped with fail-open behavior for graceful degradation.
- **Subscriptions**: RevenueCat webhook reconciliation at `/api/v1/webhooks/revenuecat`. Entitlement middleware gates premium endpoints.
- **Error handling**: Standardized `apperror.APIError` with codes (`VALIDATION_ERROR`, `UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`, `CONFLICT`, `RATE_LIMITED`, `SUBSCRIPTION_REQUIRED`, `INTERNAL_ERROR`). Internal errors never leak to clients.

### Database

- PostgreSQL 16, migrations via `golang-migrate/v4` (embedded SQL in `migrations/`)
- Migrations run separately via `cmd/migrate`, never during server startup
- Health probes: `/live` (process), `/ready` (DB ping) — outside `/api/v1`

### Integration Tests

- Build tag: `//go:build integration`
- Require real Postgres (`TEST_DATABASE_URL`) and Redis (`TEST_REDIS_URL`)
- Each test resets the DB schema — never point at a shared database

## Conventions

- Config is environment-only via `envconfig`; all defaults live in `internal/config/config.go`
- Request bodies default to 1 MiB; explicit overrides for photo/AI endpoints (5 MiB)
- Request IDs echoed in `X-Request-Id`
- Middleware order matters: RequestID → RealIP → Logger → Recoverer → body limit → rate limit → auth
- Do not put business logic in handlers or HTTP concerns in repositories
- Use `t.Setenv` in tests, never `os.Clearenv()`

## Reference Documentation

- `BACKEND_SPEC.md` — complete API contract and schema specification
- `docs/openapi.yaml` — OpenAPI 3.0 spec
- `docs/ENDPOINTS.md` — endpoint reference
- `AGENTS.md` + per-package `AGENTS.md` files — detailed package responsibilities
