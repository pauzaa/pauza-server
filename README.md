# Pauza Server

Backend API for [Pauza](https://pauza.dev) — a digital wellbeing and focus app that lets users create modes to block and restrict apps on their phone.

## Table of Contents

- [What it does](#what-it-does)
- [Tech Stack](#tech-stack)
- [Prerequisites](#prerequisites)
- [Running locally](#running-locally)
- [Configuration](#configuration)
- [Running tests](#running-tests)
- [Project structure](#project-structure)
- [API overview](#api-overview)
- [Building for production](#building-for-production)

## What it does

The server provides the infrastructure that powers the Pauza mobile app:

- **Passwordless authentication** — email OTP → JWT access + refresh tokens, single active session per user
- **Client-server replication** — syncs local SQLite (mobile) to PostgreSQL (server) using per-table `sync_version` cursors; cursor `0` triggers a full snapshot, any higher value returns incremental changes
- **Subscription management** — RevenueCat webhook ingestion and entitlement enforcement on premium endpoints
- **Social features** — friendships, shared stats, streak-based and focus-time leaderboards
- **Push notifications** — via Firebase Cloud Messaging
- **Admin API** — user management and platform analytics for the admin panel

## Tech Stack

| Component | Technology |
|---|---|
| Language | Go 1.25 |
| Router | chi v5 |
| Database | PostgreSQL 16 (pgx/v5) |
| Cache / Rate limiting | Redis 7 |
| Migrations | golang-migrate/v4 |
| Auth | JWT (golang-jwt/jwt v5) |
| Email | go-mail (SMTP) |
| Push | Firebase Cloud Messaging (Admin SDK) |
| AI analysis | OpenAI / Google Gemini (optional) |
| Containerization | Docker + Docker Compose |

---

## Prerequisites

### Docker route (recommended)

| Tool | Install |
|---|---|
| Docker Engine 24+ | https://docs.docker.com/engine/install/ |
| Docker Compose v2 | bundled with Docker Desktop; or `apt install docker-compose-plugin` |

### Native route

| Tool | Version | Install |
|---|---|---|
| Go | 1.25+ | https://go.dev/dl/ |
| PostgreSQL | 16 | https://www.postgresql.org/download/ |
| Redis | 7 | https://redis.io/docs/install/ |

---

## Running locally

### Option A — Docker Compose (recommended)

This spins up the API, PostgreSQL, Redis, and [Mailpit](https://mailpit.axllent.org) (local SMTP UI to inspect OTP emails) behind a Traefik reverse proxy.

```bash
# 1. Clone and enter the directory
git clone <repo-url>
cd pauza-server

# 2. Copy the example env file (safe dummy values, never sent to real services)
cp .env.dev.example .env.dev

# 3. Start all services
docker compose -f docker-compose.yml -f docker-compose.dev.yml --env-file .env.dev up --build

# 4. Apply database migrations (first time only, or after adding new migrations)
docker compose -f docker-compose.yml -f docker-compose.dev.yml --env-file .env.dev run --rm api ./pauza-migrate

# 5. (Optional) Seed the admin user
docker compose -f docker-compose.yml -f docker-compose.dev.yml --env-file .env.dev run --rm api ./pauza-seed-admin
```

**Service URLs once running:**

| Service | URL |
|---|---|
| API | http://localhost:8080 |
| API health check | http://localhost:8080/live |
| API readiness | http://localhost:8080/ready |
| Mailpit (email UI) | http://localhost:8025 |
| PostgreSQL | localhost:5432 |
| Redis | localhost:6379 |

### Option B — Native (no Docker)

```bash
# 1. Copy and edit the env file
cp .env.dev.example .env.dev
# Edit DATABASE_URL and REDIS_URL to point at your local Postgres and Redis instances

# 2. Load environment variables
set -a; source .env.dev; set +a

# 3. Apply migrations
go run ./cmd/migrate

# 4. Start the server
go run ./cmd/server

# 5. (Optional) Seed the admin user
go run ./cmd/seed-admin
```

The server listens on the port set by `PORT` (default `8080`).

---

## Configuration

All configuration is done via environment variables. Copy `.env.dev.example` to `.env.dev` and adjust as needed. The full list of variables with their defaults lives in `internal/config/config.go`.

Key variables:

| Variable | Description | Default |
|---|---|---|
| `PORT` | HTTP listen port | `8080` |
| `DATABASE_URL` | PostgreSQL connection string | — |
| `REDIS_URL` | Redis connection string | — |
| `JWT_SECRET` | Secret for signing JWTs | — |
| `JWT_ACCESS_TOKEN_TTL` | Access token lifetime | `15m` |
| `JWT_REFRESH_TOKEN_TTL` | Refresh token lifetime | `720h` |
| `SMTP_HOST` | SMTP server hostname | — |
| `SMTP_PORT` | SMTP server port | — |
| `SMTP_FROM` | Sender address for OTP emails | — |
| `FIREBASE_SERVICE_ACCOUNT_JSON` | Firebase service account (push notifications) | — (disabled if empty) |
| `REVENUECAT_WEBHOOK_SECRET` | Validates incoming RevenueCat webhooks | — |
| `AI_PROVIDER` | `openai` or `gemini` (AI analysis endpoints) | — (disabled if empty) |
| `ADMIN_CORS_ORIGINS` | Allowed origins for the admin panel | `http://localhost:5173` |
| `PHOTO_STORAGE_DIR` | Directory where profile photos are stored | `/var/lib/pauza/photos` |
| `LOG_LEVEL` | Structured log level (`debug`, `info`, `warn`, `error`) | `info` |

---

## Running tests

### Unit tests

No external dependencies required.

```bash
go test ./...

# With race detector
go test -race -count=1 ./...

# Single test
go test -run TestFunctionName ./internal/service/
```

### Integration tests

Integration tests spin up real database and cache interactions. They require a dedicated (throwaway) Postgres and Redis instance — **do not point these at any shared database**, as each test resets the schema.

```bash
# Set connection strings
export TEST_DATABASE_URL="postgres://pauza:pauza_dev_password@localhost:5432/pauza_test?sslmode=disable"
export TEST_REDIS_URL="redis://localhost:6379/1"

go test -tags=integration ./...
```

If you are using Docker Compose, spin up only the dependency services and run the tests against them:

```bash
# Start Postgres and Redis
docker compose -f docker-compose.yml -f docker-compose.dev.yml --env-file .env.dev up -d db redis

# Run integration tests against them
TEST_DATABASE_URL="postgres://pauza:pauza_dev_password@localhost:5432/pauza?sslmode=disable" \
TEST_REDIS_URL="redis://localhost:6379/1" \
go test -tags=integration ./...

# Tear down when done
docker compose -f docker-compose.yml -f docker-compose.dev.yml --env-file .env.dev down
```

---

## Project structure

```
pauza-server/
├── cmd/
│   ├── server/        # Main entry point — wires up config, DB, services, HTTP server
│   ├── migrate/       # Standalone migration runner
│   └── seed-admin/    # Seeds the initial admin user
├── internal/
│   ├── ai/            # AI analysis (OpenAI / Gemini) integration
│   ├── apperror/      # Standardized API error types
│   ├── auth/          # JWT creation and validation
│   ├── config/        # All env-var config with envconfig
│   ├── database/      # Database connection helpers
│   ├── domain/        # Domain models and types
│   ├── handler/       # HTTP layer — request decoding, validation, response mapping
│   ├── mail/          # SMTP email sending (OTP delivery)
│   ├── middleware/     # Auth middleware, rate limiting middleware
│   ├── pagination/    # Pagination utilities
│   ├── photostore/    # Profile photo storage
│   ├── push/          # Firebase Cloud Messaging integration
│   ├── ratelimit/     # Redis sliding-window rate limiter (Lua script)
│   ├── redact/        # PII redaction for logs
│   ├── repository/    # Database access — raw SQL via pgx, row scanning
│   ├── revenuecat/    # RevenueCat webhook parsing and API client
│   ├── server/        # HTTP server setup, middleware stack, route mounting
│   ├── service/       # Business logic — transaction boundaries, domain rules
│   ├── syncmodel/     # Sync protocol models
│   ├── testdb/        # Test database helpers
│   └── validate/      # Input validation utilities
├── migrations/        # SQL migration files (embedded, run by cmd/migrate)
├── docs/              # OpenAPI spec, endpoint reference
├── deploy/            # Traefik config for dev and prod
├── docker-compose.yml            # Base Compose file
├── docker-compose.dev.yml        # Dev overrides (Traefik on HTTP, Mailpit, local ports)
├── docker-compose.prod.yml       # Production overrides
├── Dockerfile
├── .env.dev.example   # Example environment file for local development
├── BACKEND_SPEC.md    # Complete API contract and schema specification
└── go.mod
```

### Architecture layers

```
HTTP Request
     │
     ▼
  Handler          — decodes request, validates input, maps errors to HTTP responses
     │
     ▼
  Service          — business rules, transaction boundaries, returns apperror.APIError
     │
     ▼
 Repository        — raw SQL via pgx, row scanning, returns ErrNotFound sentinel
     │
     ▼
 PostgreSQL / Redis
```

---

## API overview

Interactive API documentation is available at **https://api.pauza.dev/docs**.

The base path for all user-facing endpoints is `/api/v1`. The admin API is under `/api/v1/admin`.

Health endpoints (outside the API prefix):

| Method | Path | Description |
|---|---|---|
| `GET` | `/live` | Liveness probe — always 200 if the process is running |
| `GET` | `/ready` | Readiness probe — 200 only when the DB is reachable |

Authentication endpoints (no JWT required):

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/auth/start` | Send OTP to email |
| `POST` | `/api/v1/auth/verify` | Verify OTP, receive tokens |
| `POST` | `/api/v1/auth/refresh` | Rotate refresh token |
| `POST` | `/api/v1/auth/logout` | Revoke session |

See [`BACKEND_SPEC.md`](./BACKEND_SPEC.md) for the full API contract, request/response schemas, and error codes. The OpenAPI spec is at [`docs/openapi.yaml`](./docs/openapi.yaml).

---

## Building for production

```bash
# Build binaries
go build -o pauza-server  ./cmd/server
go build -o pauza-migrate ./cmd/migrate

# Or via Docker
docker build -t pauza-server .
```

### Deploying with Docker Compose

The production stack uses `docker-compose.prod.yml`. It pulls the pre-built image from the container registry, runs PostgreSQL and Redis with persistent volumes, and puts Traefik in front with automatic TLS via Let's Encrypt.

Create `.env.prod` on the server (based on `.env.dev.example`) and make sure it includes at minimum:

```
IMAGE_TAG=latest
PUBLIC_DOMAIN=api.pauza.dev
LETSENCRYPT_EMAIL=you@example.com
```

```bash
# Start all services
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d

# Run migrations (first deploy, or after adding new migrations)
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod run --rm api ./pauza-migrate

# Seed the admin user (first deploy only)
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod run --rm api ./pauza-seed-admin

# Deploy a new image version
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod pull
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
```

