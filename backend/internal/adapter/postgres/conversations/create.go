package conversations

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// Create inserts a fresh conversation row for the given user. Title is
// always NULL on creation in MVP — auto-titling is a later concern.
func (r *Repo) Create(ctx context.Context, userID string) (domain.Conversation, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO conversations (user_id)
		VALUES ($1)
		RETURNING id, user_id, title, created_at, updated_at
	`, userID)

	var c domain.Conversation
	if err := row.Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return domain.Conversation{}, fmt.Errorf("insert conversation: %w", err)
	}
	return c, nil
}
