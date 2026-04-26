package edges

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// CreateParams is the payload accepted by Create. user_id is required
// even though it is derivable from the source node — denormalising it
// onto edges is what lets "all my edges" run as a single index scan.
type CreateParams struct {
	UserID   string
	SourceID string
	TargetID string
	Type     string
}

// Create inserts an edge, or returns the existing one if (source, target,
// type) already exists. We treat duplicate extraction as idempotent: if
// the LLM repeats the same relationship across two messages, we keep one
// row, not two. ON CONFLICT DO UPDATE SET type = EXCLUDED.type is a
// trick that makes the RETURNING clause work on the existing row too.
func (r *Repo) Create(ctx context.Context, p CreateParams) (domain.Edge, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO edges (user_id, source_id, target_id, type)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (source_id, target_id, type) DO UPDATE
			SET type = EXCLUDED.type
		RETURNING id, user_id, source_id, target_id, type, weight, created_at
	`, p.UserID, p.SourceID, p.TargetID, p.Type)

	var (
		e       domain.Edge
		typeStr string
	)
	if err := row.Scan(
		&e.ID, &e.UserID, &e.SourceID, &e.TargetID,
		&typeStr, &e.Weight, &e.CreatedAt,
	); err != nil {
		return domain.Edge{}, fmt.Errorf("insert edge: %w", err)
	}
	e.Type = domain.EdgeType(typeStr)
	return e, nil
}
