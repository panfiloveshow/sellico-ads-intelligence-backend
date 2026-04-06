# Sellico Chromium Extension (internal QA)

MV3 extension for Chromium browsers. Designed for internal testing via **Load unpacked**.

## What it does (high level)

- **Session + ingest**: opens an authenticated extension session against Sellico backend and sends page context + live signals.
- **WB context detection**: detects ad-cabinet context and survives SPA navigation.
- **Live capture**: best-effort capture for `page-context`, `ui-signals`, `bid-snapshots`, `position-snapshots`.
- **Network bridge (allowlisted)**: intercepts allowlisted `fetch` / `XMLHttpRequest` calls inside page context and derives extra hints from WB JSON.
- **UI**: renders an in-page Sellico panel and small inline badges near recognized elements.

## Install (Load unpacked)

1. Open `chrome://extensions`
2. Enable `Developer mode`
3. Click `Load unpacked`
4. Select folder:
   - `.../marketing/extension/chromium`
5. **(Optional) Generate icons** if you see a missing icon warning:
   ```bash
   cd extension/chromium/icons
   ./generate-icons.sh
   ```
6. Open extension **Options** and configure:
   - **Backend URL** (default `http://127.0.0.1:8080`)
   - **Access token**:
     - leave empty if you are **logged in on `https://sellico.ru`** (the extension will try to read JWT from cookies automatically)
     - or paste JWT manually (without `Bearer`)
   - **Workspace ID**: external Sellico workspace/account id (string), used as `X-Workspace-ID`
   - (optional) auto-capture toggle

## Preconditions (backend)

- Bring the stack up locally:
  - `docker compose up -d`
- Ensure you are logged in on `https://sellico.ru` in the same Chrome profile (cookie-based token pickup),
  or prepare an **access token** (JWT).
- Ensure you know your **Sellico workspace/account id** (used as `X-Workspace-ID`).

### To see recommendations and data in the panel:

The extension shows recommendations from Sellico backend. For data to appear:

1. **Connect a seller cabinet** with WB API token:
   ```bash
   # Check if you have cabinets
   curl -s -H "Authorization: Bearer <token>" \
        -H "X-Workspace-ID: <workspace-id>" \
        http://127.0.0.1:8080/api/v1/seller-cabinets | jq .
   ```
   
2. **Trigger sync** to load campaigns and phrases from WB:
   ```bash
   curl -s -X POST \
        -H "Authorization: Bearer <token>" \
        -H "X-Workspace-ID: <workspace-id>" \
        http://127.0.0.1:8080/api/v1/seller-cabinets/<cabinet-id>/sync
   ```

3. **Wait for worker** to process the sync (check `docker compose ps`)

4. **Navigate to a campaign page** on WB (`cmp.wildberries.ru/campaigns/{id}`)

Without a connected cabinet and synced data, the panel will show "Нет данных от Sellico" — this is expected behavior.

## Supported pages (best-effort)

- WB campaign pages
- WB query / phrase pages
- WB product / SKU pages
- generic cabinet pages for context + UI signal capture

## Smoke checklist (quick)

- **Panel appears** on `https://seller.wildberries.ru/*` after load.
- **No UI breakage**: WB cabinet remains usable even if Sellico API fails.
- **API calls** visible in DevTools → Network:
  - `POST /api/v1/extension/sessions`
  - `POST /api/v1/extension/page-context`
  - `GET /api/v1/extension/widgets/*` (when context is recognized)
- **Ingest**: after a few seconds you should see at least some of:
  - `POST /api/v1/extension/ui-signals`
  - `POST /api/v1/extension/bid-snapshots`
  - `POST /api/v1/extension/position-snapshots`
  - `POST /api/v1/extension/network-captures/batch`

## Troubleshooting

- **401 / missing authorization header**: token is missing/expired. Log in on `https://sellico.ru` or paste JWT manually (without `Bearer`).
- **400 missing workspace id**: `Workspace ID` is empty.
- **403 access denied**: token is valid but user is not allowed in that workspace.
- **Panel not visible**:
  - check `chrome://extensions` → Errors
  - check that the page is under `https://seller.wildberries.ru/*`

## Files

- `manifest.json` — MV3 manifest
- `background.js` — backend-authenticated session and ingest bridge
- `content.js` — page detection, panel rendering, auto-capture
- `page-bridge.js` — network interception inside page context
- `options.html` / `options.js` — backend URL + auth config
- `panel.css` — in-page panel styling

## Notes / limitations (internal QA)

- Parsers are heuristic and intentionally conservative.
- Live bid / live position capture depends on what WB renders in DOM.
- Network capture is allowlisted; unsupported endpoints are ignored.
- Network payloads are **size-limited** on the client side to avoid giant transfers during QA.

## Yandex Browser install (CRX3)

Если нужно установить “как в инструкции” через `browser://tune`:

1. Упаковать расширение в `.crx`:

```bash
make pack-extension
```

2. Открыть в Яндекс.Браузере страницу `browser://tune`.
3. Перетащить файл:
   - `extension/dist/sellico-ads-intelligence.crx`

После установки расширение появится в разделе “Из других источников”.
