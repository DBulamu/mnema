package jwt

import (
	"fmt"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/golang-jwt/jwt/v5"
)

// Verify parses a token and returns the subject (user ID).
//
// All failures collapse to domain.ErrTokenInvalid so callers cannot
// infer whether the cause was bad signature, expiry, or malformed
// claims. Verifier rejects non-HMAC alg headers to defend against
// alg=none and RS256-confusion attacks.
func (i *Issuer) Verify(tokenStr string) (string, error) {
	parsed, err := jwt.ParseWithClaims(
		tokenStr,
		&jwt.RegisteredClaims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return i.secret, nil
		},
		jwt.WithIssuer("mnema"),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil || !parsed.Valid {
		return "", domain.ErrTokenInvalid
	}
	claims, ok := parsed.Claims.(*jwt.RegisteredClaims)
	if !ok || claims.Subject == "" {
		return "", domain.ErrTokenInvalid
	}
	return claims.Subject, nil
}
