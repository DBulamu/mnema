package recall

import (
	"context"
	"fmt"
	"strings"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// SearchCandidatesFinder is the in-package CandidateFinder
// implementation. It is a usecase-layer object — not an adapter —
// because the candidate-search step is *coordination*: per-anchor
// text search, plus phrasal embedding semantic search, with merging
// and deduplication. The actual storage / vector calls live behind
// two consumer-side ports the composition root wires up.
//
// Why three ports rather than reusing graph.Search directly: clean
// architecture forbids one usecase importing another, and graph.Search
// is itself a usecase. We declare the minimal shape we need here and
// the composition root bridges it to graph.Search via field-for-field
// copy — same pattern as everywhere else in this codebase.
type SearchCandidatesFinder struct {
	// SearchByText runs an ILIKE/trgm search on the user's nodes.
	// Implementations may apply a per-anchor limit; we still cap on
	// our side via PerAnchorLimit so a misconfigured adapter cannot
	// flood the answer LLM with noise.
	SearchByText TextSearcher
	// SearchByVector runs a cosine-distance search against the node
	// embeddings.
	SearchByVector VectorSearcher
	// Embed turns the user's free-form recall text into the vector
	// used for the phrasal-embedding step. Same Embed shape as
	// extraction.Embedder, so a single adapter can satisfy both.
	Embed Embedder

	// PerAnchorLimit caps how many text-search hits a single anchor
	// can contribute. The default (5) keeps recall focused on the
	// strongest matches per slot — there is no value in returning the
	// 30th weakest match for "Питер" when a phrasal embedding will
	// surface a better candidate anyway.
	PerAnchorLimit int
	// PhrasalLimit caps the semantic top-K. Default 10 — same as the
	// open question recorded in wiki/recall-mechanics.md.
	PhrasalLimit int
	// MaxCandidates caps the final merged set passed to the answer
	// LLM. Default 20: anything above tends to drift the model into
	// "summarise the list" mode rather than answering the question.
	MaxCandidates int
}

// Defaults small enough to fit in a model's context comfortably and
// large enough to not starve the answer step on a sparse graph.
const (
	defaultPerAnchorLimit = 5
	defaultPhrasalLimit   = 10
	defaultMaxCandidates  = 20
)

// TextSearcher is the per-anchor text-search port. Field shapes match
// graph.NodeSearchParams 1:1 so the composition root can wrap a
// graph.Search runner with one trivial bridge.
type TextSearcher interface {
	SearchByText(ctx context.Context, userID, query string, limit int) ([]domain.Node, error)
}

// VectorSearcher is the phrasal-embedding port.
type VectorSearcher interface {
	SearchByVector(ctx context.Context, userID string, vector []float32, limit int) ([]domain.Node, error)
}

// Embedder turns text into a vector for semantic search. Identical
// shape to extraction.Embedder so the same OpenAI/stub adapter
// satisfies both.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// FindCandidates fans the recall query out into the graph and merges
// the results.
//
// Order of preference in the final list: text-search hits first
// (anchors are the strongest signal — the user named the entity), then
// phrasal-embedding hits filling out context. Deduplicated by node id;
// the first occurrence wins, so a node found by both anchor and
// phrasal embedding keeps its anchor-derived rank.
//
// Errors from either search are non-fatal individually as long as one
// path returned something. We only surface an error when *both* paths
// fail — that is the case the user actually cares about (no signal at
// all). Why: the user picked ollama/local LLM with no embedder
// available locally on the MVP, and we do not want a missing embedder
// to block the entire pipeline; text-only candidates are a useful
// degraded mode.
func (f *SearchCandidatesFinder) FindCandidates(ctx context.Context, userID, text string, anchors []Anchor) ([]domain.Node, error) {
	if userID == "" {
		return nil, fmt.Errorf("%w: user_id is required", domain.ErrInvalidArgument)
	}

	perAnchor := f.PerAnchorLimit
	if perAnchor <= 0 {
		perAnchor = defaultPerAnchorLimit
	}
	phrasal := f.PhrasalLimit
	if phrasal <= 0 {
		phrasal = defaultPhrasalLimit
	}
	cap := f.MaxCandidates
	if cap <= 0 {
		cap = defaultMaxCandidates
	}

	merged := make([]domain.Node, 0, cap)
	seen := make(map[string]struct{}, cap)
	push := func(nodes []domain.Node) {
		for _, n := range nodes {
			if len(merged) >= cap {
				return
			}
			if _, dup := seen[n.ID]; dup {
				continue
			}
			seen[n.ID] = struct{}{}
			merged = append(merged, n)
		}
	}

	var textErr, vecErr error

	// 1) Per-anchor text search. Anchors with empty text are dropped
	// upstream by Recall.Run, but we double-check — a misbehaving
	// extractor should not crash the pipeline.
	if f.SearchByText != nil {
		for _, a := range anchors {
			q := strings.TrimSpace(a.Text)
			if q == "" {
				continue
			}
			hits, err := f.SearchByText.SearchByText(ctx, userID, q, perAnchor)
			if err != nil {
				textErr = err
				continue
			}
			push(hits)
			if len(merged) >= cap {
				break
			}
		}
	}

	// 2) Phrasal embedding semantic top-K. Skipped silently if no
	// embedder or vector searcher was wired — that's the local-MVP
	// path with stub embeddings (which is still valid: the stub
	// embedder is dimension-matched and lives in the same column).
	if len(merged) < cap && f.Embed != nil && f.SearchByVector != nil {
		vec, err := f.Embed.Embed(ctx, text)
		if err != nil {
			vecErr = fmt.Errorf("embed phrase: %w", err)
		} else if len(vec) == 0 {
			vecErr = fmt.Errorf("embed phrase: empty vector")
		} else {
			hits, err := f.SearchByVector.SearchByVector(ctx, userID, vec, phrasal)
			if err != nil {
				vecErr = err
			} else {
				push(hits)
			}
		}
	}

	if len(merged) == 0 && textErr != nil && vecErr != nil {
		return nil, fmt.Errorf("recall candidates: text=%v; vector=%v", textErr, vecErr)
	}
	return merged, nil
}
