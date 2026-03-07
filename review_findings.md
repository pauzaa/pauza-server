**Findings**

**Correctness**
1. Title: Refresh token rotation is raceable  
Severity: critical. Confidence: high. Location: [internal/handler/auth.go:783](/Users/alisher/University/bisp/pauza-server/internal/handler/auth.go#L783). Problem: `/refresh` reads the token row outside a transaction, checks `revoked=false`, and only later starts a transaction to revoke it and mint a replacement. Two concurrent refresh requests using the same token can both pass the pre-check and both mint new valid refresh tokens. Why it matters: this breaks single-use rotation semantics and weakens theft detection. Recommended change: move the lookup into the transaction and lock the row with `SELECT ... FOR UPDATE`, then check `revoked`/`expires_at`, revoke, insert the new token, and commit as one unit. Impact: correctness, security, reliability. Priority: must fix.

2. Title: Registration cleanup after SMTP failure can delete a newer registration  
Severity: high. Confidence: high. Location: [internal/handler/auth.go:415](/Users/alisher/University/bisp/pauza-server/internal/handler/auth.go#L415), [internal/handler/auth.go:243](/Users/alisher/University/bisp/pauza-server/internal/handler/auth.go#L243). Problem: after committing the new unverified user and OTP, a later mail send failure triggers broad cleanup by `email` and `purpose`. If the same email re-registers while the first send is still in flight, the first request’s cleanup can delete the second request’s user and OTP. Why it matters: legitimate users can lose a valid registration because of another request’s delayed failure path. Recommended change: stop deleting by broad predicates; either keep a pending-registration record keyed to a request-specific ID, or carry the inserted row IDs forward and only compensate those exact rows inside a controlled workflow. Impact: correctness, reliability. Priority: must fix.

4. Title: JSON request parsing accepts unknown fields and trailing garbage  
Severity: medium. Confidence: high. Location: [internal/handler/auth.go:102](/Users/alisher/University/bisp/pauza-server/internal/handler/auth.go#L102). Problem: `decodeJSONBody` does a single `Decode` and returns success immediately. That allows silent acceptance of typoed fields and extra JSON documents after the first object. Why it matters: clients can think they sent one contract while the server ignored part of it; this is a classic source of integration bugs and weak request validation. Recommended change: use `json.Decoder`, call `DisallowUnknownFields()`, decode once, then verify EOF with a second decode into `struct{}{}`. Impact: correctness, maintainability, security. Priority: should fix.

5. Title: `GET /api/v1/me` is wired as a protected 404 placeholder  
Severity: medium. Confidence: high. Location: [internal/server/server.go:123](/Users/alisher/University/bisp/pauza-server/internal/server/server.go#L123), [BACKEND_SPEC.md:830](/Users/alisher/University/bisp/pauza-server/BACKEND_SPEC.md#L830). Problem: the route exists in the spec and the auth response shape already mirrors it, but production wiring returns `NOT_FOUND`. Why it matters: the published API surface and implementation diverge; clients will ship against a contract the server does not honor. Recommended change: either implement `/me` now or remove it from the public route tree until it exists. Impact: correctness, maintainability. Priority: should fix.

6. Title: Random fallback usernames have an unnecessarily small collision budget  
Severity: low. Confidence: high. Location: [internal/handler/auth.go:93](/Users/alisher/University/bisp/pauza-server/internal/handler/auth.go#L93). Problem: usernames are generated from only 32 random bits with three retries. That is fine at tiny scale and increasingly poor as the user table grows. Why it matters: this creates avoidable operational flakiness in registration. Recommended change: use more entropy (`UUID`, `ULID`, or at least 64-96 random bits) or derive a deterministic suffix with guaranteed uniqueness. Impact: correctness, reliability. Priority: nice to have.

**Security**
7. Title: Public auth endpoints have no rate limiting or abuse controls  
Severity: high. Confidence: high. Location: [internal/server/server.go:112](/Users/alisher/University/bisp/pauza-server/internal/server/server.go#L112), [internal/handler/auth.go](/Users/alisher/University/bisp/pauza-server/internal/handler/auth.go). Problem: `register`, `verify-otp`, `login`, `refresh`, `forgot-password`, and `reset-password` are exposed without any IP, account, or token attempt throttling beyond per-OTP attempt counts. Why it matters: the service is open to credential stuffing, OTP brute force, registration spam, and mailbox abuse. Recommended change: add layered throttling middleware and durable counters keyed by IP and normalized email, with stricter budgets for login and password-reset flows. Impact: security, reliability. Priority: must fix.

8. Title: Anti-enumeration is undermined by timing differences  
Severity: high. Confidence: medium. Location: [internal/handler/auth.go:667](/Users/alisher/University/bisp/pauza-server/internal/handler/auth.go#L667), [internal/handler/auth.go:915](/Users/alisher/University/bisp/pauza-server/internal/handler/auth.go#L915). Problem: `login` returns immediately for missing users but performs bcrypt for existing users; `forgot-password` returns early for unknown users but does DB insert and SMTP for known users. Messages are generic, but response time still leaks account existence. Why it matters: attackers do not need different status codes if latency already tells them which accounts exist. Recommended change: use a dummy bcrypt hash on the not-found login path, and make forgot-password asynchronous or normalize response timing with a fixed-budget handler. Impact: security. Priority: must fix.

9. Title: SMTP sender leaks recipient email addresses into logs and error strings  
Severity: medium. Confidence: high. Location: [internal/mail/smtp.go:71](/Users/alisher/University/bisp/pauza-server/internal/mail/smtp.go#L71), [internal/mail/smtp.go:89](/Users/alisher/University/bisp/pauza-server/internal/mail/smtp.go#L89), [internal/mail/smtp.go:104](/Users/alisher/University/bisp/pauza-server/internal/mail/smtp.go#L104). Problem: the sender logs `"to"` at info level and also embeds raw recipient addresses in returned errors. Callers then log `err`, which defeats later masking. Why it matters: this is unnecessary PII exposure in logs and error pipelines. Recommended change: never include recipient addresses in returned error text; log only masked addresses at the call site, or hash them if correlation is required. Impact: security, observability. Priority: should fix.

10. Title: SMTP transport has no enforceable timeout, cancellation, or TLS policy  
Severity: medium. Confidence: high. Location: [internal/mail/smtp.go:68](/Users/alisher/University/bisp/pauza-server/internal/mail/smtp.go#L68). Problem: the code uses `net/smtp.SendMail`, which cannot honor `context.Context`, gives little control over dial/read/write timeouts, and does not let you explicitly enforce TLS policy. Why it matters: mail delivery can hang request handling and weakens transport guarantees for credentials and OTP mail. Recommended change: replace `net/smtp` with a maintained client that supports context, explicit TLS config, and timeout control; better yet, move OTP dispatch off the request path entirely. Impact: security, reliability. Priority: should fix.

11. Title: JWT secret strength is not validated  
Severity: medium. Confidence: high. Location: [internal/config/config.go:23](/Users/alisher/University/bisp/pauza-server/internal/config/config.go#L23), [internal/config/config.go:81](/Users/alisher/University/bisp/pauza-server/internal/config/config.go#L81). Problem: config only requires `JWT_SECRET` to be non-empty. That is too weak for an HMAC signing key. Why it matters: short or human-readable secrets are brute-forceable and easy to misconfigure. Recommended change: enforce a minimum secret length/entropy requirement in config validation and document a proper generated secret in [.env.example](/Users/alisher/University/bisp/pauza-server/.env.example). Impact: security. Priority: should fix.

12. Title: Auto-seeding an admin account from environment is a risky long-term auth model  
Severity: medium. Confidence: medium. Location: [cmd/server/main.go:47](/Users/alisher/University/bisp/pauza-server/cmd/server/main.go#L47), [internal/database/seed.go:14](/Users/alisher/University/bisp/pauza-server/internal/database/seed.go#L14). Problem: every startup expects admin credentials in env and opportunistically seeds an admin row if the table is empty. Why it matters: this couples secret distribution to deploy-time env, encourages static credentials, and creates a privileged bootstrap path that is easy to mishandle operationally. Recommended change: move bootstrap admin creation to an explicit one-time admin-init command or migration-time provisioning workflow, then remove it from normal app startup. Impact: security, operability. Priority: should fix.

**Reliability / Operability**
14. Title: `/health` is acting as DB readiness and container liveness at the same time  
Severity: high. Confidence: high. Location: [internal/handler/health.go:22](/Users/alisher/University/bisp/pauza-server/internal/handler/health.go#L22), [docker-compose.yml:35](/Users/alisher/University/bisp/pauza-server/docker-compose.yml#L35). Problem: every `/health` request does a live `pool.Ping`, and Docker uses that endpoint as the container healthcheck. A transient database issue will mark the whole app unhealthy and trigger restarts. Why it matters: restart loops make incidents worse and conflate “process is alive” with “all dependencies are ready”. Recommended change: split endpoints into `/live` and `/ready`; keep liveness process-local and make readiness dependency-aware with caching/backoff. Impact: reliability, operability, performance. Priority: must fix.

15. Title: Application startup is coupled to running migrations in-process  
Severity: medium. Confidence: high. Location: [cmd/server/main.go:41](/Users/alisher/University/bisp/pauza-server/cmd/server/main.go#L41). Problem: the serving binary always runs migrations at startup. Why it matters: app instances now need DDL privileges, boot time depends on migration behavior, and rollout failures become harder to reason about in multi-instance environments. Recommended change: move migrations to a dedicated deployment step or one-shot migration job; keep app startup limited to serving traffic. Impact: reliability, operability, security. Priority: should fix.

16. Title: OTP and refresh-token tables have no lifecycle cleanup  
Severity: medium. Confidence: high. Location: [migrations/000001_initial_schema.up.sql:27](/Users/alisher/University/bisp/pauza-server/migrations/000001_initial_schema.up.sql#L27), [migrations/000001_initial_schema.up.sql:40](/Users/alisher/University/bisp/pauza-server/migrations/000001_initial_schema.up.sql#L40). Problem: expired OTPs and long-revoked refresh tokens accumulate forever. Why it matters: these tables are on hot auth paths; uncontrolled growth degrades performance and complicates incident analysis. Recommended change: add a scheduled cleanup job and indexes aligned with cleanup predicates, and consider pruning old rows on successful auth operations as secondary hygiene. Impact: reliability, performance, maintainability. Priority: should fix.

17. Title: Forgot-password hides real outages behind fake success  
Severity: medium. Confidence: high. Location: [internal/handler/auth.go:932](/Users/alisher/University/bisp/pauza-server/internal/handler/auth.go#L932). Problem: DB and OTP-generation failures return `200` with a success-looking message. That helps anti-enumeration, but it also suppresses operational signals and user-visible failure. Why it matters: incidents become harder to detect and users silently fail to receive reset codes. Recommended change: keep the external message generic if you want, but emit strong metrics/alerts and consider returning a retriable 5xx when the failure is system-wide rather than account-specific. Impact: reliability, observability. Priority: should fix.

18. Title: Request observability is fragmented and partially untrusted  
Severity: low. Confidence: medium. Location: [internal/server/server.go:44](/Users/alisher/University/bisp/pauza-server/internal/server/server.go#L44), [internal/server/server.go:86](/Users/alisher/University/bisp/pauza-server/internal/server/server.go#L86), [internal/middleware/auth.go](/Users/alisher/University/bisp/pauza-server/internal/middleware/auth.go), [internal/handler/health.go](/Users/alisher/University/bisp/pauza-server/internal/handler/health.go). Problem: logging mixes injected loggers with package-global `slog`, `middleware.Recoverer` is not structured, and `RealIP` trusts forwarded headers without any trusted-proxy boundary. Why it matters: incident forensics and rate limiting become less reliable once traffic sits behind proxies or multiple log sinks. Recommended change: standardize on injected structured logging, add a custom recovery middleware, and only honor proxy headers from known upstreams. Impact: reliability, observability, security. Priority: nice to have.

**Design / Maintainability**
19. Title: Auth is implemented as a single handler-centric transaction blob  
Severity: medium. Confidence: high. Location: [internal/handler/auth.go](/Users/alisher/University/bisp/pauza-server/internal/handler/auth.go). Problem: one file owns HTTP decoding, validation, SQL, auth policy, token lifecycle, OTP lifecycle, and mail dispatch. Several of the current bugs are direct consequences of this coupling. Why it matters: the code is hard to reason about, hard to test under concurrency, and expensive to extend without regressions. Recommended change: split into service-layer use cases (`Register`, `VerifyOTP`, `Refresh`, `ForgotPassword`, `ResetPassword`) plus explicit repository interfaces and a mail job boundary. Impact: maintainability, reliability. Priority: should fix.

**Testing**
21. Title: The passing default test suite does not exercise the real auth/database paths  
Severity: high. Confidence: high. Location: [internal/handler/auth_integration_test.go:1](/Users/alisher/University/bisp/pauza-server/internal/handler/auth_integration_test.go#L1), [internal/database/testhelper_test.go:1](/Users/alisher/University/bisp/pauza-server/internal/database/testhelper_test.go#L1). Problem: `go test ./...` passes, but all integration coverage is behind the `integration` build tag and skipped when `TEST_DATABASE_URL` is absent. Why it matters: the repo looks green while the highest-risk code paths are not being exercised by the default command. Recommended change: wire an actual CI job that runs `go test -tags=integration ./...` against Postgres and make that status required. Impact: reliability, maintainability. Priority: must fix.

22. Title: There are no concurrency tests for the auth lifecycle  
Severity: high. Confidence: high. Location: [internal/handler/auth_integration_test.go](/Users/alisher/University/bisp/pauza-server/internal/handler/auth_integration_test.go). Problem: the suite covers happy paths and some sequential failure modes, but it does not test concurrent refresh, concurrent OTP verify, or re-register-during-email-failure races. Why it matters: the most serious bug in this codebase is concurrency-related, and the current tests would never catch it. Recommended change: add targeted race-style integration tests with goroutines and barrier synchronization around refresh rotation, OTP single-use, and repeated registration. Impact: correctness, reliability. Priority: must fix.

23. Title: Validation tests rely on nil-pointer panics as a guardrail  
Severity: low. Confidence: high. Location: [internal/handler/auth_test.go:3](/Users/alisher/University/bisp/pauza-server/internal/handler/auth_test.go#L3). Problem: the unit tests intentionally build handlers with a nil DB pool and assume any accidental DB reach will panic. Why it matters: this is brittle and makes failures noisy rather than explicit. Recommended change: use explicit mocks/stubs for the storage boundary, even for validation-only tests. Impact: maintainability, test quality. Priority: nice to have.

24. Title: Request-validation edge cases are not covered  
Severity: low. Confidence: high. Location: [internal/handler/auth_test.go](/Users/alisher/University/bisp/pauza-server/internal/handler/auth_test.go). Problem: I do not see coverage for unknown JSON fields, trailing JSON documents, or the exact anti-enumeration timing budget. Why it matters: the decoder behavior is currently permissive and there is no regression test to catch it once fixed. Recommended change: add tests for unknown-field rejection, extra-document rejection, and constant-time-ish auth behavior where practical. Impact: correctness, maintainability. Priority: should fix.

**Quick Wins**
- Lock refresh-token rows inside the refresh transaction and make rotation atomic.
- Make request decoding strict with `DisallowUnknownFields` plus EOF enforcement.
- Stop logging raw recipient addresses and remove them from SMTP error strings.
- Split `/health` into `/live` and `/ready`.
- Enforce a minimum `JWT_SECRET` length in config validation.

**Larger Refactors Worth Considering**
- Extract auth use cases out of [internal/handler/auth.go](/Users/alisher/University/bisp/pauza-server/internal/handler/auth.go) into a service layer with explicit transaction boundaries.
- Replace the email-string-based OTP lifecycle with a proper pending-registration / challenge model.
- Move OTP delivery to an asynchronous job path instead of sending mail inline in request handlers.
- Remove in-process migrations and admin seeding from the serving binary; make them explicit deployment/bootstrap commands.
- Standardize logging around injected `*slog.Logger` and replace `chi`’s default recoverer with structured recovery middleware.

**Missing Tests That Should Be Added**
- Concurrent refresh requests using the same token: exactly one should succeed.
- Concurrent OTP verification using the same code: exactly one should consume it.
- Re-register while a previous registration’s SMTP send fails: newer registration must survive.
- Unknown JSON fields and trailing JSON documents on every auth endpoint.
- Health endpoint behavior split between liveness and readiness.
- SMTP error/log redaction tests to ensure recipient addresses never leak.

**Highest-Risk Areas In The Codebase**
- Refresh-token lifecycle in [internal/handler/auth.go](/Users/alisher/University/bisp/pauza-server/internal/handler/auth.go).
- Registration and OTP lifecycle, especially compensating cleanup after partial failure.
- Inline SMTP delivery inside request handlers.
- Operational startup path in [cmd/server/main.go](/Users/alisher/University/bisp/pauza-server/cmd/server/main.go).

**Implementation Chunks**

1. **Chunk 1: OTP And Identity Schema**
   - Status: completed
   - Findings addressed: `#3`, `#13`, `#20`
   - Implemented in: `migrations/000003_otp_identity_schema.up.sql`, `migrations/000003_otp_identity_schema.down.sql`, `internal/auth/otp.go`, `internal/handler/auth.go`, updated tests
   - Outcome: `users.email` now has DB-enforced case-insensitive uniqueness; OTPs are stored as bcrypt hashes in `code_hash`; `otp_codes` is linked by `user_id` FK instead of `user_email`.
   - Depends on: none.

2. **Chunk 2: Registration And OTP Lifecycle Correctness**
   - Status: completed
   - Findings: `#2`
   - Implemented in: `internal/handler/auth.go`, updated `internal/handler/auth_integration_test.go`
   - Outcome: registration cleanup now compensates only exact inserted rows by ID; stale-unverified-user cleanup is ID-scoped under row locking; concurrent re-registration cannot be deleted by an earlier request's SMTP-failure cleanup.
   - Depends on: Chunk 1.

3. **Chunk 3: Auth Core Correctness**
   - Status: completed
   - Findings: `#1`, `#4`, `#6`
   - Implemented in: `internal/handler/auth.go`, updated `internal/handler/auth_test.go`, updated `internal/handler/auth_integration_test.go`
   - Outcome: refresh-token rotation is now transactional and row-locked; auth JSON decoding rejects unknown fields and trailing documents; fallback usernames use 96 bits of entropy for negligible collision risk.
   - Depends on: none.

4. **Chunk 4: Auth Abuse Controls**
   - Status: completed
   - Findings: `#7`, `#8`
   - Implemented in: `internal/ratelimit/ratelimit.go`, `internal/middleware/ratelimit.go`, `internal/server/server.go`, `cmd/server/main.go`, `internal/auth/password.go`, `internal/handler/auth.go`, updated tests
   - Outcome: public auth routes now have per-IP rate limiting with standard rate-limit headers, `verify-otp` also has per-email throttling, login performs dummy bcrypt work on the not-found path, forgot-password uses a minimum response-duration floor, and limiter cleanup is wired into server shutdown.
   - Depends on: none.

5. **Chunk 5: Mail Privacy Hardening**
   - Status: completed
   - Findings: `#9`
   - Implemented in: `internal/redact/redact.go`, `internal/redact/redact_test.go`, `internal/mail/smtp.go`, `internal/mail/mail_test.go`, `internal/handler/auth.go`
   - Outcome: raw recipient emails are no longer exposed through SMTP logs or returned SMTP error strings; SMTP errors are sanitized before return, masked recipient correlation is preserved, and auth mail-error log sites are documented as safe because they only receive sanitized errors.
   - Depends on: none.

6. **Chunk 6: Mail Transport Migration**
   - Findings: `#10`
   - Files: `internal/mail/smtp.go`, config wiring, and auth mail call sites.
   - Changes: replace `net/smtp` with a maintained client that supports context, timeout control, and explicit TLS policy.
   - Depends on: Chunk 1 preferred; Chunk 2 recommended before changing the request/mail workflow.

7. **Chunk 7: Config And Bootstrap Security**
   - Findings: `#11`, `#12`
   - Files: `internal/config/config.go`, `.env.example`, `cmd/server/main.go`, `internal/database/seed.go`
   - Changes: enforce minimum `JWT_SECRET` strength; remove admin auto-seeding from the normal startup path.
   - Depends on: none.

8. **Chunk 8: Health And Startup Operability**
   - Findings: `#14`, `#15`, `#17`
   - Files: `internal/handler/health.go`, `internal/server/server.go`, `docker-compose.yml`, `cmd/server/main.go`, `internal/handler/auth.go`
   - Changes: split `/health` into `/live` and `/ready`; remove in-process migrations from serving startup; improve forgot-password outage signaling and observability.
   - Depends on: none.

9. **Chunk 9: Auth Data Retention And Cleanup**
   - Findings: `#16`
   - Files: new migration files and any background cleanup wiring that gets added.
   - Changes: clean up expired OTPs and old refresh tokens; add indexes aligned with cleanup predicates.
   - Depends on: Chunk 1.

10. **Chunk 10: API Contract Alignment**
    - Findings: `#5`
    - Files: `internal/server/server.go`, `BACKEND_SPEC.md`
    - Changes: implement `GET /api/v1/me` or remove it from the exposed public contract.
    - Depends on: none.

11. **Chunk 11: Observability And Trusted Request Metadata**
    - Findings: `#18`
    - Files: `internal/server/server.go`, `internal/middleware/auth.go`, `internal/handler/health.go`
    - Changes: standardize injected structured logging; add structured recovery middleware; bound trust of forwarded headers and proxy-derived IPs.
    - Depends on: none.

12. **Chunk 12: Auth Architecture Refactor**
    - Findings: `#19`
    - Files: `internal/handler/auth.go` and new service/repository packages.
    - Changes: extract auth use cases into a service layer; define explicit transaction, repository, and mail boundaries.
    - Depends on: ideally after Chunks 1, 2, and 6.

13. **Chunk 13: Testing And CI Coverage**
    - Findings: `#21`, `#22`, `#23`, `#24`
    - Files: `internal/handler/auth_integration_test.go`, `internal/database/testhelper_test.go`, `internal/handler/auth_test.go`, and CI config.
    - Changes: run integration tests in CI; add concurrency tests for refresh/OTP/registration races; replace nil-pool panic-based tests with mocks/stubs; add strict JSON validation coverage and related edge cases.
    - Depends on: should be updated alongside Chunks 1, 2, 3, 5, and 8.

**Recommended Execution Order**
- Chunk 2: Registration And OTP Lifecycle Correctness
- Chunk 3: Auth Core Correctness
- Chunk 4: Auth Abuse Controls
- Chunk 5: Mail Privacy Hardening
- Chunk 6: Mail Transport Migration
- Chunk 7: Config And Bootstrap Security
- Chunk 8: Health And Startup Operability
- Chunk 9: Auth Data Retention And Cleanup
- Chunk 10: API Contract Alignment
- Chunk 11: Observability And Trusted Request Metadata
- Chunk 13: Testing And CI Coverage
- Chunk 12: Auth Architecture Refactor

**Executive Summary**
The codebase is better than a toy project, but it is not yet at a production-grade bar for an auth service. Chunks 1, 2, 3, 4, and 5 are now complete: email identity is enforced correctly at the database layer, OTPs are no longer stored in plaintext, the OTP lifecycle is anchored to stable user IDs instead of raw email strings, registration cleanup is scoped to exact inserted rows, refresh-token rotation is atomic and race-safe, auth request decoding is strict, fallback usernames now have a negligible collision risk, public auth routes now have layered abuse throttling, login/forgot-password have stronger anti-enumeration timing behavior, and SMTP logging/error paths no longer leak raw recipient addresses. The most serious remaining issues are mail transport hardening and operational concerns around health checks, startup migrations, and hidden outages in forgot-password. `go test ./...` and `go vet ./...` both pass in the current workspace, but that should not be read as strong evidence of correctness because the highest-risk behaviors are either hidden behind build tags or not tested at all.
