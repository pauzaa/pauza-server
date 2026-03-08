# Agent Instructions (pauza-server)

Go HTTP API for Pauza (chi router, pgx Postgres, `log/slog` JSON logging).

Key modules:
- Entry point: `cmd/server/main.go`
- HTTP server/router: `internal/server`
- Handlers: `internal/handler`
- Config/env: `internal/config`
- Postgres/migrations/seed: `internal/database`, `migrations/`

Go version: `go 1.25.1` (`go.mod`); Docker builder: `golang:1.25-alpine`.

## Commands

Dev stack (API + Postgres, dev env injected by compose):

```bash
docker compose up --build
```

Run locally (uses `.env`; run migrations before starting the server):

```bash
cp .env.example .env
set -a; source .env; set +a
go run ./cmd/migrate
go run ./cmd/server
```

Build / format / minimal checks:

```bash
go build ./cmd/server
go build ./cmd/migrate
go build -o pauza-server ./cmd/server
gofmt -w .
go vet ./...
```

> **Note:** The build commands above may leave `server`, `migrate`, `seed-admin`, and
> `pauza-server` binaries in the repo root. These are expected local artifacts and are
> already gitignored.

Optional tools (if installed):

```bash
goimports -w .
golangci-lint run ./...
staticcheck ./...
```

Tests (single-test patterns matter for agents):

```bash
go test ./...
go test -v ./...
go test ./internal/handler -run '^TestLive_TimestampIsRecent$'
go test ./... -run '^TestNew_RequestIDHeader$' -count=1
go test ./some/pkg -run '^TestThing$/^case_name$'
go test ./... -coverprofile=coverage.out && go tool cover -html=coverage.out -o coverage.html
```

Optional when debugging flaky/concurrency issues:

```bash
go test -race ./...
go test ./... -shuffle=on -count=1
```

## Environment

Config is loaded from environment variables via `internal/config` (envconfig).
`.env.example` is the canonical list/template.

Common variables used today:
- Server: `PORT`, `LOG_LEVEL`, `TRUSTED_PROXIES`
- Database: `DATABASE_URL`
- Auth: `JWT_SECRET`, `JWT_ACCESS_TOKEN_TTL`, `JWT_REFRESH_TOKEN_TTL`
- SMTP: `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM`
- Seeds: `ADMIN_SEED_USERNAME`, `ADMIN_SEED_PASSWORD`
- Integrations: `REVENUECAT_API_KEY`, `REVENUECAT_WEBHOOK_SECRET`, `FIREBASE_SERVICE_ACCOUNT_JSON`, `STUDENT_VERIFICATION_PROVIDER`, `STUDENT_VERIFICATION_API_KEY`
- Rate limiting: `REDIS_URL`, `REGISTER_RATE_LIMIT`, `REGISTER_RATE_WINDOW`, `LOGIN_RATE_LIMIT`, `LOGIN_RATE_WINDOW`, `REFRESH_RATE_LIMIT`, `REFRESH_RATE_WINDOW`, `FORGOT_PASSWORD_RATE_LIMIT`, `FORGOT_PASSWORD_RATE_WINDOW`, `RESET_PASSWORD_RATE_LIMIT`, `RESET_PASSWORD_RATE_WINDOW`, `VERIFY_OTP_RATE_LIMIT`, `VERIFY_OTP_RATE_WINDOW`

Rules of thumb:
- Never log secrets (JWT secret, DB URL, SMTP password, webhook secrets).
- When adding a new env var: update `internal/config/config.go` and `.env.example`.
- Auth rate limiting is Redis-backed and fail-open on backend errors; startup should log Redis connectivity clearly without leaking secrets.

### Trusted-Proxy Configuration

`TRUSTED_PROXIES` controls which peers may set the real client IP via `X-Forwarded-For` / `X-Real-Ip`. Getting this right is critical for rate limiting and audit logging.

- **Empty (default):** trust nobody — forwarded headers are ignored. Use this when the server is directly exposed to the internet with no reverse proxy.
- **Too broad:** attackers can spoof their IP to bypass per-IP rate limits.
- **Too narrow (or empty behind a proxy):** rate limits and logs will see the proxy's IP, not the real client. All clients behind that proxy share one rate-limit bucket.

Common deployment patterns:

| Environment | Suggested value |
|---|---|
| Docker Compose (default bridge) | `172.16.0.0/12` |
| Kubernetes pod network | `10.0.0.0/8` |
| AWS ALB + VPC | `10.0.0.0/8,172.16.0.0/12` |
| Cloudflare (see their IP list) | Cloudflare IP ranges |
| Direct exposure (no proxy) | *(leave empty)* |

The server logs the parsed trusted-proxy networks at startup (`"trusted proxies configured"`) so operators can verify the active configuration without inspecting env vars.

## Project Layout

- `cmd/server/`: binary entry point; owns process-level concerns (startup, shutdown).
- `internal/server/`: router + middleware; keep it wiring-only.
- `internal/handler/`: HTTP handlers; should validate inputs and translate to domain/DB calls.
- `internal/database/`: DB connection, migrations, and seeding helpers.
- `migrations/`: `golang-migrate` SQL migrations (`*.up.sql` / `*.down.sql`).

## HTTP Conventions

- Router: `chi` with common middleware already set up in `internal/server/server.go`.
- Request IDs: `middleware.RequestID` + a small middleware that echoes `X-Request-Id` back.
- Health probes: `GET /live` (liveness, process-local) and `GET /ready` (readiness, pings DB); neither under `/api/v1`.
- Responses:
  - JSON responses set `Content-Type: application/json`.
  - If `json.Encoder.Encode` fails after headers are written, only log.
- Errors:
  - Follow `BACKEND_SPEC.md` for stable error codes and safe messages.
  - Don’t leak internal errors to clients; log details server-side.

## Database / Migrations

- Driver/pool: `pgxpool.Pool`; always pass a context (`r.Context()` in handlers).
- Queries:
  - Use explicit column lists (no `SELECT *`).
  - Prefer parameterized SQL (`$1`, `$2`, ...).
  - Map `pgx.ErrNoRows` to `404` where it represents a missing resource.
- Migrations:
  - Applied via the dedicated `cmd/migrate` command, not at server startup.
  - The schema is defined by a single baseline migration (`migrations/000001_initial_schema`).
  - New migrations should be added as the next ordered pair after the baseline.
    The next available number is `000002`.
    Example: `migrations/000002_description.up.sql` and `migrations/000002_description.down.sql`.
- Seeding:
  - Keep seeds idempotent (see `internal/database/SeedAdmin`).
  - Password hashing uses bcrypt (cost 12 in current seed code).

## Code Style (Project Conventions)

- Formatting: run `gofmt`; prefer early returns; keep handlers small.
- Imports: stdlib / third-party / local (`github.com/IsorilovA/pauza-server/...`) with blank lines; allow `_` imports only for driver registration (see `internal/database/migrate.go`).
- Layout: binaries in `cmd/<name>/`; shared code in `internal/...`.
- Naming: exported `CamelCase`, unexported `camelCase`; tests use `TestXxx_Yyy`.
- Types/JSON: explicit request/response structs with `json` tags; use pointers for optional fields when "missing" vs "zero" matters.
- Time: internal `time.Time`; external timestamps in UTC RFC3339 when human-readable (see `internal/handler/health.go`).
- Context: `ctx context.Context` first for I/O; handlers pass `r.Context()` to DB.
- Logging: use `log/slog` JSON; prefer structured fields and a consistent error key (`"err"`); log at boundaries, not deep helpers.
- Errors: wrap with `fmt.Errorf("...: %w", err)`; messages lowercase/no trailing punctuation; use sentinel errors only when callers need `errors.Is`.
- Repository errors: return sentinel `ErrNotFound` directly; wrap non-sentinel repository errors with `fmt.Errorf("...: %w", err)` so `errors.Is` checks stay consistent.
- HTTP: set `Content-Type: application/json` for JSON; if encoding fails after headers, just log.
- DB: use `pgxpool.Pool`; explicit column lists (no `SELECT *`); map `pgx.ErrNoRows` to `404` where appropriate; migrations via `golang-migrate` are run by `cmd/migrate`, not at server startup.
- API semantics: follow `BACKEND_SPEC.md` for stable error codes, no sensitive leaks, and no account enumeration.
- Tests: keep deterministic (`t.Setenv`, `httptest`); allow small time tolerances; gate DB/network integration tests with build tags (e.g., `//go:build integration`).
- Secrets: never commit `.env` (gitignored); update `.env.example` when adding env vars.

## Testing Notes

- Prefer anchored `-run` regexes (`^...$`) to avoid accidental matches.
- Avoid process-wide env changes like `os.Clearenv()`; use `t.Setenv` and save/restore patterns.
- Use UTC for time assertions; allow small tolerances for wall-clock measurements.
- If you add integration tests, gate them with build tags and document the command:
  `go test -tags=integration ./...`.

## Agent Safety

- Don’t commit secrets (`.env`, credentials, service account JSON); keep logs redacted.
- Avoid destructive commands by default (dropping DB volumes, `git reset --hard`, force pushes).
- If a change touches API semantics, cross-check `BACKEND_SPEC.md` and keep behavior stable.

## Cursor / Copilot Rules

No Cursor rules found in `.cursor/rules/` or `.cursorrules`. No Copilot instructions found in `.github/copilot-instructions.md`.
