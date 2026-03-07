package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// GenerateRefreshToken produces an opaque refresh token from 32 random bytes.
// It returns the raw base64url-encoded token (sent to the client) and its
// SHA-256 hex hash (stored in the database).
func GenerateRefreshToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generating refresh token: %w", err)
	}

	raw := base64.RawURLEncoding.EncodeToString(b)
	hash := HashRefreshToken(raw)
	return raw, hash, nil
}

// HashRefreshToken returns the deterministic SHA-256 hex digest of a raw
// refresh token string.
func HashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
