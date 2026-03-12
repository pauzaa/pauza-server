package mail

import "context"

type Purpose string

// OTP purpose constants used by SendOTP callers.
const (
	PurposeAuthLogin       Purpose = "auth_login"
	PurposeAccountDeletion Purpose = "account_deletion"
)

// Sender abstracts email delivery so implementations can be swapped
// (e.g. real SMTP vs. in-memory stub for tests).
type Sender interface {
	Probe(ctx context.Context) error
	SendOTP(ctx context.Context, to, otp string, purpose Purpose) error
}
