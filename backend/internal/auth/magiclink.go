// Package auth implements passwordless authentication via magic links.
//
// Threat model: a magic link is bearer-equivalent for ~15 minutes. If an
// attacker reads the email, they sign in. We mitigate by (a) keeping the
// TTL short, (b) making the token single-use (consumed_at set on first
// use), and (c) never storing the token itself — only sha256(token).
//
// Why sha256 and not bcrypt: tokens are 256 bits of cryptographically random
// entropy with a 15-minute lifetime. Bcrypt is for low-entropy human-chosen
// passwords; here it would only add latency without security gain. Hashing
// at all (vs. plaintext) protects the DB-dump scenario.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// 32 bytes = 256 bits of entropy → base64url ≈ 43 chars in the URL.
	tokenBytes    = 32
	defaultExpiry = 15 * time.Minute
)

// Token is the random secret sent in the email. It is never stored.
// Only HashToken(token) is persisted.
type Token string

func GenerateToken() (Token, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return Token(base64.RawURLEncoding.EncodeToString(b)), nil
}

func HashToken(t Token) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

type MagicLinkStore struct {
	pool *pgxpool.Pool
}

func NewMagicLinkStore(pool *pgxpool.Pool) *MagicLinkStore {
	return &MagicLinkStore{pool: pool}
}

type IssueArgs struct {
	Email     string
	IPAddress *netip.Addr
	Now       time.Time
	TTL       time.Duration
}

type IssuedLink struct {
	ID        string
	Token     Token
	ExpiresAt time.Time
}

// Issue generates a fresh single-use token and stores its hash. The plaintext
// token is returned exactly once — the caller emails it to the user and
// then drops it; we have no way to recover it later. Now/TTL are taken
// from args to keep this function deterministic in tests.
func (s *MagicLinkStore) Issue(ctx context.Context, args IssueArgs) (IssuedLink, error) {
	if args.TTL == 0 {
		args.TTL = defaultExpiry
	}
	if args.Now.IsZero() {
		args.Now = time.Now().UTC()
	}

	token, err := GenerateToken()
	if err != nil {
		return IssuedLink{}, err
	}
	hash := HashToken(token)
	expires := args.Now.Add(args.TTL)

	// Pass nil instead of an empty string when we don't have an IP — pgx
	// then writes SQL NULL into the inet column. An empty string would
	// fail validation.
	var ip any
	if args.IPAddress != nil && args.IPAddress.IsValid() {
		ip = args.IPAddress.String()
	}

	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO auth_magic_links (email, token_hash, expires_at, ip_address)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, args.Email, hash, expires, ip).Scan(&id)
	if err != nil {
		return IssuedLink{}, fmt.Errorf("insert magic link: %w", err)
	}

	return IssuedLink{
		ID:        id,
		Token:     token,
		ExpiresAt: expires,
	}, nil
}
