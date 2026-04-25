// Package jwtauth issues and verifies short-lived JWT access tokens.
//
// Refresh tokens are *opaque* random strings stored hashed in the sessions
// table — not JWTs. This split is deliberate: access tokens must be
// stateless (no DB hit per request) and short-lived; refresh tokens must
// be revocable, so they need server state. Mixing the two leads to either
// non-revocable access (bad) or per-request DB hits (slow).
package jwtauth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken is returned for any verification failure (expired,
// signature mismatch, malformed, wrong audience, etc.). We deliberately
// collapse them so callers cannot leak which check failed.
var ErrInvalidToken = errors.New("invalid token")

// Claims is the access-token payload. Subject is the user ID.
type Claims struct {
	jwt.RegisteredClaims
}

// Issuer signs and verifies access tokens. Construct one at startup with the
// shared HMAC secret and the configured TTL.
type Issuer struct {
	secret []byte
	ttl    time.Duration
}

func NewIssuer(secret string, ttl time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl}
}

// Issue produces a signed JWT for the given user ID. now is taken as a
// parameter (rather than time.Now) so tests can pin time deterministically.
func (i *Issuer) Issue(userID string, now time.Time) (string, time.Time, error) {
	expires := now.Add(i.ttl)
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expires),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "mnema",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(i.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign jwt: %w", err)
	}
	return signed, expires, nil
}

// Verify parses a token and returns its claims. Any failure — bad signature,
// expired, malformed — collapses to ErrInvalidToken so callers cannot infer
// the reason from the error text.
func (i *Issuer) Verify(tokenStr string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		// Reject any algorithm we did not pick — alg=none and RS256
		// confusion attacks both rely on the verifier being lax here.
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return i.secret, nil
	}, jwt.WithIssuer("mnema"), jwt.WithLeeway(30*time.Second))
	if err != nil || !parsed.Valid {
		return nil, ErrInvalidToken
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || claims.Subject == "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
