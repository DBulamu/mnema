// Package db owns the PostgreSQL connection pool.
//
// We use pgx (not database/sql with a pgx driver) for first-class pgvector
// and array support, plus better performance. The pool is created once at
// startup; everything else borrows connections from it.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open builds a pgx pool, applies conservative defaults sized for a single
// API instance, and pings to fail fast if Postgres is unreachable. The
// 5-second ping timeout is intentionally short — if we can't reach the DB
// at startup, we want to crash and let the orchestrator retry rather than
// hang waiting for a flaky network.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

	// Defaults tuned for a single API node on a small managed Postgres.
	// Revisit when we go horizontal — total open connections must stay
	// under Postgres max_connections / N_instances.
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	return pool, nil
}
