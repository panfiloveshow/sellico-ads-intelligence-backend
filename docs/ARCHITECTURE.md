# Sellico Ads Intelligence — Architecture

This document describes how the backend is organised, how data flows through
the system, and where to look when you need to extend or debug a particular
behaviour. It is companion to the API contract (`openapi/openapi.yaml`) and
the recommendation rules (`internal/service/recommendation.go`).

## 1. High-level shape

```
                     ┌──────────────────┐
       (browser)     │  React frontend  │  ← Sprint 5 scaffold, /frontend
                     └──────────┬───────┘
                                │ HTTPS
                  ┌─────────────▼─────────────┐
   :443  ────────►│   nginx 1.27 (alpine)     │   TLS terminator + rate limit
                  │   nginx.prod.conf         │   security headers + HSTS
                  └─────┬──────────┬──────────┘
                        │ http     │ http
            ┌───────────▼─┐  ┌─────▼──────┐
            │  api (Go)   │  │ worker (Go)│   asynq processors,
            │ chi + pgx   │  │ asynq mux  │   recommendation engine,
            │ /metrics    │  │ schedules  │   bid automation, exports
            └────┬────────┘  └──────┬─────┘
                 │                  │
                 │       ┌──────────┴───────────┐
                 │       │                      │
            ┌────▼───────▼──┐         ┌─────────▼────────┐
            │ PostgreSQL 16 │         │   Redis 7        │
            │ (sqlc-typed)  │         │ (asynq queue,    │
            │ pgxpool 25/5  │         │  rate buckets)   │
            └───────────────┘         └──────────────────┘
                                          ▲
                  ┌───────────────────────┼───────────┐
                  │ external integrations │           │
                  │                       │           │
            ┌─────▼─────┐    ┌──────────▼──┐    ┌────▼────────┐
            │ WB API    │    │ Sellico SSO │    │ Telegram    │
            │ (advert + │    │  bridge     │    │ (notifications,
            │  content) │    │             │    │  alerts)    │
            └───────────┘    └─────────────┘    └─────────────┘

            Observability:  prometheus + alertmanager (telegram)
                            cAdvisor + node-exporter   →   grafana
```

## 2. Process layout (`cmd/`)

| Binary | Entry point | Listens | Purpose |
|--------|-------------|---------|---------|
| `api`    | `cmd/api/main.go`    | `:8080`         | Public HTTP API, SSE, `/metrics`, `/health/{live,ready}`. Owns no background work — every long task is enqueued onto Asynq. |
| `worker` | `cmd/worker/main.go` | `:8081` (health) | Asynq queues consumer + cron schedulers. Does WB syncs, recommendations, bid automation, exports. |
| `rotate-encryption-key` | `cmd/rotate-encryption-key/main.go` | — | One-shot admin tool. Re-encrypts WB tokens onto the latest keyring version. |
| `debug-sync` | `cmd/debug-sync/main.go` | — | Diagnostic CLI for sync-flow troubleshooting. |

API and worker share `internal/app.NewDependencies` for pool wiring,
logging, and `applyMemoryLimit()` (cgroup-aware). Both can run side-by-side
(`docker compose up`) or independently.

## 3. Layered package structure (`internal/`)

```
cmd/api ────┐
cmd/worker ─┤
            ▼
   internal/app           ← bootstrap (DB pool, redis, logger, memlimit)
            │
            ▼
   internal/transport     ← chi router, middlewares, handlers, DTOs
            │
            ▼
   internal/service       ← domain services (auth, workspaces, ads_read,
            │                campaign actions, recommendation engine, …)
            ▼
   internal/repository ───► sqlc-generated typed queries + manual repos
   internal/integration ──► wb (with circuit breaker), sellico, telegram, catalog
   internal/domain ───────► plain types (no I/O); shared by all layers
   internal/pkg ──────────► reusable utilities (crypto, jwt, validate,
                                         pagination, metrics, envelope, apperror)
   internal/worker ──────► asynq processors + cron handlers (driven by service)
```

**Hard rules**:
- `domain` knows nothing about anything else.
- `service` only depends on `domain`, `repository`, `integration`, `pkg`.
- `transport` only depends on `service`, `domain`, `pkg`.
- `worker` glues `service` + `integration`; never imports `transport`.

These boundaries are enforced informally via review (no automated import-cycle
guard yet). If you find yourself needing to break a boundary, that's a
signal the abstraction is wrong — bring it up in code review.

## 4. Request flow (typical read)

`GET /api/v1/ads/overview?date_from=…&date_to=…`

```
nginx (TLS, rate limit, headers)
  → middleware: tracing, recovery, auth (JWT), rbac, request id
    → AdsHandler.Overview (transport/handler)
      → AdsReadService.Overview (service/ads_read.go)
        → loadWorkspaceData (singleflight + 30s in-memory cache)
          → 8× parallel queries via errgroup:
              - cabinets, campaigns, products, phrases
              - campaign_stats, phrase_stats (date-filtered in SQL)
              - extension_evidence
              - latest auto-sync summary
          → assemble adsWorkspaceData (IDs only for cross-ref maps to
            keep heap bounded — see `attachCampaignProducts`)
          → cache + return
      → buildCabinetSummaries / buildProductSummaries / … (ads_read_builders.go)
      → aggregateWorkspaceMetrics (current + previous period in one pass)
      → trim/sort/classify (ads_read_filters.go, ads_read_classify.go)
    ← envelope.Wrap + JSON
```

Memory budget for this path: bounded by `ADS_READ_ENTITY_LIMIT` (5000) and
`ADS_READ_STATS_LIMIT` (20000). Singleflight collapses concurrent hits for
the same workspace+date window into one DB load.

## 5. Background work (asynq)

Queues defined in `cmd/api/main.go`:

| Queue | Triggered by | Handler |
|-------|--------------|---------|
| `WBSyncWorkspace`     | API: cabinet add/sync, manual; cron @1h | sync.go full workspace pull |
| `WBCampaigns`         | parent sync                              | campaign list + stats |
| `WBCampaignStats`     | parent sync                              | per-campaign daily stats backfill |
| `WBPhrases`           | parent sync                              | phrase list per campaign |
| `WBProducts`          | parent sync                              | product list per cabinet |
| `Recommendations`     | cron @2h, post-sync trigger              | rule engine → notifications |
| `Exports`             | API: POST /exports                       | XLSX/CSV file generation |

Cron schedules are env-driven (`SYNC_INTERVAL`, `RECOMMENDATION_INTERVAL`,
`BID_AUTOMATION_INTERVAL`). All cron jobs are idempotent — they short-circuit
if a recent JobRun for the same workspace/scope is in flight or just completed.

## 6. WB API integration (`internal/integration/wb/`)

| Concern | Implementation |
|---------|----------------|
| Per-token rate limit | `boundedLRU[*rate.Limiter]`, 1000-key cap, 1h TTL |
| Per-token circuit breaker | `boundedLRU[*gobreaker.CircuitBreaker]`, same cache shape |
| Retry | exponential backoff (1s, 3s, 9s), respects `Retry-After` on 429 |
| Auth | bearer in `Authorization` header (encrypted at rest in DB) |
| Cancellation | every call takes `ctx`; HTTP client timeout 30s |
| Endpoint version policy | `/adv/v0/*` (official; `/v1/normquery` rolled back per f9477b4) |

## 7. Recommendation engine (`internal/service/recommendation*.go`)

Rule-based and explainable. Each rule:
- inspects entity stats (CPC, CTR, CPO, position trend, etc.)
- emits `domain.Recommendation` with severity + confidence + reason text + suggested action
- attaches "evidence" (source rows + thresholds) so the UI can show "why"

Rule families currently live in:
- `recommendation.go` — base engine, dedup, severity classification
- `recommendation_extended.go` — bid raise/lower with mutual exclusion
- `bid_automation.go` — strategy execution (ACOS / ROAS / dayparting)

To add a rule: implement the `Rule` interface, register in the engine
constructor. No DB migration needed unless you store extra data.

## 8. Storage

PostgreSQL 16, schema in `migrations/`. 15 migrations (000001–000015) with
matching down-migrations. Generated typed access layer in
`internal/repository/sqlc/` via `make sqlc-generate`.

WB tokens in `seller_cabinets.encrypted_token` are AES-256-GCM under the
keyring (`internal/pkg/crypto/aes.go`). Wire format `v<N>:<base64>`; legacy
unversioned ciphertext continues to decrypt. Rotation via
`cmd/rotate-encryption-key`.

Redis 7 backs Asynq queues + per-token rate buckets. No application data
relies on Redis as primary store — it's a strict cache + queue.

## 9. Observability stack

- `/metrics` exposes `sellico_http_*`, `sellico_wb_*`, `sellico_worker_*`,
  pgxpool stats, Go runtime metrics.
- Prometheus scrapes 10s, 30d retention.
- `monitoring/alerts.yml` — 9 rules across api, runtime, data-plane.
- Alertmanager → Telegram (page vs warn severity routing, inhibit rules).
- cAdvisor + node-exporter for per-container and host metrics.
- Grafana behind nginx auth (Sprint 6 wiring); dashboards in
  `monitoring/grafana/provisioning/`.

## 10. Frontend (`/frontend`, scaffolded in Sprint 5)

React 19 + Vite + MUI v6 + TanStack Query 5. Auth: in-memory access token,
HttpOnly refresh cookie, single-flighted refresh on 401. Talks to the API
on the same origin in production (nginx serves bundle); dev proxy to `:8080`.

## 11. Browser extension (`/extension/chromium`, hardened in Sprint 8)

MV3, two host patterns only (`*.wildberries.ru`), permissions reduced to
`storage` + `cookies`. CRX packaging via `scripts/pack-extension.sh`,
GitHub Actions release flow on `extension-v*` tags. See
`extension/chromium/{PRIVACY.md,CHROME_WEB_STORE.md}`.

## 12. Where to look when debugging X

| Symptom | First place to look |
|---------|---------------------|
| 500 on a route | `internal/transport/handler/` for the route, then service called |
| Slow `/api/v1/ads/*` | `internal/service/ads_read_loader.go` (cache hit rate, parallel query timing) |
| WB sync stuck | `internal/integration/wb/breaker.go` state, `internal/service/sync.go` |
| OOM | `internal/app/memlimit.go` reported limit; cgroup vs Go heap; `boundedLRU` sizes |
| Auth refresh loop | `internal/transport/middleware/auth.go` (server-side); frontend lives in a separate repo |
| Token decrypt failure | `internal/pkg/crypto/aes.go` — version prefix + keyring contents |

## 13. Where to look in production

- Container logs: `docker compose -f docker-compose.prod.yml logs -f api worker`
- Recent recommendations: `psql … -c "select * from recommendations order by created_at desc limit 20;"`
- Backup status: `/var/log/sellico-backup.log` and `/var/log/sellico-restore-check.log`
- Grafana dashboard: via SSH tunnel `ssh -L 3000:grafana:3000 ops@host`

See `docs/runbook/` for incident-specific procedures.
