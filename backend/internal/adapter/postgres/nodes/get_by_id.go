package nodes

import (
	"context"
	"errors"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

// GetByID returns the active (not soft-deleted) node with the given id,
// scoped to userID. Foreign or deleted nodes collapse to ErrNodeNotFound
// — same existence-leak mitigation as conversations.
func (r *Repo) GetByID(ctx context.Context, id, userID string) (domain.Node, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			id, user_id, type, title, content, metadata,
			occurred_at, occurred_at_precision,
			activation, last_accessed_at, pinned,
			source_message_id, created_at, updated_at
		FROM nodes
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`, id, userID)

	var (
		n            domain.Node
		typeStr      string
		precisionStr *string
		metaRaw      []byte
	)
	if err := row.Scan(
		&n.ID, &n.UserID, &typeStr, &n.Title, &n.Content, &metaRaw,
		&n.OccurredAt, &precisionStr,
		&n.Activation, &n.LastAccessedAt, &n.Pinned,
		&n.SourceMessageID, &n.CreatedAt, &n.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Node{}, domain.ErrNodeNotFound
		}
		return domain.Node{}, fmt.Errorf("get node: %w", err)
	}
	n.Type = domain.NodeType(typeStr)
	if precisionStr != nil {
		p := domain.OccurredAtPrecision(*precisionStr)
		n.OccurredAtPrecision = &p
	}
	meta, err := domain.DecodeNodeMetadata(n.Type, metaRaw)
	if err != nil {
		return domain.Node{}, err
	}
	n.Metadata = meta
	return n, nil
}
