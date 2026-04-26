package rest

import (
	"context"
	"net/http"

	graphuc "github.com/DBulamu/mnema/backend/internal/usecase/graph"
	"github.com/danielgtaylor/huma/v2"
)

// getNodeRunner is the consumer-side port for the handler. The graph
// GetNode usecase satisfies it structurally.
type getNodeRunner interface {
	Run(ctx context.Context, in graphuc.GetNodeInput) (graphuc.GetNodeOutput, error)
}

type getNodeInput struct {
	ID string `path:"id" format:"uuid"`
}

type getNodeOutput struct {
	Body struct {
		Node      nodeDTO   `json:"node"`
		Edges     []edgeDTO `json:"edges"`
		Neighbors []nodeDTO `json:"neighbors"`
	}
}

// RegisterGetNode wires GET /v1/nodes/{id}. Returns the requested
// node, every edge incident on it, and the neighbour nodes those
// edges point at — enough to render a detail panel and let the user
// step into a related node by clicking it.
func RegisterGetNode(api huma.API, run getNodeRunner) {
	huma.Register(api, huma.Operation{
		OperationID: "nodes-get",
		Method:      http.MethodGet,
		Path:        "/v1/nodes/{id}",
		Summary:     "Read a single node with its 1-hop neighbourhood",
		Tags:        []string{"graph"},
		Security:    []map[string][]string{{BearerSecurityName: {}}},
	}, func(ctx context.Context, in *getNodeInput) (*getNodeOutput, error) {
		userID := UserIDFromContext(ctx)
		if userID == "" {
			return nil, toHTTP(errUnauthenticated)
		}

		res, err := run.Run(ctx, graphuc.GetNodeInput{
			UserID: userID,
			NodeID: in.ID,
		})
		if err != nil {
			return nil, toHTTP(err)
		}

		out := &getNodeOutput{}
		out.Body.Node = toNodeDTO(res.Node)
		out.Body.Edges = make([]edgeDTO, 0, len(res.Edges))
		for _, e := range res.Edges {
			out.Body.Edges = append(out.Body.Edges, toEdgeDTO(e))
		}
		out.Body.Neighbors = make([]nodeDTO, 0, len(res.Neighbors))
		for _, n := range res.Neighbors {
			out.Body.Neighbors = append(out.Body.Neighbors, toNodeDTO(n))
		}
		return out, nil
	})
}
