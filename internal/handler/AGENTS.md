# HTTP Handlers
> See `/AGENTS.md` for shared HTTP rules and `internal/AGENTS.md` for layer boundaries.

## OVERVIEW
`internal/handler` is the HTTP boundary. Handlers decode requests, validate fields, call services, and translate service results into stable JSON responses.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| Auth endpoints | `internal/handler/auth.go` | passwordless start, verify, refresh, profile |
| Health probes | `internal/handler/health.go` | `/live` and `/ready` contracts |
| Handler integration tests | `internal/handler/auth_integration_test.go` | production router + DB-backed auth flows |
| JSON error envelope | `internal/apperror/apperror.go` | standard codes, messages, details |
| Field validation | `internal/validate/validate.go` | email, OTP, username, name, platform |

## CONVENTIONS
- Use `decodeJSONBody` for JSON request parsing; it rejects unknown fields and trailing documents.
- Validate at the boundary, then pass `r.Context()` into the service layer.
- Map service sentinel errors through `writeServiceError`; do not invent ad hoc JSON error shapes.
- Keep response timestamps human-readable and UTC RFC3339.
- Passwordless auth responses must not leak whether an email already belongs to an existing user.

## TESTING
- Unit tests can parallelize; the integration suite is build-tagged and intentionally does not call `t.Parallel()`.
- `auth_integration_test.go` builds the real `server.New(...)` stack and resets Postgres between tests.
- Use `captureSender` or equivalent stubs for OTP delivery in handler-level integration tests.

## ANTI-PATTERNS
- Do not put SQL, transactions, or crypto logic in handlers.
- Do not bypass `apperror` helpers or `BACKEND_SPEC.md` response contracts.
- Do not forget `Content-Type: application/json` for JSON responses.
- Do not leak account existence via auth error messages or timing behavior.
