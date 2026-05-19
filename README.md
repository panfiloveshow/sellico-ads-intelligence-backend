# Sellico Ads Intelligence Backend

Go backend for Sellico Ads Intelligence. The service exposes a multi-tenant REST API, background workers, Wildberries integration clients, a recommendation engine, and asynchronous data exports over PostgreSQL and Redis.

## Local Run

1. Copy `.env.example` to `.env` and fill required secrets.
2. For Docker-based startup, run `docker compose up -d`. The `migrate` service applies migrations before `api` and `worker` start.
3. For bare local startup without Dockerized app processes, start infrastructure with `docker compose up -d postgres redis`.
4. Apply migrations with `make migrate-up`.
5. Start API with `go run ./cmd/api`.
6. Start worker with `go run ./cmd/worker`.

## Production Deploy (Timeweb VPS)

1. Copy `.env.prod.example` to `.env` on the server and fill in production secrets.
2. Place SSL certificates in `nginx/ssl/` (`fullchain.pem` + `privkey.pem`).
3. Run first-time setup: `DEPLOY_DIR=/opt/sellico ./scripts/deploy.sh setup`.
4. Updates are source-built on the host: `./scripts/deploy.sh update`.
5. Backup and restore-check cron are installed by setup and use the Dockerized PostgreSQL service.

GitHub Actions CD pipeline (`.github/workflows/cd.yml`) automatically builds, pushes images to GHCR, and deploys via SSH on push to `main`.

## Main Components

- `cmd/api`: HTTP server bootstrap, router wiring, health checks.
- `cmd/worker`: Asynq worker bootstrap, schedulers, job processors, export generation, Telegram notifications.
- `internal/service`: application services for auth, workspaces, campaigns, products, positions, SERP, recommendations, notifications, extension, audit logs, strategies, semantics, competitors, delivery, SEO, and product events.
- `internal/repository/sqlc`: generated typed PostgreSQL access layer.
- `internal/integration/wb`: Wildberries API and parser integration (with circuit breaker).
- `internal/integration/telegram`: Telegram Bot API client for notifications.
- `internal/integration/sellico`: Sellico platform bridge for SSO and workspace resolution.

## Features

### Core
- Auth (JWT + Sellico SSO), workspaces, RBAC (owner/manager/analyst/viewer)
- Seller cabinets with encrypted WB API tokens
- Campaigns, products, phrases, bids, positions, SERP snapshots
- Recommendation engine with configurable thresholds per workspace
- Bid automation strategies with campaign/product bindings
- Semantics, keyword clustering, competitors, delivery/promo collection, SEO analysis, and product event tracking
- Export jobs (CSV/XLSX) via background workers
- Audit logs, job run monitoring

### Infrastructure
- Prometheus metrics (`/metrics`) + Grafana dashboards
- Per-user API rate limiting (token bucket) and IP limits on public auth routes
- Circuit breaker for WB API (gobreaker) with Prometheus breaker-state metrics
- Asynq queue backlog metrics and backup textfile metrics for alerting
- Nginx reverse proxy with SSL support and security headers
- SSE endpoint (`/api/v1/events`) for real-time dashboard updates
- Telegram notifications for new recommendations and sync alerts

### Workspace Settings
- `GET /api/v1/settings` — get workspace config
- `PUT /api/v1/settings` — update thresholds, Telegram config
- `GET /api/v1/settings/thresholds` — effective thresholds with defaults

## API Contract

- Canonical OpenAPI spec: `openapi/openapi.yaml`
- Runtime spec URL: `GET /openapi.yaml`
- Runtime docs page: `GET /docs`
- The spec documents the primary routes. Router compatibility aliases are intentionally omitted.

## Commands

| Command | Description |
|---------|-------------|
| `make build` | Build API and worker binaries |
| `make test` | Run unit tests with race detector |
| `make test-integration` | Run integration tests (requires PostgreSQL) |
| `make test-cover` | Generate HTML coverage report |
| `make sqlc-generate` | Regenerate typed SQL layer |
| `make migrate-up` | Apply database migrations |
| `make migrate-down` | Rollback database migrations |
| `make backup-db` | Run PostgreSQL backup script |
| `make docker-up` | Build and start local stack |
| `make docker-down` | Stop and remove local stack |
| `make docker-monitoring` | Start Prometheus + Grafana |
| `make lint` | Run golangci-lint |
| `make gosec` | Run security scanner |
| `make pack-extension` | Package browser extension as CRX |

## CI/CD

- **CI** (`.github/workflows/ci.yml`): validates OpenAPI, builds binaries, runs tests with coverage, gosec + Trivy security scanning.
- **CD** (`.github/workflows/cd.yml`): builds Docker images, pushes to GHCR, deploys to Timeweb VPS via SSH.

## Containers

- `Dockerfile` builds either `cmd/api` or `cmd/worker` via `--build-arg TARGET=api|worker`
- Non-root `app` user, `scripts/docker-entrypoint.sh` for privilege dropping
- `docker-compose.yml` — local dev (API on :8080, nginx on :80, Prometheus on :9090, Grafana on :3000)
- `docker-compose.prod.yml` — production (GHCR images, SSL, memory limits, Redis password)
- Shared `exports` volume between API and worker

## Monitoring

- Prometheus scrapes API at `/metrics` every 10s
- Metrics: `sellico_http_requests_total`, `sellico_http_request_duration_seconds`, `sellico_http_requests_in_flight`, `sellico_wb_breaker_state`, `sellico_worker_queue_length`, `sellico_backup_last_success_timestamp_seconds`
- Grafana auto-provisions Prometheus as default datasource
- `/metrics` blocked from external access via nginx

## Notes

- Generated export files are stored under `EXPORT_STORAGE_PATH` (defaults to `./exports`).
- After adding new SQL queries, run `make sqlc-generate` to regenerate the typed layer.
- After modifying `go.mod`, run `go mod tidy` to sync `go.sum`.
