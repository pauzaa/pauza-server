package auth_test

import (
	"strings"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/auth"
)

func TestGenerateRefreshToken_NonEmpty(t *testing.T) {
	raw, hash, err := auth.GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken() error: %v", err)
	}
	if raw == "" {
		t.Error("GenerateRefreshToken() raw is empty")
	}
	if hash == "" {
		t.Error("GenerateRefreshToken() hash is empty")
	}
}

func TestGenerateRefreshToken_HashMatchesHashRefreshToken(t *testing.T) {
	raw, hash, err := auth.GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken() error: %v", err)
	}
	if got := auth.HashRefreshToken(raw); got != hash {
		t.Errorf("HashRefreshToken(raw) = %q, want %q", got, hash)
	}
}

func TestHashRefreshToken_Deterministic(t *testing.T) {
	input := "test-token-value"
	h1 := auth.HashRefreshToken(input)
	h2 := auth.HashRefreshToken(input)
	if h1 != h2 {
		t.Errorf("HashRefreshToken not deterministic: %q != %q", h1, h2)
	}
}

func TestGenerateRefreshToken_URLSafe(t *testing.T) {
	for i := range 20 {
		raw, _, err := auth.GenerateRefreshToken()
		if err != nil {
			t.Fatalf("iteration %d: GenerateRefreshToken() error: %v", i, err)
		}
		if strings.ContainsAny(raw, "+/=") {
			t.Errorf("iteration %d: raw token %q contains non-URL-safe characters", i, raw)
		}
	}
}

func TestGenerateRefreshToken_Length(t *testing.T) {
	raw, hash, err := auth.GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken() error: %v", err)
	}
	// base64url of 32 bytes without padding = ceil(32*4/3) = 43 characters
	if len(raw) != 43 {
		t.Errorf("raw token length = %d, want 43", len(raw))
	}
	// SHA-256 hex digest = 64 characters
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64", len(hash))
	}
}
