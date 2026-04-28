# План доработок Sellico Ads Intelligence — после roadmap v1.0 на проде

**Текущее состояние** (на 2026-04-27, после мерджа `roadmap-v1` → `main`):

- ✅ Прод задеплоен на `http://72.56.250.9` — все основные endpoint'ы отвечают 200
- ✅ Локальная JWT-авторизация работает: register/login/me, workspace CRUD, seller-cabinets, recommendations, products, campaigns, ads/overview, settings
- ✅ Контейнеры hardened (`cap_drop:ALL`+targeted `cap_add`, `read_only`+tmpfs, `security_opt`)
- ✅ Бэкап БД ежедневно в 03:00 (`/opt/sellico/backups/`, 7 дней retention)
- ✅ Memory: api ≤700 MiB (`GOMEMLIMIT`), worker ≤350 MiB
- ✅ **HTTPS на `https://ads.sellico.ru`** — Let's Encrypt cert, HTTP/2, HSTS preload, auto-renewal через webroot (без даунтайма)
- ⚠️ Telegram-алерты — отложено (по решению владельца)
- ⚠️ Frontend — каркас собран, наполнения нет
- ⚠️ Браузерное расширение — manifest hardened, но не упаковано/не подписано
- 🔴 **Реальных данных в системе НЕТ** — sync с WB API ни разу не запускался для тестового кабинета

---

## Принцип приоритизации

Без HTTPS публичный URL — `http://72.56.250.9`. Это значит:
- 🟢 **Можно**: внутреннее использование (свой бизнес, тестовая команда), curl-доступ к API, разработка фронтенда против реального API
- 🔴 **Нельзя**: публичный фронтенд для пользователей (без HTTPS — нет credentials в браузере, паблик-токены в открытом виде, репутационные риски)
- 🟡 **С риском**: браузерное расширение — оно уже хранит токен в `chrome.storage`, передача по HTTP технически работает, но пользователю надо явно одобрить mixed-content для встраиваемых виджетов

Поэтому **до домена** план фокусируется на:
- Внутренней верификации flow и подготовке данных
- Подготовке frontend-кода против локального API (можно деплоить позже)
- Подготовке инфраструктуры (CI/CD, monitoring без alerts) — чтобы при появлении домена включить за полчаса

---

## Phase A — Реальные данные и валидация sync (1-2 сессии, ~3-5 дней)

**Цель**: убедиться, что pipeline `WB API → DB → recommendations` работает end-to-end на реальных данных.

### A.1. Создать первый продакшен-cabinet
- Подготовить тестовый WB-аккаунт (или использовать рабочий)
- Получить WB Advertising API token (Кабинет продавца → Профиль → API)
- POST `/api/v1/seller-cabinets` с реальным token (поле `api_token` per OpenAPI)
- Verify: token validation проходит, cabinet создан, `EncryptedToken` зашифрован AES-256-GCM

### A.2. Триггернуть первый sync
- POST `/api/v1/seller-cabinets/{id}/sync` (или дождаться cron `@every 1h`)
- Verify в логах worker: `WBSyncWorkspace` job начался, прошёл, не упал на rate-limit / WB outage
- В БД появились records: campaigns, products, phrases, campaign_stats
- Job записан в `job_runs` со status=completed

### A.3. Triggernуть recommendation engine
- POST `/api/v1/recommendations/recompute` (если такой endpoint есть; иначе подождать cron `@every 2h`)
- Verify: появились records в `recommendations` таблице
- GET `/api/v1/recommendations` возвращает не-пустой список
- GET `/api/v1/ads/overview` показывает реальные KPI

### A.4. Заполнить чек-лист "система живая"
- [ ] Sync завершается без ошибок
- [ ] Pgxpool connections < 50% от пула во время sync
- [ ] Worker memory остаётся ≤ 350 MiB
- [ ] WB API circuit breaker не открыт (state=closed)
- [ ] Asynq queue depth → 0 после sync
- [ ] Расход в `ads/overview` совпадает с тем, что показывает кабинет WB вручную

**Блокеры**: нужен реальный WB-аккаунт с активной рекламой.

---

## Phase B — Frontend MVP (2-3 сессии, ~7-12 дней)

Каркас собран в Sprint 5 (`/frontend`), но рендерит только заглушку. Нужно довести до уровня MVP, чтобы пользоваться системой через браузер, а не curl.

### B.1. Локальный dev-loop работает
- `cd frontend && pnpm install && pnpm dev`
- Vite dev server проксирует /api → http://72.56.250.9 (override `VITE_API_BASE_URL`)
- Login с тестовым юзером
- Layout shell + sidebar + topbar отображаются

### B.2. OpenAPI → TypeScript-клиент
- `pnpm openapi:generate` против `/openapi.yaml`
- В `src/api/schema.gen.ts` появляется типизация всех endpoint'ов
- Все вызовы из useQuery/useMutation типизированы

### B.3. Sprint 6 — Command Center
- `pages/dashboard/CommandCenterPage.tsx` (сейчас stub) → реальный дашборд:
  - Workspace selector (header)
  - 4 KPI cards (impressions / clicks / spend / orders) из `/api/v1/ads/overview`
  - Performance compare (current vs previous period)
  - Attention items list
  - Top products / campaigns / queries (3 раздела)
- DataGrid (`@mui/x-data-grid`) для табличных представлений
- Recharts для time-series графиков (history расходов, позиций)

### B.4. Sprint 6 — Entity Detail
- `/products/:id` — детали товара, история позиций, активные кампании
- `/campaigns/:id` — детали кампании, ставки, фразы, расход по дням
- Drill-down между ними

### B.5. Sprint 7 — Recommendations
- `/recommendations` — список с фильтрацией (severity, type, status)
- Action buttons: apply / dismiss с подтверждением
- Detail panel с evidence (data points, что триггернуло)

### B.6. Sprint 7 — Settings
- `/settings/general` — workspace (name, slug)
- `/settings/cabinets` — добавление/удаление WB-cabinets, статус sync
- `/settings/thresholds` — пороги для recommendation engine
- `/settings/members` — RBAC (owner/manager/analyst/viewer), invite/remove

### B.7. Sprint 7 — Polish
- Storybook для компонентной библиотеки
- a11y-проход axe-core
- Lighthouse ≥ 90 на основных экранах
- Mobile responsive (минимум читаемо на 768px)
- i18n (ru сначала, en задел)

### B.8. Деплой frontend
- `pnpm build` → `frontend/dist/`
- Поправить `nginx.conf` (dev-вариант, который сейчас на проде) чтобы:
  - `/api/*` → проксирует в api:8080 (уже работает)
  - `/` → отдаёт `frontend/dist/index.html` с SPA fallback
- Пересобрать nginx-контейнер (или mount-volume для frontend bundle)

**Блокер при выкатке на пользователей**: HTTPS. Команда внутри сети может пользоваться по HTTP без проблем.

---

## Phase C — CI/CD pipeline активация (0.5 сессии, 2-4 часа)

### C.1. GitHub secrets
- `DEPLOY_HOST_FINGERPRINT` — `ssh-keyscan -t ed25519 72.56.250.9 | ssh-keygen -lf - -E sha256` (взять SHA256-часть)
- `DEPLOY_HOST` — `72.56.250.9`
- `DEPLOY_USER` — `admin_reprice`
- `DEPLOY_SSH_KEY` — приватный SSH-ключ (одноразово сгенерить ed25519, публичную часть положить на VPS в `~/.ssh/authorized_keys`)
- `DEPLOY_PATH` — `/opt/sellico`
- `GHCR_TOKEN` — GitHub PAT с scope `read:packages` (для docker pull на VPS)

### C.2. Перевести deploy на pre-built образы
- Сейчас `docker-compose.prod.yml` использует `build:` для api/worker (медленно на 2GB-RAM сервере)
- В CI build уже происходит и пушится в GHCR (по тегу + sha)
- Поправить `docker-compose.prod.yml`: вместо `build:` использовать `image: ${API_IMAGE:-ghcr.io/.../api:latest}`
- В `deploy.sh update`: `docker compose pull api worker && docker compose up -d api worker`

### C.3. Verification
- Сделать пустой коммит → push в main
- Проверить что:
  - CI зелёный (build, test, gosec, trivy)
  - CD: build образов, push в GHCR, SSH на VPS, pull, restart
  - `/health/ready` после деплоя возвращает 200

### C.4. Скрипт rollback
- `scripts/rollback.sh` (новый) — pulls предыдущий образ по тегу-sha и поднимает; для одноклика отката если что-то сломалось

---

## Phase D — Monitoring без алертов (0.5-1 сессия)

### D.1. Поднять monitoring containers, которые мы уже описали в compose.prod.yml
- `prometheus`, `grafana`, `cadvisor`, `node-exporter` (без `alertmanager` — отложено)
- Скорректировать `docker-compose.override.yml` чтобы они стартовали:
  ```yaml
  alertmanager: { profiles: ["alerts"] }   # выключен по умолчанию
  ```
- `docker compose up -d prometheus grafana cadvisor node-exporter`

### D.2. Доступ к Grafana без HTTPS
- В compose Grafana слушает `expose: 3000` (не публичный)
- Доступ: `ssh -L 3000:grafana:3000 admin_reprice@72.56.250.9` → открыть http://localhost:3000 на своём Mac
- Импортировать готовые дашборды (`monitoring/grafana/provisioning/dashboards/`)
- Логин `admin / ${GRAFANA_ADMIN_PASSWORD}` из `.env`

### D.3. Дашборды для отслеживания
- API request rate / latency / 5xx по endpoint
- Memory / CPU per container (cadvisor)
- Disk free, load average (node-exporter)
- Asynq queue depth по очередям
- pgxpool: total, in_use, idle
- WB API: requests/s, breaker state, retry rate

### D.4. Логи доступнее (без ELK-стека)
- Сейчас: `docker compose logs api worker` — только хвост
- Поставить `docker plugin install grafana/loki-docker-driver:latest` + Loki
- Полный поиск по логам в Grafana → Explore
- Retention 7 дней

---

## Phase E — Browser extension v1.0 publication (1 сессия)

Всё кодом готово в Sprint 8. Не сделано:

### E.1. Сгенерировать ключ подписи
- `openssl genrsa -out extension/sellico-extension-key.pem 2048` (одноразово, **сохранить в password manager** — без него обновления невозможны)
- Положить в GitHub Secret `EXTENSION_PRIVATE_KEY` (base64-encoded)

### E.2. Сгенерировать иконки
- На macOS: `brew install imagemagick && bash extension/chromium/icons/generate-icons.sh`
- Получаются icon16/48/128.png

### E.3. Скриншоты для Chrome Web Store
- 5 штук 1280×800 (см. `extension/chromium/CHROME_WEB_STORE.md`)
- Снять на тестовом WB-кабинете когда появятся реальные данные (Phase A)

### E.4. Поправить manifest для HTTP-only прода
- Сейчас `host_permissions` ждёт `https://api.sellico.ru/*`
- Без HTTPS добавить `http://72.56.250.9/*` в `host_permissions` для production deployment
- Это снизит security рейтинг при ревью Chrome Web Store, но позволит работать
- Когда появится HTTPS — убрать HTTP entry

### E.5. Подача в Chrome Web Store
- Создать developer-аккаунт ($5 один раз)
- Загрузить zip из `extension/dist/`
- Privacy policy URL — нужно опубликовать `extension/chromium/PRIVACY.md` куда-то (можно как GitHub Pages в этом репо)
- Ждать 3-7 дней review

**Альтернатива без публикации**: распространять `.crx` через прямую ссылку (load-unpacked / drag-and-drop). Подходит для closed beta.

---

## Phase F — Quality / staging hardening (1 сессия, можно параллельно)

### F.1. Integration tests с testcontainers
- Sprint 3 deferred (нужен интернет для `go get`). Сейчас есть.
- `tests/integration/sync_flow_test.go` — `auth → workspace → cabinet → sync → recommendations` end-to-end с реальным postgres + redis в testcontainers

### F.2. Уменьшить OpenAPI drift
- Сейчас `tools/check-openapi-drift/known-gaps.txt` имеет ~65 baseline-исключений
- За 1-2 сессии задокументировать недокументированные routes (особенно `/api/v1/ads/*`, `/cabinets/*`, `/strategies/*`) в `openapi.yaml`
- Удалять из known-gaps по мере покрытия

### F.3. pg_dump на хосте → восстановить полный backup-db.sh
- `sudo apt install postgresql-client-common postgresql-client` на VPS
- В cron вернуть `scripts/backup-db.sh` вместо wrapper
- Активировать GPG + S3 upload (нужен Yandex Object Storage bucket + GPG passphrase в `/etc/sellico/backup-gpg.pass`)

### F.4. restore-check.sh cron
- Тоже требует `pg_restore` на хосте
- После шага F.3 → активировать ежедневный smoke-тест восстановления

---

## Phase G — HTTPS ✅ DONE (2026-04-28)

Закрыто. Текущее состояние:
- `https://ads.sellico.ru` — Let's Encrypt cert (CN=ads.sellico.ru), valid till 2026-07-27
- HTTP→HTTPS 301 redirect, HSTS `max-age=31536000; includeSubDomains; preload`, HTTP/2
- Все security headers (X-Content-Type-Options, X-Frame-Options, Referrer-Policy)
- Auto-renewal: systemd `certbot.timer` (active, daily) + webroot challenge через `/opt/sellico/nginx/acme` (без даунтайма) + deploy hook `/etc/letsencrypt/renewal-hooks/deploy/sellico.sh` (копирует серт + nginx reload)
- Renewal dry-run: ✅ succeeded

---

## Приоритеты по бизнес-impact

| Phase | Bus value | Технический риск | Зависимости |
|---|---|---|---|
| **A. Real data + sync** | 🟢🟢🟢 без этого вся система — пустышка | низкий | реальный WB-токен |
| **B. Frontend MVP** | 🟢🟢 без UI — только curl | средний | A полезна, но не блокирует |
| **C. CI/CD pipeline** | 🟢 экономит время на деплоях | низкий | нет |
| **D. Monitoring** | 🟡 чтобы не пропустить инциденты | низкий | нет |
| **E. Extension publication** | 🟡 для широкой аудитории; для team OK без публикации | средний (Chrome review) | A для скриншотов |
| **F. Quality** | 🟡 catch bugs до прода | низкий | нет |
| **G. HTTPS** | 🟢🟢 разблокирует публичный фронт | минимальный | домен от тебя |

**Рекомендуемый порядок**:
1. **A** — обязательно первое, иначе пилим UI вслепую
2. **C** + **D** параллельно с **A** — сократят боль будущих итераций
3. **B** — большой кусок работы; можно начать после A.1-A.2 (есть данные для отладки)
4. **F** — фоном, по мере необходимости
5. **G** — когда появится домен (триггер)
6. **E** — последнее, после A для качественных скриншотов

---

## Что я могу сделать сразу СЕЙЧАС автономно

Без участия пользователя:
- ✅ **C.1-C.2 (CI/CD)**: добавить GHA secrets через `gh secret set` (если `gh` авторизуешь), переключить compose на pre-built образы
- ✅ **D.1-D.2 (monitoring)**: поднять prometheus/grafana/cadvisor/node-exporter в compose-override; impostировать дашборды
- ✅ **F.2 (OpenAPI)**: задокументировать 5-10 endpoint'ов из known-gaps в openapi.yaml
- ✅ **B.1-B.2 (frontend dev-loop)**: подготовить `pnpm install` инструкцию, убедиться что vite build проходит, сделать скрин

С участием пользователя:
- 🟡 **A (real data)**: нужен WB API token
- 🟡 **B.3-B.7 (frontend implementation)**: большой объём, итерации UI
- 🟡 **C.1 secrets**: нужно `gh auth login` или secrets через web UI
- 🟡 **E (extension)**: нужен Chrome dev-аккаунт + screenshots после A

**Какую пилим первой?**
