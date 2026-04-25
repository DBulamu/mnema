package domain

import (
	"crypto/sha256"
	"encoding/hex"
)

// Token is the plaintext value sent to the user (in the magic-link email,
// or returned at login). It is never stored — only HashToken(Token) is
// persisted. Treat instances as secret.
type Token string

// HashToken returns a hex-encoded sha256 of the token.
//
// Why sha256 and not bcrypt: tokens carry 256 bits of CSPRNG entropy and
// have short or moderate TTL. Bcrypt's slow KDF is for low-entropy human
// passwords; here it would only add latency without security gain.
// Hashing at all (vs. plaintext) protects the DB-dump scenario.
func HashToken(t Token) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}
