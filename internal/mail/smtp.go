package mail

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"

	"github.com/IsorilovA/pauza-server/internal/redact"
)

// SMTPConfig holds all parameters needed to construct an SMTPSender.
type SMTPConfig struct {
	Host             string
	Port             int
	Username         string
	Password         string
	From             string
	OTPExpiryMinutes int
	Timeout          time.Duration
	TLSPolicy        string
	Logger           *slog.Logger
}

// SMTPSender sends emails via an SMTP server using go-mail.
type SMTPSender struct {
	host             string
	port             int
	username         string
	password         string
	from             string
	otpExpiryMinutes int
	timeout          time.Duration
	tlsPolicy        string
	logger           *slog.Logger
}

// NewSMTPSender returns an SMTPSender configured from the given SMTPConfig.
// OTPExpiryMinutes is embedded in the OTP email body. Timeout controls the
// SMTP connection/send deadline; TLSPolicy must be one of "mandatory",
// "opportunistic", or "none".
// If Logger is nil, slog.Default() is used.
func NewSMTPSender(cfg SMTPConfig) *SMTPSender {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &SMTPSender{
		host:             cfg.Host,
		port:             cfg.Port,
		username:         cfg.Username,
		password:         cfg.Password,
		from:             cfg.From,
		otpExpiryMinutes: cfg.OTPExpiryMinutes,
		timeout:          cfg.Timeout,
		tlsPolicy:        cfg.TLSPolicy,
		logger:           cfg.Logger,
	}
}

// goMailTLSPolicy maps a config-level TLS policy string to the corresponding
// go-mail TLSPolicy constant. Unrecognized values default to TLSMandatory.
func goMailTLSPolicy(policy string) gomail.TLSPolicy {
	switch strings.ToLower(policy) {
	case "opportunistic":
		return gomail.TLSOpportunistic
	case "none":
		return gomail.NoTLS
	default:
		return gomail.TLSMandatory
	}
}

// newClient builds a go-mail client with the sender's configured transport
// settings.
func (s *SMTPSender) newClient() (*gomail.Client, error) {
	opts := []gomail.Option{
		gomail.WithPort(s.port),
		gomail.WithTimeout(s.timeout),
		gomail.WithTLSPolicy(goMailTLSPolicy(s.tlsPolicy)),
	}
	if s.username != "" && s.password != "" {
		opts = append(opts,
			gomail.WithUsername(s.username),
			gomail.WithPassword(s.password),
			gomail.WithSMTPAuth(gomail.SMTPAuthPlain),
		)
	}
	client, err := gomail.NewClient(s.host, opts...)
	if err != nil {
		return nil, fmt.Errorf("%s", redact.SanitizeEmail(err.Error()))
	}
	return client, nil
}

// subjectForPurpose returns an email subject line for the given OTP purpose.
// It returns an empty string for unrecognized purposes; callers must validate
// before invoking this helper.
func subjectForPurpose(purpose Purpose) string {
	switch purpose {
	case PurposeAuthLogin:
		return "Your Pauza sign-in code"
	case PurposeAccountDeletion:
		return "Confirm your Pauza account deletion"
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
// The underlying go-mail client honours the configured timeout via context
// deadline and applies the configured TLS policy.
func (s *SMTPSender) Probe(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("probing smtp transport: %w", err)
	}

	client, err := s.newClient()
	if err != nil {
		return fmt.Errorf("probing smtp transport: %s", err)
	}

	if err := client.DialWithContext(ctx); err != nil {
		return fmt.Errorf("probing smtp transport: %s", redact.SanitizeEmail(err.Error()))
	}
	defer client.Close() //nolint:errcheck // best-effort close after successful probe dial

	return nil
}

func (s *SMTPSender) SendOTP(ctx context.Context, to, otp string, purpose Purpose) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("sending otp email: %w", err)
	}

	if to == "" || otp == "" || purpose == "" {
		return fmt.Errorf("sending otp email: to, otp, and purpose must be non-empty")
	}

	if containsCRLF(s.host) || containsCRLF(s.from) || containsCRLF(s.username) || containsCRLF(s.password) || containsCRLF(to) || containsCRLF(otp) || containsCRLF(string(purpose)) {
		return fmt.Errorf("sending otp email: header injection detected")
	}

	subject := subjectForPurpose(purpose)
	if subject == "" {
		return fmt.Errorf("sending otp email: unknown purpose %q", purpose)
	}

	s.logger.InfoContext(ctx, "sending otp email", "to", redact.Email(to), "purpose", string(purpose))

	body := fmt.Sprintf(
		"Your Pauza verification code is: %s\r\n\r\n"+
			"This code expires in %d minutes. Do not share it with anyone.\r\n\r\n"+
			"If you did not request this code, you can safely ignore this email.",
		otp, s.otpExpiryMinutes,
	)

	// Build go-mail message.
	msg := gomail.NewMsg()
	if err := msg.From(s.from); err != nil {
		return fmt.Errorf("sending otp email: %s", redact.SanitizeEmail(err.Error()))
	}
	if err := msg.To(to); err != nil {
		return fmt.Errorf("sending otp email: %s", redact.SanitizeEmail(err.Error()))
	}
	msg.Subject(subject)
	msg.SetBodyString(gomail.TypeTextPlain, body)

	// Build go-mail client with configured options.
	client, err := s.newClient()
	if err != nil {
		return fmt.Errorf("sending otp email: %s", err)
	}

	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		// Deliberately use %s (not %w) to prevent callers from using
		// errors.Is / errors.As to match or unwrap the original SMTP
		// error, which may embed the raw recipient address.
		return fmt.Errorf("sending otp email: %s", redact.SanitizeEmail(err.Error()))
	}

	return nil
}
