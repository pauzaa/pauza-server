package validate_test

import (
	"strings"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/validate"
)

func TestEmail_Valid(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"simple", "user@example.com"},
		{"short_domain", "a@b.co"},
		{"plus_tag", "user+tag@domain.org"},
		{"uppercase", "UPPER@CASE.COM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if msg := validate.Email(tc.input); msg != "" {
				t.Errorf("Email(%q) = %q, want empty", tc.input, msg)
			}
		})
	}
}

func TestEmail_Invalid(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", "required"},
		{"whitespace", "   ", "required"},
		{"no_at", "notanemail", "format"},
		{"missing_local", "@missing.local", "format"},
		{"too_long", strings.Repeat("a", 250) + "@b.com", "255"},
		{"display_name_angle", "\"John Doe\" <john@example.com>", "format"},
		{"display_name_bare", "John Doe <john@example.com>", "format"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := validate.Email(tc.input)
			if msg == "" {
				t.Errorf("Email(%q) = empty, want error containing %q", tc.input, tc.want)
			} else if !strings.Contains(msg, tc.want) {
				t.Errorf("Email(%q) = %q, want substring %q", tc.input, msg, tc.want)
			}
		})
	}
}

func TestUsername_Valid(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"min_length", "abc"},
		{"underscore", "user_name"},
		{"mixed_case_digits", "User123"},
		{"all_underscores", "___"},
		{"max_length", strings.Repeat("a", 30)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if msg := validate.Username(tc.input); msg != "" {
				t.Errorf("Username(%q) = %q, want empty", tc.input, msg)
			}
		})
	}
}

func TestUsername_Invalid(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", "required"},
		{"too_short", "ab", "3-30"},
		{"too_long", strings.Repeat("a", 31), "3-30"},
		{"spaces", "no spaces", "3-30"},
		{"dashes", "no-dashes", "3-30"},
		{"dots", "has.dot", "3-30"},
		{"emoji", "emoji😀", "3-30"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := validate.Username(tc.input)
			if msg == "" {
				t.Errorf("Username(%q) = empty, want error containing %q", tc.input, tc.want)
			} else if !strings.Contains(msg, tc.want) {
				t.Errorf("Username(%q) = %q, want substring %q", tc.input, msg, tc.want)
			}
		})
	}
}

func TestName_Valid(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"simple", "John Doe"},
		{"max_length", strings.Repeat("a", 100)},
		{"unicode", "名前テスト"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if msg := validate.Name(tc.input); msg != "" {
				t.Errorf("Name(%q) = %q, want empty", tc.input, msg)
			}
		})
	}
}

func TestName_Invalid(t *testing.T) {
	msg := validate.Name(strings.Repeat("a", 101))
	if msg == "" {
		t.Error("Name(101 chars) = empty, want error")
	}
	if !strings.Contains(msg, "100") {
		t.Errorf("Name(101 chars) = %q, want substring '100'", msg)
	}
}

func TestOTP_Valid(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"all_zeros", "000000"},
		{"typical", "123456"},
		{"all_nines", "999999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if msg := validate.OTP(tc.input); msg != "" {
				t.Errorf("OTP(%q) = %q, want empty", tc.input, msg)
			}
		})
	}
}

func TestOTP_Invalid(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", "required"},
		{"too_short", "12345", "6 digits"},
		{"too_long", "1234567", "6 digits"},
		{"letters", "abcdef", "6 digits"},
		{"space", "12 345", "6 digits"},
		{"trailing_letter", "12345a", "6 digits"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := validate.OTP(tc.input)
			if msg == "" {
				t.Errorf("OTP(%q) = empty, want error containing %q", tc.input, tc.want)
			} else if !strings.Contains(msg, tc.want) {
				t.Errorf("OTP(%q) = %q, want substring %q", tc.input, msg, tc.want)
			}
		})
	}
}

func TestPlatform_Valid(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"android", "android"},
		{"ios", "ios"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if msg := validate.Platform(tc.input); msg != "" {
				t.Errorf("Platform(%q) = %q, want empty", tc.input, msg)
			}
		})
	}
}

func TestPlatform_Invalid(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", "required"},
		{"wrong_case_android", "Android", "android or ios"},
		{"wrong_case_ios", "IOS", "android or ios"},
		{"unsupported", "web", "android or ios"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := validate.Platform(tc.input)
			if msg == "" {
				t.Errorf("Platform(%q) = empty, want error containing %q", tc.input, tc.want)
			} else if !strings.Contains(msg, tc.want) {
				t.Errorf("Platform(%q) = %q, want substring %q", tc.input, msg, tc.want)
			}
		})
	}
}
