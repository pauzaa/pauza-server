package validate

import (
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"
)

// usernameRe matches 3-30 characters that are alphanumeric or underscore.
var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,30}$`)

// otpRe matches exactly 6 ASCII digits.
var otpRe = regexp.MustCompile(`^[0-9]{6}$`)

// Email validates an email address.
// Only bare addresses (user@domain) are accepted; display-name forms like
// "John Doe" <john@example.com> are rejected.
// Returns "" when valid, otherwise a human-readable error message.
func Email(v string) string {
	if strings.TrimSpace(v) == "" {
		return "email is required"
	}
	if utf8.RuneCountInString(v) > 255 {
		return "email must not exceed 255 characters"
	}
	addr, err := mail.ParseAddress(v)
	if err != nil {
		return "email format is invalid"
	}
	// Reject display-name forms; the parsed address must exactly equal the input.
	if addr.Address != v {
		return "email format is invalid"
	}
	return ""
}

// Password validates a password.
// Returns "" when valid, otherwise a human-readable error message.
func Password(v string) string {
	if v == "" {
		return "password is required"
	}
	if utf8.RuneCountInString(v) < 8 {
		return "password must be at least 8 characters"
	}
	return ""
}

// Username validates a username.
// Returns "" when valid, otherwise a human-readable error message.
func Username(v string) string {
	if v == "" {
		return "username is required"
	}
	if !usernameRe.MatchString(v) {
		return "username must be 3-30 characters and contain only letters, digits, or underscores"
	}
	return ""
}

// Name validates a display name.
// Returns "" when valid, otherwise a human-readable error message.
func Name(v string) string {
	if utf8.RuneCountInString(v) > 100 {
		return "name must not exceed 100 characters"
	}
	return ""
}

// OTP validates a one-time password code.
// Returns "" when valid, otherwise a human-readable error message.
func OTP(v string) string {
	if v == "" {
		return "otp is required"
	}
	if !otpRe.MatchString(v) {
		return "otp must be exactly 6 digits"
	}
	return ""
}

// Platform validates a device platform value.
// Returns "" when valid, otherwise a human-readable error message.
func Platform(v string) string {
	if v == "" {
		return "platform is required"
	}
	if v != "android" && v != "ios" {
		return "platform must be android or ios"
	}
	return ""
}
