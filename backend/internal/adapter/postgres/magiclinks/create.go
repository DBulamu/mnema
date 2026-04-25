package magiclinks

import (
	"context"
	"fmt"
	"net/netip"
	"time"
)

// Create stores a hashed magic-link record and returns its ID.
//
// Primitive params instead of a struct so this method satisfies the
// usecase-side port (declared at the consumer) by structural typing —
// adapter and usecase share no Go types beyond domain primitives.
func (r *Repo) Create(
	ctx context.Context,
	email string,
	tokenHash string,
	expiresAt time.Time,
	ipAddress *netip.Addr,
) (string, error) {
	// pgx writes SQL NULL when we pass nil interface, which matches the
	// optional inet column. An empty string would fail inet validation.
	var ip any
	if ipAddress != nil && ipAddress.IsValid() {
		ip = ipAddress.String()
	}

	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO auth_magic_links (email, token_hash, expires_at, ip_address)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, email, tokenHash, expiresAt, ip).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert magic link: %w", err)
	}
	return id, nil
}
