package auth

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"
)

// OTPExpiry is the duration after which an OTP code expires.
const OTPExpiry = 10 * time.Minute

// MaxOTPAttempts is the maximum number of verification attempts allowed per OTP.
const MaxOTPAttempts = 3

// otpDigits is the number of possible 6-digit OTP codes (10^6 = 1,000,000:
// 000000–999999). Declared as a constant and converted to a *big.Int inside
// GenerateOTP so there is no mutable package-level state.
const otpDigits = 1_000_000

// GenerateOTP returns a cryptographically random 6-digit string, zero-padded.
func GenerateOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(otpDigits))
	if err != nil {
		return "", fmt.Errorf("generating otp: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
