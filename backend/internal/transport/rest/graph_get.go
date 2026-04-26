package rest

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
	graphuc "github.com/DBulamu/mnema/backend/internal/usecase/graph"
	"github.com/danielgtaylor/huma/v2"
)

// getGraphRunner is the consumer-side port for the handler. The graph
// usecase satisfies it structurally — declared here so the handler does
// not import the concrete usecase type beyond its input/output shapes.
type getGraphRunner interface {
	Run(ctx context.Context, in graphuc.GetGraphInput) (graphuc.GetGraphOutput, error)
}

// getGraphInput is the request shape. Type accepts comma-separated
// values (?type=person,event) — this is what huma's []string query
// binding produces. We also split each entry on commas defensively in
// case a future huma version starts honoring repeated keys; today the
// repeat form picks up only the first value.
//
// Since is parsed as RFC3339; an empty string means "no lower bound".
type getGraphInput struct {
	Type  []string `query:"type" doc:"Filter by node type. Comma-separated, e.g. ?type=person,event"`
	Since string   `query:"since" doc:"RFC3339 lower bound on created_at." format:"date-time"`
	Limit int      `query:"limit" minimum:"1" maximum:"1000" default:"200"`
}

type getGraphOutput struct {
	Body struct {
		Nodes []nodeDTO `json:"nodes"`
		Edges []edgeDTO `json:"edges"`
	}
}

// RegisterGetGraph wires GET /v1/graph. Returns the user's nodes (with
// optional type/since filters) and the edges that fully connect inside
// that node window.
func RegisterGetGraph(api huma.API, run getGraphRunner) {
	huma.Register(api, huma.Operation{
		OperationID: "graph-get",
		Method:      http.MethodGet,
		Path:        "/v1/graph",
		Summary:     "Read the caller's graph window",
		Tags:        []string{"graph"},
		Security:    []map[string][]string{{BearerSecurityName: {}}},
	}, func(ctx context.Context, in *getGraphInput) (*getGraphOutput, error) {
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

		var sincePtr *time.Time
		if in.Since != "" {
			parsed, err := time.Parse(time.RFC3339, in.Since)
			if err != nil {
				return nil, huma.Error400BadRequest("invalid since: must be RFC3339")
			}
			sincePtr = &parsed
		}

		res, err := run.Run(ctx, graphuc.GetGraphInput{
			UserID: userID,
			Types:  types,
			Since:  sincePtr,
			Limit:  in.Limit,
		})
		if err != nil {
			return nil, toHTTP(err)
		}

		out := &getGraphOutput{}
		out.Body.Nodes = make([]nodeDTO, 0, len(res.Nodes))
		for _, n := range res.Nodes {
			out.Body.Nodes = append(out.Body.Nodes, toNodeDTO(n))
		}
		out.Body.Edges = make([]edgeDTO, 0, len(res.Edges))
		for _, e := range res.Edges {
			out.Body.Edges = append(out.Body.Edges, toEdgeDTO(e))
		}
		return out, nil
	})
}
