// Package edges is the Postgres-backed repository for graph edges.
//
// One method per file; the package mirrors the convention used by
// conversations/messages/nodes.
package edges

import "github.com/jackc/pgx/v5/pgxpool"

// Repo persists typed edges between graph nodes.
type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}
