# Migrations

Goose migrations applied at backend startup.

## Naming convention

`YYYYMMDDhhmmss_short_name.sql`, e.g. `20260426091500_add_nodes.sql`.

Why timestamp instead of sequential numbers: timestamp-based names never
collide between branches/developers and naturally preserve the order of
authorship. Goose detects either format and tracks them in
`goose_db_version`.

## Creating a new migration

```bash
# From the backend directory:
goose -dir internal/migrations/sql create <short_name> sql
```

Goose v3 defaults to sequential numbering. Pass `-s false` or set
`GOOSE_TABLE` not — instead, run with the env var:

```bash
GOOSE_FORMAT=timestamp goose -dir internal/migrations/sql create <name> sql
```

Or just create the file by hand with the timestamp prefix (also fine).

## Format

Each file uses goose annotations:

```sql
-- +goose Up
CREATE TABLE foo (...);

-- +goose Down
DROP TABLE foo;
```

For statements that contain semicolons (functions, DO blocks), wrap them in
`-- +goose StatementBegin` / `-- +goose StatementEnd`.
