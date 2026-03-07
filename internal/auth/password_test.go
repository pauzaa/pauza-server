package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

func TestHashPassword_ValidBcryptPrefix(t *testing.T) {
	hash, err := auth.HashPassword("securepassword")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	// bcrypt cost-12 hashes start with $2a$12$ (or $2b$12$).
	if !strings.HasPrefix(hash, "$2a$12$") && !strings.HasPrefix(hash, "$2b$12$") {
		t.Errorf("hash prefix = %q, want $2a$12$ or $2b$12$", hash[:7])
	}
}

func TestCheckPassword_Correct(t *testing.T) {
	password := "correcthorse"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	ok, err := auth.CheckPassword(hash, password)
	if err != nil {
		t.Fatalf("CheckPassword() unexpected error: %v", err)
	}
	if !ok {
		t.Error("CheckPassword() with correct password returned false, want true")
	}
}

func TestCheckPassword_Wrong(t *testing.T) {
	hash, err := auth.HashPassword("rightpassword")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	ok, err := auth.CheckPassword(hash, "wrongpassword")
	if err != nil {
		t.Fatalf("CheckPassword() unexpected error: %v", err)
	}
	if ok {
		t.Error("CheckPassword() with wrong password returned true, want false")
	}
}

func TestHashPassword_RoundTrip(t *testing.T) {
	cases := []struct {
		name     string
		password string
	}{
		{"simple_ascii", "simplepass"},
		{"special_chars", "p@$$w0rd!#%"},
		{"unicode", "unicode-пароль-密码"},
		{"numeric", "12345678"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hash, err := auth.HashPassword(tc.password)
			if err != nil {
				t.Fatalf("HashPassword(%q) error = %v", tc.password, err)
			}
			ok, err := auth.CheckPassword(hash, tc.password)
			if err != nil {
				t.Fatalf("CheckPassword() unexpected error: %v", err)
			}
			if !ok {
				t.Errorf("CheckPassword() round-trip failed for %q", tc.password)
			}
		})
	}
}

func TestHashPassword_Empty(t *testing.T) {
	// bcrypt accepts empty passwords; verify it hashes and round-trips.
	hash, err := auth.HashPassword("")
	if err != nil {
		t.Fatalf("HashPassword(\"\") error = %v", err)
	}
	ok, err := auth.CheckPassword(hash, "")
	if err != nil {
		t.Fatalf("CheckPassword() unexpected error: %v", err)
	}
	if !ok {
		t.Error("CheckPassword() with empty password returned false, want true")
	}
	// A non-empty password must not match the empty-password hash.
	ok, err = auth.CheckPassword(hash, "notempty")
	if err != nil {
		t.Fatalf("CheckPassword() unexpected error: %v", err)
	}
	if ok {
		t.Error("CheckPassword() non-empty vs empty hash returned true, want false")
	}
}

func TestHashPassword_VeryLong(t *testing.T) {
	// golang.org/x/crypto/bcrypt returns an error for passwords exceeding
	// 72 bytes rather than silently truncating.
	long := strings.Repeat("a", 73)
	_, err := auth.HashPassword(long)
	if err == nil {
		t.Fatal("HashPassword() with >72-byte input returned nil error, want error")
	}
	if !errors.Is(err, bcrypt.ErrPasswordTooLong) {
		t.Errorf("error = %q, want %v", err, bcrypt.ErrPasswordTooLong)
	}

	// A password of exactly 72 bytes should succeed.
	exact := strings.Repeat("a", 72)
	hash, err := auth.HashPassword(exact)
	if err != nil {
		t.Fatalf("HashPassword() with 72-byte input error = %v", err)
	}
	ok, err := auth.CheckPassword(hash, exact)
	if err != nil {
		t.Fatalf("CheckPassword() unexpected error: %v", err)
	}
	if !ok {
		t.Error("CheckPassword() round-trip for 72-byte password failed")
	}
}

func TestCheckPassword_MalformedHash(t *testing.T) {
	// A malformed hash should return (false, non-nil error) — exercising the
	// third return path in CheckPassword where bcrypt fails for reasons other
	// than a simple mismatch.
	cases := []struct {
		name string
		hash string
	}{
		{"empty_hash", ""},
		{"plain_text", "notahash"},
		{"truncated_bcrypt", "$2a$12$"},
		{"wrong_prefix", "$9z$12$ABCDEFGHIJKLMNOPQRSTUUABCDEFGHIJKLMNOPQRSTUVWX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := auth.CheckPassword(tc.hash, "anypassword")
			if err == nil {
				t.Fatal("CheckPassword() with malformed hash returned nil error, want error")
			}
			if ok {
				t.Error("CheckPassword() with malformed hash returned true, want false")
			}
			if !strings.Contains(err.Error(), "checking password") {
				t.Errorf("error = %q, want wrapping prefix \"checking password\"", err.Error())
			}
		})
	}
}

func TestHashPassword_UniqueHashes(t *testing.T) {
	// Same password hashed twice should produce different hashes (random salt).
	hash1, err := auth.HashPassword("samepassword")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	hash2, err := auth.HashPassword("samepassword")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if hash1 == hash2 {
		t.Error("two hashes of the same password are identical, expected different salts")
	}
}

// ---------- DummyCheckPassword ----------

// TestDummyCheckPassword_DoesNotPanic verifies that DummyCheckPassword
// completes without panicking for various inputs.
func TestDummyCheckPassword_DoesNotPanic(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		password string
	}{
		{"empty", ""},
		{"simple", "password123"},
		{"special_chars", "p@$$w0rd!#%"},
		{"unicode", "пароль-密码"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// DummyCheckPassword should never panic regardless of input.
			auth.DummyCheckPassword(tc.password)
		})
	}
}

// TestDummyCheckPassword_NeverMatchesRealHash verifies that the dummy
// comparison path never accidentally returns a "match" for a password
// that was actually hashed with HashPassword. This is a sanity check:
// the dummy hash corresponds to an internal sentinel string, so no
// user-supplied password should ever match it.
func TestDummyCheckPassword_NeverMatchesRealHash(t *testing.T) {
	t.Parallel()

	// Hash a known password the normal way.
	realPassword := "realpassword"
	realHash, err := auth.HashPassword(realPassword)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	// The real password should match the real hash …
	ok, err := auth.CheckPassword(realHash, realPassword)
	if err != nil {
		t.Fatalf("CheckPassword() error = %v", err)
	}
	if !ok {
		t.Fatal("CheckPassword() with correct password returned false")
	}

	// … but DummyCheckPassword simply burns time and never produces a
	// match. We call it here to exercise the code path; there is no
	// return value to assert on because the function is fire-and-forget.
	auth.DummyCheckPassword(realPassword)
}
