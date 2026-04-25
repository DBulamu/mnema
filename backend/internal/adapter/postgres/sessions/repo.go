// Package sessions implements usecase.SessionRepo backed by Postgres.
package sessions

import "github.com/jackc/pgx/v5/pgxpool"

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}
