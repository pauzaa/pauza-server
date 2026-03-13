package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/IsorilovA/pauza-server/internal/auth"
)

func TestIssueAccessToken_RoundTrip(t *testing.T) {
	userID := "550e8400-e29b-41d4-a716-446655440000"
	email := "test@example.com"
	secret := "test-secret-key-at-least-32-bytes!"
	ttl := 15 * time.Minute

	tokenStr, err := auth.IssueAccessToken(userID, email, secret, ttl)
	if err != nil {
		t.Fatalf("IssueAccessToken() error = %v", err)
	}
	if tokenStr == "" {
		t.Fatal("IssueAccessToken() returned empty token")
	}

	claims, err := auth.ValidateAccessToken(tokenStr, secret)
	if err != nil {
		t.Fatalf("ValidateAccessToken() error = %v", err)
	}
	if claims.Subject != userID {
		t.Errorf("claims.Subject = %q, want %q", claims.Subject, userID)
	}
	if claims.Email != email {
		t.Errorf("claims.Email = %q, want %q", claims.Email, email)
	}
	if claims.Issuer != "pauza" {
		t.Errorf("claims.Issuer = %q, want %q", claims.Issuer, "pauza")
	}
}

func TestValidateAccessToken_Expired(t *testing.T) {
	userID := "550e8400-e29b-41d4-a716-446655440000"
	email := "test@example.com"
	secret := "test-secret-key-at-least-32-bytes!"
	ttl := -1 * time.Second // already expired

	tokenStr, err := auth.IssueAccessToken(userID, email, secret, ttl)
	if err != nil {
		t.Fatalf("IssueAccessToken() error = %v", err)
	}

	_, err = auth.ValidateAccessToken(tokenStr, secret)
	if err == nil {
		t.Fatal("ValidateAccessToken() with expired token returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "token is expired") {
		t.Errorf("error = %q, want mention of expired token", err.Error())
	}
}

func TestValidateAccessToken_WrongSecret(t *testing.T) {
	userID := "550e8400-e29b-41d4-a716-446655440000"
	email := "test@example.com"
	ttl := 15 * time.Minute

	tokenStr, err := auth.IssueAccessToken(userID, email, "correct-secret-that-is-at-least-32-bytes!", ttl)
	if err != nil {
		t.Fatalf("IssueAccessToken() error = %v", err)
	}

	_, err = auth.ValidateAccessToken(tokenStr, "wrong-secret-that-is-at-least-32-bytes!!")
	if err == nil {
		t.Fatal("ValidateAccessToken() with wrong secret returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "validating access token") {
		t.Errorf("error = %q, want mention of validating access token", err.Error())
	}
}

func TestValidateAccessToken_WrongIssuer(t *testing.T) {
	secret := "test-secret-key-at-least-32-bytes!"

	// Manually craft a token with a different issuer.
	now := time.Now().UTC()
	claims := jwt.RegisteredClaims{
		Issuer:    "not-pauza",
		Subject:   "some-user-id",
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}

	_, err = auth.ValidateAccessToken(tokenStr, secret)
	if err == nil {
		t.Fatal("ValidateAccessToken() with wrong issuer returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "validating access token") {
		t.Errorf("error = %q, want mention of validating access token", err.Error())
	}
}

func TestValidateAccessToken_Malformed(t *testing.T) {
	malformed := []struct {
		name  string
		token string
	}{
		{"empty string", ""},
		{"random garbage", "not.a.jwt"},
		{"partial token", "eyJhbGciOiJIUzI1NiJ9."},
		{"three dots no content", "a.b.c"},
	}

	secret := "test-secret-key-at-least-32-bytes!"

	for _, tc := range malformed {
		t.Run(tc.name, func(t *testing.T) {
			_, err := auth.ValidateAccessToken(tc.token, secret)
			if err == nil {
				t.Errorf("ValidateAccessToken(%q) returned nil error, want error", tc.token)
			}
		})
	}
}

func TestIssueAccessToken_EmptySecret(t *testing.T) {
	_, err := auth.IssueAccessToken("user-id", "a@b.com", "", 15*time.Minute)
	if err == nil {
		t.Fatal("IssueAccessToken() with empty secret returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "secret must be at least 32 bytes") {
		t.Errorf("error = %q, want mention of secret length requirement", err.Error())
	}
}

func TestIssueAccessToken_EmptyUserID(t *testing.T) {
	_, err := auth.IssueAccessToken("", "a@b.com", "some-secret", 15*time.Minute)
	if err == nil {
		t.Fatal("IssueAccessToken() with empty userID returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "userID must not be empty") {
		t.Errorf("error = %q, want mention of empty userID", err.Error())
	}
}

func TestValidateAccessToken_EmptySecret(t *testing.T) {
	// First issue a valid token with a real secret.
	tokenStr, err := auth.IssueAccessToken("user-id", "a@b.com", "test-secret-key-at-least-32-bytes!", 15*time.Minute)
	if err != nil {
		t.Fatalf("IssueAccessToken() error = %v", err)
	}

	_, err = auth.ValidateAccessToken(tokenStr, "")
	if err == nil {
		t.Fatal("ValidateAccessToken() with empty secret returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "secret must be at least 32 bytes") {
		t.Errorf("error = %q, want mention of secret length requirement", err.Error())
	}
}

func TestIssueAccessToken_ZeroTTL(t *testing.T) {
	secret := "test-secret-key-at-least-32-bytes!"

	// Zero TTL means the token expires immediately (ExpiresAt == IssuedAt).
	tokenStr, err := auth.IssueAccessToken("user-id", "a@b.com", secret, 0)
	if err != nil {
		t.Fatalf("IssueAccessToken() with zero TTL error = %v", err)
	}

	// Parse without validation so the assertion is deterministic and not
	// sensitive to wall-clock advancement or JWT second-level granularity.
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, err := parser.ParseWithClaims(tokenStr, &auth.Claims{}, func(t *jwt.Token) (any, error) {
		return []byte(secret), nil
	})
	if err != nil {
		t.Fatalf("parsing zero-TTL token: %v", err)
	}

	claims, ok := token.Claims.(*auth.Claims)
	if !ok {
		t.Fatal("unexpected claims type")
	}
	if claims.ExpiresAt == nil || claims.IssuedAt == nil {
		t.Fatal("ExpiresAt or IssuedAt is nil")
	}
	if !claims.ExpiresAt.Time.Equal(claims.IssuedAt.Time) {
		t.Errorf("ExpiresAt = %v, want equal to IssuedAt = %v", claims.ExpiresAt.Time, claims.IssuedAt.Time)
	}
	if claims.Issuer != "pauza" {
		t.Errorf("claims.Issuer = %q, want %q", claims.Issuer, "pauza")
	}
}

func TestValidateAccessToken_EmptySubject(t *testing.T) {
	secret := "test-secret-key-at-least-32-bytes!"

	// Manually craft a token with an empty subject.
	now := time.Now().UTC()
	claims := jwt.RegisteredClaims{
		Issuer:    "pauza",
		Subject:   "",
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}

	_, err = auth.ValidateAccessToken(tokenStr, secret)
	if err == nil {
		t.Fatal("ValidateAccessToken() with empty subject returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "empty subject") {
		t.Errorf("error = %q, want mention of empty subject", err.Error())
	}
}
