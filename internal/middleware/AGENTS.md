# HTTP Middleware
> See `/AGENTS.md` for shared HTTP rules and `internal/AGENTS.md` for layering.

## OVERVIEW
`internal/middleware` contains request-scoped cross-cutting behavior: JWT auth context, trusted proxy IP extraction, panic recovery, and rate-limit enforcement adapters.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| JWT middleware | `internal/middleware/auth.go` | bearer parsing, role checks, auth user context |
| Rate-limit middleware | `internal/middleware/ratelimit.go` | key extraction, `X-RateLimit-*`, 429 + `Retry-After` |
| Real client IP extraction | `internal/middleware/realip.go` | trusted proxy-only `X-Forwarded-For` / `X-Real-Ip` handling |
| Panic recovery | `internal/middleware/recovery.go` | safe JSON error + structured logs |
| Context helpers | `internal/middleware/context.go` | auth-user context getter/setter |

## CONVENTIONS
- Keep middleware deterministic and side-effect light; business rules remain in services.
- Always return generic client auth errors while logging detailed causes server-side.
- Only trust forwarded IP headers when the direct peer matches configured trusted CIDRs.
- Emit rate-limit headers only when the limiter returns real budget values.
- Keep auth context shape stable for handlers that call `requireUser`.

## ANTI-PATTERNS
- Do not parse JWTs in handlers if middleware already enforces auth.
- Do not trust `RemoteAddr` blindly behind proxies.
- Do not swallow limiter backend errors without fail-open logging.
- Do not leak panic traces or token details in HTTP responses.
