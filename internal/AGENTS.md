# Internal Packages
> See `/AGENTS.md` for project-wide commands, env, and shared HTTP/DB rules.

## OVERVIEW
`internal/` contains application code. The stable boundary is `handler -> service -> repository`, with shared auth, middleware, rate-limit, mail, config, and database support packages alongside it.

## WHERE TO LOOK
| Change | Location | Notes |
| --- | --- | --- |
| HTTP request/response shape | `internal/handler/` | decode, validate, map service errors |
| Business rules | `internal/service/auth.go` | passwordless auth policy, transactions, OTP verification limits |
| SQL and row mapping | `internal/repository/` | explicit queries and `ErrNotFound` |
| JWT/OTP/password primitives | `internal/auth/` | JWT/OTP for user auth, password helpers for admin credentials |
| Middleware ordering | `internal/server/server.go` + `internal/middleware/` | request ID, real IP, recovery, auth |
| Rate-limit backends | `internal/ratelimit/` | memory, Redis, fail-open |
| Startup DB helpers | `internal/database/` | connect, migrate, seed, cleanup |
| SMTP and OTP delivery | `internal/mail/` | `mail.Sender` abstraction and SMTP sender |
| Env parsing/validation | `internal/config/` | `envconfig` tags, defaults, semantic checks |

## CONVENTIONS
- `handler/` validates inputs, calls services with `r.Context()`, and writes JSON only.
- `service/` owns domain rules, transaction boundaries, and sentinel errors returned upward.
- `repository/` owns SQL, locking, row scans, and `ErrNotFound`; it does not know about HTTP.
- `auth/` holds token and OTP primitives for user auth, plus password helpers used by admin credential flows.
- `middleware/` owns request-scoped cross-cutting behavior; ordering in `internal/server/server.go` is significant.
- `database/` is for process/runtime helpers, not feature-specific request queries.
- `mail/`, `validate/`, `apperror/`, and `redact/` are shared support packages; keep their contracts narrow.

## ANTI-PATTERNS
- Do not open DB transactions in handlers.
- Do not move JWT, OTP, or admin-password logic into handlers or middleware.
- Do not let repository code write HTTP responses or depend on `net/http`.
- Do not parse env outside `internal/config`.
- Do not bypass `internal/server/server.go` when changing route or middleware wiring.

## RELATED DOCS
- `internal/auth/AGENTS.md`
- `internal/database/AGENTS.md`
- `internal/handler/AGENTS.md`
- `internal/ratelimit/AGENTS.md`
