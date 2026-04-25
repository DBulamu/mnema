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
