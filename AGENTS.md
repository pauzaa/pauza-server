# PROJECT KNOWLEDGE BASE

Generated: 2026-03-14
Commit: 8a889a0
Branch: main

## OVERVIEW
Pauza backend HTTP API in Go. The server uses chi routing, pgx/Postgres, JSON slog logging, Redis-backed rate limiting with fail-open behavior, and RevenueCat webhook reconciliation for subscription state.

## STRUCTURE
```text
pauza-server/
├── cmd/                    # binaries: server, migrate, seed-admin
├── internal/               # app packages; see internal/AGENTS.md
├── migrations/             # embedded SQL migrations
├── docs/                   # OpenAPI + endpoint docs + deployment notes
├── BACKEND_SPEC.md         # API and schema contract
├── .env.dev.example        # local Docker Compose environment template
├── .env.prod.example       # production-style Compose environment template
├── docker-compose.yml      # shared Compose base
├── docker-compose.dev.yml  # local dev overlay (Nginx + Postgres + Redis)
└── docker-compose.prod.yml # production overlay (single-host Nginx + API + DB + Redis)
```

Child docs:
- `cmd/AGENTS.md`
- `internal/AGENTS.md`
- `internal/auth/AGENTS.md`
- `internal/database/AGENTS.md`
- `internal/handler/AGENTS.md`
- `internal/middleware/AGENTS.md`
- `internal/push/AGENTS.md`
- `internal/repository/AGENTS.md`
- `internal/ratelimit/AGENTS.md`
- `internal/revenuecat/AGENTS.md`
- `internal/server/AGENTS.md`
- `internal/service/AGENTS.md`
- `internal/syncmodel/AGENTS.md`
- `internal/ai/AGENTS.md`
- `docs/AGENTS.md`
- `migrations/AGENTS.md`

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| Startup and shutdown | `cmd/server/main.go` | config -> DB -> mail -> Redis -> push -> `server.New` -> cleanup |
| Router and middleware wiring | `internal/server/` | probes, auth/admin/social/sync routes, middleware stack, rate-limit attachment |
| Business rules | `internal/service/` | auth, admin, sync, social, webhook reconciliation |
| SQL and transactions | `internal/repository/` | auth, entitlement, social, sync, admin SQL |
| HTTP boundary | `internal/handler/` | request decoding, validation, response/error mapping |
| JWT/OTP/password primitives | `internal/auth/` | token and credential primitives |
| Rate limiting | `internal/ratelimit/` + `internal/middleware/ratelimit.go` | Redis backends + HTTP header behavior |
| AI provider abstraction | `internal/ai/` | OpenAI and Gemini behind a `Provider` interface |
| DB runtime helpers | `internal/database/` | connect, migrate, seed, cleanup, destructive integration test helpers |
| API contract | `docs/openapi.yaml` + `BACKEND_SPEC.md` | stable response and error semantics |

## CONVENTIONS
- Env is loaded via `envconfig`; `internal/config/config.go` owns defaults and validation.
- Migrations are applied with `cmd/migrate`, not during `cmd/server` startup.
- Health probes stay outside `/api/v1`: `/live` is process-only, `/ready` pings Postgres.
- Layering is `handler -> service -> repository`; support packages (`auth`, `middleware`, `ratelimit`, `mail`, `revenuecat`, `ai`) stay narrowly scoped.
- Request IDs are echoed back in `X-Request-Id`.
- Request bodies default to 1 MiB and use explicit overrides only where needed (e.g. photo upload).
- Runtime rate limiting is Redis-backed and wrapped with fail-open behavior so backend outages degrade gracefully after startup.

## ANTI-PATTERNS
- Never log or commit secrets (`JWT_SECRET`, DB URLs, SMTP creds, Firebase keys, webhook secrets, AI API keys, `.env.dev`, `.env.prod`).
- Do not widen `TRUSTED_PROXIES` casually; incorrect trust lets clients spoof source IPs.
- Do not move business logic into handlers or HTTP concerns into repositories.
- Do not use unbounded/non-parameterized SQL in repositories.
- Do not point integration helpers at shared databases; integration test helpers reset schema state.
- Do not run process-wide env wipes in tests (`os.Clearenv()`); use `t.Setenv`.

## COMMANDS
```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build

cp .env.dev.example .env.dev
set -a; source .env.dev; set +a
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
```

## NOTES
- Compose does not apply migrations automatically; run the migrate binary as a release step.
- Build commands may leave gitignored local binaries in repo root (`server`, `migrate`, `seed-admin`, `pauza-server`).
- The current baseline schema is `migrations/000001_initial_schema.up.sql`.
