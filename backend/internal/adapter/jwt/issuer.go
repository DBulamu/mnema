// Package jwt is the production adapter for the access-token issuer port.
//
// HMAC-SHA256 with a shared secret. We use golang-jwt v5; verifying code
// rejects any non-HMAC alg in the header to defend against alg=none and
// RS256-confusion attacks.
package jwt

import "time"

// Issuer signs and verifies access tokens. Construct one at startup with
// the HMAC secret and the access-token TTL.
type Issuer struct {
	secret []byte
	ttl    time.Duration
}

func NewIssuer(secret string, ttl time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl}
}
