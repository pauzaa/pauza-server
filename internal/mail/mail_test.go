package mail

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// Compile-time interface satisfaction checks.
var _ Sender = (*SMTPSender)(nil)
var _ EmailSender = (*SMTPSender)(nil)

// newTestSender returns an SMTPSender suitable for unit tests that never
// reach a real SMTP server.  It uses deterministic dummy values and a
// silent logger.
func newTestSender() *SMTPSender {
	return NewSMTPSender(
		"smtp.example.com", 587,
		"u", "p", "f@example.com",
		10,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func TestNewSMTPSender_SetsFields(t *testing.T) {
	s := NewSMTPSender("smtp.example.com", 587, "user@example.com", "secret", "noreply@example.com", 10, nil)
	if s == nil {
		t.Fatal("expected non-nil SMTPSender")
	}
	if s.host != "smtp.example.com" {
		t.Errorf("host = %q, want %q", s.host, "smtp.example.com")
	}
	if s.port != 587 {
		t.Errorf("port = %d, want %d", s.port, 587)
	}
	if s.username != "user@example.com" {
		t.Errorf("username = %q, want %q", s.username, "user@example.com")
	}
	if s.password != "secret" {
		t.Errorf("password = %q, want %q", s.password, "secret")
	}
	if s.from != "noreply@example.com" {
		t.Errorf("from = %q, want %q", s.from, "noreply@example.com")
	}
	if s.otpExpiryMinutes != 10 {
		t.Errorf("otpExpiryMinutes = %d, want %d", s.otpExpiryMinutes, 10)
	}
	if s.logger == nil {
		t.Error("expected non-nil logger when nil is passed")
	}
}

func TestNewSMTPSender_UsesProvidedLogger(t *testing.T) {
	custom := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewSMTPSender("smtp.example.com", 587, "user@example.com", "secret", "noreply@example.com", 10, custom)
	if s.logger != custom {
		t.Error("expected sender to use the provided logger")
	}
}

func TestSubjectForPurpose_EmailVerification(t *testing.T) {
	got := subjectForPurpose(PurposeEmailVerification)
	want := "Verify your Pauza account"
	if got != want {
		t.Errorf("subject = %q, want %q", got, want)
	}
}

func TestSubjectForPurpose_PasswordReset(t *testing.T) {
	got := subjectForPurpose(PurposePasswordReset)
	want := "Reset your Pauza password"
	if got != want {
		t.Errorf("subject = %q, want %q", got, want)
	}
}

func TestSubjectForPurpose_UnknownReturnsEmpty(t *testing.T) {
	got := subjectForPurpose("unknown")
	if got != "" {
		t.Errorf("subject = %q for unknown purpose, want empty string", got)
	}
}

func TestSendOTP_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := newTestSender()
	err := s.SendOTP(ctx, "to@example.com", "123456", PurposeEmailVerification)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error = %q, want it to contain %q", err, "context canceled")
	}
}

func TestSendOTP_HeaderInjection(t *testing.T) {
	s := newTestSender()

	tests := []struct {
		name    string
		to      string
		otp     string
		purpose string
	}{
		{"newline in to", "evil@example.com\nBcc: spy@evil.com", "123456", PurposeEmailVerification},
		{"cr in to", "evil@example.com\rBcc: spy@evil.com", "123456", PurposeEmailVerification},
		{"newline in otp", "ok@example.com", "123\nBcc: spy@evil.com", PurposeEmailVerification},
		{"newline in purpose", "ok@example.com", "123456", "verify\nBcc: spy@evil.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.SendOTP(context.Background(), tt.to, tt.otp, tt.purpose)
			if err == nil {
				t.Fatal("expected error for header injection attempt")
			}
			if !strings.Contains(err.Error(), "header injection") {
				t.Errorf("error = %q, want it to contain %q", err, "header injection")
			}
		})
	}
}

func TestPurposeConstants(t *testing.T) {
	if PurposeEmailVerification != "email_verification" {
		t.Errorf("PurposeEmailVerification = %q, want %q", PurposeEmailVerification, "email_verification")
	}
	if PurposePasswordReset != "password_reset" {
		t.Errorf("PurposePasswordReset = %q, want %q", PurposePasswordReset, "password_reset")
	}
}

func TestContainsCRLF(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty string", "", false},
		{"clean string", "hello@example.com", false},
		{"bare LF", "a\nb", true},
		{"bare CR", "a\rb", true},
		{"CRLF pair", "a\r\nb", true},
		{"LF at start", "\nabc", true},
		{"CR at end", "abc\r", true},
		{"only LF", "\n", true},
		{"only CR", "\r", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsCRLF(tt.in)
			if got != tt.want {
				t.Errorf("containsCRLF(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestSendOTP_EmptyParams(t *testing.T) {
	s := newTestSender()

	tests := []struct {
		name    string
		to      string
		otp     string
		purpose string
	}{
		{"empty to", "", "123456", PurposeEmailVerification},
		{"empty otp", "ok@example.com", "", PurposeEmailVerification},
		{"empty purpose", "ok@example.com", "123456", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.SendOTP(context.Background(), tt.to, tt.otp, tt.purpose)
			if err == nil {
				t.Fatal("expected error for empty parameter")
			}
			if !strings.Contains(err.Error(), "non-empty") {
				t.Errorf("error = %q, want it to contain %q", err, "non-empty")
			}
		})
	}
}

func TestSendOTP_UnknownPurpose(t *testing.T) {
	s := newTestSender()
	err := s.SendOTP(context.Background(), "ok@example.com", "123456", "bogus_purpose")
	if err == nil {
		t.Fatal("expected error for unknown purpose")
	}
	if !strings.Contains(err.Error(), "unknown purpose") {
		t.Errorf("error = %q, want it to contain %q", err, "unknown purpose")
	}
}

func TestSendOTP_HeaderInjectionInFrom(t *testing.T) {
	s := NewSMTPSender("smtp.example.com", 587, "u", "p", "evil@example.com\nBcc: spy@evil.com", 10, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := s.SendOTP(context.Background(), "ok@example.com", "123456", PurposeEmailVerification)
	if err == nil {
		t.Fatal("expected error for header injection in from")
	}
	if !strings.Contains(err.Error(), "header injection") {
		t.Errorf("error = %q, want it to contain %q", err, "header injection")
	}
}

func TestSendOTP_HeaderInjectionInHost(t *testing.T) {
	s := NewSMTPSender("smtp.example.com\nevil-header: injected", 587, "u", "p", "f@example.com", 10, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := s.SendOTP(context.Background(), "ok@example.com", "123456", PurposeEmailVerification)
	if err == nil {
		t.Fatal("expected error for header injection in host")
	}
	if !strings.Contains(err.Error(), "header injection") {
		t.Errorf("error = %q, want it to contain %q", err, "header injection")
	}
}

func TestSendOTP_HeaderInjectionInUsername(t *testing.T) {
	s := NewSMTPSender("smtp.example.com", 587, "user\r\nRCPT TO:<spy@evil.com>", "p", "f@example.com", 10, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := s.SendOTP(context.Background(), "ok@example.com", "123456", PurposeEmailVerification)
	if err == nil {
		t.Fatal("expected error for header injection in username")
	}
	if !strings.Contains(err.Error(), "header injection") {
		t.Errorf("error = %q, want it to contain %q", err, "header injection")
	}
}

func TestSendOTP_HeaderInjectionInPassword(t *testing.T) {
	s := NewSMTPSender("smtp.example.com", 587, "u", "pass\r\nRCPT TO:<spy@evil.com>", "f@example.com", 10, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := s.SendOTP(context.Background(), "ok@example.com", "123456", PurposeEmailVerification)
	if err == nil {
		t.Fatal("expected error for header injection in password")
	}
	if !strings.Contains(err.Error(), "header injection") {
		t.Errorf("error = %q, want it to contain %q", err, "header injection")
	}
}
