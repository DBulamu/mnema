package graph

import (
	"context"
	"fmt"
	"strings"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// SearchMode picks how the search ranks results. Text is the always-on
// path that needs no LLM provider; semantic depends on an Embedder being
// wired into the usecase and falls back to an error when it is not.
type SearchMode string

const (
	SearchModeText     SearchMode = "text"
	SearchModeSemantic SearchMode = "semantic"
)

// nodeSearcher is the consumer-side port for search. The postgres
// adapter satisfies it via a tiny bridge in the composition root —
// same pattern as nodeLister.
type nodeSearcher interface {
	Search(ctx context.Context, p NodeSearchParams) ([]domain.Node, error)
}

// NodeSearchParams mirrors the adapter's filter struct. Vector lives
// here even though only the semantic path uses it: declaring two
// parameter structs would force the bridge to switch on mode, and the
// adapter already does that switch by inspecting which field is set.
type NodeSearchParams struct {
	UserID string
	Query  string
	Vector []float32
	Types  []domain.NodeType
	Limit  int
}

// embedQuery turns the user's text query into a vector for semantic
// search. Same interface shape as extraction.Embedder so the same
// adapter can be wired into both — search must agree with extraction
// on dimensions, otherwise cosine-distance over the stored embeddings
// is meaningless. Declared on the consumer side so this package does
// not import extraction.
type embedQuery interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Search ranks the user's nodes against a free-form query. Two modes,
// one endpoint:
//
//   - text: ILIKE on title+content, leveraging the trgm index;
//   - semantic: cosine-distance against the embedding column.
//
// Semantic mode requires an Embedder wired in; if the deployment was
// started without one (e.g. provider misconfiguration) we surface a
// 400-shaped error rather than silently degrading to text — the caller
// asked for semantic and a quiet fallback would mask the misconfig.
type Search struct {
	Nodes    nodeSearcher
	Embedder embedQuery
}

type SearchInput struct {
	UserID string
	Query  string
	Mode   SearchMode
	Types  []domain.NodeType
	Limit  int
}

type SearchOutput struct {
	Nodes []domain.Node
}

// Search-specific limit bounds. We cap below the graph window's max:
// a search result list is rendered linearly in the UI, and 200 is
// already more than a human will scroll through.
const (
	searchDefaultLimit = 50
	searchMaxLimit     = 200
)

func (uc *Search) Run(ctx context.Context, in SearchInput) (SearchOutput, error) {
	if in.UserID == "" {
		return SearchOutput{}, fmt.Errorf("%w: user_id is required", domain.ErrInvalidArgument)
	}
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return SearchOutput{}, fmt.Errorf("%w: query is required", domain.ErrInvalidArgument)
	}
	for _, t := range in.Types {
		if !t.Valid() {
			return SearchOutput{}, fmt.Errorf("%w: unknown node type %q", domain.ErrInvalidArgument, t)
		}
	}

	mode := in.Mode
	if mode == "" {
		mode = SearchModeText
	}
	switch mode {
	case SearchModeText, SearchModeSemantic:
	default:
		return SearchOutput{}, fmt.Errorf("%w: unknown search mode %q", domain.ErrInvalidArgument, in.Mode)
	}

	limit := in.Limit
	if limit <= 0 {
		limit = searchDefaultLimit
	}
	if limit > searchMaxLimit {
		limit = searchMaxLimit
	}

	params := NodeSearchParams{
		UserID: in.UserID,
		Types:  in.Types,
		Limit:  limit,
	}

	if mode == SearchModeSemantic {
		if uc.Embedder == nil {
			// Surface as invalid-argument so the transport layer maps
			// to 400; this is a deploy-shape problem the client can
			// signal back to the user ("semantic search is not
			// configured") rather than a 500.
			return SearchOutput{}, fmt.Errorf("%w: semantic search not available", domain.ErrInvalidArgument)
		}
		vec, err := uc.Embedder.Embed(ctx, q)
		if err != nil {
			return SearchOutput{}, fmt.Errorf("embed query: %w", err)
		}
		if len(vec) == 0 {
			return SearchOutput{}, fmt.Errorf("embed query: empty vector")
		}
		params.Vector = vec
	} else {
		params.Query = q
	}

	nodes, err := uc.Nodes.Search(ctx, params)
	if err != nil {
		return SearchOutput{}, fmt.Errorf("search nodes: %w", err)
	}
	return SearchOutput{Nodes: nodes}, nil
}
