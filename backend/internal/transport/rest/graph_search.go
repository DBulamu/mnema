package rest

import (
	"context"
	"net/http"
	"strings"

	"github.com/DBulamu/mnema/backend/internal/domain"
	graphuc "github.com/DBulamu/mnema/backend/internal/usecase/graph"
	"github.com/danielgtaylor/huma/v2"
)

// searchGraphRunner is the consumer-side port for the handler. The
// graph.Search usecase satisfies it structurally — same pattern as
// getGraphRunner.
type searchGraphRunner interface {
	Run(ctx context.Context, in graphuc.SearchInput) (graphuc.SearchOutput, error)
}

// searchGraphInput is the request shape. Mode is exposed as a string
// (rather than an enum on the wire) because huma's enum support trips
// over empty/default and we already validate inside the usecase.
type searchGraphInput struct {
	Q     string   `query:"q" required:"true" minLength:"1" maxLength:"500" doc:"Free-form search query."`
	Mode  string   `query:"mode" enum:"text,semantic" doc:"Ranking mode. text uses ILIKE+trgm, semantic uses embedding cosine distance. Default: text."`
	Type  []string `query:"type" doc:"Optional filter by node type. Comma-separated, e.g. ?type=person,event"`
	Limit int      `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type searchGraphOutput struct {
	Body struct {
		Nodes []nodeDTO `json:"nodes"`
	}
}

// RegisterSearchGraph wires GET /v1/graph/search. Returns a ranked list
// of the caller's nodes. Edges are intentionally not returned: search is
// a "find this thing" surface, not a graph window — the client follows
// up with /v1/graph if it wants the surrounding structure.
func RegisterSearchGraph(api huma.API, run searchGraphRunner) {
	huma.Register(api, huma.Operation{
		OperationID: "graph-search",
		Method:      http.MethodGet,
		Path:        "/v1/graph/search",
		Summary:     "Search the caller's graph by text or semantic similarity",
		Tags:        []string{"graph"},
		Security:    []map[string][]string{{BearerSecurityName: {}}},
	}, func(ctx context.Context, in *searchGraphInput) (*searchGraphOutput, error) {
		userID := UserIDFromContext(ctx)
		if userID == "" {
			return nil, toHTTP(errUnauthenticated)
		}

		types := make([]domain.NodeType, 0, len(in.Type))
		for _, raw := range in.Type {
			for _, part := range strings.Split(raw, ",") {
				t := strings.TrimSpace(part)
				if t == "" {
					continue
				}
				types = append(types, domain.NodeType(t))
			}
		}

		res, err := run.Run(ctx, graphuc.SearchInput{
			UserID: userID,
			Query:  in.Q,
			Mode:   graphuc.SearchMode(in.Mode),
			Types:  types,
			Limit:  in.Limit,
		})
		if err != nil {
			return nil, toHTTP(err)
		}

		out := &searchGraphOutput{}
		out.Body.Nodes = make([]nodeDTO, 0, len(res.Nodes))
		for _, n := range res.Nodes {
			out.Body.Nodes = append(out.Body.Nodes, toNodeDTO(n))
		}
		return out, nil
	})
}
