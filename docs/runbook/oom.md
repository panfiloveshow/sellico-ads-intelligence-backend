# Runbook: api / worker OOM kill

**Severity**: page (when ContainerOOMKilled fires from cAdvisor)

## Symptom

- Telegram alert `ContainerOOMKilled` for `name=~"sellico.*"`
- `/api/v1/...` returns 502 / connection refused for some seconds
- `docker compose ps` shows `api` or `worker` in "restarting" state

## Initial triage (5 min)

1. **Confirm scope** — only api? only worker? both? all containers?
   ```
   docker compose -f /opt/sellico/docker-compose.prod.yml ps
   docker compose -f /opt/sellico/docker-compose.prod.yml logs --tail=200 api worker
   ```

2. **Check current memory limit and actual usage**:
   ```
   docker stats --no-stream sellico-api sellico-worker
   ```
   Compare against the limit set in `docker-compose.prod.yml`
   (`deploy.resources.limits.memory`). At time of writing: api 1G,
   worker 384M.

3. **Check whether GOMEMLIMIT was applied**. On startup the api logs:
   ```
   memory limit set from cgroup (80% headroom)
   ```
   or
   ```
   memory limit from GOMEMLIMIT env (handled by runtime)
   ```
   Absence of either = likely runaway heap. Check `internal/app/memlimit.go`.

## Common causes

| Cause | Tell | Fix |
|-------|------|-----|
| Hot-path materialises N×M cross-ref objects | api OOMs during dashboard refresh by a tenant with many campaigns/products | Verify `attachCampaignProducts` still stores IDs only (`campaignProductIDs map[uuid.UUID][]uuid.UUID`). Recent regression possible. |
| Large `ads_read_*` query result | api OOMs after a single user opens overview | Lower `ADS_READ_ENTITY_LIMIT` / `ADS_READ_STATS_LIMIT` env (default 5000/20000). Bounce api. |
| WB sync pulls huge campaign list | worker OOMs during a sync run | Check `last_sellico_sync_at` for the affected cabinet; may need to raise worker memory limit if a tenant legitimately has more campaigns than the cap. |
| Asynq queue depth explosion | worker OOMs during recovery from API outage | Check `sellico_worker_queue_length{state="pending"}`. If > 5000 across queues, drain via `asynq` CLI or pause cron temporarily. |
| New code path with unbounded slice / map | OOM appeared after recent deploy | `git log --since='1 day' -- internal/service/` — revert candidate commit. |

## Stabilise

If api/worker is in a crash loop and we can't fix root cause in <10 min:

1. **Raise the memory limit by 50%** in `docker-compose.prod.yml`
   (`memory: 1G` → `memory: 1.5G`), `docker compose up -d` to apply.
   Buys time. Update `GOMEMLIMIT` env to 80% of new limit.

2. **Pause cron schedules** if the leak is in worker:
   ```
   docker compose exec worker /app/worker --pause-schedules   # if implemented
   ```
   Or scale worker to 0 temporarily:
   ```
   docker compose stop worker
   ```
   Backlog will accumulate in Redis but the API stays up.

## Diagnose deeply (after stabilising)

1. **Heap profile**. The api exposes pprof at `/debug/pprof/heap` (only
   reachable from the docker network). From the VPS:
   ```
   docker compose exec api wget -O /tmp/heap.prof http://127.0.0.1:8080/debug/pprof/heap
   docker cp $(docker compose ps -q api):/tmp/heap.prof ./
   go tool pprof -top heap.prof
   ```

2. **Check for goroutine leak**:
   ```
   docker compose exec api wget -O - http://127.0.0.1:8080/debug/pprof/goroutine?debug=2 | head -200
   ```

3. **Look at the panic trace** if the OOM-kill produced one:
   ```
   docker compose logs --since 30m api worker | grep -A 50 'fatal\|panic\|oom\|signal: killed'
   ```

## Post-incident

- File a postmortem in `docs/runbook/incidents/YYYY-MM-DD-oom.md` (template: 5-whys; what alerted you; what was tried; what worked; what we'll change).
- If the cause was a new code path, add a regression test or memory budget assertion.
- If the cause was tenant growth, either revisit the per-query caps or
  shard the workspace.

## What NOT to do

- Don't `docker system prune -af` to free disk in a memory incident — it
  doesn't help and removes images you might need to roll back.
- Don't disable `GOMEMLIMIT` or remove the cgroup limit — that just makes
  the kill silent and harder to diagnose.
- Don't rotate the encryption key during an incident; key rotation is
  read+write-heavy on `seller_cabinets` and could mask the real cause.
