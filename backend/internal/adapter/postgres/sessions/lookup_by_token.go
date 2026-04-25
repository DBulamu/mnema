package sessions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

// LookupByTokenHash returns the active session for the supplied hash, if
// it exists and is not expired or revoked. Misses collapse to
// domain.ErrSessionInvalid so callers cannot probe whether a token is
// merely expired vs. wrong vs. revoked.
//
// We pass `now` rather than relying on Postgres now() so behavior is
// deterministic in tests that pin the clock.
func (r *Repo) LookupByTokenHash(ctx context.Context, tokenHash string, now time.Time) (domain.Session, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, user_id, expires_at
		FROM sessions
		WHERE refresh_token_hash = $1
		  AND revoked_at IS NULL
		  AND expires_at > $2
	`, tokenHash, now)

	var s domain.Session
	if err := row.Scan(&s.ID, &s.UserID, &s.ExpiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Session{}, domain.ErrSessionInvalid
		}
		return domain.Session{}, fmt.Errorf("lookup session: %w", err)
	}
	return s, nil
}
