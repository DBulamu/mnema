package conversations

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

// ListByUser returns the user's conversations, freshest first. When
// after is nil this is a plain "first page". When after points at the
// last row of the previous page, the query uses the row-comparison
// keyset on (updated_at, id) to skip ahead — equivalent to OFFSET but
// stable under inserts and indexable on (user_id, updated_at DESC, id
// DESC).
func (r *Repo) ListByUser(
	ctx context.Context,
	userID string,
	limit int,
	after *domain.ConversationCursor,
) ([]domain.Conversation, error) {
	const baseSelect = `SELECT id, user_id, title, created_at, updated_at FROM conversations`

	var (
		rows pgx.Rows
		err  error
	)
	if after == nil {
		rows, err = r.pool.Query(ctx, baseSelect+`
			WHERE user_id = $1
			ORDER BY updated_at DESC, id DESC
			LIMIT $2
		`, userID, limit)
	} else {
		rows, err = r.pool.Query(ctx, baseSelect+`
			WHERE user_id = $1
			  AND (updated_at, id) < ($2, $3)
			ORDER BY updated_at DESC, id DESC
			LIMIT $4
		`, userID, after.UpdatedAt, after.ID, limit)
	}
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
