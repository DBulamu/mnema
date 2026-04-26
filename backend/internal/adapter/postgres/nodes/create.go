package nodes

import (
	"context"
	"fmt"
	"time"

	"github.com/DBulamu/mnema/backend/internal/domain"
)

// CreateParams is the structural payload Create accepts. It mirrors the
// columns we let the caller set at insert time; activation, last_accessed_at,
// created_at and updated_at use DB defaults (activation=1.0, now()).
//
// Pointers (*string, *time.Time) carry the "not set" signal — Postgres
// stores them as NULL. Metadata is the strongly-typed NodeMetadata
// interface from domain; the adapter encodes it into JSONB so callers
// never see raw bytes. nil metadata is stored as the JSONB '{}' literal.
type CreateParams struct {
	UserID              string
	Type                string
	Title               *string
	Content             *string
	Metadata            domain.NodeMetadata
	OccurredAt          *time.Time
	OccurredAtPrecision *string
	SourceMessageID     *string
}

// Create inserts a new node and returns it as a domain.Node.
//
// We do not validate the type string here — the node_type enum in
// Postgres will reject anything unknown. Validation lives in the
// usecase layer where we have a domain.NodeType to call .Valid() on.
func (r *Repo) Create(ctx context.Context, p CreateParams) (domain.Node, error) {
	metadataBytes, err := domain.EncodeNodeMetadata(p.Metadata)
	if err != nil {
		return domain.Node{}, fmt.Errorf("encode metadata: %w", err)
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO nodes (
			user_id, type, title, content, metadata,
			occurred_at, occurred_at_precision, source_message_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING
			id, user_id, type, title, content, metadata,
			occurred_at, occurred_at_precision,
			activation, last_accessed_at, pinned,
			source_message_id, created_at, updated_at
	`,
		p.UserID, p.Type, p.Title, p.Content, metadataBytes,
		p.OccurredAt, p.OccurredAtPrecision, p.SourceMessageID,
	)

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
		return domain.Node{}, fmt.Errorf("insert node: %w", err)
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
