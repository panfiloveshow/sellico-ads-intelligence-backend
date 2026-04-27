# Engineering onboarding

This guide gets a new engineer productive in ~2 hours. Read this once,
then `docs/ARCHITECTURE.md` for the deep dive.

## Local setup

### Prerequisites

- Go ≥ 1.25 (`go version`)
- Docker + Docker Compose v2
- Make
- Node ≥ 20.11 (only if working on the frontend)
- Optional: `golangci-lint`, `sqlc`, `imagemagick`

### First-time bootstrap

```bash
git clone git@github.com:panfiloveshow/sellico-ads-intelligence-backend.git
cd sellico-ads-intelligence-backend
cp .env.example .env       # fill in JWT_SECRET, ENCRYPTION_KEY, etc.

make docker-up             # starts postgres + redis + api + worker + nginx
make migrate-up            # apply DB migrations (auto-runs in compose)

# verify
curl -s http://localhost:8080/health/ready
```

For frontend dev:
```bash
cd frontend
pnpm install
pnpm dev                   # http://localhost:5173
```

## Daily workflow

```bash
make test                  # unit tests + race detector
make lint                  # golangci-lint
go run ./cmd/api           # run api standalone (no docker)
go run ./cmd/worker        # run worker standalone
```

To regenerate the typed SQL layer after editing a `.sql` file:
```bash
make sqlc-generate
```

## Adding a new endpoint

Walking through "add `GET /api/v1/foo/bar`":

1. **Write the SQL query** in `internal/repository/sqlc/queries/foo.sql`,
   then `make sqlc-generate`.
2. **Add a service method** in `internal/service/foo.go`:
   ```go
   func (s *FooService) GetBar(ctx context.Context, ...) (*domain.Bar, error) { ... }
   ```
3. **Add a handler** in `internal/transport/handler/foo.go` that calls
   the service and wraps the response with `envelope.Wrap`.
4. **Wire it in the router** (`internal/transport/router.go`):
   ```go
   r.Get("/foo/bar", deps.FooHandler.GetBar)
   ```
5. **Document in `openapi/openapi.yaml`** — add the path under `paths:`,
   define the response schema under `components.schemas`.
6. **Update the drift check baseline** if needed:
   `go run ./tools/check-openapi-drift` — it'll tell you what to whitelist
   (or fix).
7. **Tests**:
   - Unit test the service in `internal/service/foo_test.go` (mock the queries).
   - Handler test in `internal/transport/handler/foo_test.go` if there's
     non-trivial routing/serialization logic.
8. **Frontend** (if applicable): regenerate the TypeScript client with
   `pnpm openapi:generate`, then use `api.GET('/api/v1/foo/bar')` in a
   new TanStack Query.

## Adding a new background job

1. **Define the task type** in `internal/worker/tasks.go` with a constant
   and a payload struct.
2. **Write the processor** in `internal/worker/processor_foo.go`:
   ```go
   func (p *FooProcessor) ProcessTask(ctx context.Context, t *asynq.Task) error { ... }
   ```
3. **Register in `cmd/worker/main.go`**: `mux.HandleFunc(TaskFoo, foo.ProcessTask)`.
4. **Enqueue from the API** via `asynq.Client`, or from a cron schedule
   in `cmd/worker/main.go`.

## Repository layout

See `docs/ARCHITECTURE.md` § 3 (Layered package structure) for the
canonical map. TL;DR:

- `internal/domain` — pure types, no I/O.
- `internal/service` — business logic.
- `internal/repository` — DB access (sqlc-generated).
- `internal/integration` — third-party clients (WB, Sellico, Telegram).
- `internal/transport` — HTTP layer (chi router, handlers, DTOs).
- `internal/pkg` — reusable utilities (jwt, crypto, validation, …).
- `internal/worker` — asynq processors.
- `internal/app` — bootstrap (DB, Redis, logger, memlimit).

## Where things live

| Need | Path |
|------|------|
| Add a config option | `internal/config/config.go` + `.env.example` |
| Add a DB migration | `migrations/0000XX_<name>.{up,down}.sql` |
| Add a SQL query | `internal/repository/sqlc/queries/*.sql`, then `make sqlc-generate` |
| Update the API contract | `openapi/openapi.yaml` |
| Add an alert rule | `monitoring/alerts.yml` |
| Add an ADR | `docs/adr/000X-<slug>.md` (use `0000-template.md`) |

## Deploying

CI/CD (GitHub Actions) auto-deploys `main` to the production VPS via SSH.
For manual deploys:
```bash
ssh ops@72.56.250.9
cd /opt/sellico
./scripts/deploy.sh update      # pull + restart api + worker
./scripts/deploy.sh restart     # full down + up
./scripts/deploy.sh logs api    # tail logs
```

See `docs/deployment/{ssl,backups,key-rotation}.md` for cert setup,
backup configuration, and key rotation runbooks.

## Getting help

- Architecture questions: `docs/ARCHITECTURE.md` and the relevant ADR.
- Debug guide: `docs/ARCHITECTURE.md` § 12 ("Where to look when debugging X").
- Incident response: `docs/runbook/`.
- Open an issue with the `question` label.
