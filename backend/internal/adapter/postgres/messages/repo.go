// Package messages is the Postgres-backed message repository.
//
// Same conventions as the conversations package: one method per file,
// pgx errors translated to domain sentinels.
package messages

import "github.com/jackc/pgx/v5/pgxpool"

// Repo persists chat messages. Safe for concurrent use.
type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}
