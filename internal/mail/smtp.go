package mail

import (
	"context"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
)

// SMTPSender sends emails via an SMTP server.
type SMTPSender struct {
	host             string
	port             int
	username         string
	password         string
	from             string
	otpExpiryMinutes int
	logger           *slog.Logger
}

// NewSMTPSender returns an SMTPSender configured with the given SMTP
// connection details. otpExpiryMinutes is embedded in the OTP email body.
// If logger is nil, slog.Default() is used.
func NewSMTPSender(host string, port int, username, password, from string, otpExpiryMinutes int, logger *slog.Logger) *SMTPSender {
	if logger == nil {
		logger = slog.Default()
	}
	return &SMTPSender{
		host:             host,
		port:             port,
		username:         username,
		password:         password,
		from:             from,
		otpExpiryMinutes: otpExpiryMinutes,
		logger:           logger,
	}
}

// subjectForPurpose returns an email subject line for the given OTP purpose.
// It returns an empty string for unrecognized purposes; callers must validate
// before invoking this helper.
func subjectForPurpose(purpose string) string {
	switch purpose {
	case PurposeEmailVerification:
		return "Verify your Pauza account"
	case PurposePasswordReset:
		return "Reset your Pauza password"
	default:
		return ""
	}
}

// containsCRLF reports whether s contains CR or LF characters.
func containsCRLF(s string) bool {
	return strings.ContainsAny(s, "\r\n")
}

// SendOTP sends a one-time password email to the given address.
//
// It logs the send event (to and purpose) at INFO level but never logs the
// OTP value itself.
//
// Header-injection: host, from, username, password, to, otp, and purpose are
// validated to reject embedded CR/LF characters before being interpolated into
// the message, SMTP address, or AUTH command.
//
// Note: net/smtp.SendMail does not accept a context, so ctx cannot cancel
// an in-flight SMTP transaction. We check ctx.Err before the network call
// so already-cancelled callers fail fast.
func (s *SMTPSender) SendOTP(ctx context.Context, to, otp, purpose string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("sending otp email to %s: %w", to, err)
	}

	if to == "" || otp == "" || purpose == "" {
		return fmt.Errorf("sending otp email: to, otp, and purpose must be non-empty")
	}

	if containsCRLF(s.host) || containsCRLF(s.from) || containsCRLF(s.username) || containsCRLF(s.password) || containsCRLF(to) || containsCRLF(otp) || containsCRLF(purpose) {
		return fmt.Errorf("sending otp email: header injection detected")
	}

	subject := subjectForPurpose(purpose)
	if subject == "" {
		return fmt.Errorf("sending otp email: unknown purpose %q", purpose)
	}

	s.logger.InfoContext(ctx, "sending otp email", "to", to, "purpose", purpose)

	body := fmt.Sprintf(
		"Your Pauza verification code is: %s\r\n\r\n"+
			"This code expires in %d minutes. Do not share it with anyone.\r\n\r\n"+
			"If you did not request this code, you can safely ignore this email.",
		otp, s.otpExpiryMinutes,
	)

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"utf-8\"\r\n\r\n%s\r\n",
		s.from, to, subject, body)

	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	smtpAuth := smtp.PlainAuth("", s.username, s.password, s.host)

	if err := smtp.SendMail(addr, smtpAuth, s.from, []string{to}, []byte(msg)); err != nil {
		return fmt.Errorf("sending otp email to %s: %w", to, err)
	}

	return nil
}
