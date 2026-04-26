package nodes

import (
	"context"
	"fmt"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// FindOrCreateTime returns the time-period node for (userID, title),
// creating it if necessary. Idempotent: concurrent calls with the same
// title resolve to the same row thanks to the partial unique index
// idx_nodes_time_dedup (migration 20260426121131).
//
// `title` is the canonical period label — "2025", "2025-03",
// "2025-03-12". The caller (extraction time-tree builder) is responsible
// for picking the right granularity from a node's OccurredAtPrecision.
//
// Why not UPSERT with RETURNING in one shot? RETURNING on
// "INSERT ... ON CONFLICT DO NOTHING" returns zero rows when the
// conflict path is taken — pgx then surfaces ErrNoRows and we'd lose
// the existing row. Two statements (insert-or-noop, then select) is
// simpler and the dedup index keeps both paths fast.
func (r *Repo) FindOrCreateTime(ctx context.Context, userID, title string) (domain.Node, error) {
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO nodes (user_id, type, title)
		VALUES ($1, 'time', $2)
		ON CONFLICT DO NOTHING
	`, userID, title); err != nil {
		return domain.Node{}, fmt.Errorf("insert time node: %w", err)
	}

	row := r.pool.QueryRow(ctx, `
		SELECT
			id, user_id, type, title, content, metadata,
			occurred_at, occurred_at_precision,
			activation, last_accessed_at, pinned,
			source_message_id, image_url, created_at, updated_at
		FROM nodes
		WHERE user_id = $1 AND type = 'time' AND title = $2 AND deleted_at IS NULL
	`, userID, title)

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
		&n.SourceMessageID, &n.ImageURL, &n.CreatedAt, &n.UpdatedAt,
	); err != nil {
		return domain.Node{}, fmt.Errorf("select time node: %w", err)
	}
	n.Type = domain.NodeType(typeStr)
	if precisionStr != nil {
		pp := domain.OccurredAtPrecision(*precisionStr)
		n.OccurredAtPrecision = &pp
	}
	meta, err := domain.DecodeNodeMetadata(n.Type, metaRaw)
	if err != nil {
		return domain.Node{}, err
	}
	n.Metadata = meta
	return n, nil
}
