package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is the work-factor used for password hashing.
// Cost 12 balances security and latency for this application.
const bcryptCost = 12

// dummyHash is a precomputed bcrypt hash at cost 12 used to equalize
// timing on login attempts for non-existent accounts. Without this,
// the login handler returns immediately when a user is not found,
// leaking account existence through response latency differences.
// The hash corresponds to an arbitrary string and is never expected
// to match any real password.
var dummyHash = mustGenerateDummyHash()

func mustGenerateDummyHash() string {
	h, err := bcrypt.GenerateFromPassword([]byte("dummy-timing-equalization"), bcryptCost)
	if err != nil {
		panic("auth: failed to generate dummy bcrypt hash: " + err.Error())
	}
	return string(h)
}

// DummyCheckPassword performs a bcrypt comparison against a precomputed
// dummy hash. It is used on code paths where a user was not found so
// that the response latency is indistinguishable from a real password
// check, preventing timing-based account enumeration. The result is
// always (false, nil) under normal operation.
func DummyCheckPassword(password string) {
	// We intentionally discard the result. The sole purpose is to burn
	// the same CPU time as a real bcrypt comparison at the same cost.
	_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
}

// HashPassword returns a bcrypt hash of the given password.
// Note: bcrypt operates on at most 72 bytes of input; the Go implementation
// returns an error if the password exceeds this limit.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return string(hash), nil
}

// CheckPassword compares a bcrypt hash with a plaintext password.
// It returns (true, nil) when the password matches, (false, nil) when it does
// not, and (false, err) only if an unexpected internal error occurs. Callers
// never need to inspect the underlying hashing library's error types.
func CheckPassword(hash, password string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return false, nil
	}
	return false, fmt.Errorf("checking password: %w", err)
}
