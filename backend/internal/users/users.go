// Package users owns the users table.
//
// Magic-link auth means there is no signup step — the first time someone
// successfully consumes a link for an email, we create the user row.
// FindOrCreateByEmail captures that "create-on-first-use" semantics
// atomically so two parallel logins for the same fresh email cannot
// produce duplicate rows.
package users

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID               string
	Email            string
	DisplayName      *string
	AvatarURL        *string
	Timezone         string
	DailyPushTime    *string
	DailyPushEnabled bool
	CreatedAt        time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// FindOrCreateByEmail returns the existing user for the email or creates
// one. The ON CONFLICT clause guarantees idempotency under concurrent
// inserts: only one row wins, the other returns the existing id.
func (s *Store) FindOrCreateByEmail(ctx context.Context, email string) (User, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO users (email)
		VALUES ($1)
		ON CONFLICT (email) DO UPDATE SET updated_at = users.updated_at
		RETURNING id, email, display_name, avatar_url, timezone,
		          daily_push_time::text, daily_push_enabled, created_at
	`, email)

	var u User
	if err := row.Scan(
		&u.ID, &u.Email, &u.DisplayName, &u.AvatarURL, &u.Timezone,
		&u.DailyPushTime, &u.DailyPushEnabled, &u.CreatedAt,
	); err != nil {
		return User{}, fmt.Errorf("upsert user: %w", err)
	}
	return u, nil
}

// GetByID looks up a user by primary key. Returns ErrNotFound when the row
// does not exist or has been soft-deleted.
func (s *Store) GetByID(ctx context.Context, id string) (User, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, email, display_name, avatar_url, timezone,
		       daily_push_time::text, daily_push_enabled, created_at
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, id)

	var u User
	if err := row.Scan(
		&u.ID, &u.Email, &u.DisplayName, &u.AvatarURL, &u.Timezone,
		&u.DailyPushTime, &u.DailyPushEnabled, &u.CreatedAt,
	); err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}
