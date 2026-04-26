package rest

import (
	"context"
	"net/http"

	"github.com/DBulamu/mnema/backend/internal/domain"
	chatuc "github.com/DBulamu/mnema/backend/internal/usecase/chat"
	"github.com/danielgtaylor/huma/v2"
)

type getConversationRunner interface {
	Run(
		ctx context.Context,
		conversationID, userID string,
		limit int,
		before *domain.MessageCursor,
	) (chatuc.GetConversationOutput, error)
}

type getConversationInput struct {
	ID     string `path:"id" format:"uuid"`
	Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Cursor string `query:"cursor"`
}

type getConversationOutput struct {
	Body struct {
		Conversation conversationDTO `json:"conversation"`
		Messages     []messageDTO    `json:"messages"`
		NextCursor   string          `json:"next_cursor,omitempty"`
	}
}

// RegisterGetConversation wires GET /v1/conversations/{id}. Returns
// the thread plus a page of messages in chronological order. Pass the
// previous response's next_cursor to load older history (the cursor
// always anchors at the OLDEST message currently shown).
func RegisterGetConversation(api huma.API, run getConversationRunner) {
	huma.Register(api, huma.Operation{
		OperationID: "conversations-get",
		Method:      http.MethodGet,
		Path:        "/v1/conversations/{id}",
		Summary:     "Get a chat thread with a page of messages",
		Tags:        []string{"chat"},
		Security:    []map[string][]string{{BearerSecurityName: {}}},
	}, func(ctx context.Context, in *getConversationInput) (*getConversationOutput, error) {
		userID := UserIDFromContext(ctx)
		if userID == "" {
			return nil, toHTTP(errUnauthenticated)
		}

		before, err := decodeMessageCursor(in.Cursor)
		if err != nil {
			return nil, toHTTP(err)
		}

		res, err := run.Run(ctx, in.ID, userID, in.Limit, before)
		if err != nil {
			return nil, toHTTP(err)
		}

		out := &getConversationOutput{}
		out.Body.Conversation = toConversationDTO(res.Conversation)
		out.Body.Messages = make([]messageDTO, 0, len(res.Messages))
		for _, m := range res.Messages {
			out.Body.Messages = append(out.Body.Messages, toMessageDTO(m))
		}
		out.Body.NextCursor = encodeMessageCursor(res.NextCursor)
		return out, nil
	})
}
