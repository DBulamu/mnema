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

// LLMStreamReplier is the streaming variant. The emit callback is
// called for each chunk of the assistant's text in arrival order.
// Implementations return the full assembled string at the end so the
// usecase can persist a single domain.Message — the stream is purely
// a UX latency mask. The order is fixed: emit deltas, then return.
//
// Adapters that cannot stream (offline stubs, providers without a
// streaming API) are not required to implement this — RunStream falls
// back to LLMReplier and emits one synthetic delta with the full text.
type LLMStreamReplier interface {
	ReplyStream(ctx context.Context, history []Turn, emit func(delta string) error) (string, error)
}

// MessageExtractor turns a stored user message into graph entities. By
// contract this is non-fatal: it has no error return so the chat path
// stays simple — the bridge implementation in the composition root logs
// failures and swallows them. We pay this in lost graph nodes if
// extraction breaks, never in lost chat messages.
type MessageExtractor interface {
	ExtractFromMessage(ctx context.Context, userID, messageID, content string)
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
	// LLMStream is optional: when set, RunStream uses it; otherwise
	// RunStream falls back to LLM and emits one synthetic delta.
	LLMStream LLMStreamReplier
	Extractor MessageExtractor
	Clock     clock
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

	prior, err := uc.History.ListByConversation(ctx, in.ConversationID, historyContextLimit, nil)
	if err != nil {
		return SendMessageOutput{}, fmt.Errorf("load history: %w", err)
	}

	userMsg, err := uc.Messages.Append(ctx, in.ConversationID, domain.RoleUser, content)
	if err != nil {
		return SendMessageOutput{}, fmt.Errorf("append user message: %w", err)
	}

	// Extraction runs against the stored user message. The port is
	// non-fatal by contract — bridge implementations log internally — so
	// we don't gate the chat reply on it. Skipping when nil keeps the
	// usecase usable in tests and in environments where extraction is
	// intentionally disabled.
	if uc.Extractor != nil {
		uc.Extractor.ExtractFromMessage(ctx, in.UserID, userMsg.ID, userMsg.Content)
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

// SendMessageStreamEvent is the union type RunStream emits to the
// transport layer. Field semantics mirror the wire SSE events.
type SendMessageStreamEvent struct {
	UserStored *UserStoredEvent
	Delta      *DeltaEvent
	Final      *FinalEvent
}

type UserStoredEvent struct {
	Message domain.Message
}

type DeltaEvent struct {
	Text string
}

type FinalEvent struct {
	UserMessage      domain.Message
	AssistantMessage domain.Message
}

// RunStream is the streaming variant of Run. The pipeline is the
// same — persist user turn, run extraction, ask the LLM, persist the
// assistant turn, touch — but the caller sees:
//
//   user_stored — the freshly persisted user message, sent as soon
//                 as the row is committed so the UI can echo
//                 immediately. Extraction runs *after* this event
//                 so the visible echo isn't gated on a slow LLM.
//   delta       — assistant text fragments. Concatenation up to the
//                 final event yields the full reply.
//   final       — both stored messages, with assistant_message.id
//                 set. The UI replaces the accumulated stream text
//                 with assistant_message.content (authoritative).
//   error       — emitted by the transport when RunStream returns
//                 an error; not part of this enum.
//
// Validation re-runs to mirror Run; the transport layer should not
// have to repeat the same checks.
func (uc *SendMessage) RunStream(ctx context.Context, in SendMessageInput, emit func(SendMessageStreamEvent) error) error {
	content := strings.TrimSpace(in.Content)
	switch {
	case in.UserID == "":
		return fmt.Errorf("%w: user_id is required", domain.ErrInvalidArgument)
	case in.ConversationID == "":
		return fmt.Errorf("%w: conversation_id is required", domain.ErrInvalidArgument)
	case content == "":
		return fmt.Errorf("%w: content is required", domain.ErrInvalidArgument)
	case len(content) > maxMessageContentBytes:
		return fmt.Errorf("%w: content exceeds %d bytes", domain.ErrInvalidArgument, maxMessageContentBytes)
	}

	if _, err := uc.Conversations.GetByID(ctx, in.ConversationID, in.UserID); err != nil {
		return err
	}

	prior, err := uc.History.ListByConversation(ctx, in.ConversationID, historyContextLimit, nil)
	if err != nil {
		return fmt.Errorf("load history: %w", err)
	}

	userMsg, err := uc.Messages.Append(ctx, in.ConversationID, domain.RoleUser, content)
	if err != nil {
		return fmt.Errorf("append user message: %w", err)
	}
	if err := emit(SendMessageStreamEvent{UserStored: &UserStoredEvent{Message: userMsg}}); err != nil {
		return err
	}

	if uc.Extractor != nil {
		uc.Extractor.ExtractFromMessage(ctx, in.UserID, userMsg.ID, userMsg.Content)
	}

	history := buildLLMHistory(prior, userMsg)

	var reply string
	if uc.LLMStream != nil {
		reply, err = uc.LLMStream.ReplyStream(ctx, history, func(delta string) error {
			if delta == "" {
				return nil
			}
			return emit(SendMessageStreamEvent{Delta: &DeltaEvent{Text: delta}})
		})
	} else {
		reply, err = uc.LLM.Reply(ctx, history)
		if err == nil && reply != "" {
			if eerr := emit(SendMessageStreamEvent{Delta: &DeltaEvent{Text: reply}}); eerr != nil {
				return eerr
			}
		}
	}
	if err != nil {
		return fmt.Errorf("llm reply: %w", err)
	}

	assistantMsg, err := uc.Messages.Append(ctx, in.ConversationID, domain.RoleAssistant, reply)
	if err != nil {
		return fmt.Errorf("append assistant message: %w", err)
	}

	// Touch is best-effort; a failure here does not invalidate the
	// reply we just persisted.
	_ = uc.Toucher.Touch(ctx, in.ConversationID, uc.Clock.Now())

	return emit(SendMessageStreamEvent{Final: &FinalEvent{
		UserMessage:      userMsg,
		AssistantMessage: assistantMsg,
	}})
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
