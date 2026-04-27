# Runbook: PostgreSQL container down or unreachable

**Severity**: page (cascades into APIDown alert)

## Symptom

- Telegram alert `APIDown` (or both `APIDown` + `WBCircuitBreakerOpen`)
- API health: `curl http://127.0.0.1:8080/health/ready` returns 503
- Logs show `database not ready: ping postgres: ...`

## First 5 minutes

1. **Is the container running?**
   ```
   docker compose -f /opt/sellico/docker-compose.prod.yml ps postgres
   ```
   - "Up": go to step 2.
   - "Exited (...)": `docker compose logs --tail=100 postgres` to see why.
   - "Restarting": healthcheck failing — usually disk full or corruption.

2. **Disk full?** Almost always the culprit when PG dies suddenly.
   ```
   df -h /
   du -sh /var/lib/docker/volumes/marketing_pgdata/_data
   ```
   If `/` is < 10% free → see "Disk full" below.

3. **Can we connect from the API container?**
   ```
   docker compose exec api wget -q -O - http://127.0.0.1:8080/health/ready
   ```

## Recovery

### Disk full

1. Free space first — drop old docker images and dangling volumes:
   ```
   docker image prune -f
   docker volume prune -f         # WARNING: removes anonymous volumes only — pgdata is named, safe
   ```
2. Trim docker logs (default they grow forever):
   ```
   for c in $(docker ps -q); do docker inspect "$c" --format '{{.LogPath}}'; done | xargs -r truncate -s 0
   ```
3. If still tight, prune old backups locally (offsite copy is in S3, see
   `docs/deployment/backups.md`):
   ```
   find /opt/sellico/backups -name '*.dump' -mtime +3 -delete
   ```

### Container crashed but volume intact

```
docker compose up -d postgres
docker compose logs -f --tail=50 postgres
# wait for "database system is ready to accept connections"
docker compose up -d --force-recreate api worker
```

### Volume corrupted (rare; only if PG refuses to start)

1. **STOP** — this is destructive. Get the on-call lead's approval.
2. Restore from the latest verified backup:
   ```
   # Find newest local backup
   ls -t /opt/sellico/backups/*.dump | head -1

   # Or pull from S3 (see docs/deployment/backups.md for full procedure)
   aws --endpoint-url=https://storage.yandexcloud.net s3 ls s3://sellico-backups/postgres/ | tail -1
   ```
3. Move the bad data dir aside (don't delete — for forensics):
   ```
   docker compose stop postgres
   sudo mv /var/lib/docker/volumes/marketing_pgdata/_data /var/lib/docker/volumes/marketing_pgdata/_data.bad-$(date +%Y%m%d)
   sudo mkdir /var/lib/docker/volumes/marketing_pgdata/_data
   ```
4. Restore:
   ```
   docker compose up -d postgres
   docker compose run --rm migrate
   pg_restore -h localhost -U sellico -d sellico --no-owner --no-privileges /opt/sellico/backups/latest.dump
   docker compose up -d --force-recreate api worker
   ```

### Healthcheck failing under load

If postgres is up but the readiness probe times out (alert `APIDown`
without `ContainerOOMKilled`):
- Check connection count: `psql -c 'SELECT count(*) FROM pg_stat_activity;'`
- Compare against pgxpool max (`DB_MAX_CONNS`, default 25, × api replicas)
  + extras for migrate / worker
- If close to PG's `max_connections` (default 100), bump pgxpool max
  carefully or postgres `max_connections` and restart.

## Post-incident

- Verify backup integrity: `scripts/restore-check.sh` should be green.
- Update `docs/runbook/db-down.md` with anything that wasn't here.
- If volume corruption: file a vendor ticket with Timeweb if the host
  was the cause (kernel panic, fs corruption).
