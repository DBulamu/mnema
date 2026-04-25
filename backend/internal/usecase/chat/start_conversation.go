// Package chat holds conversation-centric usecases: starting a thread,
// listing/reading threads, and the main "send a message" flow that
// drives the LLM round-trip.
package chat

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// conversationCreator is the minimum the start usecase needs. Declared
// at the consumer side per the project's ISP convention.
type conversationCreator interface {
	Create(ctx context.Context, userID string) (domain.Conversation, error)
}

// StartConversation opens a fresh thread owned by the user. Empty
// title, no messages — the first POST to /messages will populate it.
type StartConversation struct {
	Conversations conversationCreator
}

func (uc *StartConversation) Run(ctx context.Context, userID string) (domain.Conversation, error) {
	if userID == "" {
		return domain.Conversation{}, fmt.Errorf("%w: user_id is required", domain.ErrInvalidArgument)
	}
	return uc.Conversations.Create(ctx, userID)
}
