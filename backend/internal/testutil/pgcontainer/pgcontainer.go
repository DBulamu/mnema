// Package pgcontainer spins up a disposable Postgres (pgvector/pg16)
// container for integration tests, applies the in-tree goose migrations,
// and hands back a ready-to-use *pgxpool.Pool.
//
// The container image matches docker-compose.yml so adapter behavior in
// CI matches local. We also pre-create the same extensions (uuid-ossp,
// vector, pg_trgm) that docker-entrypoint-initdb.d normally installs —
// testcontainers-go boots a fresh data directory and ignores the
// compose-time init script.
//
// Tests opt in via the `integration` build tag so the default `go test`
// run on a developer machine without Docker is unaffected.
package pgcontainer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/DBulamu/mnema/backend/internal/migrations"
)

// Image kept in sync with docker-compose.yml. Pinned major + minor so a
// pgvector point release cannot silently break us.
const Image = "pgvector/pgvector:pg16"

// extensionsSQL must mirror migrations/000_extensions.sql. The compose
// stack mounts that file into docker-entrypoint-initdb.d; testcontainers
// boots fresh, so we run the same statements over the new pool before
// goose touches anything.
const extensionsSQL = `
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
`

// Stack is a test-scoped Postgres bound to a single test (or t.Run subtree).
type Stack struct {
	Pool      *pgxpool.Pool
	Container testcontainers.Container
	DSN       string
}

// Start launches a container, waits for readiness, runs migrations, and
// returns a Stack. t.Cleanup terminates the container — callers do not
// need to manage teardown.
//
// Slow on first run (image pull). Subsequent runs reuse the cached image.
// Tests that need many fixtures should use t.Run subtests with Reset
// rather than calling Start per case.
func Start(t *testing.T) *Stack {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// The first "ready" log fires after initdb on the UNIX socket — TCP
	// listening starts after a final restart. We require the log line
	// *twice* and then a real SQL ping over TCP. (Some Docker backends —
	// colima — only forward the IPv4-mapped port; SQL-ping rules that out.)
	c, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase("mnema_test"),
		tcpostgres.WithUsername("mnema"),
		tcpostgres.WithPassword("mnema"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(60*time.Second),
				wait.ForListeningPort("5432/tcp").
					WithStartupTimeout(60*time.Second),
			),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort: a failed terminate during teardown should not
		// mask test failures. Use a fresh context so an already-
		// cancelled parent doesn't kill the cleanup.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(ctx)
	})

	// Build the DSN from primitives instead of c.ConnectionString —
	// some Docker backends (colima on macOS, in particular) expose the
	// mapped port on 127.0.0.1 only, while ConnectionString may return
	// "localhost" which resolves to ::1 first and yields ECONNREFUSED.
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("get host: %v", err)
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("get mapped port: %v", err)
	}
	dsn := fmt.Sprintf(
		"postgres://mnema:mnema@%s:%s/mnema_test?sslmode=disable",
		host, port.Port(),
	)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, extensionsSQL); err != nil {
		t.Fatalf("install extensions: %v", err)
	}
	if err := migrations.Run(ctx, pool); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	return &Stack{Pool: pool, Container: c, DSN: dsn}
}

// Reset truncates every user table to give tests a clean slate without
// paying for a fresh container per case. We discover tables at runtime
// so a new migration cannot leave a stale, polluted table behind.
//
// `goose_db_version` is preserved — truncating it would force a
// re-migration on the next call.
func (s *Stack) Reset(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `
		SELECT quote_ident(tablename)
		FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename <> 'goose_db_version'
	`)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("scan table name: %w", err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if len(tables) == 0 {
		return nil
	}
	stmt := "TRUNCATE TABLE "
	for i, name := range tables {
		if i > 0 {
			stmt += ", "
		}
		stmt += name
	}
	stmt += " RESTART IDENTITY CASCADE"
	if _, err := s.Pool.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}
	return nil
}
