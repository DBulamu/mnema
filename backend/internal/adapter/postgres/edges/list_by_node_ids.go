package edges

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// ListByNodeIDs returns the user's edges whose source AND target both
// appear in nodeIDs. The intersection is intentional: the graph endpoint
// returns a node window first, and we want only the edges that fully
// connect inside that window — half-attached edges would render as
// dangling lines in the UI.
//
// Soft-deleted edges are excluded. user_id is checked redundantly so a
// stolen node id from another user can never surface someone else's
// edges, even if the caller passes a foreign id by mistake.
func (r *Repo) ListByNodeIDs(ctx context.Context, userID string, nodeIDs []string) ([]domain.Edge, error) {
	if userID == "" {
		return nil, fmt.Errorf("list edges by node ids: user_id is required")
	}
	if len(nodeIDs) == 0 {
		return nil, nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, source_id, target_id, type, weight, created_at
		FROM edges
		WHERE user_id = $1
		  AND deleted_at IS NULL
		  AND source_id = ANY($2::uuid[])
		  AND target_id = ANY($2::uuid[])
		ORDER BY created_at DESC
	`, userID, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("list edges by node ids: %w", err)
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
