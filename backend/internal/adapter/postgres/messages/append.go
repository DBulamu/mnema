package messages

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// Append inserts a message into the given conversation. The role is
// passed as a string and the Postgres enum will reject anything outside
// {user, assistant, system}, so we don't double-validate here — the DB
// is the source of truth on the enum and a violation surfaces as a
// constraint error.
func (r *Repo) Append(
	ctx context.Context,
	conversationID string,
	role domain.MessageRole,
	content string,
) (domain.Message, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO messages (conversation_id, role, content)
		VALUES ($1, $2, $3)
		RETURNING id, conversation_id, role, content, created_at
	`, conversationID, string(role), content)

	var m domain.Message
	var roleStr string
	if err := row.Scan(&m.ID, &m.ConversationID, &roleStr, &m.Content, &m.CreatedAt); err != nil {
		return domain.Message{}, fmt.Errorf("insert message: %w", err)
	}
	m.Role = domain.MessageRole(roleStr)
	return m, nil
}
