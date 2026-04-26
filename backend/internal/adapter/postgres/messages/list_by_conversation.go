package messages

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

// ListByConversation returns messages of a conversation in chronological
// order (oldest first). The before cursor selects rows strictly older
// than (created_at, id) of the previous page — this is the "load older"
// direction used by chat UIs that prepend history above the current
// view. nil before means "newest tail".
//
// The query orders DESC at the SQL level to use the
// (conversation_id, created_at, id) index from the cursor's anchor and
// then re-sorts ASC client-side so callers always see natural reading
// order.
func (r *Repo) ListByConversation(
	ctx context.Context,
	conversationID string,
	limit int,
	before *domain.MessageCursor,
) ([]domain.Message, error) {
	const baseSelect = `SELECT id, conversation_id, role, content, created_at FROM messages`

	var (
		rows pgx.Rows
		err  error
	)
	if before == nil {
		rows, err = r.pool.Query(ctx, `
			SELECT id, conversation_id, role, content, created_at FROM (
				`+baseSelect+`
				WHERE conversation_id = $1
				ORDER BY created_at DESC, id DESC
				LIMIT $2
			) t
			ORDER BY created_at ASC, id ASC
		`, conversationID, limit)
	} else {
		rows, err = r.pool.Query(ctx, `
			SELECT id, conversation_id, role, content, created_at FROM (
				`+baseSelect+`
				WHERE conversation_id = $1
				  AND (created_at, id) < ($2, $3)
				ORDER BY created_at DESC, id DESC
				LIMIT $4
			) t
			ORDER BY created_at ASC, id ASC
		`, conversationID, before.CreatedAt, before.ID, limit)
	}
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
