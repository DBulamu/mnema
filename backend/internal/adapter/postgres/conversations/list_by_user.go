package conversations

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// ListByUser returns the user's conversations, freshest first. MVP uses
// a plain LIMIT — the (user_id, updated_at DESC) index keeps this cheap
// up to thousands of rows. Cursor pagination is left for when we need
// to page beyond a single user's recent activity.
func (r *Repo) ListByUser(ctx context.Context, userID string, limit int) ([]domain.Conversation, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, title, created_at, updated_at
		FROM conversations
		WHERE user_id = $1
		ORDER BY updated_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Conversation, 0, limit)
	for rows.Next() {
		var c domain.Conversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conversations: %w", err)
	}
	return out, nil
}
