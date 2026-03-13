# Server Assembly
> See `/AGENTS.md` for repo-wide rules and `internal/AGENTS.md` for package boundaries.

## OVERVIEW
`internal/server` wires the HTTP process together: middleware stack, dependency construction, rate-limit instances, route mounting, and `http.Server` configuration.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| Router + base middleware | `internal/server/server.go` | request ID echo, trusted proxy IP, logging, panic recovery |
| Route map | `internal/server/routes.go` | health, auth, admin, social, sync, webhook, photo routes |
| Dependency wiring | `internal/server/modules.go` | repository/service/handler composition |
| Limiter construction | `internal/server/limiters.go` | Redis limiter creation + fail-open wrappers |
| HTTP server timeouts | `internal/server/http_server.go` | read/write/idle timeout defaults |

## CONVENTIONS
- Keep this package focused on composition; request behavior belongs in handlers/services/middleware.
- Apply global API body-size limits in router middleware, with targeted per-route overrides only when needed.
- Mount probes (`/live`, `/ready`) outside `/api/v1`.
- Build all runtime limiters from Redis and wrap with fail-open.
- Return a cleanup function from `New(...)` and ensure callers invoke it during shutdown.

## ANTI-PATTERNS
- Do not embed business logic in route mounting functions.
- Do not bypass middleware ordering when adding routes.
- Do not silently switch limiter backends in startup wiring.
- Do not add routes directly in command binaries; keep routing centralized here.
