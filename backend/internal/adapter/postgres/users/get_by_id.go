package users

import (
	"context"
	"errors"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

// GetByID looks up a user by primary key. Filters out soft-deleted rows
// and translates pgx.ErrNoRows into domain.ErrUserNotFound so callers
// can rely on errors.Is rather than driver-specific sentinels.
func (r *Repo) GetByID(ctx context.Context, id string) (domain.User, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, email, display_name, avatar_url, timezone,
		       daily_push_time::text, daily_push_enabled, created_at
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, id)

	var u domain.User
	if err := row.Scan(
		&u.ID, &u.Email, &u.DisplayName, &u.AvatarURL, &u.Timezone,
		&u.DailyPushTime, &u.DailyPushEnabled, &u.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, domain.ErrUserNotFound
		}
		return domain.User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}
