## Current State

- **Entry point**: `cmd/server/main.go` - loads config, connects to DB, runs migrations, seeds admin, starts HTTP server with graceful shutdown.
- **Router**: `internal/server/server.go` - chi router with `RequestID`, `RealIP`, request logger, `Recoverer` middleware. Only route is `GET /health`.
- **Handlers**: Only `internal/handler/health.go` exists. Returns JSON health check with DB ping.
- **Config**: `internal/config/config.go` - all env vars from the spec are already declared (`JWTSecret`, `JWTAccessTokenTTL`, `JWTRefreshTokenTTL`, SMTP config, etc.). No new env vars needed.
- **Error model**: `internal/apperror/` - full spec-compliant error envelope with all error codes, `WriteError`, convenience functions (`Unauthorized`, `Conflict`, `ValidationFieldErrors`, `InternalError`, etc.). Well-tested.
- **Validation**: `internal/validate/` - `Email`, `Password`, `Username`, `Name`, `OTP`, `Platform` validators already implemented and tested.
- **Database**: `internal/database/` - `Connect`, `RunMigrations`, `SeedAdmin`. Integration test infrastructure with `testPool`, `resetDatabase`, `tableExists`, `rowCount`, `coreTables` helpers.
- **Migrations**: `migrations/000001_initial_schema.up.sql` - all 19 tables from the spec are created including `users`, `otp_codes`, `refresh_tokens`. **No `email_verified` column on `users`**.
- **Dependencies** (`go.mod`): `chi/v5`, `golang-migrate/v4`, `pgx/v5`, `envconfig`, `golang.org/x/crypto` (bcrypt). **No JWT library yet.**
- **No auth middleware, no auth handlers, no services, no SMTP client, no JWT logic** - the entire auth vertical is missing.

## Key Decisions

1. **Pending user model**: Add `email_verified BOOLEAN NOT NULL DEFAULT FALSE` to `users` via migration `000002`. Register inserts an unverified user; verify-otp flips the flag. Login rejects unverified accounts with the same "invalid email or password" message (anti-enumeration).

2. **Username at registration**: Auto-generate a random username (e.g., `user_<8-hex-chars>`) at registration time. The `username` column's NOT NULL UNIQUE constraint is satisfied immediately; users update it later via `PATCH /api/v1/me`.

3. **JWT library**: Use `github.com/golang-jwt/jwt/v5` - the de facto standard Go JWT library. HMAC-SHA256 (HS256) signing with `cfg.JWTSecret`.

4. **Refresh token format**: Generate a 32-byte `crypto/rand` token, base64url-encoded (opaque to client). Store SHA-256 hash in DB. On refresh, hash the incoming token and look up by `token_hash`. Token rotation: revoke old, issue new. Reuse detection: if a revoked token is presented, revoke all tokens for that user.

5. **OTP generation**: 6-digit numeric via `crypto/rand`. Stored as plaintext in `otp_codes` (the spec doesn't require hashing OTPs; they're short-lived and single-use). Expires after 10 minutes. Max 3 attempts per email per 10-minute window - tracked by counting unused, non-expired OTP rows with matching email and purpose, plus an `attempts` column on the `otp_codes` row.

6. **OTP attempt tracking**: Add `attempts INTEGER NOT NULL DEFAULT 0` to `otp_codes` via migration `000002`. Each failed verify bumps the counter; at 3 attempts the OTP is invalidated. This is simpler than a separate counter table.

7. **Package/layering**:
   - `internal/auth/` - auth service: password hashing/comparison, JWT issuing/validating, refresh token generation/hashing, OTP generation. Pure business logic, no HTTP.
   - `internal/mail/` - SMTP email sender interface + concrete implementation. Interface enables test doubles.
   - `internal/handler/auth.go` - HTTP handlers for the 6 auth endpoints.
   - `internal/middleware/` - JWT auth middleware, user context extraction.
   - No separate "repository" layer - handlers call the DB pool directly via query functions, consistent with the existing `SeedAdmin` pattern. This keeps things simple for this phase.

8. **SMTP interface boundary**:
   ```
   type EmailSender interface {
       SendOTP(ctx context.Context, to string, code string, purpose string) error
   }
   ```
   Concrete SMTP implementation in `internal/mail/smtp.go`. Logs send attempts but never logs the OTP code itself. For tests, use a mock/stub.

9. **Auth middleware**: A new middleware in `internal/middleware/jwt.go` that:
   - Extracts `Authorization: Bearer <token>` header
   - Validates the JWT (signature, expiry)
   - Extracts `sub` (user_id) and `email` from claims
   - Stores them in `context.Context` via a typed key
   - Returns 401 for missing/invalid tokens
   - Provides `UserFromContext(ctx)` helper

10. **Register <-> Verify-OTP 409 behavior**: The spec says register returns 409 if the email already exists. Decision: 409 is returned if a **verified** user exists with that email. If an **unverified** user exists, we delete the stale unverified row (and its OTPs) and re-register, allowing the user to retry registration. This handles the case where someone abandons registration before verifying.

11. **Rate limiting**: The spec requires rate limiting but the codebase has zero rate-limiting infrastructure. Full rate limiting is a cross-cutting concern. For this phase, **defer full rate-limiting middleware** but implement the OTP-specific attempt limit (3 attempts per OTP) within the verify-otp logic itself. The broader rate-limit middleware can be added in a subsequent phase. This plan will note where rate limits should eventually be applied.

12. **No migration for `otp_codes.attempts`**: On reflection, rather than adding an `attempts` column, simply count the number of recent failed verification rows. Actually, the simplest approach: track attempts per OTP row by adding `attempts` to the `otp_codes` table in the migration. This is cleaner than counting separate rows.

13. **Testing strategy**:
    - **Handler tests**: httptest-based, mock the DB pool and email sender. Test request validation, response shapes, status codes.
    - **Auth service unit tests**: Test JWT issuing/validation, password hashing, OTP generation, refresh token hashing.
    - **DB integration tests**: Gated with `//go:build integration`. Test OTP create/verify/expiry, refresh token insert/lookup/revoke/rotation, user insert/lookup. Use the existing `testPool` helper pattern.

---

## Implementation Plan

### Chunk 1: Migration + JWT dependency

**Name**: Add `email_verified` column, `otp_codes.attempts`, and JWT library dependency

**Files**:
- `migrations/000002_auth_columns.up.sql` (new)
- `migrations/000002_auth_columns.down.sql` (new)
- `go.mod` / `go.sum` (modified - add `github.com/golang-jwt/jwt/v5`)

**Changes**:
- Create migration `000002_auth_columns.up.sql`:
  - `ALTER TABLE users ADD COLUMN email_verified BOOLEAN NOT NULL DEFAULT FALSE;`
  - `ALTER TABLE otp_codes ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;`
- Create the corresponding down migration that reverses both ALTERs.
- Run `go get github.com/golang-jwt/jwt/v5` to add the JWT library to `go.mod`.

**Depends on**: none

**Tests**: Existing migration integration tests will validate the new migration applies cleanly. No new test files needed.

**Parallelizable**: yes

---

### Chunk 2: Password hashing utilities

**Name**: `internal/auth/password.go` - bcrypt hash and compare

**Files**:
- `internal/auth/password.go` (new)
- `internal/auth/password_test.go` (new)

**Changes**:
- `HashPassword(password string) (string, error)` - bcrypt with cost 12.
- `CheckPassword(hash, password string) error` - wraps `bcrypt.CompareHashAndPassword`.
- No logging of password values.

**Tests** (`password_test.go`):
- `HashPassword` returns a valid bcrypt hash (verify `$2a$12$` prefix).
- `CheckPassword` succeeds for correct password, fails for wrong password.
- Empty password / very long password (bcrypt 72-byte limit) behavior documented and tested.
- Round-trip: hash then check with same input succeeds.

**Depends on**: none

**Parallelizable**: yes (parallel with Chunks 1, 3, 4, 5)

---

### Chunk 3: JWT issuing and validation

**Name**: `internal/auth/jwt.go` - access token creation and parsing

**Files**:
- `internal/auth/jwt.go` (new)
- `internal/auth/jwt_test.go` (new)

**Changes**:
- `Claims` struct with `Sub` (user_id UUID string), `Email`, standard `jwt.RegisteredClaims`.
- `IssueAccessToken(userID, email, secret string, ttl time.Duration) (string, error)` - HS256-signed JWT.
- `ValidateAccessToken(tokenString, secret string) (*Claims, error)` - parse + validate signature and expiry.

**Tests** (`jwt_test.go`):
- Issue + validate round-trip succeeds; claims contain correct `sub`/`email`.
- Expired token rejected.
- Wrong secret rejected.
- Malformed token string rejected.
- Empty secret, zero TTL edge cases.

**Depends on**: Chunk 1 (needs `golang-jwt/jwt/v5` in `go.mod`)

**Parallelizable**: no

---

### Chunk 4: OTP generation

**Name**: `internal/auth/otp.go` - cryptographic OTP generation and constants

**Files**:
- `internal/auth/otp.go` (new)
- `internal/auth/otp_test.go` (new)

**Changes**:
- `GenerateOTP() (string, error)` - 6-digit string via `crypto/rand`, zero-padded.
- `const OTPExpiry = 10 * time.Minute`
- `const MaxOTPAttempts = 3`

**Tests** (`otp_test.go`):
- Output matches `^[0-9]{6}$`.
- Multiple calls produce different values (generate 100, check uniqueness - probabilistic but near-certain with `crypto/rand`).
- All 100 match the 6-digit regex.

**Depends on**: none

**Parallelizable**: yes (parallel with Chunks 1, 2, 3, 5)

---

### Chunk 5: Refresh token generation and hashing

**Name**: `internal/auth/token.go` - opaque refresh token generation and SHA-256 hashing

**Files**:
- `internal/auth/token.go` (new)
- `internal/auth/token_test.go` (new)

**Changes**:
- `GenerateRefreshToken() (raw string, hash string, err error)` - 32 random bytes, base64url-encoded raw, SHA-256 hex hash.
- `HashRefreshToken(raw string) string` - deterministic SHA-256 hash of the raw token string.

**Tests** (`token_test.go`):
- `GenerateRefreshToken` produces non-empty raw and hash.
- Hash matches `HashRefreshToken(raw)`.
- `HashRefreshToken` is deterministic for same input.
- Token is URL-safe (no `+` or `/` characters).
- Sufficient length (base64url of 32 bytes ~= 43 chars).

**Depends on**: none

**Parallelizable**: yes (parallel with Chunks 1, 2, 3, 4)

---

### Chunk 6: Email sender interface and SMTP implementation

**Name**: `internal/mail/` - `EmailSender` interface and SMTP concrete implementation

**Files**:
- `internal/mail/mail.go` (new)
- `internal/mail/smtp.go` (new)
- `internal/mail/mail_test.go` (new)

**Changes**:

`mail.go`:
- Define `EmailSender` interface with `SendOTP(ctx context.Context, to, otp, purpose string) error`.

`smtp.go`:
- `SMTPSender` struct holding SMTP host, port, username, password, from address.
- `NewSMTPSender(host string, port int, username, password, from string) *SMTPSender`.
- `SendOTP` implementation using `net/smtp`. Logs send event at INFO with `to` and `purpose`, **never logs the OTP code**. Wraps errors: `fmt.Errorf("sending otp email to %s: %w", to, err)`.
- Subject varies by purpose: "Verify your Pauza account" vs "Reset your Pauza password".

`mail_test.go`:
- `NewSMTPSender` sets fields correctly.
- Interface satisfaction compile-time check (`var _ EmailSender = (*SMTPSender)(nil)`).

**Depends on**: none

**Parallelizable**: yes (parallel with Chunks 1-5)

---

### Chunk 7: Auth middleware and user context helpers

**Name**: JWT auth middleware and context extraction

**Files**:
- `internal/middleware/auth.go` (new)
- `internal/middleware/context.go` (new)
- `internal/middleware/auth_test.go` (new)

**Changes**:

`context.go`:
- `AuthUser` struct: `UserID string`, `Email string`.
- Unexported context key type.
- `WithUser(ctx, user) context.Context` and `UserFromContext(ctx) (AuthUser, bool)`.

`auth.go`:
- `JWTAuth(secret string) func(http.Handler) http.Handler` - chi-compatible middleware.
- Extracts `Authorization: Bearer <token>`, calls `auth.ValidateAccessToken`.
- On success: stores `AuthUser` in context, calls next.
- On failure: responds with `apperror.Unauthorized`.

**Tests** (`auth_test.go`):
- Missing Authorization header -> 401.
- Malformed header (no "Bearer" prefix) -> 401.
- Expired JWT -> 401.
- Wrong secret -> 401.
- Valid JWT -> next handler called, `UserFromContext` returns correct `UserID`/`Email`.
- Uses real tokens via `auth.IssueAccessToken`.

**Depends on**: Chunk 3

**Parallelizable**: no

---

### Chunk 8: AuthHandler struct + register endpoint

**Name**: `internal/handler/auth.go` - handler struct and `POST /api/v1/auth/register`

**Files**:
- `internal/handler/auth.go` (new)
- `internal/handler/auth_test.go` (new)

**Changes**:

`auth.go`:
- Define `AuthHandler` struct holding `*pgxpool.Pool`, `mail.EmailSender`, JWT secret, JWT TTLs, `*slog.Logger`. Constructor: `NewAuthHandler(...)`.
- **Register handler** (`POST /api/v1/auth/register`):
  - Decode `{email, password}`, validate with `validate.Email`/`validate.Password`, normalize email to lowercase.
  - Check for verified user with this email -> 409 CONFLICT.
  - If unverified user exists: delete stale row (cascade removes OTPs).
  - Hash password, generate random username (`user_` + 8 hex chars), insert user with `email_verified = false`.
  - Generate OTP, insert into `otp_codes` with `purpose = 'email_verification'`, 10-min expiry.
  - Send OTP via `EmailSender.SendOTP`. On failure: log, delete user, return 500.
  - Return 200 `{"otp_required": true}`.
  - Catch Postgres unique-violation on email -> 409 (handles concurrent registration race).

`auth_test.go`:
- Request validation: missing email -> 422, short password -> 422, invalid email -> 422.
- Response shape for validation errors matches the spec envelope.
- Uses a mock `EmailSender` (in-memory implementation).
- Note: full DB-dependent flow tested in integration chunk.

**Depends on**: Chunks 2, 4, 6

**Parallelizable**: no

---

### Chunk 9: Verify-OTP endpoint

**Name**: `POST /api/v1/auth/verify-otp` handler

**Files**:
- `internal/handler/auth.go` (modified - add method to `AuthHandler`)
- `internal/handler/auth_test.go` (modified - add test cases)

**Changes**:
- **Verify-OTP handler**:
  - Decode `{email, otp}`, validate, normalize email.
  - Query `otp_codes` for most recent unused, non-expired row matching email + `purpose = 'email_verification'`.
  - No row -> 401 "invalid or expired OTP".
  - `attempts >= 3` -> 429 RATE_LIMITED.
  - Code mismatch -> increment attempts, return 401.
  - Code match -> mark OTP used, set `email_verified = true`, issue access + refresh tokens, insert refresh token hash, query user profile, return 200 with `{access_token, refresh_token, user}`.

**Tests**:
- Validation: missing/invalid OTP format -> 422, missing email -> 422.
- Response shape verification for validation errors.

**Depends on**: Chunk 8, Chunks 3, 5

**Parallelizable**: no

---

### Chunk 10: Login endpoint

**Name**: `POST /api/v1/auth/login` handler

**Files**:
- `internal/handler/auth.go` (modified)
- `internal/handler/auth_test.go` (modified)

**Changes**:
- **Login handler**:
  - Decode `{email, password}`, validate, normalize email.
  - Query `users` by email where `email_verified = true`. Not found -> 401 "invalid email or password".
  - `auth.CheckPassword` -> failure -> 401 same message (anti-enumeration).
  - Generate access + refresh tokens, store refresh token hash.
  - Return 200 `{access_token, refresh_token, user}`.

**Tests**:
- Validation: missing email -> 422, missing password -> 422.
- Anti-enumeration: error message is always "invalid email or password" regardless of failure reason.

**Depends on**: Chunk 8

**Parallelizable**: yes (parallel with Chunks 9, 11, 12)

---

### Chunk 11: Refresh endpoint

**Name**: `POST /api/v1/auth/refresh` handler

**Files**:
- `internal/handler/auth.go` (modified)
- `internal/handler/auth_test.go` (modified)

**Changes**:
- **Refresh handler**:
  - Decode `{refresh_token}`, validate non-empty.
  - Hash incoming token via `auth.HashRefreshToken`.
  - Look up `refresh_tokens` by `token_hash`. Not found -> 401.
  - `revoked = true` -> **reuse detected**: revoke ALL tokens for user, return 401.
  - `expired` -> 401.
  - Revoke current token, issue new access + refresh pair, store new hash.
  - Return 200 `{access_token, refresh_token}`.

**Tests**:
- Validation: empty/missing `refresh_token` -> 422 or 401.
- Response shape for success case.

**Depends on**: Chunk 8, Chunk 5

**Parallelizable**: yes (parallel with Chunks 9, 10, 12)

---

### Chunk 12: Forgot-password + reset-password endpoints

**Name**: `POST /api/v1/auth/forgot-password` and `POST /api/v1/auth/reset-password` handlers

**Files**:
- `internal/handler/auth.go` (modified)
- `internal/handler/auth_test.go` (modified)

**Changes**:

**Forgot-password**:
- Decode `{email}`, validate, normalize.
- **Always return 200** `{"message": "If the email is registered, a reset code has been sent."}` (anti-enumeration).
- If verified user found: generate OTP, insert `otp_codes` with `purpose = 'password_reset'`, send email. If send fails: log, still return 200.
- If not found: do nothing, return 200.

**Reset-password**:
- Decode `{email, otp, new_password}`, validate all three.
- Look up OTP for email + `purpose = 'password_reset'`, unused, non-expired.
- Same attempt logic as verify-otp (3 max).
- OTP invalid -> 401. OTP valid -> mark used, hash new password, update `users.password_hash`, revoke all refresh tokens for user.
- Return 200 `{"message": "Password reset successfully"}`.

**Tests**:
- Forgot-password: always returns 200 regardless of input (anti-enumeration).
- Reset-password: validation - missing fields -> 422, short new password -> 422.

**Depends on**: Chunk 8

**Parallelizable**: yes (parallel with Chunks 9, 10, 11)

---

### Chunk 13: Wire routes into the server

**Name**: Wire auth handlers and JWT middleware into the chi router

**Files**:
- `internal/server/server.go` (modified)
- `internal/server/server_test.go` (modified)

**Changes**:

`server.go`:
- Update `New()` to:
  - Construct `mail.NewSMTPSender(...)` from config.
  - Construct `handler.NewAuthHandler(pool, emailSender, cfg.JWTSecret, cfg.JWTAccessTokenTTL, cfg.JWTRefreshTokenTTL, logger)`.
  - Add public auth routes:
    ```
    r.Route("/api/v1/auth", func(r chi.Router) {
        r.Post("/register", authHandler.Register)
        r.Post("/verify-otp", authHandler.VerifyOTP)
        r.Post("/login", authHandler.Login)
        r.Post("/refresh", authHandler.Refresh)
        r.Post("/forgot-password", authHandler.ForgotPassword)
        r.Post("/reset-password", authHandler.ResetPassword)
    })
    ```
  - Add protected route group stub with JWT middleware (for future endpoints).
  - `New()` signature may need to accept additional dependencies, or construct SMTP sender internally from config.

`server_test.go`:
- Update `testConfig()` to include JWT/SMTP fields with test values so `New()` doesn't panic.
- Route existence tests: `POST /api/v1/auth/register` returns non-404, `POST /api/v1/auth/login` returns non-404.
- Protected route stub returns 401 without Authorization header.
- All existing tests continue to pass.

**Depends on**: Chunks 7, 8, 9, 10, 11, 12

**Parallelizable**: no

---

### Chunk 14: DB integration tests - user and OTP operations

**Name**: Integration tests for user and OTP database operations

**Files**:
- `internal/database/auth_integration_test.go` (new)

**Changes** (gated with `//go:build integration`):
- Uses existing `testPool` / `resetDatabase` / `RunMigrations` pattern.
- **User operations**: insert user with `email_verified = false`, query by email, update `email_verified`, verify case-insensitive email lookup.
- **OTP operations**: insert OTP, query valid OTP (not expired, not used, matching email + purpose), mark used, increment attempts, verify expiry logic (insert with `expires_at` in the past, confirm not returned).
- **Registration DB flow**: insert user + OTP, verify OTP marks used + sets `email_verified`, token generation is not tested here (that's auth service unit test territory).

**Depends on**: Chunk 1

**Parallelizable**: yes (parallel with Chunks 2-12)

---

### Chunk 15: DB integration tests - refresh token operations

**Name**: Integration tests for refresh token DB operations

**Files**:
- `internal/database/auth_integration_test.go` (modified - add tests to same file)

**Changes** (gated with `//go:build integration`):
- **Refresh token operations**: insert token hash, look up by hash, revoke single token, revoke all tokens for user, verify revoked token lookup returns `revoked = true`.
- **Token rotation**: insert token, "refresh" (revoke old, insert new), verify old is revoked and new is valid.
- **Theft detection**: present a revoked token hash, verify all user tokens get revoked.

**Depends on**: Chunk 14

**Parallelizable**: no

---

### Chunk 16: End-to-end HTTP integration tests

**Name**: Full flow integration tests via httptest + real DB

**Files**:
- `internal/handler/auth_integration_test.go` (new)

**Changes** (gated with `//go:build integration`):
- Uses `httptest.Server` + `server.New` with a real DB pool and mock `EmailSender` (captures OTPs in memory).
- **Happy paths**:
  - Register -> Verify-OTP -> Login (full flow).
  - Refresh -> new tokens -> old token rejected.
  - Forgot-password -> Reset-password -> Login with new password.
- **Error paths**:
  - Register with duplicate (verified) email -> 409.
  - Register -> re-register (stale unverified cleanup).
  - Login with wrong password -> 401, login with non-existent email -> 401 (same message).
  - Login with unverified user -> 401.
  - Refresh with revoked token -> all tokens revoked (theft detection).
  - Forgot-password with non-existent email -> 200.
  - Verify-OTP with wrong code 3x -> 429.
  - Verify-OTP with expired OTP -> 401.

**Depends on**: Chunk 13

**Parallelizable**: no

---

## Chunk Summary Table

| #  | Name                                   | Depends On        | Parallelizable         |
|----|----------------------------------------|-------------------|------------------------|
| 1  | Migration + JWT dep                    | none              | yes                    |
| 2  | Password hashing utilities             | none              | yes (|| 1, 3, 4, 5)    |
| 3  | JWT issuing and validation             | 1                 | no                     |
| 4  | OTP generation                         | none              | yes (|| 1, 2, 3, 5)    |
| 5  | Refresh token generation + hashing     | none              | yes (|| 1, 2, 3, 4)    |
| 6  | Email sender interface + SMTP          | none              | yes (|| 1-5)           |
| 7  | Auth middleware + context              | 3                 | no                     |
| 8  | AuthHandler struct + register endpoint | 2, 4, 6           | no                     |
| 9  | Verify-OTP endpoint                    | 8, 3, 5           | no                     |
| 10 | Login endpoint                         | 8                 | yes (|| 9, 11, 12)     |
| 11 | Refresh endpoint                       | 8, 5              | yes (|| 9, 10, 12)     |
| 12 | Forgot-password + reset-password       | 8                 | yes (|| 9, 10, 11)     |
| 13 | Wire routes into server                | 7, 8, 9, 10, 11, 12 | no                  |
| 14 | DB integration tests - user + OTP ops  | 1                 | yes (|| 2-12)          |
| 15 | DB integration tests - refresh ops     | 14                | no                     |
| 16 | E2E HTTP integration tests             | 13                | no                     |

## Rationale

- **No separate repository layer**: The existing codebase calls `pool.Exec`/`pool.QueryRow` directly in `SeedAdmin`. Introducing a repository interface would add abstraction the codebase doesn't use yet. Handlers will embed SQL queries directly, keeping the code flat and consistent with the existing pattern. If the codebase grows, a repository layer can be extracted later.
- **Single `AuthHandler` struct**: Keeps all auth endpoint dependencies co-located. Avoids scattered globals. The struct holds the pool, email sender, JWT config, and logger - everything an auth handler needs.
- **`email_verified` over separate pending table**: Simpler migration, simpler queries, matches spec wording "store pending user." The cleanup logic (delete stale unverified users on re-register) handles the only downside.
- **OTP attempts on the `otp_codes` row**: Simpler than a separate rate-limit table. Each OTP row tracks its own attempts. The 3-attempt limit is per-OTP (aligned with spec: "3 OTP verification attempts per email per 10-minute window" - since each OTP has a 10-minute window, per-OTP tracking achieves the same effect).
- **Deferred rate limiting middleware**: The spec requires IP-based and user-based rate limiting, but implementing a full sliding-window rate limiter is a significant cross-cutting concern. The OTP attempt limit covers the most security-critical rate limit. General rate limiting should be a separate work item.
- **`net/smtp` for SMTP**: Stdlib is sufficient for sending simple OTP emails. Avoids adding a third-party mail library. Can be upgraded later if HTML templates or advanced features are needed.

## Open Questions

**None** - all ambiguities have been resolved:
- Pending user model -> `email_verified` column (user confirmed).
- Username at registration -> auto-generated (user confirmed).
- OTP attempt tracking -> `attempts` column on `otp_codes`.
- Rate limiting -> deferred except for OTP attempts.
- JWT library -> `golang-jwt/jwt/v5`.
- Refresh token storage -> SHA-256 hash per spec.

## Risks

1. **Migration on existing data**: Migration `000002` adds `email_verified BOOLEAN NOT NULL DEFAULT FALSE` to `users`. If the admin seed user already exists, it will be set to `email_verified = false`. This doesn't matter because admin uses a separate auth path (`admin_credentials` table), but should be noted.

2. **Bcrypt 72-byte limit**: Bcrypt silently truncates passwords longer than 72 bytes. The spec doesn't mention a max password length. The implementation should document this but not enforce an artificial limit - this matches common practice.

3. **SMTP reliability**: If the SMTP server is down, registration will fail (user gets 500) after the user row is created. The plan handles this by deleting the user row on send failure, but there's a small window for a partial state if the delete also fails. Acceptable for now; a more robust solution (outbox pattern) can be added later.

4. **Concurrent registration with same email**: Two concurrent `POST /register` requests for the same email could race. The `users.email UNIQUE` constraint will cause one to fail with a Postgres unique violation. The handler should catch `pgx` unique violation errors (via `pgerrcode`) and return 409 rather than 500.

5. **Refresh token table growth**: Revoked tokens are never cleaned up in this plan. A periodic cleanup job (delete revoked tokens older than their `expires_at`) should be added in a future phase.

6. **Integration test database**: Tests require a running Postgres instance with `TEST_DATABASE_URL` set. This is consistent with the existing pattern but means CI must provision a test database.
