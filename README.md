# Mnema

A "digital brain" for thoughts, ideas, and memories — captured through chat,
organized as a graph, surfaced proactively over a lifetime.

> Status: **very early MVP**. No usable code yet — repository is being
> bootstrapped. Architecture and API are designed but not implemented.

## What it is

Mnema is a personal life graph:

- **Capture** — text and voice via a chat interface; AI extracts nodes
  (memories, ideas, dreams, people, places, events) and links them.
- **Graph** — a 2D timeline-graph (events, people, dates on a horizontal
  axis) with photo previews. Events act as containers for nodes (e.g. "Mom's
  45th birthday" groups all the memories from that day).
- **Decay & revival** — nodes fade over time like neurons; the system can
  resurface them on anniversaries, when patterns repeat, or by semantic
  similarity to today's writing.
- **Biography** — over years, the accumulated graph becomes the source for
  an AI-synthesized life story.

The product hypothesis: live your life, get a biography.

## Tech stack

| Layer | Choice |
|---|---|
| Backend | Go + [huma v2](https://huma.rocks/) (spec-first OpenAPI) |
| Database | PostgreSQL 15 + pgvector + pg_trgm |
| Migrations | [goose](https://github.com/pressly/goose) (embedded SQL) |
| Storage | S3-compatible — MinIO locally, Spaces in prod |
| Email | Resend in prod, [mailpit](https://github.com/axllent/mailpit) locally |
| LLM | vendor-agnostic adapter, default OpenAI (GPT-4o-mini, Whisper, text-embedding-3-small) |
| Frontend | React + Vite + TypeScript, types generated from `docs/openapi.yaml` |
| Logs | log/slog (stdlib) |
| Auth | Magic-link email + JWT |

The code targets three environments — `local`, `test`, `prod` — selected
via the `APP_ENV` variable. Provider implementations (`EmailSender`,
`LLMProvider`, `Storage`) are swapped per environment.

See [`docs/openapi.yaml`](docs/openapi.yaml) for the API contract.

## Repository layout

```
mnema/
├── backend/         # Go API (huma v2)
├── frontend/        # React + Vite + TS
├── migrations/      # PostgreSQL migrations (goose)
├── docs/
│   └── openapi.yaml # API contract — source of truth
├── docker-compose.yml
├── .env.example
└── README.md
```

## Local development

> Not runnable yet — the backend and frontend are placeholders. The
> `docker-compose.yml` already works for the infrastructure pieces.

### 1. Start infrastructure

```bash
cp .env.example .env
docker compose up -d
```

This brings up:

- **Postgres** + pgvector + pg_trgm on `localhost:5432`
  (db `mnema`, user `mnema`, password `mnema`)
- **MinIO** S3-compatible storage on `localhost:9000`
  (console at `localhost:9001`, user `mnema` / password `mnemamnema`)
- **mailpit** SMTP on `localhost:1025` (web UI at `localhost:8025`)

### 2. Run backend

```bash
cd backend
# TODO: go run ./cmd/api
```

### 3. Run frontend

```bash
cd frontend
# TODO: pnpm install && pnpm dev
```

## License

MIT — see [LICENSE](LICENSE).
