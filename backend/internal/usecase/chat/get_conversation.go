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

// messagesLister returns recent messages of a conversation in
// chronological order.
type messagesLister interface {
	ListByConversation(ctx context.Context, conversationID string, limit int) ([]domain.Message, error)
}

// maxMessagesLimit caps a single GET payload. Older messages will be
// reachable through cursor pagination once that lands.
const maxMessagesLimit = 200

// defaultMessagesLimit is enough to render a thread on first open
// without demanding the client think about pagination.
const defaultMessagesLimit = 50

// GetConversation returns the thread metadata plus its tail of
// messages. Ownership is enforced inside the lookup.
type GetConversation struct {
	Conversations conversationOwnerLooker
	Messages      messagesLister
}

type GetConversationOutput struct {
	Conversation domain.Conversation
	Messages     []domain.Message
}

func (uc *GetConversation) Run(
	ctx context.Context,
	conversationID, userID string,
	limit int,
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
	msgs, err := uc.Messages.ListByConversation(ctx, conversationID, limit)
	if err != nil {
		return GetConversationOutput{}, err
	}
	return GetConversationOutput{Conversation: conv, Messages: msgs}, nil
}
