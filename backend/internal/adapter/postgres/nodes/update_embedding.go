package nodes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/DBulamu/mnema/backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

// UpdateEmbedding writes a vector + model identifier onto an existing
// node. The pgx driver does not know about the pgvector type, so we
// serialise to its text form ([f1,f2,...]) and let Postgres cast on
// INSERT — same wire format pgvector uses internally, so there is no
// performance penalty over a binary codec.
//
// Bumping updated_at is intentional: the embedding is a property of the
// node's current text, so re-embedding counts as an update for any
// downstream cache invalidation.
//
// Errors:
//   - ErrNodeNotFound when the row does not exist (lets callers
//     differentiate "node deleted between extraction and embed" from a
//     real DB failure);
//   - validation errors when the vector is empty or the model id is
//     blank — those are programmer mistakes, not transient issues.
func (r *Repo) UpdateEmbedding(ctx context.Context, nodeID string, vec []float32, model string) error {
	if len(vec) == 0 {
		return errors.New("nodes.UpdateEmbedding: empty vector")
	}
	if strings.TrimSpace(model) == "" {
		return errors.New("nodes.UpdateEmbedding: empty model")
	}

	literal := encodePgvector(vec)

	tag, err := r.pool.Exec(ctx, `
		UPDATE nodes
		SET embedding = $1::vector,
		    embedding_model = $2,
		    updated_at = now()
		WHERE id = $3 AND deleted_at IS NULL
	`, literal, model, nodeID)
	if err != nil {
		// pgx wraps "no rows" as ErrNoRows on QueryRow, but Exec returns
		// a (tag, err) pair — only real DB / network errors come back
		// non-nil. Compare for ErrNoRows defensively in case a future
		// driver change makes this surface as an error.
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNodeNotFound
		}
		return fmt.Errorf("update embedding: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNodeNotFound
	}
	return nil
}

// encodePgvector renders a float32 slice as the pgvector text format
// '[f1,f2,...]'. We use 'g' precision which round-trips a float32 in
// 9 digits or fewer — accurate without inflating row size.
func encodePgvector(vec []float32) string {
	var b strings.Builder
	// Pre-size: '[' + ']' + per-element ~12 chars + commas. A 1536-dim
	// vector lands around 18 KiB; growing once is cheaper than letting
	// the builder double its way there.
	b.Grow(2 + len(vec)*13)
	b.WriteByte('[')
	for i, f := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
