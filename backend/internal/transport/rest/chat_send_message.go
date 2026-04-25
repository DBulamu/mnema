package rest

import (
	"context"
	"net/http"

	chatuc "github.com/DBulamu/mnema/backend/internal/usecase/chat"
	"github.com/danielgtaylor/huma/v2"
)

type sendMessageRunner interface {
	Run(ctx context.Context, in chatuc.SendMessageInput) (chatuc.SendMessageOutput, error)
}

type sendMessageInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Content string `json:"content" minLength:"1" maxLength:"16384"`
	}
}

type sendMessageOutput struct {
	Body struct {
		UserMessage      messageDTO `json:"user_message"`
		AssistantMessage messageDTO `json:"assistant_message"`
	}
}

// RegisterSendMessage wires POST /v1/conversations/{id}/messages. The
// hot path of the chat: persist user turn → ask LLM → persist
// assistant turn → return both.
func RegisterSendMessage(api huma.API, run sendMessageRunner) {
	huma.Register(api, huma.Operation{
		OperationID: "conversations-send-message",
		Method:      http.MethodPost,
		Path:        "/v1/conversations/{id}/messages",
		Summary:     "Send a message and receive the assistant's reply",
		Tags:        []string{"chat"},
		Security:    []map[string][]string{{BearerSecurityName: {}}},
	}, func(ctx context.Context, in *sendMessageInput) (*sendMessageOutput, error) {
		userID := UserIDFromContext(ctx)
		if userID == "" {
			return nil, toHTTP(errUnauthenticated)
		}

		res, err := run.Run(ctx, chatuc.SendMessageInput{
			ConversationID: in.ID,
			UserID:         userID,
			Content:        in.Body.Content,
		})
		if err != nil {
			return nil, toHTTP(err)
		}

		out := &sendMessageOutput{}
		out.Body.UserMessage = toMessageDTO(res.UserMessage)
		out.Body.AssistantMessage = toMessageDTO(res.AssistantMessage)
		return out, nil
	})
}
