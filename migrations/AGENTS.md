# SQL Migrations
> See `/AGENTS.md` for release workflow and database safety rules.

## OVERVIEW
`migrations/` holds embedded SQL schema migrations consumed by `cmd/migrate` via `migrations.FS`.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| Embedded migration filesystem | `migrations/migrations.go` | embeds `*.sql` into binaries |
| Baseline schema (up) | `migrations/000001_initial_schema.up.sql` | current pre-release flattened schema |
| Baseline rollback (down) | `migrations/000001_initial_schema.down.sql` | rollback for baseline migration |

## CONVENTIONS
- Add new migration files in ordered numeric sequence.
- Provide both `up` and `down` SQL for each migration.
- Keep migrations forward-safe and compatible with existing production data.
- Run migrations with `go run ./cmd/migrate` (or built migrate binary), not from server startup.

## ANTI-PATTERNS
- Do not edit already-applied migrations in place.
- Do not rely on app startup to perform schema changes.
- Do not introduce destructive statements without an explicit rollback plan.
