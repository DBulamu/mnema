package system

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// 32 bytes = 256 bits of CSPRNG entropy → base64url ≈ 43 chars.
const tokenBytes = 32

// TokenGenerator yields opaque random tokens for magic-link and refresh
// tokens. Bytes from crypto/rand; base64url so the value is URL-safe
// without escaping (magic links go straight into email href).
type TokenGenerator struct{}

func (TokenGenerator) NewToken() (domain.Token, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return domain.Token(base64.RawURLEncoding.EncodeToString(b)), nil
}
