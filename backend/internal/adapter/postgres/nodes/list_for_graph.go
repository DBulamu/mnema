package nodes

import (
	"context"
	"fmt"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// ListForGraphParams carries the optional filters the graph endpoint
// supports. All fields are zero-value-meaningful: an empty Types slice
// means "any type", a nil Since means "no lower bound on created_at".
//
// The struct lives at the adapter layer rather than the usecase layer
// because it maps directly to SQL fragments — the usecase has its own
// consumer-side mirror so the adapter stays unaware of usecase types.
type ListForGraphParams struct {
	UserID string
	// Types restricts results to the listed node types. Empty means no
	// filter; the caller is expected to have validated each entry with
	// domain.NodeType.Valid() so we don't need an enum cast in SQL.
	Types []domain.NodeType
	// Since, when non-nil, restricts results to nodes with created_at
	// >= *Since. Used for incremental graph refreshes from the UI.
	Since *time.Time
	// Limit caps the number of rows returned. The adapter trusts the
	// caller to have applied product-level min/max bounds — this layer
	// only enforces "must be positive".
	Limit int
}

// ListForGraph returns the user's active nodes ordered by created_at DESC,
// applying the provided filters. Soft-deleted nodes are excluded.
//
// The query is built with positional parameters — never string-concat
// user input — and only switches on whether each filter is set. This
// keeps a single prepared-statement shape per filter combination rather
// than per request, which is fine at MVP scale.
func (r *Repo) ListForGraph(ctx context.Context, p ListForGraphParams) ([]domain.Node, error) {
	if p.UserID == "" {
		return nil, fmt.Errorf("list nodes for graph: user_id is required")
	}
	if p.Limit <= 0 {
		return nil, fmt.Errorf("list nodes for graph: limit must be positive")
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
	if p.Since != nil {
		args = append(args, *p.Since)
		where += fmt.Sprintf(" AND created_at >= $%d", len(args))
	}
	args = append(args, p.Limit)
	limitArg := len(args)

	query := fmt.Sprintf(`
		SELECT
			id, user_id, type, title, content, metadata,
			occurred_at, occurred_at_precision,
			activation, last_accessed_at, pinned,
			source_message_id, created_at, updated_at
		FROM nodes
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d
	`, where, limitArg)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list nodes for graph: %w", err)
	}
	defer rows.Close()

	var out []domain.Node
	for rows.Next() {
		var (
			n            domain.Node
			typeStr      string
			precisionStr *string
			metaRaw      []byte
		)
		if err := rows.Scan(
			&n.ID, &n.UserID, &typeStr, &n.Title, &n.Content, &metaRaw,
			&n.OccurredAt, &precisionStr,
			&n.Activation, &n.LastAccessedAt, &n.Pinned,
			&n.SourceMessageID, &n.CreatedAt, &n.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		n.Type = domain.NodeType(typeStr)
		if precisionStr != nil {
			p := domain.OccurredAtPrecision(*precisionStr)
			n.OccurredAtPrecision = &p
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
