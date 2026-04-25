package conversations

import (
	"context"
	"errors"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

// GetByID returns the conversation with the given id, but only if it
// belongs to userID. Foreign threads collapse to ErrConversationNotFound
// rather than a 403 to avoid leaking the existence of others' rows.
func (r *Repo) GetByID(ctx context.Context, id, userID string) (domain.Conversation, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, user_id, title, created_at, updated_at
		FROM conversations
		WHERE id = $1 AND user_id = $2
	`, id, userID)

	var c domain.Conversation
	if err := row.Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Conversation{}, domain.ErrConversationNotFound
		}
		return domain.Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	return c, nil
}
