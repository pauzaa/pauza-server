# HTTP Handlers
> See `/AGENTS.md` for shared HTTP rules and `internal/AGENTS.md` for layer boundaries.

## OVERVIEW
`internal/handler` is the HTTP boundary. Handlers decode requests, validate fields, call services, and translate service results into stable JSON responses across auth, profile, social, sync, admin, webhook, and AI analysis domains.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| Auth + profile endpoints | `internal/handler/auth.go` | passwordless start/verify/refresh/logout, profile and preference updates, photo upload |
| Social endpoints | `internal/handler/social.go` | device registration, friend graph, leaderboard APIs |
| Sync endpoint | `internal/handler/sync.go` | table sync request validation + service bridge |
| Admin endpoints | `internal/handler/admin.go` | admin login, users/stats, entitlement override operations |
| RevenueCat webhook endpoint | `internal/handler/webhook.go` | signature verification + webhook dispatch |
| AI analysis endpoints | `internal/handler/ai.go` | usage analysis, focus schedule, daily report, addiction check |
| Health probes | `internal/handler/health.go` | `/live` and `/ready` contracts |
| Handler integration tests | `internal/handler/*_integration_test.go` | production router + DB-backed auth/social/sync flows |
| JSON error envelope | `internal/apperror/apperror.go` | standard codes, messages, details |
| Field validation | `internal/validate/validate.go` + `internal/syncmodel/request.go` | domain field validation and sync table payload validation |

## CONVENTIONS
- Use `decodeJSONBody` for JSON request parsing; it rejects unknown fields and trailing documents.
- Validate at the boundary, then pass `r.Context()` into the service layer.
- Map service sentinel errors through `writeServiceError`; do not invent ad hoc JSON error shapes.
- Keep response timestamps human-readable and UTC RFC3339.
- Passwordless auth responses must not leak whether an email already belongs to an existing user.
- For sync endpoints, return field-specific validation errors using `apperror.ValidationFieldErrors`.

## TESTING
- Unit tests can parallelize; the integration suite is build-tagged and intentionally does not call `t.Parallel()`.
- Integration suites build the real `server.New(...)` stack and reset Postgres between tests.
- Use `captureSender` or equivalent stubs for OTP delivery in handler-level integration tests.

## ANTI-PATTERNS
- Do not put SQL, transactions, or crypto logic in handlers.
- Do not bypass `apperror` helpers or `BACKEND_SPEC.md` response contracts.
- Do not forget `Content-Type: application/json` for JSON responses.
- Do not leak account existence via auth error messages or timing behavior.
- Do not re-parse JWTs in handlers; consume user identity from middleware context.
