# Push Notifications
> See `/AGENTS.md` for repo-wide rules and `internal/AGENTS.md` for layering.

## OVERVIEW
`internal/push` provides push-delivery abstractions and implementations. It supports disabled/noop mode, Firebase delivery, and an opt-in preference gate that respects per-user push settings.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| Push contracts and wrappers | `internal/push/push.go` | `Sender`, `Notification`, noop and preference sender |
| Firebase sender | `internal/push/firebase.go` | Firebase Admin SDK sender + token fan-out behavior |
| Firebase integration tests | `internal/push/firebase_integration_test.go` | integration coverage for live Firebase behavior |

## CONVENTIONS
- Keep `Sender` interface stable so services can depend on abstraction only.
- Use `NoopSender` when Firebase credentials are unavailable.
- Gate sends through `PreferenceSender` so user push settings are always respected.
- Keep notification payload fields minimal and typed (`FriendMetadata` for social events).

## ANTI-PATTERNS
- Do not call Firebase directly from service packages.
- Do not bypass preference checks for user-facing notifications.
- Do not log raw device tokens or service-account secrets.
