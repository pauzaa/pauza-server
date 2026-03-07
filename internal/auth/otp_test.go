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
