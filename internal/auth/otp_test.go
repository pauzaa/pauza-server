package auth_test

import (
	"regexp"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/auth"
)

var otpRe = regexp.MustCompile(`^[0-9]{6}$`)

func TestGenerateOTP_Format(t *testing.T) {
	otp, err := auth.GenerateOTP()
	if err != nil {
		t.Fatalf("GenerateOTP() error: %v", err)
	}
	if !otpRe.MatchString(otp) {
		t.Errorf("GenerateOTP() = %q, want match ^[0-9]{6}$", otp)
	}
}

func TestGenerateOTP_AllMatchSixDigits(t *testing.T) {
	for i := range 100 {
		otp, err := auth.GenerateOTP()
		if err != nil {
			t.Fatalf("GenerateOTP() iteration %d error: %v", i, err)
		}
		if len(otp) != 6 {
			t.Fatalf("GenerateOTP() iteration %d len = %d, want 6", i, len(otp))
		}
		if !otpRe.MatchString(otp) {
			t.Errorf("GenerateOTP() iteration %d = %q, want match ^[0-9]{6}$", i, otp)
		}
	}
}

func TestGenerateOTP_Uniqueness(t *testing.T) {
	const n = 100
	seen := make(map[string]struct{}, n)
	for i := range n {
		otp, err := auth.GenerateOTP()
		if err != nil {
			t.Fatalf("GenerateOTP() iteration %d error: %v", i, err)
		}
		seen[otp] = struct{}{}
	}
	// With 1,000,000 possible values, 100 crypto-random samples should be
	// nearly all distinct. Requiring at least 90 unique values makes a stuck
	// or biased generator fail while staying deterministic under real randomness.
	if len(seen) < 90 {
		t.Errorf("GenerateOTP() produced %d unique values out of %d, want at least 90", len(seen), n)
	}
}

func TestHashOTP_ReturnsBcryptHash(t *testing.T) {
	code := "123456"
	h, err := auth.HashOTP(code)
	if err != nil {
		t.Fatalf("HashOTP() error: %v", err)
	}
	// bcrypt hashes start with "$2a$" or "$2b$" and are 60 characters long.
	if len(h) != 60 {
		t.Errorf("HashOTP() length = %d, want 60 (bcrypt)", len(h))
	}
	if h[0] != '$' {
		t.Errorf("HashOTP() does not look like a bcrypt hash: %q", h)
	}
}

func TestHashOTP_DifferentHashesForSameInput(t *testing.T) {
	code := "123456"
	h1, err := auth.HashOTP(code)
	if err != nil {
		t.Fatalf("HashOTP() first call error: %v", err)
	}
	h2, err := auth.HashOTP(code)
	if err != nil {
		t.Fatalf("HashOTP() second call error: %v", err)
	}
	// bcrypt produces unique salts, so hashes must differ.
	if h1 == h2 {
		t.Error("HashOTP() returned identical hashes for same input; expected unique bcrypt salts")
	}
}

func TestVerifyOTP_CorrectCode(t *testing.T) {
	code := "654321"
	h, err := auth.HashOTP(code)
	if err != nil {
		t.Fatalf("HashOTP() error: %v", err)
	}
	match, err := auth.VerifyOTP(h, code)
	if err != nil {
		t.Fatalf("VerifyOTP() error: %v", err)
	}
	if !match {
		t.Error("VerifyOTP() = false, want true for correct code")
	}
}

func TestVerifyOTP_WrongCode(t *testing.T) {
	code := "123456"
	h, err := auth.HashOTP(code)
	if err != nil {
		t.Fatalf("HashOTP() error: %v", err)
	}
	match, err := auth.VerifyOTP(h, "654321")
	if err != nil {
		t.Fatalf("VerifyOTP() error: %v", err)
	}
	if match {
		t.Error("VerifyOTP() = true, want false for wrong code")
	}
}
