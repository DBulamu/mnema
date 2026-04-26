package edges

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// ListByUser returns the user's active edges, capped at limit. Ordering
// by created_at DESC matches the recent-first feed used elsewhere; the
// dedicated graph view will switch to weight-based ordering later.
func (r *Repo) ListByUser(ctx context.Context, userID string, limit int) ([]domain.Edge, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, source_id, target_id, type, weight, created_at
		FROM edges
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list edges: %w", err)
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
