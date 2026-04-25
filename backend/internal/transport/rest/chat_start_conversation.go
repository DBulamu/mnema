package rest

import (
	"context"
	"net/http"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/danielgtaylor/huma/v2"
)

type startConversationRunner interface {
	Run(ctx context.Context, userID string) (domain.Conversation, error)
}

type startConversationOutput struct {
	Body struct {
		Conversation conversationDTO `json:"conversation"`
	}
}

// RegisterStartConversation wires POST /v1/conversations. Creates an
// empty thread owned by the caller; first message lands via
// POST /v1/conversations/{id}/messages.
func RegisterStartConversation(api huma.API, run startConversationRunner) {
	huma.Register(api, huma.Operation{
		OperationID:   "conversations-start",
		Method:        http.MethodPost,
		Path:          "/v1/conversations",
		Summary:       "Open a new chat thread",
		Tags:          []string{"chat"},
		Security:      []map[string][]string{{BearerSecurityName: {}}},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, _ *struct{}) (*startConversationOutput, error) {
		userID := UserIDFromContext(ctx)
		if userID == "" {
			return nil, toHTTP(errUnauthenticated)
		}

		conv, err := run.Run(ctx, userID)
		if err != nil {
			return nil, toHTTP(err)
		}

		out := &startConversationOutput{}
		out.Body.Conversation = toConversationDTO(conv)
		return out, nil
	})
}
