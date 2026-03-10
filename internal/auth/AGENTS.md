# Auth Primitives
> See `/AGENTS.md` for repo-wide rules and `internal/AGENTS.md` for layering.

## OVERVIEW
`internal/auth` owns JWT, refresh-token, and OTP primitives for passwordless user auth, plus password helpers used by admin credentials. It is security-sensitive and intentionally separate from HTTP, SQL, and SMTP delivery.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| Access JWTs | `internal/auth/jwt.go` | HS256, issuer `pauza`, subject = user ID |
| Refresh tokens | `internal/auth/token.go` | raw token generation + SHA-256 hashing |
| Passwords | `internal/auth/password.go` | bcrypt hash/check for admin credentials |
| OTPs | `internal/auth/otp.go` | 6-digit codes, bcrypt hash, expiry/attempt limits |

## CONVENTIONS
- Keep helpers side-effect free except for randomness and hashing.
- Service code owns persistence and transaction boundaries; `internal/auth` should not talk to Postgres or HTTP.
- Access tokens use HS256 and must preserve issuer `pauza` and `sub=user_id` semantics.
- Refresh tokens are opaque: only the raw token leaves the service layer; only the hash is stored.
- OTP rules live here: 10 minute expiry and 3 attempt max.
- Errors from JWT/password helpers are for service or middleware logging; handlers still return safe client messages.

## ANTI-PATTERNS
- Do not sign or validate tokens inside handlers.
- Do not store raw refresh tokens.
- Do not log OTPs, secrets, or full tokens.
- Do not change JWT issuer or claim shape without updating middleware, service, and tests together.
- Do not let this package depend on mail, repository, or HTTP types.

## RELATED FILES
- `internal/service/auth.go`
- `internal/middleware/auth.go`
- `internal/mail/smtp.go`
