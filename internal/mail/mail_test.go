package mail

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Compile-time interface satisfaction checks.
var _ Sender = (*SMTPSender)(nil)

// newTestSender returns an SMTPSender suitable for unit tests that never
// reach a real SMTP server.  It uses deterministic dummy values and a
// silent logger.
func newTestSender() *SMTPSender {
	return NewSMTPSender(SMTPConfig{
		Host:             "smtp.example.com",
		Port:             587,
		Username:         "u",
		Password:         "p",
		From:             "f@example.com",
		OTPExpiryMinutes: 10,
		Timeout:          30 * time.Second,
		TLSPolicy:        "mandatory",
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestNewSMTPSender_SetsFields(t *testing.T) {
	s := NewSMTPSender(SMTPConfig{
		Host:             "smtp.example.com",
		Port:             587,
		Username:         "user@example.com",
		Password:         "secret",
		From:             "noreply@example.com",
		OTPExpiryMinutes: 10,
		Timeout:          30 * time.Second,
		TLSPolicy:        "mandatory",
	})
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
	if s.timeout != 30*time.Second {
		t.Errorf("timeout = %v, want %v", s.timeout, 30*time.Second)
	}
	if s.tlsPolicy != "mandatory" {
		t.Errorf("tlsPolicy = %q, want %q", s.tlsPolicy, "mandatory")
	}
	if s.logger == nil {
		t.Error("expected non-nil logger when nil is passed")
	}
}

func TestNewSMTPSender_UsesProvidedLogger(t *testing.T) {
	custom := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewSMTPSender(SMTPConfig{
		Host:             "smtp.example.com",
		Port:             587,
		Username:         "user@example.com",
		Password:         "secret",
		From:             "noreply@example.com",
		OTPExpiryMinutes: 10,
		Timeout:          30 * time.Second,
		TLSPolicy:        "mandatory",
		Logger:           custom,
	})
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
	s := NewSMTPSender(SMTPConfig{
		Host:             "smtp.example.com",
		Port:             587,
		Username:         "u",
		Password:         "p",
		From:             "evil@example.com\nBcc: spy@evil.com",
		OTPExpiryMinutes: 10,
		Timeout:          30 * time.Second,
		TLSPolicy:        "mandatory",
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	err := s.SendOTP(context.Background(), "ok@example.com", "123456", PurposeEmailVerification)
	if err == nil {
		t.Fatal("expected error for header injection in from")
	}
	if !strings.Contains(err.Error(), "header injection") {
		t.Errorf("error = %q, want it to contain %q", err, "header injection")
	}
}

func TestSendOTP_HeaderInjectionInHost(t *testing.T) {
	s := NewSMTPSender(SMTPConfig{
		Host:             "smtp.example.com\nevil-header: injected",
		Port:             587,
		Username:         "u",
		Password:         "p",
		From:             "f@example.com",
		OTPExpiryMinutes: 10,
		Timeout:          30 * time.Second,
		TLSPolicy:        "mandatory",
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	err := s.SendOTP(context.Background(), "ok@example.com", "123456", PurposeEmailVerification)
	if err == nil {
		t.Fatal("expected error for header injection in host")
	}
	if !strings.Contains(err.Error(), "header injection") {
		t.Errorf("error = %q, want it to contain %q", err, "header injection")
	}
}

func TestSendOTP_HeaderInjectionInUsername(t *testing.T) {
	s := NewSMTPSender(SMTPConfig{
		Host:             "smtp.example.com",
		Port:             587,
		Username:         "user\r\nRCPT TO:<spy@evil.com>",
		Password:         "p",
		From:             "f@example.com",
		OTPExpiryMinutes: 10,
		Timeout:          30 * time.Second,
		TLSPolicy:        "mandatory",
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	err := s.SendOTP(context.Background(), "ok@example.com", "123456", PurposeEmailVerification)
	if err == nil {
		t.Fatal("expected error for header injection in username")
	}
	if !strings.Contains(err.Error(), "header injection") {
		t.Errorf("error = %q, want it to contain %q", err, "header injection")
	}
}

func TestSendOTP_HeaderInjectionInPassword(t *testing.T) {
	s := NewSMTPSender(SMTPConfig{
		Host:             "smtp.example.com",
		Port:             587,
		Username:         "u",
		Password:         "pass\r\nRCPT TO:<spy@evil.com>",
		From:             "f@example.com",
		OTPExpiryMinutes: 10,
		Timeout:          30 * time.Second,
		TLSPolicy:        "mandatory",
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	err := s.SendOTP(context.Background(), "ok@example.com", "123456", PurposeEmailVerification)
	if err == nil {
		t.Fatal("expected error for header injection in password")
	}
	if !strings.Contains(err.Error(), "header injection") {
		t.Errorf("error = %q, want it to contain %q", err, "header injection")
	}
}

// fakeSMTPServer starts a TCP listener that speaks just enough SMTP to reach
// the RCPT TO stage and then rejects the recipient with a 550 error that
// embeds the raw email address — mimicking real SMTP bounce diagnostics.
// It returns the listener (caller must close) and the "host:port" address.
func fakeSMTPServer(t *testing.T, recipientEmail string) (net.Listener, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed
		}
		defer conn.Close()

		w := bufio.NewWriter(conn)
		r := bufio.NewReader(conn)

		// Greet the client.
		fmt.Fprintf(w, "220 fake SMTP ready\r\n")
		w.Flush()

		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)

			switch {
			case strings.HasPrefix(strings.ToUpper(line), "EHLO"), strings.HasPrefix(strings.ToUpper(line), "HELO"):
				fmt.Fprintf(w, "250-fake Hello\r\n250 AUTH PLAIN\r\n")
			case strings.HasPrefix(strings.ToUpper(line), "AUTH"):
				fmt.Fprintf(w, "235 Authentication succeeded\r\n")
			case strings.HasPrefix(strings.ToUpper(line), "MAIL FROM"):
				fmt.Fprintf(w, "250 OK\r\n")
			case strings.HasPrefix(strings.ToUpper(line), "RCPT TO"):
				// Reject with an error that embeds the raw email.
				fmt.Fprintf(w, "550 5.1.1 <%s>: Recipient address rejected\r\n", recipientEmail)
			case strings.HasPrefix(strings.ToUpper(line), "QUIT"):
				fmt.Fprintf(w, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(w, "500 Unrecognized\r\n")
			}
			w.Flush()
		}
	}()

	return ln, ln.Addr().String()
}

// smtpSanitizationFixture sets up a fake SMTP server that rejects the given
// recipient and returns the resulting error, error message, and captured log
// output. It is a shared helper for the SMTP privacy/sanitization tests.
func smtpSanitizationFixture(t *testing.T, recipient string) (error, string, string) {
	t.Helper()
	ln, addr := fakeSMTPServer(t, recipient)
	defer ln.Close()

	host, port := splitHostPort(t, addr)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	s := NewSMTPSender(SMTPConfig{
		Host:             host,
		Port:             port,
		Username:         "user",
		Password:         "pass",
		From:             "noreply@example.com",
		OTPExpiryMinutes: 10,
		Timeout:          30 * time.Second,
		TLSPolicy:        "none",
		Logger:           logger,
	})

	err := s.SendOTP(context.Background(), recipient, "123456", PurposeEmailVerification)
	if err == nil {
		t.Fatal("expected SMTP error, got nil")
	}

	return err, err.Error(), logBuf.String()
}

func TestSendOTP_SMTPErrorOmitsRecipientFromError(t *testing.T) {
	const recipient = "victim@example.com"
	_, errMsg, _ := smtpSanitizationFixture(t, recipient)

	if strings.Contains(errMsg, recipient) {
		t.Errorf("returned error contains raw recipient email %q: %s", recipient, errMsg)
	}
}

func TestSendOTP_SMTPErrorRetainsPrefix(t *testing.T) {
	const recipient = "victim@example.com"
	_, errMsg, _ := smtpSanitizationFixture(t, recipient)

	if !strings.Contains(errMsg, "sending otp email") {
		t.Errorf("error missing prefix: %s", errMsg)
	}
}

func TestSendOTP_LogOmitsRawRecipient(t *testing.T) {
	const recipient = "victim@example.com"
	_, _, logOutput := smtpSanitizationFixture(t, recipient)

	if strings.Contains(logOutput, recipient) {
		t.Errorf("log output contains raw recipient email %q:\n%s", recipient, logOutput)
	}
}

func TestSendOTP_LogContainsMaskedRecipient(t *testing.T) {
	const recipient = "victim@example.com"
	_, _, logOutput := smtpSanitizationFixture(t, recipient)

	if !strings.Contains(logOutput, "v***@e***.com") {
		t.Errorf("log output missing masked recipient; got:\n%s", logOutput)
	}
}

// splitHostPort is a test helper that splits a net.Listener address.
func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("Atoi(%q): %v", portStr, err)
	}
	return host, port
}
