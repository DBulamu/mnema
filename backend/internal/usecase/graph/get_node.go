package graph

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// GetNode is the usecase behind GET /v1/nodes/{id}. It returns the
// requested node, its 1-hop edges, and the neighbour nodes those
// edges point at.
//
// The shape is "node + neighbourhood, not subgraph": clients render a
// detail panel and a small list of related nodes, not a recursive
// graph view. Walking deeper is the client's job — call this endpoint
// again with the neighbour's id when the user clicks one.
type GetNode struct {
	Nodes nodeReader
	Edges edgeNeighbourhoodLister
}

// nodeReader is the consumer-side port for fetching a single node and
// for hydrating neighbour nodes in one batch. Same shape as the
// pgnodes adapter — direct pass-through, no bridge needed.
type nodeReader interface {
	GetByID(ctx context.Context, id, userID string) (domain.Node, error)
	ListByIDs(ctx context.Context, userID string, ids []string) ([]domain.Node, error)
}

// edgeNeighbourhoodLister returns every active edge incident on a
// single node id. Distinguished from the usecase's existing edge port
// (which intersects a window of nodes) because here we don't have a
// pre-fetched window — we're starting from one node and discovering
// the edges around it.
type edgeNeighbourhoodLister interface {
	ListByNode(ctx context.Context, userID, nodeID string) ([]domain.Edge, error)
}

type GetNodeInput struct {
	UserID string
	NodeID string
}

type GetNodeOutput struct {
	Node      domain.Node
	Edges     []domain.Edge
	Neighbors []domain.Node
}

func (uc *GetNode) Run(ctx context.Context, in GetNodeInput) (GetNodeOutput, error) {
	switch {
	case in.UserID == "":
		return GetNodeOutput{}, fmt.Errorf("%w: user_id is required", domain.ErrInvalidArgument)
	case in.NodeID == "":
		return GetNodeOutput{}, fmt.Errorf("%w: node_id is required", domain.ErrInvalidArgument)
	}

	node, err := uc.Nodes.GetByID(ctx, in.NodeID, in.UserID)
	if err != nil {
		return GetNodeOutput{}, err
	}

	edges, err := uc.Edges.ListByNode(ctx, in.UserID, node.ID)
	if err != nil {
		return GetNodeOutput{}, fmt.Errorf("list edges: %w", err)
	}

	// Collect neighbour ids from edges, skipping the node itself
	// (self-loops don't exist on the wire, but defending here keeps
	// the response stable if the schema ever loosens).
	seen := make(map[string]struct{}, len(edges)*2)
	neighborIDs := make([]string, 0, len(edges))
	for _, e := range edges {
		other := e.SourceID
		if other == node.ID {
			other = e.TargetID
		}
		if other == node.ID {
			continue
		}
		if _, ok := seen[other]; ok {
			continue
		}
		seen[other] = struct{}{}
		neighborIDs = append(neighborIDs, other)
	}

	neighbors, err := uc.Nodes.ListByIDs(ctx, in.UserID, neighborIDs)
	if err != nil {
		return GetNodeOutput{}, fmt.Errorf("list neighbours: %w", err)
	}

	return GetNodeOutput{
		Node:      node,
		Edges:     edges,
		Neighbors: neighbors,
	}, nil
}
