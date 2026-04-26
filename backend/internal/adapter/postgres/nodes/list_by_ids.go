package nodes

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// ListByIDs returns the user's active nodes whose ids are in `ids`.
// Foreign or soft-deleted ids are silently skipped — same existence-
// leak mitigation as GetByID. Order is unspecified; callers that need
// a particular order should sort client-side.
//
// Used by the node-detail endpoint to fetch neighbour nodes in one
// round-trip after enumerating the 1-hop edges.
func (r *Repo) ListByIDs(ctx context.Context, userID string, ids []string) ([]domain.Node, error) {
	if userID == "" {
		return nil, fmt.Errorf("list nodes by ids: user_id is required")
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT
			id, user_id, type, title, content, metadata,
			occurred_at, occurred_at_precision,
			activation, last_accessed_at, pinned,
			source_message_id, image_url, created_at, updated_at
		FROM nodes
		WHERE user_id = $1 AND deleted_at IS NULL AND id = ANY($2::uuid[])
	`, userID, ids)
	if err != nil {
		return nil, fmt.Errorf("list nodes by ids: %w", err)
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
			&n.SourceMessageID, &n.ImageURL, &n.CreatedAt, &n.UpdatedAt,
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
