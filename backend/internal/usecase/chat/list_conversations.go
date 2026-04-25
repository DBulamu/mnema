package chat

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// conversationsLister returns the user's threads ordered freshest first.
type conversationsLister interface {
	ListByUser(ctx context.Context, userID string, limit int) ([]domain.Conversation, error)
}

// ListConversations is a read-only listing capped by maxListLimit.
type ListConversations struct {
	Conversations conversationsLister
}

// maxListLimit caps how many threads we return per request. Keeps the
// response bounded; cursor pagination is a follow-up when users
// actually accumulate many threads.
const maxListLimit = 100

// defaultListLimit is what we return when the caller doesn't ask for a
// specific number. 20 fits a typical mobile screen; the web grid can
// request more explicitly.
const defaultListLimit = 20

func (uc *ListConversations) Run(ctx context.Context, userID string, limit int) ([]domain.Conversation, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user_id is required", domain.ErrInvalidArgument)
	}
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	return uc.Conversations.ListByUser(ctx, userID, limit)
}
