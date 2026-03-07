package redact_test

import (
	"strings"
	"testing"

	"github.com/IsorilovA/pauza-server/internal/redact"
)

func TestEmail_Standard(t *testing.T) {
	got := redact.Email("alice@example.com")
	want := "a***@e***.com"
	if got != want {
		t.Errorf("Email(%q) = %q, want %q", "alice@example.com", got, want)
	}
}

func TestEmail_MissingAt(t *testing.T) {
	got := redact.Email("notanemail")
	if got != "***" {
		t.Errorf("Email(%q) = %q, want %q", "notanemail", got, "***")
	}
}

func TestEmail_SingleCharLocal(t *testing.T) {
	got := redact.Email("a@example.com")
	want := "a***@e***.com"
	if got != want {
		t.Errorf("Email(%q) = %q, want %q", "a@example.com", got, want)
	}
}

func TestEmail_DomainWithoutDot(t *testing.T) {
	got := redact.Email("user@localhost")
	want := "u***@***"
	if got != want {
		t.Errorf("Email(%q) = %q, want %q", "user@localhost", got, want)
	}
}

func TestSanitizeEmail_NoEmails(t *testing.T) {
	input := "plain text without addresses"
	got := redact.SanitizeEmail(input)
	if got != input {
		t.Errorf("SanitizeEmail(%q) = %q, want unchanged", input, got)
	}
}

func TestSanitizeEmail_SMTPErrorAngleBrackets(t *testing.T) {
	input := "550 5.1.1 <bob@example.com>: Recipient address rejected"
	got := redact.SanitizeEmail(input)
	if strings.Contains(got, "bob@example.com") {
		t.Errorf("SanitizeEmail(%q) still contains raw email: %q", input, got)
	}
	want := "550 5.1.1 <b***@e***.com>: Recipient address rejected"
	if got != want {
		t.Errorf("SanitizeEmail(%q) = %q, want %q", input, got, want)
	}
}

func TestSanitizeEmail_MultipleEmails(t *testing.T) {
	input := "from alice@example.com to bob@domain.org done"
	got := redact.SanitizeEmail(input)
	if strings.Contains(got, "alice@example.com") {
		t.Errorf("SanitizeEmail still contains alice: %q", got)
	}
	if strings.Contains(got, "bob@domain.org") {
		t.Errorf("SanitizeEmail still contains bob: %q", got)
	}
	want := "from a***@e***.com to b***@d***.org done"
	if got != want {
		t.Errorf("SanitizeEmail(%q) = %q, want %q", input, got, want)
	}
}

func TestSanitizeEmail_Empty(t *testing.T) {
	got := redact.SanitizeEmail("")
	if got != "" {
		t.Errorf("SanitizeEmail(%q) = %q, want empty", "", got)
	}
}

func TestSanitizeEmail_JustAnEmail(t *testing.T) {
	input := "user@example.com"
	got := redact.SanitizeEmail(input)
	want := "u***@e***.com"
	if got != want {
		t.Errorf("SanitizeEmail(%q) = %q, want %q", input, got, want)
	}
}

func TestSanitizeEmail_RawEmailDoesNotRemain(t *testing.T) {
	input := "error sending to user@example.com failed"
	got := redact.SanitizeEmail(input)
	if strings.Contains(got, "user@example.com") {
		t.Errorf("SanitizeEmail output %q still contains raw email", got)
	}
}

func TestEmail_BareAtDomain(t *testing.T) {
	// "@domain.com" has at == 0 so Email returns fully masked "***".
	// SanitizeEmail's regex requires a non-empty local-part, so it would
	// never feed "@domain.com" into Email — the two are consistent.
	got := redact.Email("@domain.com")
	if got != "***" {
		t.Errorf("Email(%q) = %q, want %q", "@domain.com", got, "***")
	}
}

func TestSanitizeEmail_BareAtDomainPassesThrough(t *testing.T) {
	// The regex requires ≥1 local-part character, so "@domain.com" is
	// not recognised as an email and passes through unchanged.  This is
	// consistent with Email() returning "***" for such inputs.
	input := "bounce: @domain.com rejected"
	got := redact.SanitizeEmail(input)
	if got != input {
		t.Errorf("SanitizeEmail(%q) = %q, want unchanged", input, got)
	}
}

func TestEmail_UnicodeLocal(t *testing.T) {
	got := redact.Email("ülker@example.com")
	want := "ü***@e***.com"
	if got != want {
		t.Errorf("Email(%q) = %q, want %q", "ülker@example.com", got, want)
	}
}

func TestEmail_UnicodeDomain(t *testing.T) {
	got := redact.Email("user@münchen.de")
	want := "u***@m***.de"
	if got != want {
		t.Errorf("Email(%q) = %q, want %q", "user@münchen.de", got, want)
	}
}

func TestSanitizeEmail_InternationalizedAddress(t *testing.T) {
	input := "failed for ülker@münchen.de with 550"
	got := redact.SanitizeEmail(input)
	if strings.Contains(got, "ülker@münchen.de") {
		t.Errorf("SanitizeEmail(%q) still contains raw email: %q", input, got)
	}
	want := "failed for ü***@m***.de with 550"
	if got != want {
		t.Errorf("SanitizeEmail(%q) = %q, want %q", input, got, want)
	}
}
