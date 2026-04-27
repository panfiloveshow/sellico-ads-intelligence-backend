# Sellico Ads Intelligence — Frontend

React 19 + TypeScript + Vite + MUI v6 + TanStack Query 5. Built per the spec at
`../frontend-spec/ARCHITECTURE.md`.

## Local dev

Requires Node 20.11+. Recommended package manager: pnpm (npm/yarn also work).

```bash
cd frontend
pnpm install         # or: npm install
pnpm dev             # http://localhost:5173, proxies /api → http://localhost:8080
```

The dev server proxies `/api`, `/openapi.yaml`, and `/docs` to the local Go
backend (`go run ./cmd/api`). To target a deployed API instead, set
`VITE_API_BASE_URL=https://api.sellico.ru` in `frontend/.env.local`.

## Build for production

```bash
pnpm build           # produces frontend/dist/
```

The bundle is served by the same nginx container as the API in prod
(`docker-compose.prod.yml`, Sprint 6 will wire the mount). The standalone
`Dockerfile` here exists for ad-hoc preview deploys.

## OpenAPI client generation

The TypeScript client is generated from `../openapi/openapi.yaml`:

```bash
pnpm openapi:generate
```

This overwrites `src/api/schema.gen.ts`. The generated file is committed so
casual contributors don't need the codegen toolchain on first checkout.

## Project layout

```
src/
  main.tsx               // react-dom render, providers (Theme, Query, Router)
  theme.ts               // MUI palette + typography per spec
  App.tsx                // routes table, RequireAuth guard
  api/
    client.ts            // openapi-fetch instance with auth interceptor
    schema.gen.ts        // generated, do not hand-edit
  lib/
    auth.tsx             // AuthProvider + useAuth hook
    authTokens.ts        // in-memory access token + refresh single-flight
  components/layout/     // AppLayout shell (sidebar + topbar)
  pages/
    auth/LoginPage.tsx
    dashboard/CommandCenterPage.tsx
```

## Sprint roadmap

This scaffold is **Sprint 5** of the v1.0 roadmap — auth flow + layout shell
+ command-center stub. Subsequent sprints fill in:

- **Sprint 6** — Command Center + Entity Detail with live data, virtualised
  tables, charts (Recharts), SSE for real-time updates.
- **Sprint 7** — Recommendations, Settings, RBAC, a11y pass, i18n,
  Lighthouse ≥ 90, Playwright e2e.
