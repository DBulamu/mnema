// Package extraction is the usecase that turns a stored chat message
// into graph entities — one or more nodes plus their connecting edges.
//
// This is the bridge between the conversation hot path (chat) and the
// life graph (nodes/edges). Extraction is invoked after a user message
// is persisted; the usecase calls an LLM-backed Extractor port, validates
// the output against domain.NodeType / domain.EdgeType, and writes the
// surviving rows.
//
// MVP shape:
//   - sync invocation from chat.SendMessage (errors are logged, not fatal);
//   - one extraction per user message, no batching;
//   - per-node embedding generation runs as a second step against the
//     just-stored node — failures are non-fatal so a flaky embedding
//     provider never costs us the node itself.
package extraction

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// ExtractedNode is the LLM's structured proposal for a single graph node.
// Pointers carry "the model didn't say" — we never fabricate an
// occurred_at when the model didn't infer one.
type ExtractedNode struct {
	// LocalID is an extractor-scoped identifier (e.g. "n1") that edges
	// reference. It exists only to wire up edges within one extraction
	// payload; it is not stored.
	LocalID string

	Type                domain.NodeType
	Title               *string
	Content             *string
	Metadata            domain.NodeMetadata
	OccurredAt          *time.Time
	OccurredAtPrecision *domain.OccurredAtPrecision
}

// ExtractedEdge connects two ExtractedNodes by their LocalID.
type ExtractedEdge struct {
	SourceLocalID string
	TargetLocalID string
	Type          domain.EdgeType
}

// Extraction is the full LLM output for one message.
type Extraction struct {
	Nodes []ExtractedNode
	Edges []ExtractedEdge
}

// Extractor turns a piece of free-form user text into a graph proposal.
// The port lives at the consumer (this package); LLM-backed adapters
// satisfy it structurally so they can be swapped without touching the
// usecase.
type Extractor interface {
	Extract(ctx context.Context, content string) (Extraction, error)
}

// nodeCreator persists one extracted node. Accepting primitives keeps
// the adapter ignorant of usecase types.
type nodeCreator interface {
	Create(ctx context.Context, p NodeCreateParams) (domain.Node, error)
}

// NodeCreateParams mirrors the adapter's CreateParams shape — declared
// here on the consumer side so the usecase doesn't import the adapter.
// The adapter's struct is structurally identical; the composition root
// bridges between them when their nominal types diverge.
type NodeCreateParams struct {
	UserID              string
	Type                string
	Title               *string
	Content             *string
	Metadata            domain.NodeMetadata
	OccurredAt          *time.Time
	OccurredAtPrecision *string
	SourceMessageID     *string
}

// edgeCreator persists one edge between two already-created nodes.
type edgeCreator interface {
	Create(ctx context.Context, p EdgeCreateParams) (domain.Edge, error)
}

// EdgeCreateParams mirrors the edges adapter's CreateParams.
type EdgeCreateParams struct {
	UserID   string
	SourceID string
	TargetID string
	Type     string
}

// Embedder turns a piece of text into a fixed-size vector for the H11
// revival path. The Model() string is stored alongside the vector on the
// node so we can detect dimension changes and trigger a re-embed when we
// swap providers — vectors with different dimensions cannot be compared.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Model() string
}

// embeddingUpdater attaches a freshly-generated vector to an existing
// node. Split from Create so a failed embedding does not poison node
// persistence — extraction first writes the node, then tries to embed.
type embeddingUpdater interface {
	UpdateEmbedding(ctx context.Context, nodeID string, vec []float32, model string) error
}

// Extract is the usecase: take a freshly-stored user message, ask the
// LLM what graph entities it implies, persist them.
//
// Invariants enforced here, not in adapters:
//   - every node has a valid domain.NodeType;
//   - every edge references node LocalIDs that the same payload created;
//   - every edge has a valid domain.EdgeType;
//   - at least one of title/content is non-empty (a node with no surface
//     in the UI is rejected — we'd rather drop it than store noise).
//
// Edges that reference unknown LocalIDs are skipped, not failed: a
// model can produce a valid node list and a partially-broken edge list,
// and we want the nodes to land regardless.
type Extract struct {
	Extractor Extractor
	Nodes     nodeCreator
	Edges     edgeCreator
	// Embedder and Embeddings are optional. When either is nil the
	// extraction usecase persists nodes and edges as before but skips
	// embedding generation — this keeps test wiring trivial and lets a
	// deploy turn embeddings off without touching the chat hot path.
	Embedder   Embedder
	Embeddings embeddingUpdater
}

type ExtractInput struct {
	UserID    string
	MessageID string
	Content   string
}

type ExtractOutput struct {
	NodeIDs []string
	EdgeIDs []string
	// Embedded counts how many of NodeIDs received a vector successfully.
	// EmbedFailures is the number of nodes whose vector generation or
	// persistence failed — exposed (not surfaced as an error) because
	// embedding is non-fatal by design and we still want the count for
	// observability at the call site.
	Embedded      int
	EmbedFailures int
}

func (uc *Extract) Run(ctx context.Context, in ExtractInput) (ExtractOutput, error) {
	switch {
	case in.UserID == "":
		return ExtractOutput{}, fmt.Errorf("%w: user_id is required", domain.ErrInvalidArgument)
	case in.MessageID == "":
		return ExtractOutput{}, fmt.Errorf("%w: message_id is required", domain.ErrInvalidArgument)
	case in.Content == "":
		return ExtractOutput{}, fmt.Errorf("%w: content is required", domain.ErrInvalidArgument)
	}

	proposal, err := uc.Extractor.Extract(ctx, in.Content)
	if err != nil {
		return ExtractOutput{}, fmt.Errorf("extractor: %w", err)
	}

	// Validate and persist nodes first; build a LocalID → DB id map so
	// edges can be wired up afterwards.
	idMap := make(map[string]string, len(proposal.Nodes))
	out := ExtractOutput{}
	srcMsg := in.MessageID
	for _, en := range proposal.Nodes {
		if !en.Type.Valid() {
			// Unknown type → silently drop. The model occasionally
			// invents categories; we'd rather lose one node than
			// pollute the graph with arbitrary types.
			continue
		}
		title := nonEmpty(en.Title)
		content := nonEmpty(en.Content)
		if title == nil && content == nil {
			continue
		}
		var precPtr *string
		if en.OccurredAtPrecision != nil {
			if !en.OccurredAtPrecision.Valid() {
				return out, fmt.Errorf("%w: invalid occurred_at_precision %q", domain.ErrInvalidArgument, *en.OccurredAtPrecision)
			}
			s := string(*en.OccurredAtPrecision)
			precPtr = &s
		}

		node, err := uc.Nodes.Create(ctx, NodeCreateParams{
			UserID:              in.UserID,
			Type:                string(en.Type),
			Title:               title,
			Content:             content,
			Metadata:            en.Metadata,
			OccurredAt:          en.OccurredAt,
			OccurredAtPrecision: precPtr,
			SourceMessageID:     &srcMsg,
		})
		if err != nil {
			return out, fmt.Errorf("create node: %w", err)
		}
		out.NodeIDs = append(out.NodeIDs, node.ID)
		if en.LocalID != "" {
			idMap[en.LocalID] = node.ID
		}

		// Per-node embedding. Errors are deliberately swallowed into a
		// counter: we'd rather store a node without a vector than lose
		// the node because the embedding API is rate-limited or down.
		// A backfill job (not yet implemented) can revisit nodes whose
		// embedding is NULL.
		if uc.Embedder != nil && uc.Embeddings != nil {
			if uc.embedNode(ctx, node) {
				out.Embedded++
			} else {
				out.EmbedFailures++
			}
		}
	}

	for _, ee := range proposal.Edges {
		if !ee.Type.Valid() {
			continue
		}
		srcID, okS := idMap[ee.SourceLocalID]
		tgtID, okT := idMap[ee.TargetLocalID]
		if !okS || !okT || srcID == tgtID {
			// Edge references a node we didn't persist (validation drop)
			// or is a self-loop. Skip rather than fail — partial graphs
			// are better than no graph for the user.
			continue
		}
		edge, err := uc.Edges.Create(ctx, EdgeCreateParams{
			UserID:   in.UserID,
			SourceID: srcID,
			TargetID: tgtID,
			Type:     string(ee.Type),
		})
		if err != nil {
			return out, fmt.Errorf("create edge: %w", err)
		}
		out.EdgeIDs = append(out.EdgeIDs, edge.ID)
	}

	return out, nil
}

// embedNode generates a vector for the just-stored node and writes it
// back. Returns true on success, false on any error along the way; the
// caller increments the appropriate counter in ExtractOutput.
//
// Why ignore the specific error? The chat path is non-fatal here by
// design (a missing vector still leaves the node fully usable for
// listing / display); centralising "log + count" in the bridge would
// duplicate context the usecase already has, so we keep the failure
// boolean at this layer and let the call site decide what to do with
// the count.
func (uc *Extract) embedNode(ctx context.Context, node domain.Node) bool {
	text := embeddingTextFor(node)
	if text == "" {
		return false
	}
	vec, err := uc.Embedder.Embed(ctx, text)
	if err != nil || len(vec) == 0 {
		return false
	}
	if err := uc.Embeddings.UpdateEmbedding(ctx, node.ID, vec, uc.Embedder.Model()); err != nil {
		return false
	}
	return true
}

// embeddingTextFor builds the short string the embedder receives. We
// concatenate title + content because the search query may match either
// (a person node has only title; a thought node has only content). We
// do NOT include type or metadata: the embedding space is for semantic
// similarity, not categorical filtering — that is what indexes are for.
func embeddingTextFor(n domain.Node) string {
	parts := make([]string, 0, 2)
	if n.Title != nil {
		if t := strings.TrimSpace(*n.Title); t != "" {
			parts = append(parts, t)
		}
	}
	if n.Content != nil {
		if c := strings.TrimSpace(*n.Content); c != "" {
			parts = append(parts, c)
		}
	}
	return strings.Join(parts, "\n")
}

// nonEmpty returns p if its trimmed value is not empty, otherwise nil.
// Used so we don't store *string pointers to whitespace strings.
func nonEmpty(p *string) *string {
	if p == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*p)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
