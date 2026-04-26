package nodes

import (
	"context"
	"fmt"
	"time"
)

// BumpActivation re-activates the listed nodes for the given user.
//
// Why the operation is shaped this way:
//   - "+= delta capped at 1.0" matches H11: activation lives on [0, 1]
//     and a single revival shouldn't fully re-saturate the node — that
//     would let a one-off question pin a node forever. With delta=0.5,
//     a node has to be referenced repeatedly to climb back to 1.0.
//   - last_accessed_at always advances to `now`, even on already-
//     saturated nodes — the decay clock should restart whenever the
//     user touches a memory, regardless of activation.
//   - We scope by user_id in the WHERE clause so a forged node id from
//     another tenant cannot reach this UPDATE.
//   - Empty IDs slice short-circuits — turning it into a no-op SQL
//     would still take a round trip.
func (r *Repo) BumpActivation(ctx context.Context, userID string, ids []string, delta float32, now time.Time) error {
	if userID == "" {
		return fmt.Errorf("nodes.BumpActivation: user_id is required")
	}
	if len(ids) == 0 {
		return nil
	}
	if delta <= 0 {
		return fmt.Errorf("nodes.BumpActivation: delta must be positive")
	}

	_, err := r.pool.Exec(ctx, `
		UPDATE nodes
		SET activation = LEAST(1.0, activation + $3),
		    last_accessed_at = $4,
		    updated_at = $4
		WHERE user_id = $1
		  AND id = ANY($2::uuid[])
		  AND deleted_at IS NULL
	`, userID, ids, delta, now)
	if err != nil {
		return fmt.Errorf("bump activation: %w", err)
	}
	return nil
}
