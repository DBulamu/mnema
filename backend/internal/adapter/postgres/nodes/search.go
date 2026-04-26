package nodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// SearchParams carries the inputs for the user-scoped node search.
//
// Two modes share one entry point because the response shape is the
// same — a ranked list of nodes — and the choice between text and
// semantic happens per query, not per deployment. The caller is
// responsible for checking query / vector presence; the adapter only
// asserts that exactly one of them is set.
//
// Types is an optional post-filter (the index works on user_id, the
// type list narrows after the match). Limit is enforced positive here
// for the same reason as ListForGraph: this layer doesn't know product
// caps, only what the SQL needs.
type SearchParams struct {
	UserID string
	Query  string
	// Vector, when non-nil, switches the query to cosine-distance
	// ordering against the `embedding` column. Mutually exclusive with
	// Query — the usecase enforces "exactly one mode" before calling.
	Vector []float32
	Types  []domain.NodeType
	Limit  int
}

// Search returns the user's active nodes ranked by relevance to either
// a text query (ILIKE on title+content, leveraging the trgm index) or
// a semantic vector (cosine distance against `embedding`).
//
// Soft-deleted nodes are excluded; tenant scoping is enforced by
// user_id in the WHERE clause regardless of mode.
//
// Why ILIKE + trgm rather than full-text search: at MVP scale the user
// has thousands of nodes, not millions, and tsvector requires picking
// a language — Russian/English mixing in this product is the norm. The
// trgm gin index already serves the content column and ILIKE against a
// short query is index-friendly.
func (r *Repo) Search(ctx context.Context, p SearchParams) ([]domain.Node, error) {
	if p.UserID == "" {
		return nil, fmt.Errorf("nodes.Search: user_id is required")
	}
	if p.Limit <= 0 {
		return nil, fmt.Errorf("nodes.Search: limit must be positive")
	}
	hasQuery := strings.TrimSpace(p.Query) != ""
	hasVector := len(p.Vector) > 0
	if hasQuery == hasVector {
		// Either both set or both empty — the usecase should have
		// rejected this. Surface a clear error so a wiring bug is
		// not papered over with an arbitrary fallback.
		return nil, fmt.Errorf("nodes.Search: exactly one of Query or Vector must be set")
	}

	args := []any{p.UserID}
	where := "user_id = $1 AND deleted_at IS NULL"

	if len(p.Types) > 0 {
		typeStrs := make([]string, len(p.Types))
		for i, t := range p.Types {
			typeStrs[i] = string(t)
		}
		args = append(args, typeStrs)
		where += fmt.Sprintf(" AND type = ANY($%d::node_type[])", len(args))
	}

	var (
		orderBy string
		extraSelect string
	)
	if hasQuery {
		// ILIKE-pattern wraps the trimmed query. We pass the pattern as
		// a parameter (not interpolated) so pg can still pick the trgm
		// index and so SQLi is impossible by construction.
		args = append(args, "%"+strings.TrimSpace(p.Query)+"%")
		patternIdx := len(args)
		where += fmt.Sprintf(
			" AND (title ILIKE $%d OR content ILIKE $%d)",
			patternIdx, patternIdx,
		)
		// Title hits rank above content hits — the title is a curated
		// label and a match there is far more likely to be the node
		// the user means.
		orderBy = fmt.Sprintf(
			"CASE WHEN title ILIKE $%d THEN 0 ELSE 1 END, created_at DESC",
			patternIdx,
		)
	} else {
		// Semantic mode: skip rows that have no vector at all rather
		// than letting them appear at the bottom — without a vector
		// the cosine-distance is undefined and Postgres would return
		// NULL, which sorts unpredictably depending on collation.
		where += " AND embedding IS NOT NULL"
		args = append(args, encodePgvector(p.Vector))
		vecIdx := len(args)
		extraSelect = fmt.Sprintf(", (embedding <=> $%d::vector) AS distance", vecIdx)
		orderBy = "distance ASC"
	}

	args = append(args, p.Limit)
	limitIdx := len(args)

	query := fmt.Sprintf(`
		SELECT
			id, user_id, type, title, content, metadata,
			occurred_at, occurred_at_precision,
			activation, last_accessed_at, pinned,
			source_message_id, image_url, created_at, updated_at%s
		FROM nodes
		WHERE %s
		ORDER BY %s
		LIMIT $%d
	`, extraSelect, where, orderBy, limitIdx)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search nodes: %w", err)
	}
	defer rows.Close()

	var out []domain.Node
	for rows.Next() {
		var (
			n            domain.Node
			typeStr      string
			precisionStr *string
			metaRaw      []byte
			distance     float64 // populated only in semantic mode
		)
		dest := []any{
			&n.ID, &n.UserID, &typeStr, &n.Title, &n.Content, &metaRaw,
			&n.OccurredAt, &precisionStr,
			&n.Activation, &n.LastAccessedAt, &n.Pinned,
			&n.SourceMessageID, &n.ImageURL, &n.CreatedAt, &n.UpdatedAt,
		}
		if hasVector {
			dest = append(dest, &distance)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		n.Type = domain.NodeType(typeStr)
		if precisionStr != nil {
			pp := domain.OccurredAtPrecision(*precisionStr)
			n.OccurredAtPrecision = &pp
		}
		meta, err := domain.DecodeNodeMetadata(n.Type, metaRaw)
		if err != nil {
			return nil, err
		}
		n.Metadata = meta
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes: %w", err)
	}
	return out, nil
}
