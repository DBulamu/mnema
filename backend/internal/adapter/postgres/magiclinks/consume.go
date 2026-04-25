package magiclinks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

// Consume atomically validates and marks a magic link as used.
//
// Atomicity matters: two parallel consumes of the same token must not
// both succeed. We use a single UPDATE...WHERE consumed_at IS NULL
// AND expires_at > now ... RETURNING — Postgres takes a row lock for
// the UPDATE, so only one writer wins. No explicit transaction needed.
//
// We pass `now` instead of using Postgres now() to keep the behavior
// deterministic in tests that pin the clock.
func (r *Repo) Consume(ctx context.Context, tokenHash string, now time.Time) (domain.MagicLink, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE auth_magic_links
		SET consumed_at = $2
		WHERE token_hash = $1
		  AND consumed_at IS NULL
		  AND expires_at > $2
		RETURNING id, email
	`, tokenHash, now)

	var link domain.MagicLink
	if err := row.Scan(&link.ID, &link.Email); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.MagicLink{}, domain.ErrLinkInvalid
		}
		return domain.MagicLink{}, fmt.Errorf("consume magic link: %w", err)
	}
	return link, nil
}
