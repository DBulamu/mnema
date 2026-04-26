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

// ExtractedEdge connects two ExtractedNodes — either by LocalID (a node
// proposed in the same payload) or by ExistingID (a node the resolver
// passed in from the user's existing graph).
//
// Exactly one of {SourceLocalID, SourceExistingID} must be non-empty;
// same for the target. The usecase enforces this and drops malformed
// edges silently.
type ExtractedEdge struct {
	SourceLocalID    string
	SourceExistingID string
	TargetLocalID    string
	TargetExistingID string
	Type             domain.EdgeType
}

// Extraction is the full LLM output for one message.
type Extraction struct {
	Nodes []ExtractedNode
	Edges []ExtractedEdge
}

// ExistingNode is a compact view of a node already in the user's graph.
// The resolver pre-fetches the top-K most semantically-relevant nodes
// and passes them to the extractor so the model can re-use an existing
// id (e.g. the same "бабушка" person) instead of creating a duplicate.
//
// Title and Content are denormalised on purpose — the model needs the
// surface text to recognise the entity. We deliberately do NOT include
// embeddings here: the kNN was already done by the resolver, this is
// the human-readable shortlist for the LLM to disambiguate against.
type ExistingNode struct {
	ID      string
	Type    domain.NodeType
	Title   *string
	Content *string
}

// Extractor turns a piece of free-form user text into a graph proposal.
// The port lives at the consumer (this package); LLM-backed adapters
// satisfy it structurally so they can be swapped without touching the
// usecase.
//
// The `existing` slice is the resolver's shortlist of nodes the new
// message might already reference — adapters use it to map "бабушка"
// in this message to the same person node from a previous message.
// Empty when the user has no graph yet, or when the resolver is wired
// to nil.
type Extractor interface {
	Extract(ctx context.Context, content string, existing []ExistingNode) (Extraction, error)
}

// CandidateResolver finds existing nodes in the user's graph that the
// new message may reference. It is consulted before Extractor.Extract;
// the shortlist is forwarded to the LLM so duplicates can collapse.
//
// Optional: when nil the usecase still works, the LLM just won't see
// any priors and every new entity becomes a new node (the pre-resolve
// behaviour).
type CandidateResolver interface {
	ResolveCandidates(ctx context.Context, userID, content string) ([]ExistingNode, error)
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

// timeNodeFinder is the consumer-side port that returns (or creates)
// the time-period node for a canonical title — "2025", "2025-03",
// "2025-03-12". Idempotent: every call with the same (userID, title)
// resolves to the same node id. Used by the post-extraction time-tree
// builder to anchor regular nodes onto the time axis.
type timeNodeFinder interface {
	FindOrCreateTime(ctx context.Context, userID, title string) (domain.Node, error)
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
	// Resolver is optional. When set, the usecase asks it for a
	// shortlist of existing nodes likely-referenced by the new message,
	// and forwards them to the Extractor so the LLM can re-use ids
	// instead of creating duplicates. Failures are non-fatal — the
	// extractor still runs with an empty shortlist.
	Resolver CandidateResolver

	// TimeNodes is optional. When set, every node with a non-nil
	// OccurredAt receives part_of edges to year/month/day time-period
	// nodes (as appropriate for the precision), connecting the regular
	// graph to a deterministic time axis. Time-узлы дедуплицируются на
	// уровне адаптера — см. FindOrCreateTime.
	TimeNodes timeNodeFinder
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
	// TimeNodes counts how many distinct time-period nodes (year /
	// month / day) were touched by this extraction — created or
	// reused. TimeEdges is the count of part_of edges added to anchor
	// regular nodes onto the time axis. Both stay zero when no node had
	// an occurred_at, or when TimeNodes port is unwired.
	TimeNodesTouched int
	TimeEdges        int
}

// nodeWithTime holds a freshly-created node and its occurred_at /
// precision, so the time-tree builder can stitch it onto the time
// axis without re-walking the proposal.
type nodeWithTime struct {
	id         string
	occurredAt time.Time
	precision  domain.OccurredAtPrecision
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

	// Pre-resolve: ask the resolver for a shortlist of existing nodes
	// the new message might reference. Failure is non-fatal — we'd
	// rather extract without priors than fail the whole pipeline when
	// a kNN search hiccups. The shortlist is the only signal the LLM
	// gets about the user's existing graph; everything else is fresh
	// content per call.
	var existing []ExistingNode
	if uc.Resolver != nil {
		var rerr error
		existing, rerr = uc.Resolver.ResolveCandidates(ctx, in.UserID, in.Content)
		if rerr != nil {
			existing = nil
		}
	}
	allowed := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		allowed[e.ID] = struct{}{}
	}

	proposal, err := uc.Extractor.Extract(ctx, in.Content, existing)
	if err != nil {
		return ExtractOutput{}, fmt.Errorf("extractor: %w", err)
	}

	// Validate and persist nodes first; build a LocalID → DB id map so
	// edges can be wired up afterwards.
	idMap := make(map[string]string, len(proposal.Nodes))
	withTime := make([]nodeWithTime, 0)
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
		// Anchor onto the time axis if the model gave us a date. We
		// skip the time-узел type itself from this list — it is the
		// axis, not anchored on itself.
		if en.OccurredAt != nil && en.Type != domain.NodeTime {
			prec := domain.PrecisionDay
			if en.OccurredAtPrecision != nil {
				prec = *en.OccurredAtPrecision
			}
			withTime = append(withTime, nodeWithTime{
				id:         node.ID,
				occurredAt: *en.OccurredAt,
				precision:  prec,
			})
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
		srcID, okS := resolveEndpoint(ee.SourceLocalID, ee.SourceExistingID, idMap, allowed)
		tgtID, okT := resolveEndpoint(ee.TargetLocalID, ee.TargetExistingID, idMap, allowed)
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

	// Time tree — a deterministic post-processing step. The LLM is
	// unreliable about dates, but once it has produced an occurred_at
	// we can compute the canonical time-period titles ourselves and
	// stitch the node onto the axis. Entirely skipped when TimeNodes
	// port is unwired.
	if uc.TimeNodes != nil && len(withTime) > 0 {
		nTouched, nEdges, err := uc.buildTimeTree(ctx, in.UserID, withTime)
		if err != nil {
			return out, fmt.Errorf("build time tree: %w", err)
		}
		out.TimeNodesTouched = nTouched
		out.TimeEdges = nEdges
	}

	return out, nil
}

// buildTimeTree anchors every node with an occurred_at onto a tree of
// time-period узлов. For each node it walks down by precision —
// year → month → day — and builds part_of edges:
//
//   node ─part_of→ day ─part_of→ month ─part_of→ year      (precision=day)
//   node ─part_of→ month ─part_of→ year                    (precision=month)
//   node ─part_of→ year                                    (precision=year)
//
// The day → month → year edges are created at most once per period in
// this run thanks to a local cache; cross-run dedup is handled by the
// adapter (FindOrCreateTime is idempotent) and by the edges UPSERT on
// (source_id, target_id, type) — re-running this on the same node is a
// no-op.
//
// Errors abort the whole tree to keep the user's view consistent — a
// half-built tree confuses the timeline UI more than no tree at all.
func (uc *Extract) buildTimeTree(ctx context.Context, userID string, items []nodeWithTime) (int, int, error) {
	touched := make(map[string]struct{})
	edges := 0
	// linkedPeriods caches "we already built day→month and month→year
	// for this title in this run" so we don't UPSERT the same edge
	// twice within one extraction. Cross-run dedup is the edges table
	// UNIQUE — this cache only saves us a couple of round-trips per
	// extraction.
	linkedDay := make(map[string]struct{})
	linkedMonth := make(map[string]struct{})

	getOrCreate := func(title string) (string, error) {
		n, err := uc.TimeNodes.FindOrCreateTime(ctx, userID, title)
		if err != nil {
			return "", err
		}
		touched[n.ID] = struct{}{}
		return n.ID, nil
	}

	addEdge := func(srcID, tgtID string) error {
		if srcID == tgtID {
			return nil
		}
		_, err := uc.Edges.Create(ctx, EdgeCreateParams{
			UserID:   userID,
			SourceID: srcID,
			TargetID: tgtID,
			Type:     string(domain.EdgePartOf),
		})
		if err != nil {
			return err
		}
		edges++
		return nil
	}

	for _, it := range items {
		t := it.occurredAt.UTC()
		yearTitle := timeTitleYear(t)
		yearID, err := getOrCreate(yearTitle)
		if err != nil {
			return 0, 0, err
		}

		switch it.precision {
		case domain.PrecisionYear:
			if err := addEdge(it.id, yearID); err != nil {
				return 0, 0, err
			}
		case domain.PrecisionMonth:
			monthTitle := timeTitleMonth(t)
			monthID, err := getOrCreate(monthTitle)
			if err != nil {
				return 0, 0, err
			}
			if err := addEdge(it.id, monthID); err != nil {
				return 0, 0, err
			}
			if _, seen := linkedMonth[monthTitle]; !seen {
				if err := addEdge(monthID, yearID); err != nil {
					return 0, 0, err
				}
				linkedMonth[monthTitle] = struct{}{}
			}
		default: // PrecisionDay (and any unknown — safest default)
			monthTitle := timeTitleMonth(t)
			dayTitle := timeTitleDay(t)
			monthID, err := getOrCreate(monthTitle)
			if err != nil {
				return 0, 0, err
			}
			dayID, err := getOrCreate(dayTitle)
			if err != nil {
				return 0, 0, err
			}
			if err := addEdge(it.id, dayID); err != nil {
				return 0, 0, err
			}
			if _, seen := linkedDay[dayTitle]; !seen {
				if err := addEdge(dayID, monthID); err != nil {
					return 0, 0, err
				}
				linkedDay[dayTitle] = struct{}{}
			}
			if _, seen := linkedMonth[monthTitle]; !seen {
				if err := addEdge(monthID, yearID); err != nil {
					return 0, 0, err
				}
				linkedMonth[monthTitle] = struct{}{}
			}
		}
	}

	return len(touched), edges, nil
}

// Canonical time-period titles. Kept short and machine-stable so the
// UI can parse them back into Date if needed: "2025", "2025-03",
// "2025-03-12". Always UTC: time-узлы are an axis, not local-clock
// observations — anchoring them to a user's TZ would mean the same
// occurred_at lands on different days for different sessions.
func timeTitleYear(t time.Time) string  { return t.Format("2006") }
func timeTitleMonth(t time.Time) string { return t.Format("2006-01") }
func timeTitleDay(t time.Time) string   { return t.Format("2006-01-02") }

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

// resolveEndpoint maps an edge endpoint to a concrete node id. Either
// LocalID (the model proposed a fresh node in this payload) or
// ExistingID (the model picked one from the resolver's shortlist) wins;
// LocalID takes precedence if both are set. ExistingID is whitelisted
// against the resolver's shortlist so the model cannot point an edge
// at an arbitrary uuid — it has to be a node we've already shown it.
func resolveEndpoint(localID, existingID string, idMap map[string]string, allowed map[string]struct{}) (string, bool) {
	if localID != "" {
		id, ok := idMap[localID]
		return id, ok
	}
	if existingID != "" {
		if _, ok := allowed[existingID]; ok {
			return existingID, true
		}
	}
	return "", false
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
