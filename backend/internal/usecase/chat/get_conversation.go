package chat

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// conversationOwnerLooker fetches a conversation and verifies ownership.
// Returns ErrConversationNotFound for both "missing" and "not yours".
type conversationOwnerLooker interface {
	GetByID(ctx context.Context, id, userID string) (domain.Conversation, error)
}

// messagesLister returns messages of a conversation in chronological
// order. before==nil yields the newest tail; otherwise the page is the
// rows strictly older than (created_at, id).
type messagesLister interface {
	ListByConversation(
		ctx context.Context,
		conversationID string,
		limit int,
		before *domain.MessageCursor,
	) ([]domain.Message, error)
}

// maxMessagesLimit caps a single GET payload. Older messages are
// reachable through cursor pagination.
const maxMessagesLimit = 200

// defaultMessagesLimit is enough to render a thread on first open
// without demanding the client think about pagination.
const defaultMessagesLimit = 50

// GetConversation returns the thread metadata plus a page of messages.
// Ownership is enforced inside the lookup.
type GetConversation struct {
	Conversations conversationOwnerLooker
	Messages      messagesLister
}

// GetConversationOutput carries the requested page. NextCursor points
// to the OLDEST message in Messages (chronologically the first row);
// pass it back as `before` to load the page just before it. nil when
// there is no further history.
type GetConversationOutput struct {
	Conversation domain.Conversation
	Messages     []domain.Message
	NextCursor   *domain.MessageCursor
}

func (uc *GetConversation) Run(
	ctx context.Context,
	conversationID, userID string,
	limit int,
	before *domain.MessageCursor,
) (GetConversationOutput, error) {
	if conversationID == "" || userID == "" {
		return GetConversationOutput{}, fmt.Errorf("%w: conversation_id and user_id are required", domain.ErrInvalidArgument)
	}
	if limit <= 0 {
		limit = defaultMessagesLimit
	}
	if limit > maxMessagesLimit {
		limit = maxMessagesLimit
	}

	conv, err := uc.Conversations.GetByID(ctx, conversationID, userID)
	if err != nil {
		return GetConversationOutput{}, err
	}

	// limit+1 sentinel: the adapter returns chronological-ASC, so the
	// extra row sits at index 0 (the oldest of the over-fetched batch).
	// If present, we drop it and treat the next-oldest row as the next
	// cursor anchor.
	msgs, err := uc.Messages.ListByConversation(ctx, conversationID, limit+1, before)
	if err != nil {
		return GetConversationOutput{}, err
	}

	out := GetConversationOutput{Conversation: conv, Messages: msgs}
	if len(msgs) > limit {
		// Drop the oldest row (sentinel); the new oldest is the next
		// cursor anchor for "load older".
		out.Messages = msgs[1:]
		anchor := out.Messages[0]
		out.NextCursor = &domain.MessageCursor{
			CreatedAt: anchor.CreatedAt,
			ID:        anchor.ID,
		}
	}
	return out, nil
}
