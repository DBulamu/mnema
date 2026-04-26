package chat

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// conversationsLister returns the user's threads ordered freshest first.
// The after cursor selects rows strictly older than (updated_at, id) of
// the previous page; nil means "first page".
type conversationsLister interface {
	ListByUser(
		ctx context.Context,
		userID string,
		limit int,
		after *domain.ConversationCursor,
	) ([]domain.Conversation, error)
}

// ListConversations is a read-only listing capped by maxListLimit.
type ListConversations struct {
	Conversations conversationsLister
}

// maxListLimit caps how many threads we return per request. Keeps the
// response bounded and the keyset query plan stable.
const maxListLimit = 100

// defaultListLimit is what we return when the caller doesn't ask for a
// specific number. 20 fits a typical mobile screen; the web grid can
// request more explicitly.
const defaultListLimit = 20

// ListConversationsOutput carries one page plus an opaque cursor for
// the next one. NextCursor is nil when there is no further page —
// callers must treat it as opaque (the encoded format lives in the
// transport layer, not here).
type ListConversationsOutput struct {
	Items      []domain.Conversation
	NextCursor *domain.ConversationCursor
}

func (uc *ListConversations) Run(
	ctx context.Context,
	userID string,
	limit int,
	after *domain.ConversationCursor,
) (ListConversationsOutput, error) {
	if userID == "" {
		return ListConversationsOutput{}, fmt.Errorf("%w: user_id is required", domain.ErrInvalidArgument)
	}
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	// Fetch limit+1 so we can tell whether a next page exists without
	// a separate count query: if the adapter returned more than limit,
	// the extra row is the seed for NextCursor and is dropped from the
	// page.
	items, err := uc.Conversations.ListByUser(ctx, userID, limit+1, after)
	if err != nil {
		return ListConversationsOutput{}, err
	}

	out := ListConversationsOutput{Items: items}
	if len(items) > limit {
		last := items[limit-1]
		out.Items = items[:limit]
		out.NextCursor = &domain.ConversationCursor{
			UpdatedAt: last.UpdatedAt,
			ID:        last.ID,
		}
	}
	return out, nil
}
