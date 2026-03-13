# API Documentation
> See `/AGENTS.md` for repository-wide workflow expectations.

## OVERVIEW
`docs/` contains API and deployment documentation. Keep these files aligned with implemented handler/service behavior and middleware contracts.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| OpenAPI contract | `docs/openapi.yaml` | canonical request/response schemas and endpoint descriptions |
| Endpoint reference | `docs/ENDPOINTS.md` | human-readable endpoint behavior, auth and error details |
| Deployment guidance | `docs/deployment_finalizing.md` | production setup and rollout checklist |

## CONVENTIONS
- Update `docs/openapi.yaml` when endpoint shapes, required fields, auth, or rate-limit semantics change.
- Keep `docs/ENDPOINTS.md` consistent with OpenAPI and actual handler behavior.
- Use terminology consistent with `BACKEND_SPEC.md` and response envelopes.
- Document rate limits, required headers, and auth requirements for new endpoints.

## ANTI-PATTERNS
- Do not document behavior that is not implemented.
- Do not leave stale field names after handler/request model changes.
- Do not omit breaking API changes from docs updates.
