package rest

import (
	"context"
	"net/http"

	"github.com/DBulamu/mnema/backend/internal/domain"
	chatuc "github.com/DBulamu/mnema/backend/internal/usecase/chat"
	"github.com/danielgtaylor/huma/v2"
)

type listConversationsRunner interface {
	Run(
		ctx context.Context,
		userID string,
		limit int,
		after *domain.ConversationCursor,
	) (chatuc.ListConversationsOutput, error)
}

type listConversationsInput struct {
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"20"`
	Cursor string `query:"cursor"`
}

type listConversationsOutput struct {
	Body struct {
		Items      []conversationDTO `json:"items"`
		NextCursor string            `json:"next_cursor,omitempty"`
	}
}

// RegisterListConversations wires GET /v1/conversations. Returns the
// freshest page; pass the previous response's next_cursor to load
// older threads. An empty next_cursor means "no more pages".
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

		after, err := decodeConversationCursor(in.Cursor)
		if err != nil {
			return nil, toHTTP(err)
		}

		page, err := run.Run(ctx, userID, in.Limit, after)
		if err != nil {
			return nil, toHTTP(err)
		}

		out := &listConversationsOutput{}
		out.Body.Items = make([]conversationDTO, 0, len(page.Items))
		for _, c := range page.Items {
			out.Body.Items = append(out.Body.Items, toConversationDTO(c))
		}
		out.Body.NextCursor = encodeConversationCursor(page.NextCursor)
		return out, nil
	})
}
