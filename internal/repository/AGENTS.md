# Repository Layer
> See `/AGENTS.md` for SQL safety rules and `internal/AGENTS.md` for layering.

## OVERVIEW
`internal/repository` is the SQL boundary. It owns pgx query execution, row mapping, lock behavior, and not-found semantics used by services.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| Auth/user/OTP/refresh SQL | `internal/repository/auth.go` | auth persistence, locks, refresh-token lifecycle |
| Admin SQL | `internal/repository/admin.go` | admin credentials, user/admin listing and stats |
| Entitlement SQL | `internal/repository/entitlement.go` | RevenueCat-backed entitlement snapshot upserts/lookups |
| Social SQL | `internal/repository/social.go` | device tokens, friendships, leaderboard metrics |
| Sync SQL | `internal/repository/sync.go` | per-table sync upsert/delete/select behavior |
| Shared tx contracts | `internal/repository/tx.go` | `DBTX`, `Pool`, `Tx` abstractions |

## CONVENTIONS
- Keep all SQL parameterized and explicit-column.
- Preserve `ErrNotFound` mapping for empty row results (`pgx.ErrNoRows` → `ErrNotFound`).
- Accept `DBTX` in repository methods so callers can use pool or explicit transactions.
- Keep ordering and cursor semantics stable for paginated and sync reads.
- Keep query responsibilities feature-local (auth/admin/social/sync/entitlements).
- Cross-repo dependencies must be expressed as narrow interfaces (e.g. `LeaderboardRefresher` in `sync.go`) and injected at construction — never instantiated inline.
- Sync cursor SQL (`ServerCursor`) lives in `SyncRepository`, not in the service layer.
- Cascade collection functions (`collectModeCascade`, `collectRestrictionSessionCascade`) accept a slice of IDs and batch all sub-queries using `ANY($2)`. Never call them inside a per-deletion loop; call once before the loop and look up results by ID.
- `insertTombstone` uses `ON CONFLICT (user_id, table_name, record_id) DO NOTHING`; never insert a tombstone without this guard.
- Close `pgx.Rows` explicitly after each loop rather than relying on `defer` when multiple rowsets are opened sequentially in the same function.

## ANTI-PATTERNS
- Do not import or depend on `net/http` from repository packages.
- Do not leak pgx-specific errors past service boundary without wrapping/mapping.
- Do not use `SELECT *` in application queries.
- Do not perform cross-feature business policy checks here; keep those in services.
- Do not instantiate a sibling repository struct directly inside another repository (use an injected interface instead).
