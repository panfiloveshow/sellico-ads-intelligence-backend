# ADR-0001: singleflight + 30s in-memory cache in `AdsReadService`

- **Status**: Accepted
- **Date**: 2026-04-06
- **Authors**: backend team
- **Deciders**: tech lead

## Context

The dashboard makes 6–10 parallel reads of `/api/v1/ads/*` per page load
(overview, top products, top campaigns, top queries, …). Each read called
`loadWorkspaceData()` which runs 8 parallel SQL queries (cabinets, campaigns,
products, phrases, two stats tables, extension evidence, last sync).

Without coalescing, a single F5 by an active user kicked off **80+ DB
queries** for the same workspace+date range, peaking the pgxpool and
spiking memory because every parallel goroutine materialised a full
`adsWorkspaceData` (≈10–80 MB depending on tenant size).

This was the proximate cause of the OOM crashes documented in commits
44a7449 → 8bf53d6.

## Decision

`AdsReadService` carries:
1. `singleflight.Group` keyed by `workspace+date_from+date_to` — collapses
   N concurrent loaders into 1.
2. `sync.RWMutex`-guarded `dataCache` with 30 s TTL — serves the dashboard's
   second-tab-open / second-poll for free.
3. Cross-reference maps `campaignProductIDs` / `productCampaignIDs` store
   only `uuid.UUID` slices — full structs are looked up on demand via
   `productByIDMap()` / `campaignByIDMap()`.

Cache eviction: passive — entries expire on next read; size cap of 50 to
bound footprint.

## Alternatives considered

| Option | Pros | Cons | Why rejected |
|--------|------|------|--------------|
| singleflight + in-memory cache (chosen) | Zero infra; works for both API replicas as long as they share a workspace owner; instant cache hits | Stale data up to 30 s; cache lives per-process | Best return for a 30-min change |
| Redis cache | Shared across replicas, longer TTL possible | Adds a network round-trip in the hot path; serialisation overhead for the 10–80 MB struct | Worse for current scale (single api replica); revisit at multi-replica |
| Materialised view in PG | DB does the work; consistent | Refresh latency, locks during refresh, schema migration burden | Doesn't help the goroutine fan-out; would still need singleflight |
| Pull data once at session start, cache in browser | Eliminates server-side load entirely | Massive payload over the wire; freshness becomes hard | Worse UX |

## Consequences

- **Good**: ~80× reduction in DB calls per dashboard refresh. OOM from
  parallel materialisation gone. p95 latency on `/ads/overview` dropped
  from ~2.2 s to ~280 ms (warm cache).
- **Bad**: 30-second freshness lag on the dashboard. Surfaced in product
  copy ("data refreshed every 30 s"). For real-time, the SSE channel
  (`/api/v1/events`) is the right tool; ads_read explicitly opts out.
- **Coordination**: any new method on `AdsReadService` should accept the
  shared `adsWorkspaceData`, not call repository directly. This is the
  rule that file decomposition (ADR-0005, planned) protects.

## How we'll know it worked

- `sellico_http_request_duration_seconds{handler="/api/v1/ads/overview"}` p95 < 500 ms with N concurrent users
- No OOM kill in `container_oom_events_total{name=~"sellico.*"}` for ≥ 30 days
- pgxpool `connections_in_use` stays < 50% of pool size during peak

## Links

- Commits: `15b161a`, `8bf53d6`
- File: `internal/service/ads_read_loader.go`
