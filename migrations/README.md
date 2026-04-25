# Migrations

PostgreSQL migrations applied in order.

- `000_extensions.sql` is loaded automatically by the `postgres` container at
  first start (mounted into `/docker-entrypoint-initdb.d`). It enables
  `uuid-ossp`, `vector`, and `pg_trgm`.
- All other migrations are applied by the backend at startup via
  [goose](https://github.com/pressly/goose) (embedded with `//go:embed`).

The full v0 schema (users, auth_magic_links, sessions, conversations,
messages, nodes, edges, media, resurfacing_events) is documented in the
private wiki — it will be split into goose migrations as the backend is
implemented.
