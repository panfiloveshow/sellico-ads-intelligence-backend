# Sellico integration — service-account pattern

This backend follows the same Sellico integration pattern as the
`financial-dashboard` project (the reference Laravel app). All API contracts
documented in `financial-dashboard/rules.md` and `backandrules.md`.

## Configuration (`.env`)

| Variable | Required | Purpose |
|---|---|---|
| `SELLICO_API_BASE_URL` | yes (default `https://sellico.ru/api`) | Upstream base URL |
| `SELLICO_API_TOKEN` | one-of | Static service-account bearer (preferred in prod) |
| `SELLICO_EMAIL` + `SELLICO_PASSWORD` | one-of | Backend auto-logs-in via `POST /api/login` and caches the token for 23h |

If neither auth path is configured, the service-account features are
silently disabled — the system still boots and the worker runs the legacy
per-user discovery path (no-op when no workspace has cached a Sellico
personal token).

## What's implemented now

### `internal/integration/sellico/service_account.go`

Six new client methods, all using the service-account bearer or a
user-supplied bearer where the upstream requires it:

| Method | Endpoint | Auth |
|---|---|---|
| `Login(email, password)` | `POST /api/login` | none |
| `CurrentUser(userToken)` | `GET /api/user` | user bearer |
| **`CollectorIntegrations(serviceToken)`** ⭐ | `GET /api/collector/integrations` | service bearer |
| `GetIntegrations(serviceToken, workspaceID?)` (deprecated) | `GET /api/get-integrations/{ws?}` | service bearer |
| `GetIntegration(serviceToken, id)` | `GET /api/get-integration/{id}` | service bearer |
| `CheckPermission(serviceToken, params)` | `GET /api/check-permission` | service bearer + body |
| `CreateActivity(userToken, ws, payload)` | `POST /api/workspaces/{ws}/activities` | user bearer |
| `ListWorkspaceIntegrations(userToken, ws)` | `GET /api/workspaces/{ws}/integrations` | user bearer |

**Discovery uses `CollectorIntegrations`** — single HTTP call returns every
integration on the platform; `IntegrationRefreshService` groups them by
Sellico work_space_id and joins to local workspaces via `external_workspace_id`.
Much cheaper than the per-workspace loop the legacy `GetIntegrations` required.

### `internal/integration/sellico/service_token.go`

`ServiceTokenManager` — single source of truth for the service-account
bearer:
- Static `SELLICO_API_TOKEN` is returned as-is forever.
- Otherwise calls `Login(email, password)`, caches result for 23h.
- `Invalidate()` (called on `ErrUnauthorized`) forces a fresh `/login`
  on next `Get()`.
- Concurrent-safe via `sync.Mutex`; `Get()` is the cache-hit hot path.

### `internal/service/integration_refresh.go`

Worker auto-discovery service. Two paths can run side-by-side:

- **Legacy** `RefreshAllWorkspaces(ctx)` — iterates workspaces with cached
  per-user tokens, calls `ListWorkspaceIntegrations(userToken, externalWorkspaceID)`.
- **New** `RefreshViaServiceAccount(ctx)` — iterates workspaces with
  `external_workspace_id`, uses the service-account token to call
  `GetIntegrations(serviceToken, externalWorkspaceID)`.

Single entry-point `Refresh(ctx)` — invokes the service-account path when
configured, then always invokes the legacy path.

Both paths upsert via `UpsertSellicoSellerCabinet` (dedup by
`external_integration_id`), so they are idempotent and safe to alternate.

### `cmd/worker/main.go`

Wires the manager and chains `WithServiceAccount(mgr)` onto the refresh
service when `IsConfigured()` is true. Logs which mode is active at
startup so `docker compose logs worker | head` immediately shows the
operating state.

## What's NOT implemented yet (next session)

### `CheckPermission` HTTP middleware (currently using local JWT)

Reference `CheckPermission` middleware (in
`financial-dashboard/app/Http/Middleware/CheckPermission.php`) uses a route
permission map and proxies every authenticated request through Sellico
`GET /check-permission`:

```
[ROUTE_PERMISSION_MAP]
GET /api/dashboard/summary  → finance.dashboard.view
GET /api/products/*         → finance.products.view
PUT /api/products/*         → finance.products.edit
... etc
```

For each request:
1. Read `X-Token`, `X-User-Id`, `X-Workspace-Id` from headers.
2. Look up the required permission slug for the (method, path) pair.
3. Call `sellico.CheckPermission(serviceToken, params)`.
4. Sanity-check that the supplied token actually belongs to the supplied
   user_id (call `sellico.CurrentUser(token)` and compare `id`).
5. On 2xx, set context attributes (`sellico.token`, `sellico.workspace`,
   `sellico.user`) for downstream services.
6. After response (terminate hook), call
   `sellico.CreateActivity(userToken, workspaceID, payload)`.

### Migration checklist (when ready to switch from local JWT)

1. **Add the route → permission map** for our endpoints, modelled on the
   reference. Example mapping (incomplete):
   - `GET /api/v1/ads/overview` → `ads.dashboard.view`
   - `GET /api/v1/recommendations` → `ads.recommendations.view`
   - `POST /api/v1/recommendations/{id}/apply` → `ads.recommendations.edit`
   - `GET /api/v1/seller-cabinets` → `integrations.view`
   - `POST /api/v1/seller-cabinets` → `integrations.edit`

2. **New middleware** `internal/transport/middleware/sellico_permission.go`:
   - Replaces `Auth` middleware in router protected groups.
   - Reads `X-Token`, `X-User-Id`, `X-Workspace-Id`.
   - Calls `sellico.CheckPermission` + `sellico.CurrentUser` (cached 2-5min).
   - Sets `UserIDKey` (mapped to local user, see step 4).
   - Skips request if no permission required (e.g. /health).

3. **Cache layer** for `CheckPermission` and `CurrentUser`:
   - Redis-backed (already in dependencies), key by sha256(token+user+ws+permission).
   - TTL 2 minutes (matches reference, balances revoke-speed vs Sellico load).

4. **Local user provisioning**:
   - On first request from a new Sellico user, upsert a row in `users`
     keyed by `external_user_id` (Sellico user_id).
   - All downstream code already uses `users.id` (UUID) → no churn.

5. **Activity hook** (after response):
   - `internal/transport/middleware/sellico_activity.go` reads context,
     fires `sellico.CreateActivity` async (don't block response).

6. **Remove (or keep flag-gated) local auth**:
   - `/auth/register`, `/auth/login`, `/auth/refresh`, `/auth/me` —
     either delete entirely, or keep behind a `DEV_LOCAL_AUTH=true` flag
     for local dev when sellico.ru is unavailable.

7. **Frontend update**:
   - Stop calling `/api/v1/auth/*` — get the token from Sellico's main app.
   - Send `X-Token` + `X-User-Id` + `X-Workspace-Id` on every request.
   - Refresh token via Sellico's flow (out of scope for this backend).

This is roughly a 1-day effort plus testing. Tracked separately so this
PR stays focused on the discovery path.
