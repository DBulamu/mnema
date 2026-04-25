// Package migrations runs schema migrations at startup using goose.
//
// SQL files live next to this package and are baked into the binary via
// //go:embed. That means a deployable artifact is a single file — no extra
// step to ship migrations alongside the API. The trade-off: applying
// migrations without starting the API requires building a small CLI, which
// we'll do when prod operations need it.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed sql/*.sql
var fs embed.FS

// Run applies all pending migrations against the supplied pool.
// goose needs a *sql.DB; pgx exposes one via stdlib.OpenDBFromPool, which
// shares the underlying pool — no extra connections are opened.
func Run(ctx context.Context, pool *pgxpool.Pool) error {
	goose.SetBaseFS(fs)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	return runWithDB(ctx, sqlDB)
}

func runWithDB(ctx context.Context, db *sql.DB) error {
	if err := goose.UpContext(ctx, db, "sql"); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
