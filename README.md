# Sellico Ads Intelligence Backend

Go backend for Sellico Ads Intelligence. The service exposes a multi-tenant REST API, background workers, Wildberries integration clients, a recommendation engine, and asynchronous data exports over PostgreSQL and Redis.

## Local Run

1. Copy `.env.example` to `.env` and fill required secrets.
2. Start infrastructure with `docker compose up -d postgres redis`.
3. Apply migrations with `make migrate-up`.
4. Start API with `go run ./cmd/api`.
5. Start worker with `go run ./cmd/worker`.

## Main Components

- `cmd/api`: HTTP server bootstrap, router wiring, health checks.
- `cmd/worker`: Asynq worker bootstrap, schedulers, job processors, export generation.
- `internal/service`: application services for auth, workspaces, campaigns, products, positions, SERP, recommendations, extension, audit logs.
- `internal/repository/sqlc`: generated typed PostgreSQL access layer.
- `internal/integration/wb`: Wildberries API and parser integration code.

## Current MVP Surface

- Auth, workspaces, RBAC, seller cabinets, campaigns.
- Products, positions, SERP snapshots, recommendations, audit logs, extension sessions/context.
- Export jobs for `campaigns`, `campaign_stats`, `phrases`, `phrase_stats`, `products`, `positions`, `recommendations` in `csv` and `xlsx`.
- Health probes: `/health/live` and `/health/ready`.
- Worker queues for real campaign, campaign stats and phrase sync, plus recommendation generation and user-triggered exports.

## Commands

- `make build`: build API and worker binaries.
- `make test`: run tests.
- `make sqlc-generate`: regenerate typed SQL layer.
- `make docker-up`: build and start local stack.

## Notes

- Generated export files are stored under `EXPORT_STORAGE_PATH` (defaults to `./exports`).
