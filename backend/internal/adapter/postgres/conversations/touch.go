package conversations

import (
	"context"
	"fmt"
	"time"
)

// Touch sets updated_at on the conversation. Called by the chat usecase
// after appending a message so that ListByUser sorts the thread to the
// top. We pass an explicit time rather than now() so that the Clock
// port stays the single source of truth in tests.
func (r *Repo) Touch(ctx context.Context, id string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE conversations
		SET updated_at = $2
		WHERE id = $1
	`, id, at)
	if err != nil {
		return fmt.Errorf("touch conversation: %w", err)
	}
	return nil
}
