# RevenueCat Client
> See `/AGENTS.md` for subscription-related context and `internal/service/AGENTS.md` for webhook reconciliation flow.

## OVERVIEW
`internal/revenuecat` encapsulates RevenueCat API integration details: subscriber fetch calls, API error typing, and entitlement-state derivation from subscriber payloads.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| API client | `internal/revenuecat/client.go` | `NewClient`, `GetSubscriber`, `APIError` |
| RevenueCat models | `internal/revenuecat/models.go` | webhook/subscriber payload structs |
| Client/model tests | `internal/revenuecat/*_test.go` | parsing and entitlement-derivation behavior |

## CONVENTIONS
- Keep HTTP concerns (auth headers, status handling, decode) inside this package.
- Return typed `APIError` for non-200 responses so callers can branch on status.
- Keep entitlement derivation deterministic and testable by passing an explicit `now`.
- Keep this package transport-focused; reconciliation policy belongs to `internal/service/webhook.go`.

## ANTI-PATTERNS
- Do not mix database writes into this package.
- Do not hardcode webhook policy decisions here.
- Do not log API keys or full subscriber payloads containing sensitive identifiers.
