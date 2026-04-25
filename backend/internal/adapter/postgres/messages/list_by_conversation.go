package messages

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// ListByConversation returns the most recent messages of a conversation
// in chronological order (oldest first), capped at limit. The query
// fetches the tail by created_at DESC then re-orders client-side so
// callers always see "natural reading order" without paying for a sort.
func (r *Repo) ListByConversation(
	ctx context.Context,
	conversationID string,
	limit int,
) ([]domain.Message, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, conversation_id, role, content, created_at
		FROM (
			SELECT id, conversation_id, role, content, created_at
			FROM messages
			WHERE conversation_id = $1
			ORDER BY created_at DESC
			LIMIT $2
		) t
		ORDER BY created_at ASC
	`, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Message, 0, limit)
	for rows.Next() {
		var m domain.Message
		var roleStr string
		if err := rows.Scan(&m.ID, &m.ConversationID, &roleStr, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.Role = domain.MessageRole(roleStr)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	return out, nil
}
