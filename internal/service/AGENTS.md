# Service Layer
> See `/AGENTS.md` for repo rules and `internal/AGENTS.md` for boundaries.

## OVERVIEW
`internal/service` owns domain behavior and transaction boundaries. It orchestrates repositories and support clients for auth, admin, social, sync, and RevenueCat webhook reconciliation.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| Passwordless auth | `internal/service/auth*.go` | OTP start/verify, refresh, logout, profile and delete flows |
| Admin operations | `internal/service/admin.go` | admin login, user listing/detail, entitlement overrides |
| Sync orchestration | `internal/service/sync.go` | transactional multi-table sync dispatch |
| Social features | `internal/service/social.go` | device tokens, friendships, leaderboard, premium gating |
| RevenueCat webhook reconciliation | `internal/service/webhook.go` | subscriber fetch + entitlement upserts with override guard |
| Service errors | `internal/service/auth_types.go` | sentinel app errors consumed by handlers |

## CONVENTIONS
- Services accept context from handlers and pass it through to repository and external calls.
- Services map low-level errors to stable sentinel/service errors for handler translation.
- Open transactions at service boundary when operations span multiple repository writes.
- Keep anti-enumeration behavior in auth flow responses.
- Keep webhook reconciliation idempotent and tolerant of duplicate webhook deliveries.

## ANTI-PATTERNS
- Do not write HTTP responses directly in services.
- Do not embed SQL in service code.
- Do not bypass repository `ErrNotFound` semantics when mapping to API errors.
- Do not log secrets, OTP values, raw refresh tokens, or webhook signatures.
