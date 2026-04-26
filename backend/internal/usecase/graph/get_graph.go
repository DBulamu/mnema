// Package graph holds usecases that read the user's life graph as a
// whole. The current shape is read-only: callers fetch a window of
// nodes plus the edges that fully connect inside that window.
//
// Edge filtering is "intersection only" — we do not include edges that
// dangle out of the returned node set. The UI cannot draw half an edge,
// and returning them would force every client to filter again.
package graph

import (
	"context"
	"fmt"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// nodeLister returns nodes for the user, applying the graph filters.
// Declared at the consumer side; the postgres adapter satisfies it via
// a tiny bridge in the composition root.
type nodeLister interface {
	ListForGraph(ctx context.Context, p NodeListParams) ([]domain.Node, error)
}

// NodeListParams mirrors the adapter's filter struct. Declared here so
// the usecase doesn't import the adapter package.
type NodeListParams struct {
	UserID string
	Types  []domain.NodeType
	Since  *time.Time
	Limit  int
}

// edgeLister returns the edges that connect the supplied node ids.
// nodeIDs comes from the node window we just selected — see GetGraph.
type edgeLister interface {
	ListByNodeIDs(ctx context.Context, userID string, nodeIDs []string) ([]domain.Edge, error)
}

// GetGraph is the usecase. Two adapters, one read of nodes, one read of
// edges constrained to those nodes.
type GetGraph struct {
	Nodes nodeLister
	Edges edgeLister
}

// GetGraphInput is what the transport hands the usecase. Types is empty
// when the caller didn't filter; Since is nil for "no lower bound".
type GetGraphInput struct {
	UserID string
	Types  []domain.NodeType
	Since  *time.Time
	Limit  int
}

// GetGraphOutput is the read view: nodes + their fully-internal edges.
type GetGraphOutput struct {
	Nodes []domain.Node
	Edges []domain.Edge
}

// Limit bounds. We cap at maxLimit because the graph is rendered in a
// single payload — a larger window would just make the client OOM
// before it makes the server feel slow. Cursor pagination lands later.
const (
	defaultLimit = 200
	maxLimit     = 1000
)

func (uc *GetGraph) Run(ctx context.Context, in GetGraphInput) (GetGraphOutput, error) {
	if in.UserID == "" {
		return GetGraphOutput{}, fmt.Errorf("%w: user_id is required", domain.ErrInvalidArgument)
	}
	for _, t := range in.Types {
		if !t.Valid() {
			return GetGraphOutput{}, fmt.Errorf("%w: unknown node type %q", domain.ErrInvalidArgument, t)
		}
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	nodes, err := uc.Nodes.ListForGraph(ctx, NodeListParams{
		UserID: in.UserID,
		Types:  in.Types,
		Since:  in.Since,
		Limit:  limit,
	})
	if err != nil {
		return GetGraphOutput{}, fmt.Errorf("list nodes: %w", err)
	}
	if len(nodes) == 0 {
		return GetGraphOutput{Nodes: nodes}, nil
	}

	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	edges, err := uc.Edges.ListByNodeIDs(ctx, in.UserID, ids)
	if err != nil {
		return GetGraphOutput{}, fmt.Errorf("list edges: %w", err)
	}
	return GetGraphOutput{Nodes: nodes, Edges: edges}, nil
}
