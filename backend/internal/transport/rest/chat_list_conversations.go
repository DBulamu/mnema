package rest

import (
	"context"
	"net/http"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/danielgtaylor/huma/v2"
)

type listConversationsRunner interface {
	Run(ctx context.Context, userID string, limit int) ([]domain.Conversation, error)
}

type listConversationsInput struct {
	Limit int `query:"limit" minimum:"1" maximum:"100" default:"20"`
}

type listConversationsOutput struct {
	Body struct {
		Items []conversationDTO `json:"items"`
	}
}

// RegisterListConversations wires GET /v1/conversations. MVP returns
// the freshest N threads; cursor pagination is a follow-up.
func RegisterListConversations(api huma.API, run listConversationsRunner) {
	huma.Register(api, huma.Operation{
		OperationID: "conversations-list",
		Method:      http.MethodGet,
		Path:        "/v1/conversations",
		Summary:     "List the caller's chat threads",
		Tags:        []string{"chat"},
		Security:    []map[string][]string{{BearerSecurityName: {}}},
	}, func(ctx context.Context, in *listConversationsInput) (*listConversationsOutput, error) {
		userID := UserIDFromContext(ctx)
		if userID == "" {
			return nil, toHTTP(errUnauthenticated)
		}

		items, err := run.Run(ctx, userID, in.Limit)
		if err != nil {
			return nil, toHTTP(err)
		}

		out := &listConversationsOutput{}
		out.Body.Items = make([]conversationDTO, 0, len(items))
		for _, c := range items {
			out.Body.Items = append(out.Body.Items, toConversationDTO(c))
		}
		return out, nil
	})
}
