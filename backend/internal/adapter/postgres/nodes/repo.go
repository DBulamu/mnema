// Package nodes is the Postgres-backed repository for graph nodes.
//
// One method per file. The adapter accepts primitive parameters and
// returns domain entities — it never imports any usecase package, so
// usecases can declare their own consumer-side interfaces and the
// adapter satisfies them structurally.
package nodes

import "github.com/jackc/pgx/v5/pgxpool"

// Repo persists graph nodes. Safe for concurrent use.
type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}
