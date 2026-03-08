# Polishing Plan

This document captures the remaining findings after the major refactor and recommends the order to execute them.

The project is assumed to have never been deployed to a persistent production database. Because of that, the rewritten baseline migration is treated as intentional and is not listed here as a defect. From this point onward, migrations should be treated as append-only.

## Recommended Execution Order

1. Fix API error-contract consistency.
2. Make test coverage match the real production serving stack.
3. Add direct service-layer behavioral tests.
4. Decouple operational binaries from unrelated configuration.
5. Redesign and externalize auth rate limiting.
6. Make rate-limit budgets configurable.
7. Tighten trusted-proxy operational guidance and startup visibility.

This order is deliberate:
- The panic-response contract and test realism affect immediate correctness and confidence.
- Service-layer tests are the cheapest way to prevent regressions while doing the remaining work.
- Config decoupling improves deployability and maintenance with low risk.
- Rate-limiter redesign is more invasive and should happen after the verification base is improved.

## Findings

### 1. Panic recovery still returns a bare 500 instead of the standard JSON error envelope

- Severity: medium
- Confidence: high
- Location: `internal/middleware/recovery.go`
- Problem:
  The custom recovery middleware logs panics correctly, but it writes only an HTTP `500` status with no JSON body.
- Why it matters:
  The rest of the API uses a stable JSON error envelope. Panic paths currently break that contract and force clients into a special-case failure path.
- Recommended change:
  If headers have not been written yet, return the standard internal-error JSON envelope with `Content-Type: application/json`. If the response has already started, only log.
- Impact:
  correctness, maintainability, reliability
- Priority:
  should fix

### 2. Auth integration tests do not use the production middleware stack

- Severity: medium
- Confidence: high
- Location: `internal/handler/auth_integration_test.go`, `internal/server/server.go`
- Problem:
  The auth integration tests still assemble their own router using `chi`'s stock `RealIP` and `Recoverer`, and they do not mount the production auth rate-limit middleware.
- Why it matters:
  The tests cover handler behavior, but not the actual stack that will front those handlers in production. That leaves gaps around trusted proxy handling, custom recovery behavior, and route-level rate limiting.
- Recommended change:
  Build integration tests from `server.New(...)` directly, or replicate the exact production middleware chain including `TrustedRealIP`, custom `Recoverer`, and auth rate limiting.
- Impact:
  reliability, maintainability, test quality
- Priority:
  should fix

### 3. Service layer has very thin direct behavioral coverage

- Severity: medium
- Confidence: high
- Location: `internal/service/auth_test.go`
- Problem:
  The new service package owns important business logic, but its direct tests mostly cover helpers and constants instead of meaningful behavior.
- Why it matters:
  The refactor moved complexity into the service layer without adding proportional unit-level confidence. That makes regression detection slower and failures harder to localize.
- Recommended change:
  Add service-layer tests with stub repositories and stub mailers covering:
  - registration conflict handling
  - SMTP failure cleanup
  - refresh token reuse detection
  - unauthorized paths
  - forgot-password internal failures
  - reset-password error branches
- Impact:
  maintainability, reliability, test quality
- Priority:
  should fix

### 4. Command binaries still depend on unrelated full-app configuration

- Severity: medium
- Confidence: high
- Location: `cmd/migrate/main.go`, `cmd/seed-admin/main.go`, `internal/config/config.go`
- Problem:
  `migrate` and `seed-admin` both call the full `config.Load()`, which still requires unrelated settings such as SMTP, RevenueCat, Firebase, and student-verification configuration.
- Why it matters:
  Operational commands should require only the minimum inputs they actually use. Right now these commands are more fragile than necessary in CI/CD and maintenance workflows.
- Recommended change:
  Split configuration loading by command or add smaller config loaders:
  - `migrate`: database only
  - `seed-admin`: database + admin credentials
  - `server`: full runtime config
- Impact:
  maintainability, reliability, operability
- Priority:
  should fix

### 5. Rate limiting is still per-process only

- Severity: medium
- Confidence: high
- Location: `internal/ratelimit/ratelimit.go`, `internal/server/server.go`
- Problem:
  Auth throttling is implemented as an in-memory map inside each server process.
- Why it matters:
  In multi-instance deployments, the effective budget becomes per replica. Restarts also reset state. That makes the limiter weak as a security control.
- Recommended change:
  Move auth throttling to a shared backend such as Redis or a DB-backed counter store. If that is deferred, document the current limiter as a best-effort local guard rather than a strong abuse-prevention mechanism.
- Impact:
  security, reliability
- Priority:
  should fix

### 6. Rate-limiter key cardinality can drive unbounded memory growth within the window

- Severity: medium
- Confidence: high
- Location: `internal/ratelimit/ratelimit.go`
- Problem:
  The limiter stores one entry per unique key until eviction. Attackers can generate large numbers of unique IP or email keys and force the server to retain them for the full time window.
- Why it matters:
  This creates avoidable memory pressure and makes the limiter itself an attack surface.
- Recommended change:
  Add bounded storage or move to an external rate-limit backend with hard caps and TTL semantics. If the in-memory limiter remains, enforce a maximum number of live keys and define fail-open vs fail-closed behavior explicitly.
- Impact:
  reliability, security, performance
- Priority:
  should fix

### 7. One shared anonymous IP bucket across all public auth endpoints is too coarse

- Severity: medium
- Confidence: high
- Location: `internal/server/server.go`
- Problem:
  `register`, `login`, `refresh`, `forgot-password`, and `reset-password` currently share the same `5/minute` anonymous IP budget.
- Why it matters:
  Different endpoints have different abuse models and user expectations. A shared NAT or carrier-grade network can easily cause legitimate cross-user interference.
- Recommended change:
  Split budgets by endpoint class and key type. At minimum:
  - login: stricter anonymous + optional account keying
  - register: separate anonymous budget
  - refresh: separate budget, likely looser than login
  - forgot/reset password: their own budgets
  Make all of them configurable per environment.
- Impact:
  reliability, security, usability
- Priority:
  should fix

### 8. Default test pass still overstates confidence because integration behavior is opt-in

- Severity: low
- Confidence: high
- Location: `internal/handler/auth_integration_test.go`, `internal/database/testhelper_test.go`
- Problem:
  `go test ./...` passes, but DB-backed integration tests are still behind the `integration` build tag and require `TEST_DATABASE_URL`.
- Why it matters:
  A green default run is useful, but it does not mean the full system behavior is verified.
- Recommended change:
  Ensure CI runs `go test -tags=integration ./...` against Postgres and treats it as required.
- Impact:
  reliability, test quality
- Priority:
  should fix

### 9. Hardcoded auth rate-limit constants reduce operational flexibility

- Severity: low
- Confidence: high
- Location: `internal/server/server.go`
- Problem:
  The limiter budgets are compile-time constants in server wiring.
- Why it matters:
  Different environments and traffic profiles need different budgets. Hardcoding turns operational tuning into a code change.
- Recommended change:
  Move auth rate-limit values into config with conservative defaults.
- Impact:
  maintainability, operability
- Priority:
  nice to have

### 10. Trusted-proxy handling is correct in code but still easy to misconfigure operationally

- Severity: low
- Confidence: medium
- Location: `internal/config/config.go`, `internal/middleware/realip.go`, `docker-compose.yml`
- Problem:
  Correct behavior now depends on `TRUSTED_PROXIES` being accurate for each environment.
- Why it matters:
  If the list is too broad, spoofing risk returns. If it is too narrow, logging and rate limiting fall back to proxy IPs.
- Recommended change:
  Improve deployment docs and optionally log parsed trusted networks on startup at info level.
- Impact:
  security, operability
- Priority:
  nice to have

## Suggested Work Breakdown

### Phase 1: Contract and verification (completed)

- Return JSON internal-error envelopes from panic recovery.
- Update tests for recovery behavior.
- Rework auth integration tests to use `server.New(...)` or the exact production stack.
- Ensure middleware behavior is covered in integration, not only unit tests.
- Done: recovery now returns the standard JSON internal-error envelope, server/recovery tests assert the contract end to end, and auth integration tests boot through `server.New(...)` with production middleware enabled.

### Phase 2: Service confidence (completed)

- Add direct service-layer tests for core auth flows and error branches.
- Add stubs/fakes for repository and mail dependencies where needed.
- Done: added direct `internal/service` behavioral coverage for register/login/refresh/forgot-password/reset-password flows, plus repo/mail/pool fakes so those paths run in the default test suite without Postgres.

### Phase 3: Operational decoupling (completed)

- Split config loading by command purpose.
- Keep the server on the full runtime config.
- Shrink `migrate` and `seed-admin` requirements to only what they need.
- Done: added dedicated migrate and seed-admin config loaders/tests, switched those binaries off the full runtime config, and left the server on `config.Load()`.

### Phase 4: Abuse-control redesign

- Replace the per-process in-memory limiter with a shared limiter backend.
- Split budgets by endpoint class and key type.
- Decide and document behavior under limiter backend failure.

### Phase 5: Operational polish

- Move rate-limit values into config.
- Improve trusted-proxy documentation and optional startup logging.
- Ensure CI runs integration tests with Postgres.

## Release Guidance

If only some of this work can be done now, the recommended minimum before calling the polishing complete is:

1. JSON panic responses
2. production-stack integration tests
3. service-layer behavioral tests
4. CI coverage for integration tests

The distributed rate-limiter work is important, but it is the most invasive item and can reasonably follow once the verification base is stronger.
