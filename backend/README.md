# Backend

Go API for Mnema. Spec-first via [huma v2](https://huma.rocks/) — the source
of truth is [`../docs/openapi.yaml`](../docs/openapi.yaml).

## Status

Empty placeholder. Next steps:

1. `go mod init github.com/DBulamu/mnema/backend`
2. Skeleton: `cmd/api/main.go`, env config, structured logger (log/slog).
3. Wire goose migrations from `../migrations/` (embed via `//go:embed`).
4. First endpoint: `POST /auth/magic-link/request` — full path
   email → DB → mailpit.
5. JWT issuance on magic-link consumption.
6. Conversation API + LLM adapter (stub provider for `local`/`test`).
