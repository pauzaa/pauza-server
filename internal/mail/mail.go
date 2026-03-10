package mail

import "context"

// OTP purpose constants used by SendOTP callers.
const (
	PurposeEmailVerification = "email_verification"
	PurposePasswordReset     = "password_reset"
)

// Sender abstracts email delivery so implementations can be swapped
// (e.g. real SMTP vs. in-memory stub for tests).
type Sender interface {
	Probe(ctx context.Context) error
	SendOTP(ctx context.Context, to, otp, purpose string) error
}
