# Rate Limiting
> See `/AGENTS.md` for env names and `internal/AGENTS.md` for package boundaries.

## OVERVIEW
`internal/ratelimit` implements the limiter backends and failure semantics used by auth middleware. HTTP header emission and request-key extraction live in `internal/middleware/ratelimit.go`.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| Shared limiter contract | `internal/ratelimit/ratelimit.go` | `Limiter`, `Result`, legacy in-memory backend |
| Redis limiter | `internal/ratelimit/redis.go` | Lua script, shared counters, caller-owned client |
| Fail-open wrapper | `internal/ratelimit/failopen.go` | warn and allow on backend error |
| HTTP integration | `internal/middleware/ratelimit.go` | `IPKey`, `EmailKey`, `X-RateLimit-*`, `Retry-After` |
| Redis integration tests | `internal/ratelimit/redis_integration_test.go` | requires `TEST_REDIS_URL` |

## CONVENTIONS
- Treat `Limiter` as backend logic only; middleware owns HTTP status and header behavior.
- Runtime rate limiting is Redis-backed in every environment; startup should fail if Redis is unavailable.
- Redis limiters should be wrapped in `NewFailOpen(...)` so post-startup backend outages degrade to logging and allow.
- `Stop()` must release any background resources; it is a no-op for shared-client implementations like Redis.
- Keep key-prefix and window semantics stable across middleware, tests, and observability.

## TESTING
- Unit tests cover fail-open behavior and middleware semantics.
- Redis tests are build-tagged integration tests and require `TEST_REDIS_URL`.
- Header behavior is asserted from middleware tests, not backend tests.

## ANTI-PATTERNS
- Do not swallow backend errors without logging.
- Do not change reset or remaining semantics without updating middleware tests.
- Do not assume `RemoteAddr` is trustworthy unless trusted-proxy middleware already ran.
- Do not silently reintroduce an in-process limiter into server startup paths.
