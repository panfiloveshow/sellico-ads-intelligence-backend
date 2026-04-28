# ADR-0004: REST + OpenAPI, not GraphQL

- **Status**: Accepted
- **Date**: 2026-03-23
- **Authors**: backend team
- **Deciders**: tech lead, frontend lead

## Context

Two consumers of the API exist now: the React dashboard (a single client
team builds it alongside the backend) and the browser extension. A third
might appear: a future native mobile app. All consumers are first-party.

The data model is straightforward CRUD-plus-aggregation: workspaces,
campaigns, products, phrases, recommendations. No deeply nested graphs, no
mobile-bandwidth obsession, no third-party developers shaping queries we
can't predict.

## Decision

Stay with REST. Document the contract in `openapi/openapi.yaml`. Generate
the TypeScript client from it (`pnpm openapi:generate`). Keep the API
flat: most endpoints map to a single domain object or aggregation.

A drift-check tool (`tools/check-openapi-drift`) runs in CI to make sure
the spec keeps up with the router.

## Alternatives considered

| Option | Pros | Cons | Why rejected |
|--------|------|------|--------------|
| REST + OpenAPI (chosen) | Zero new tooling; works with curl, browser DevTools, any HTTP lib; trivial caching at nginx; spec-first generates clients on demand | N+1 risk if a screen needs deep aggregations | Caching + dedicated aggregation endpoints (`/ads/overview`) handle this |
| GraphQL | One endpoint; clients pick fields; introspection | Caching is hard; auth gets stapled per-resolver; resolver N+1; tooling overhead; no real benefit while consumer count = 2 | Adds complexity without solving a real problem we have |
| gRPC | Strong typing, streaming, fast | Browser support requires gRPC-web shim; debug story worse | Not worth the friction for a browser-first product |
| RPC over JSON (no spec) | Fastest to add endpoints | Discoverability and client codegen suffer | Drift would be silent; we'd lose the API as a reviewable artefact |

## Consequences

- **Good**: handlers stay readable; integration tests are HTTP recordings;
  any contributor can curl the API and learn it.
- **Good**: aggregations live in the API server (`AdsReadService`), so
  optimisation work has one home.
- **Bad**: when a screen needs three resources, the client makes three
  requests. We accept this — most screens already use TanStack Query
  parallelism, so wall-clock cost is one round-trip.
- **Bad**: when adding a new field to an aggregation, the OpenAPI schema
  must be updated — the drift checker enforces this.

If we ever ship a public API for third-party developers, revisit this
decision. GraphQL or hypermedia (HAL/JSON:API) might justify their cost
at that point.

## How we'll know it worked

- Frontend developers can ship a new page without backend changes when
  the data already exists at an existing endpoint.
- API drift check stays at green (no new whitelist entries) over time.
- p95 of "page-load critical path" requests stays under 500 ms.

## Links

- Spec: `openapi/openapi.yaml`
- Drift check: `tools/check-openapi-drift/`
- Frontend consumes the spec from a separate repository (out of scope for this backend).
