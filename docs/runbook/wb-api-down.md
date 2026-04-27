# Runbook: WB API down or degraded

**Severity**: warn (escalates to page if workspaces miss > 24h of sync)

## Symptom

- Telegram alert `WBCircuitBreakerOpen` for ≥ 5 min
- User-visible: campaign data shows "last sync 2h ago" in the dashboard
- Logs: repeated `wb api error: server error (5xx)` or `circuit breaker open`

## How the system protects itself

- **Circuit breaker** (`internal/integration/wb/breaker.go`): per-token,
  trips after 5 consecutive failures, half-open after 30s, full close
  after 2 successful trial calls.
- **Retry**: exponential backoff (1s, 3s, 9s) for 429 / 5xx.
- **Rate limit**: per-token bucket (default 10 req/s) — clients respect
  `Retry-After` from WB.

So a brief WB blip (≤ 1 minute) is invisible to users. The alert fires
when degradation lasts longer.

## First steps

1. **Is it WB or us?** Check from the VPS:
   ```
   curl -I https://advert-api.wildberries.ru/  # WB main
   curl -I https://content-api.wildberries.ru/ # WB content
   ```
   Both should be 200/3xx. If they timeout, it's WB.

2. **Check the WB status channel**: https://t.me/wildberriesnews or
   sellers' chat.

3. **Look at the breaker state**: query Prometheus
   ```
   max(sellico_wb_breaker_state) by (token_prefix)
   ```
   - 0 = closed (healthy)
   - 1 = half-open (recovering)
   - 2 = open (refusing calls for 30s+)

## During the outage

- **Don't** disable the breaker — it's there to protect against thundering
  herds when WB recovers.
- **Don't** lower the rate limit — that just slows recovery.
- **Do** post a status banner in the frontend (Settings → System Status)
  if the outage exceeds 30 minutes.

## After WB recovers

- Breakers close automatically on first success after the 30s timeout.
- Sync queue catches up — `sellico_worker_queue_length{state="pending"}`
  should drain within 15 min for normal load.
- If pending stays > 1000 for > 30 min: scale worker temporarily, or
  inspect for stuck handlers (`docker compose logs worker | grep -i panic`).

## Post-incident

- Note the WB outage window in `docs/runbook/incidents/`.
- If WB issues are increasing in frequency, consider raising the breaker
  failure threshold from 5 → 10 to reduce false positives.
- If sync data integrity is suspect (e.g. WB returned partial data
  during recovery), trigger a fresh full sync for affected workspaces:
  ```
  curl -X POST https://api.sellico.ru/api/v1/seller-cabinets/{id}/sync \
       -H "Authorization: Bearer ..."
  ```
