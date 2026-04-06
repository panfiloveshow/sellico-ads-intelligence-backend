# Sellico Extension — QA checklist (internal)

This document is for **internal QA** of the unpacked MV3 extension located in `extension/chromium`.

## Definition of Done (DoD)

Consider the extension “QA-ready” when all items below are green on at least 2 different WB pages.

- **Installability**: can be installed via `Load unpacked` without errors in `chrome://extensions`.
- **Configuration**: Options accept and persist `backendUrl`, `accessToken` (raw JWT), `workspaceId` (UUID), `autoCapture`.
- **Non-invasive behavior**: WB cabinet remains usable even if Sellico backend is down / returns errors.
- **Session**: `POST /api/v1/extension/sessions` succeeds (201).
- **Context**: `POST /api/v1/extension/page-context` succeeds (201) after page load and after SPA navigation.
- **Widgets**: at least one widget request succeeds (200):
  - `GET /api/v1/extension/widgets/search?query=...` OR
  - `GET /api/v1/extension/widgets/product?...` OR
  - `GET /api/v1/extension/widgets/campaign?...`
- **Ingest (best-effort)**: at least one of the ingest batches succeeds (201) within 30–60 seconds:
  - `POST /api/v1/extension/ui-signals`
  - `POST /api/v1/extension/bid-snapshots`
  - `POST /api/v1/extension/position-snapshots`
  - `POST /api/v1/extension/network-captures/batch`

## Preconditions

- Backend stack is up: `docker compose up -d`
- You have:
  - **Access token** (JWT access token, without `Bearer`)
  - **Workspace ID** (UUID where your user is a member)

## Smoke scenarios

### Scenario A — first install + session + context

1. Install extension via `Load unpacked` (`extension/chromium`).
2. Open Options and fill:
   - Backend URL: `http://127.0.0.1:8080`
   - Access token: `<raw jwt>`
   - Workspace ID: `<uuid>`
3. Open `https://seller.wildberries.ru/` and wait 5–15 seconds.
4. Check DevTools → Network (filter by `extension/` or `/api/v1/extension`):
   - `POST /api/v1/extension/sessions` → 201
   - `POST /api/v1/extension/page-context` → 201

### Scenario B — SPA navigation survival

1. Inside WB cabinet, navigate between sections (without full reload).
2. Ensure panel title updates and new `page-context` requests appear.

### Scenario C — ingest batches

1. Stay on a supported page for ~30–60 seconds.
2. Confirm at least one batch succeeded:
   - `ui-signals` and/or `network-captures/batch`
   - optionally bid/position snapshots (depends on what WB renders)

## Диагностика: Нет данных в панели

Если панель Sellico появляется, но показывает «Нет данных от Sellico» — следуй этому чеклисту.

### 1. Проверь токен и workspace

- Открой Options (`chrome://extensions` → Sellico → Details → Extension options).
- Убедись, что `Workspace ID` заполнен (UUID формата `xxxxxxxx-xxxx-...`).
- Если `Access token` пуст — расширение попытается взять токен из cookies `sellico.ru`. Для этого нужно быть залогиненным на `https://sellico.ru`.
- Если токен не подхватился автоматически — вставь его вручную (без `Bearer`).

### 2. Проверь ответы API в DevTools

Открой DevTools → Network, отфильтруй по `/api/v1/extension`:

| Запрос | Ожидаемый статус | Что делать при ошибке |
|---|---|---|
| `POST /sessions` | 201 | 401 → неверный токен; 403 → неверный workspace |
| `POST /page-context` | 201 | 400 → проверь URL страницы |
| `GET /widgets/campaign?...` | 200 | 404 → кампания не найдена в БД |
| `GET /widgets/search?...` | 200 | 200 с пустым `widget` → нет данных в БД |

### 3. Проверь подключение кабинета WB

Данные в панели появляются только если в системе есть подключённый `seller_cabinet` с WB API-токеном.

```bash
# Проверить наличие кабинетов через API
curl -s -H "Authorization: Bearer <token>" \
     -H "X-Workspace-ID: <workspace-id>" \
     http://127.0.0.1:8080/api/v1/seller-cabinets | jq .
```

Если список пуст — подключи кабинет через UI Sellico или API.

### 4. Запусти синхронизацию вручную

```bash
# Триггернуть sync для конкретного кабинета
curl -s -X POST \
     -H "Authorization: Bearer <token>" \
     -H "X-Workspace-ID: <workspace-id>" \
     http://127.0.0.1:8080/api/v1/seller-cabinets/<cabinet-id>/sync
```

После sync подожди 30–60 секунд и обнови страницу WB.

### 5. Проверь, что воркер запущен

```bash
docker compose ps
# Убедись, что сервис worker (или app) в статусе "running"
```

### 6. Поддерживаемые страницы

Расширение инжектируется только на:
- `https://seller.wildberries.ru/*`
- `https://cmp.wildberries.ru/*`

На других доменах панель не появится.

---

## What to capture in a QA bug report

- WB URL (full).
- Extension version (from `manifest.json`).
- Backend URL (Options).
- HTTP status + response body for failing requests.
- Screenshot of panel state + console errors (if any).

