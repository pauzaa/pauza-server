package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is the work-factor used for password hashing for admin credentials.
const bcryptCost = 12

// HashPassword returns a bcrypt hash of the given password for admin use.
// Note: bcrypt operates on at most 72 bytes of input; the Go implementation
// returns an error if the password exceeds this limit.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return string(hash), nil
}

// CheckPassword compares a bcrypt hash with a plaintext password for admin use.
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
