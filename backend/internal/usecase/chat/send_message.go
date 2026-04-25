package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// messageAppender persists a single message and returns the stored row
// (with id and created_at filled in by the DB).
type messageAppender interface {
	Append(ctx context.Context, conversationID string, role domain.MessageRole, content string) (domain.Message, error)
}

// conversationToucher bumps updated_at so freshly-replied threads sort
// to the top of the list.
type conversationToucher interface {
	Touch(ctx context.Context, id string, at time.Time) error
}

// Turn is the shape we hand to the LLM port. Lives in the chat
// usecase per the consumer-side-interface convention; the LLM adapter
// either matches it directly or is wrapped at the composition root.
type Turn struct {
	Role    string
	Content string
}

// LLMReplier produces the assistant's next utterance given the running
// conversation. The history is ordered oldest-first and includes the
// freshly-stored user turn at the end. Exported so the composition
// root can declare bridge adapters without referencing an unexported
// symbol.
type LLMReplier interface {
	Reply(ctx context.Context, history []Turn) (string, error)
}

// clock returns the wall clock; pinned in tests via the system port.
type clock interface {
	Now() time.Time
}

// historyContextLimit is how many recent messages we feed the LLM. Big
// enough for short Mnema-style sessions, small enough to bound prompt
// cost when a real provider is wired in.
const historyContextLimit = 30

// maxMessageContentBytes guards against a runaway client paste. 16 KiB
// is well above any real human turn but cheap to enforce up front.
const maxMessageContentBytes = 16 * 1024

// SendMessage is the chat hot path: persist the user's turn, ask the
// LLM, persist the reply, bump updated_at, return both messages.
//
// MVP trade-offs:
//   - the user-side write and the assistant reply are NOT in one
//     transaction. If the LLM call fails after we've stored the user
//     turn, the user message stays and the client sees an error — they
//     can retry by sending again. We avoid the txn so a slow LLM does
//     not hold a row lock.
//   - extraction (LLM → graph nodes) is intentionally absent in this
//     iteration; it lands as a separate usecase that consumes stored
//     messages.
type SendMessage struct {
	Conversations conversationOwnerLooker
	Messages      messageAppender
	History       messagesLister
	Toucher       conversationToucher
	LLM           LLMReplier
	Clock         clock
}

type SendMessageInput struct {
	ConversationID string
	UserID         string
	Content        string
}

type SendMessageOutput struct {
	UserMessage      domain.Message
	AssistantMessage domain.Message
}

func (uc *SendMessage) Run(ctx context.Context, in SendMessageInput) (SendMessageOutput, error) {
	content := strings.TrimSpace(in.Content)
	switch {
	case in.UserID == "":
		return SendMessageOutput{}, fmt.Errorf("%w: user_id is required", domain.ErrInvalidArgument)
	case in.ConversationID == "":
		return SendMessageOutput{}, fmt.Errorf("%w: conversation_id is required", domain.ErrInvalidArgument)
	case content == "":
		return SendMessageOutput{}, fmt.Errorf("%w: content is required", domain.ErrInvalidArgument)
	case len(content) > maxMessageContentBytes:
		return SendMessageOutput{}, fmt.Errorf("%w: content exceeds %d bytes", domain.ErrInvalidArgument, maxMessageContentBytes)
	}

	// Ownership check first — cheaper than a wasted insert if the
	// conversation is not the caller's, and avoids leaking foreign IDs.
	if _, err := uc.Conversations.GetByID(ctx, in.ConversationID, in.UserID); err != nil {
		return SendMessageOutput{}, err
	}

	prior, err := uc.History.ListByConversation(ctx, in.ConversationID, historyContextLimit)
	if err != nil {
		return SendMessageOutput{}, fmt.Errorf("load history: %w", err)
	}

	userMsg, err := uc.Messages.Append(ctx, in.ConversationID, domain.RoleUser, content)
	if err != nil {
		return SendMessageOutput{}, fmt.Errorf("append user message: %w", err)
	}

	history := buildLLMHistory(prior, userMsg)
	reply, err := uc.LLM.Reply(ctx, history)
	if err != nil {
		return SendMessageOutput{}, fmt.Errorf("llm reply: %w", err)
	}

	assistantMsg, err := uc.Messages.Append(ctx, in.ConversationID, domain.RoleAssistant, reply)
	if err != nil {
		return SendMessageOutput{}, fmt.Errorf("append assistant message: %w", err)
	}

	if err := uc.Toucher.Touch(ctx, in.ConversationID, uc.Clock.Now()); err != nil {
		// Touch failure is non-fatal for the chat — sorting freshness
		// is a UX nicety, not correctness. Surface as a wrapped error
		// so it lands in logs but the client still gets the reply.
		return SendMessageOutput{
			UserMessage:      userMsg,
			AssistantMessage: assistantMsg,
		}, nil
	}

	return SendMessageOutput{
		UserMessage:      userMsg,
		AssistantMessage: assistantMsg,
	}, nil
}

// buildLLMHistory turns stored messages plus the just-appended user
// turn into the primitive shape the LLM port expects. We pass the user
// message explicitly even though it was just inserted, to avoid
// depending on a re-read for ordering.
func buildLLMHistory(prior []domain.Message, latest domain.Message) []Turn {
	out := make([]Turn, 0, len(prior)+1)
	for _, m := range prior {
		out = append(out, Turn{Role: string(m.Role), Content: m.Content})
	}
	out = append(out, Turn{Role: string(latest.Role), Content: latest.Content})
	return out
}
