package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// OTPExpiry is the duration after which an OTP code expires.
const OTPExpiry = 10 * time.Minute

// MaxOTPAttempts is the maximum number of verification attempts allowed per OTP.
const MaxOTPAttempts = 3

// otpBcryptCost is the bcrypt work-factor for OTP hashing.
// A lower cost than passwords is acceptable because OTPs are short-lived
// (10 minutes) and attempt-limited (3 tries), but bcrypt still makes offline
// brute-force of the 6-digit space impractical after a DB leak.
const otpBcryptCost = 10

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

// HashOTP returns a bcrypt hash of the plaintext OTP code. The hash is
// stored in the database instead of the raw code so that a database leak
// does not trivially reveal usable OTPs. Unlike a simple SHA-256, bcrypt's
// per-hash salt and work-factor make offline brute-force of the small 6-digit
// keyspace impractical.
func HashOTP(code string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(code), otpBcryptCost)
	if err != nil {
		return "", fmt.Errorf("hashing otp: %w", err)
	}
	return string(hash), nil
}

// VerifyOTP compares a bcrypt-hashed OTP with a plaintext OTP code.
// It returns true when the code matches, false otherwise. An error is
// returned only for unexpected internal failures, not for mismatches.
func VerifyOTP(hash, code string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(code))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return false, nil
	}
	return false, fmt.Errorf("verifying otp: %w", err)
}
