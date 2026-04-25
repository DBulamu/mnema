package sessions

import (
	"context"
	"fmt"
	"time"
)

// Revoke marks a session as revoked. Idempotent — repeated calls or
// calls on already-revoked rows are no-ops because of the WHERE clause.
func (r *Repo) Revoke(ctx context.Context, id string, now time.Time) error {
	if _, err := r.pool.Exec(ctx, `
		UPDATE sessions
		SET revoked_at = $2
		WHERE id = $1 AND revoked_at IS NULL
	`, id, now); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}
