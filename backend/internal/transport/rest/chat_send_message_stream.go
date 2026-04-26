package rest

import (
	"context"
	"net/http"

	chatuc "github.com/DBulamu/mnema/backend/internal/usecase/chat"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"
)

// sendMessageStreamRunner is the consumer-side port for the SSE
// chat handler. SendMessage.RunStream satisfies it structurally.
type sendMessageStreamRunner interface {
	RunStream(ctx context.Context, in chatuc.SendMessageInput, emit func(chatuc.SendMessageStreamEvent) error) error
}

type sendMessageStreamInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Content string `json:"content" minLength:"1" maxLength:"16384"`
	}
}

// SSE event payloads. Same shape rationale as recall_stream.go:
// narrow, UI-focused.

type chatUserStoredEvent struct {
	Message messageDTO `json:"message"`
}

type chatDeltaEvent struct {
	Text string `json:"text"`
}

type chatFinalEvent struct {
	UserMessage      messageDTO `json:"user_message"`
	AssistantMessage messageDTO `json:"assistant_message"`
}

type chatErrorEvent struct {
	Message string `json:"message"`
}

// RegisterSendMessageStream wires POST /v1/conversations/{id}/messages/stream
// as an SSE endpoint. Event sequence:
//
//	user_stored — exactly once, after the user's message is committed.
//	delta       — zero or more assistant text fragments.
//	final       — exactly once, with the persisted user_message and
//	              assistant_message rows.
//	error       — at most once, terminates the stream.
//
// The non-streaming endpoint stays alive for non-browser clients —
// they get the same final shape in one round-trip.
func RegisterSendMessageStream(api huma.API, run sendMessageStreamRunner) {
	sse.Register(api, huma.Operation{
		OperationID: "conversations-send-message-stream",
		Method:      http.MethodPost,
		Path:        "/v1/conversations/{id}/messages/stream",
		Summary:     "Send a message and stream the assistant's reply.",
		Tags:        []string{"chat"},
		Security:    []map[string][]string{{BearerSecurityName: {}}},
	}, map[string]any{
		"user_stored": chatUserStoredEvent{},
		"delta":       chatDeltaEvent{},
		"final":       chatFinalEvent{},
		"error":       chatErrorEvent{},
	}, func(ctx context.Context, in *sendMessageStreamInput, send sse.Sender) {
		userID := UserIDFromContext(ctx)
		if userID == "" {
			_ = send.Data(chatErrorEvent{Message: "unauthenticated"})
			return
		}

		err := run.RunStream(ctx, chatuc.SendMessageInput{
			ConversationID: in.ID,
			UserID:         userID,
			Content:        in.Body.Content,
		}, func(ev chatuc.SendMessageStreamEvent) error {
			switch {
			case ev.UserStored != nil:
				return send.Data(chatUserStoredEvent{Message: toMessageDTO(ev.UserStored.Message)})
			case ev.Delta != nil:
				return send.Data(chatDeltaEvent{Text: ev.Delta.Text})
			case ev.Final != nil:
				return send.Data(chatFinalEvent{
					UserMessage:      toMessageDTO(ev.Final.UserMessage),
					AssistantMessage: toMessageDTO(ev.Final.AssistantMessage),
				})
			}
			return nil
		})
		if err != nil {
			_ = send.Data(chatErrorEvent{Message: err.Error()})
		}
	})
}
