package nodes

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// ListByUser returns the user's active nodes ordered by created_at DESC,
// up to limit rows. The ordering is by creation, not activation — this
// is the "recent first" feed; activation-based ordering is a separate
// query for the active-memory view (added when /v1/graph lands).
func (r *Repo) ListByUser(ctx context.Context, userID string, limit int) ([]domain.Node, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id, user_id, type, title, content, metadata,
			occurred_at, occurred_at_precision,
			activation, last_accessed_at, pinned,
			source_message_id, image_url, created_at, updated_at
		FROM nodes
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
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
