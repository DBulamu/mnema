// Package conversations is the Postgres-backed conversation repository.
//
// One method per file by convention; this file holds the struct and
// constructor. Each method translates pgx errors into domain sentinels
// so callers can rely on errors.Is.
package conversations

import "github.com/jackc/pgx/v5/pgxpool"

// Repo persists conversation threads. Safe for concurrent use because
// pgxpool.Pool is.
type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}
