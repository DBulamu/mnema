package users

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// FindOrCreateByEmail returns the existing user for the email or creates
// a fresh one. The ON CONFLICT clause guarantees idempotency under
// concurrent inserts: only one row wins, the loser returns the existing
// id. The DO UPDATE on updated_at is a no-op write that lets us use
// RETURNING uniformly for both the insert and the conflict path.
func (r *Repo) FindOrCreateByEmail(ctx context.Context, email string) (domain.User, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO users (email)
		VALUES ($1)
		ON CONFLICT (email) DO UPDATE SET updated_at = users.updated_at
		RETURNING id, email, display_name, avatar_url, timezone,
		          daily_push_time::text, daily_push_enabled, created_at
	`, email)

	var u domain.User
	if err := row.Scan(
		&u.ID, &u.Email, &u.DisplayName, &u.AvatarURL, &u.Timezone,
		&u.DailyPushTime, &u.DailyPushEnabled, &u.CreatedAt,
	); err != nil {
		return domain.User{}, fmt.Errorf("upsert user: %w", err)
	}
	return u, nil
}
