package sessions

import (
	"context"
	"fmt"
	"net/netip"
	"time"
)

// Create persists a new session row and returns its ID. Hash is computed
// by the usecase — this layer never sees plaintext tokens.
func (r *Repo) Create(
	ctx context.Context,
	userID string,
	refreshTokenHash string,
	expiresAt time.Time,
	userAgent string,
	ipAddress *netip.Addr,
) (string, error) {
	// nil interface ⇒ pgx writes SQL NULL. Empty strings would fail
	// validation on inet, and we want NULL semantics on user_agent too.
	var ua any
	if userAgent != "" {
		ua = userAgent
	}
	var ip any
	if ipAddress != nil && ipAddress.IsValid() {
		ip = ipAddress.String()
	}

	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, refresh_token_hash, expires_at, user_agent, ip_address)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, userID, refreshTokenHash, expiresAt, ua, ip).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert session: %w", err)
	}
	return id, nil
}
