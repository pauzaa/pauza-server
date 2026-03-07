package redact

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// emailRe matches email addresses inside arbitrary text.
// It uses Unicode-aware character classes (\p{L}, \p{N}) so that
// internationalized local-parts and domain labels are also caught
// during sanitization, while remaining safe for SMTP log redaction.
var emailRe = regexp.MustCompile(`[\p{L}\p{N}._%+\-]+@[\p{L}\p{N}.\-]+\.[\p{L}]{2,}`)

// Email returns a partially redacted email suitable for safe logging.
// "alice@example.com" becomes "a***@e***.com".
//
// Inputs without a valid local-part (e.g. "@domain.com", "notanemail")
// are fully masked to "***".  SanitizeEmail's regex already requires a
// non-empty local-part, so the two functions are consistent: any string
// that the regex matches will also be meaningfully redacted by Email.
func Email(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "***"
	}
	local := email[:at]
	domain := email[at+1:]

	// Use the first rune (not byte) so multi-byte Unicode local-parts
	// are handled correctly.
	firstLocal, _ := utf8.DecodeRuneInString(local)
	maskedLocal := string(firstLocal) + "***"

	dot := strings.LastIndex(domain, ".")
	if dot <= 0 {
		return maskedLocal + "@***"
	}

	firstDomain, _ := utf8.DecodeRuneInString(domain)
	maskedDomain := string(firstDomain) + "***" + domain[dot:]

	return maskedLocal + "@" + maskedDomain
}

// SanitizeEmail replaces every email address found in s with its
// redacted form (via Email), so raw addresses never leak into logs
// or error messages returned to callers.
func SanitizeEmail(s string) string {
	return emailRe.ReplaceAllStringFunc(s, Email)
}
