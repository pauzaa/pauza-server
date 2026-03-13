# Sync Models
> See `/AGENTS.md` for API contract rules and `internal/service/AGENTS.md` for sync orchestration.

## OVERVIEW
`internal/syncmodel` defines the sync protocol data model shared by handlers, services, and repositories. It owns request/response table shapes and request-to-domain validation/conversion.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| Sync request schema | `internal/syncmodel/request.go` | generic request table wrappers and validation/conversion |
| Sync response schema | `internal/syncmodel/types.go` | response table changes and row shapes |
| Request validation tests | `internal/syncmodel/request_test.go` | field-level validation coverage |

## CONVENTIONS
- Keep JSON tags and field names aligned with `docs/openapi.yaml`.
- Keep per-table cursor semantics consistent (`last_synced_at` in ms, `>= 0`).
- Validate and convert input at the model boundary; handlers consume field errors.
- Keep table type names stable to avoid silent protocol drift with clients.

## ANTI-PATTERNS
- Do not move sync validation logic into handlers.
- Do not introduce breaking field renames without docs + client coordination.
- Do not mix repository or HTTP side effects into model conversion functions.
