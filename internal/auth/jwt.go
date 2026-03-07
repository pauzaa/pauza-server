package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// tokenIssuer is the value written to and validated in the "iss" claim.
const tokenIssuer = "pauza"

// Claims holds the custom JWT claims for access tokens.
// The user ID is stored in the standard RegisteredClaims.Subject field.
type Claims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

// IssueAccessToken creates an HS256-signed JWT with the given user ID, email,
// secret, and time-to-live. The user ID is stored in the standard Subject
// claim and can be read back via Claims.Subject after validation.
func IssueAccessToken(userID, email, secret string, ttl time.Duration) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("issuing access token: userID must not be empty")
	}
	if secret == "" {
		return "", fmt.Errorf("issuing access token: secret must not be empty")
	}

	now := time.Now().UTC()
	claims := Claims{
		Email: email,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("signing access token: %w", err)
	}
	return signed, nil
}

// ValidateAccessToken parses and validates an HS256-signed JWT. It checks the
// signature and expiry. On success it returns the decoded claims.
func ValidateAccessToken(tokenString, secret string) (*Claims, error) {
	if secret == "" {
		return nil, fmt.Errorf("validating access token: secret must not be empty")
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	}, jwt.WithIssuer(tokenIssuer))
	if err != nil {
		return nil, fmt.Errorf("validating access token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, fmt.Errorf("validating access token: unexpected claims type")
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("validating access token: token has empty subject")
	}
	return claims, nil
}
