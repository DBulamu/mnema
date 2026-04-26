package edges

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// ListByNode returns every active edge where nodeID is either the
// source or the target. Used by the node-detail endpoint to enumerate
// 1-hop neighbours without first fetching a node window — that is what
// distinguishes this from ListByNodeIDs (intersect by both endpoints).
//
// userID is checked redundantly so a stolen node id from another user
// can never surface foreign edges. Soft-deleted edges are excluded.
func (r *Repo) ListByNode(ctx context.Context, userID, nodeID string) ([]domain.Edge, error) {
	if userID == "" {
		return nil, fmt.Errorf("list edges by node: user_id is required")
	}
	if nodeID == "" {
		return nil, fmt.Errorf("list edges by node: node_id is required")
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, source_id, target_id, type, weight, created_at
		FROM edges
		WHERE user_id = $1
		  AND deleted_at IS NULL
		  AND (source_id = $2 OR target_id = $2)
		ORDER BY created_at DESC
	`, userID, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list edges by node: %w", err)
	}
	defer rows.Close()

	var out []domain.Edge
	for rows.Next() {
		var (
			e       domain.Edge
			typeStr string
		)
		if err := rows.Scan(
			&e.ID, &e.UserID, &e.SourceID, &e.TargetID,
			&typeStr, &e.Weight, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}
		e.Type = domain.EdgeType(typeStr)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate edges: %w", err)
	}
	return out, nil
}
