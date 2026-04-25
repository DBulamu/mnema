// Package users implements usecase.UserRepo backed by Postgres.
//
// Convention: one method per file. New() and the struct definition stay
// here; method implementations live next to their file names so a reader
// can find a query by grepping for the method name.
package users

import "github.com/jackc/pgx/v5/pgxpool"

// Repo implements usecase.UserRepo. Hold one Repo per process — it is
// safe for concurrent use because the underlying pgxpool is.
type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}
