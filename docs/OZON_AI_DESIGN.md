# Ozon: реклама + ИИ-автопилот + репрайсер — дизайн модуля

Дата: 2026-08-07. Статус: дизайн, не реализовано.
Цель: функционал уровня WB-модуля (автобиддер, стратегии, репрайсер) для Ozon,
с ИИ-управлением кампаниями через GLM-5.2 (NVIDIA NIM), и чётким разделением WB/Ozon на фронте.

---

## 1. Что даёт Ozon API (выжимка исследования)

### Performance API (реклама) — `https://api-performance.ozon.ru`
- **Auth**: `client_id`/`client_secret` → `POST /api/client/token`, токен живёт **30 минут** (кешировать и обновлять). Лимит **100 000 запросов/сутки**.
- **Оплата за клик (CPC, ex-Трафареты + Вывод в топ)** — полный CRUD:
  - создание: `POST /api/client/campaign/cpc/v2/product` (бюджеты в **микрорублях**, 1₽ = 1 000 000);
  - стратегии кампании: `TARGET_BIDS` (ручная ставка CPC — наша ниша), `TARGET_CIR` (целевой ДРР), `MAX_CLICKS`, `TOP_PROMOTION` (Топ-4/12/20/30);
  - ставки **per-SKU**: `PUT /api/client/campaign/{id}/products` (до 500 SKU/кампания);
  - activate/deactivate, `PATCH /campaign/{id}` (бюджеты, даты, автодобавление SKU).
- **Оплата за заказ (CPO, ex-Продвижение в поиске)** — per-SKU, без кампаний:
  `search_promo/v2/bids/set` (до 1000 SKU/запрос), `product/enable|disable`, `get_cpo_min_bids` (фикс-ставки — актуальная модель). Списание только при заказе → безрисково по ДРР.
- **Сигналы для биддера**: конкурентные ставки `GET /campaign/{id}/products/bids/competitive` (200 SKU/запрос), мин. ставки `POST /api/client/min/sku` + `GET /api/client/limits/list`, отчёт по поисковым фразам `POST /api/client/statistics/phrases`.
- **Статистика**: синхронно `GET /api/client/statistics/daily` (показы/клики/расход/заказы по дням), `statistics/expense`; асинхронно `POST /api/client/statistics` → UUID → CSV/JSON (до 10 кампаний, 62 дня, 1 одновременная выгрузка на аккаунт). `POST /statistics/products/sku` не расходует лимиты, но данные **не свежее вчера**.
- ⚠️ **Гранулярность — день, лаг ~сутки. Почасовой статистики нет** → цикл оптимизации «раз в несколько часов / раз в день», не поминутный. Intraday можно контролировать только расход.
- Баннеры/видео — только чтение статистики, создание из ЛК. Брендовая полка удалена.

### Seller API (репрайсер + данные) — `https://api-seller.ozon.ru`
- **Auth**: заголовки `Client-Id` + `Api-Key` (ключ живёт 6 месяцев). Лимит 50 rps.
- **Цены**: `POST /v1/product/import/prices` — до 1000 товаров/запрос, **не чаще 10 изменений цены одного товара в час**. Поля: `price`, `old_price`, `min_price`, `net_price` (себестоимость — пушим из юнит-экономики Sellico!), флаги авто-акций.
- **Юнит-экономика из коробки**: `POST /v5/product/info/prices` отдаёт **комиссии FBO/FBS, эквайринг, логистику, индекс цен (color_index: SUPER/GREEN/…) и цены конкурентов** (на Ozon и внешних площадках) — «Пол маржи» на Ozon считается точнее, чем на WB, почти без внешних данных.
- **Воронка и позиции**: `POST /v1/analytics/data` (метрика `position_category`, лимит 1 раз/мин; полный набор — с Premium Plus), `POST /v1/analytics/product-queries` — запросы, показы, **средняя позиция** по своим SKU.
- Финансы (факт-ДРР): `/v3/finance/transaction/list`, товары `/v3/product/list`, остатки `/v4/product/info/stocks`, оборачиваемость `/v1/analytics/turnover/stocks`.
- Встроенные стратегии цен Ozon (`/v1/pricing-strategy/*`) — конкурент нашему репрайсеру, но `competitors/list` — бесплатный источник цен конкурентов.

### Про OAuth-приложения
С 2026 у Ozon есть OAuth-приложения (один токен на оба API, селлер не передаёт нам секреты). Правильный путь для SaaS в перспективе, но для старта используем ключи, которые Sellico **уже хранит** в интеграциях: `client_id`+`api_key` (Seller) и `performance_api_key`+`performance_client_secret` (Performance). Миграция на OAuth — отдельная фаза, не блокер.

### LLM: GLM-5.2 через NVIDIA NIM
- OpenAI-совместимый endpoint `https://integrate.api.nvidia.com/v1`, модель `z-ai/glm-5.2`.
- Поддерживает tool calling (OpenAI-формат), streaming, structured output.
- **Бесплатный тир ~40 RPM на ключ** — хватает на батч-цикл (один прогон агента на кабинет = 5–15 запросов), не хватит на high-frequency. Клиент делаем OpenAI-совместимым и провайдер-независимым: `LLM_BASE_URL` + `LLM_API_KEY` + `LLM_MODEL` в env — смена модели без кода.

---

## 2. Архитектура backend

Принцип: **WB-код не трогаем** (он в проде и работает). Ozon — зеркальный модуль на тех же паттернах: handler → service → sqlc, asynq-воркер, те же guardrails. Переиспользуем то, что уже marketplace-нейтрально: `strategies`/`strategy_bindings`/`StrategyParams`, чистые движки `bidengine.go`/`priceengine.go`, крипту, tenancy.

### 2.1 Кабинеты и креды
- `seller_cabinets` + колонка `marketplace TEXT NOT NULL DEFAULT 'wb'` (`'wb' | 'ozon'`).
- Ozon требует две пары кредов → колонка `encrypted_credentials TEXT` (AES-GCM, внутри JSON:
  `{"client_id","api_key","perf_client_id","perf_client_secret"}`). Для WB остаётся `encrypted_token`.
- `internal/service/seller_cabinet.go:483` — убрать фильтр `!= "WildBerries"`, маппить OZON-интеграции Sellico (там уже есть все 4 поля). Валидация при подключении: Seller API `/v1/roles` (заодно узнаём `expires_at` ключа) + Performance `POST /api/client/token`.

### 2.2 Клиенты — `internal/integration/ozon/`
```
ozon/
  seller_client.go   // api-seller.ozon.ru: Client-Id+Api-Key, rate.Limiter 50rps→ставим 10
  perf_client.go     // api-performance.ozon.ru: токен-кеш (TTL 25 мин, singleflight),
                     // счётчик дневного бюджета запросов (100k/сутки)
  types.go
```
Тот же паттерн, что у `wb.Client`: per-cabinet rate limiter (LRU), circuit breaker, Prometheus-метрики (`OzonAPILatency`), retry с уважением Retry-After. Бюджеты кампаний конвертируем микрорубли ↔ рубли на границе клиента, внутрь системы — **целые рубли** (как везде у нас).

### 2.3 Таблицы (новая миграция)
Зеркалим минимально необходимое, WB-таблицы не трогаем:
```sql
ozon_campaigns            (id, seller_cabinet_id, ozon_campaign_id, title, adv_object_type, -- SKU|SEARCH_PROMO
                           state, placement, autopilot_strategy, daily_budget_rub, weekly_budget_rub, ...)
ozon_campaign_products    (campaign_id, sku, bid_rub, target_cir, top_position, is_active)
ozon_campaign_stats       (campaign_id, date, views, clicks, spend_rub, orders, revenue_rub)  -- ДРР считаем
ozon_product_stats        (cabinet_id, sku, date, ...)          -- из statistics/products/sku + analytics/data
ozon_search_queries       (cabinet_id, sku, query, date, views, position, conversion)  -- statistics/phrases
ozon_cpo_products         (cabinet_id, sku, enabled, bid, bid_kind)  -- 'percent'|'fixed_rub'
ozon_product_prices       (cabinet_id, sku, offer_id, price_rub, old_price_rub, min_price_rub,
                           net_price_rub, color_index, commission_fbo_pct, commission_fbs_pct, ...)
ozon_bid_changes          (как bid_changes: old/new, reason, source 'ai'|'strategy'|'manual', status, decision_context JSONB)
ozon_price_changes        (как price_changes)
ai_decisions              (id, workspace_id, cabinet_id, run_id, model, action_type, target JSONB,
                           proposal JSONB, rationale TEXT, guardrail_verdict, status
                           'proposed'|'approved'|'auto_applied'|'rejected'|'failed',
                           prompt_tokens, completion_tokens, created_at)   -- полный аудит ИИ
```
`strategies` переиспользуем как есть (там уже `seller_cabinet_id`) + новые типы:
`ozon_cpc_target_drr`, `ozon_cpo_optimizer`, `ozon_ai_autopilot`, `ozon_price_margin_floor` (движок `DecideMarginFloor` переиспользуется — экономика приходит из `/v5/product/info/prices` вместо Sellico-моста, мост остаётся fallback'ом).

### 2.4 Воркер
Отдельный asynq-сервер `ozon` (по прецеденту repricer-сервера), namespace `ozon:`:
```
ozon:sync_campaigns      @every 1h      кампании + товары + ставки
ozon:sync_stats          @every 2h      statistics/daily + expense (intraday-контроль расхода)
ozon:sync_deep_stats     @daily 06:00   async-отчёты, phrases, analytics/data, product-queries
ozon:sync_prices         @every 6h      v5/product/info/prices (цены, комиссии, индекс, конкуренты)
ozon:ai_autopilot        @every 4h      ИИ-прогон по кабинетам с активным ozon_ai_autopilot
ozon:strategy_sweep      @every 1h      детерминированные стратегии (без LLM)
ozon:repricer_sweep      @every 1h      ценовые стратегии → import/prices (чанки по 1000)
ozon:reconcile           @every 15m     подтверждение, что ставки/цены реально применились
```
Частоты консервативные под лимиты: 100k/сутки Performance — запас огромный; узкое место — 1 одновременная async-выгрузка статистики и 10 изменений цены товара/час.

### 2.5 ИИ-слой (ядро задачи)

**Паттерн: LLM предлагает — детерминированный код решает.** ИИ никогда не пишет в Ozon напрямую; каждое его действие проходит тот же каскад guardrails, что у WB-автобиддера.

```
internal/integration/llm/client.go   // OpenAI-совместимый chat+tools, любой провайдер
internal/service/ozon_ai_manager.go  // агентный цикл
internal/service/ozon_guardrails.go  // валидатор действий (детерминированный)
```

Цикл `ozon:ai_autopilot` на кабинет:
1. **Контекст-пакет** (без tool-звонков за базовыми данными — собираем сами из своих таблиц, экономим RPM): кампании + статистика 14 дней, юнит-экономика по SKU (комиссии, net_price, целевая маржа), конкурентные и мин. ставки, поисковые фразы, параметры стратегии (целевой ДРР, дневной лимит расхода, режим).
2. **Агентный цикл GLM** (5–15 итераций max) с инструментами:
   - read-tools (по требованию): `get_campaign_details`, `get_search_queries(sku)`, `get_competitor_prices(sku)`, `get_stats_history(campaign_id, days)`;
   - write-tools (только предложения): `propose_bid_change`, `propose_budget_change`, `propose_campaign_pause`, `propose_campaign_create`, `propose_sku_add_remove`, `propose_cpo_bid`, `finish(summary)`.
   Structured output: каждое предложение = `{action, target, new_value, rationale, expected_effect}`.
3. **Guardrails** (код, не LLM): ставка в пределах `min/sku`…`MaxBid`; изменение ≤ `MaxChangePercent`; cooldown и `MaxChangesPerDay` на объект; дневной расход кабинета ≤ лимита; экономика SKU готова (иначе scale-up запрещён, как у WB); бюджет ≥ минимума из `dynamic_budget`. Отклонённое — в `ai_decisions` со статусом `rejected` + причина.
4. **Применение по уровню автоматизации** (те же уровни, что WB `AutomationLevel`):
   - **1 — shadow**: только записываем решения (счётчик «что бы сделал ИИ» + counterfactual, как `bid_decision_observations`);
   - **2 — copilot**: решения попадают в ленту на фронте, селлер жмёт «применить/отклонить»;
   - **3 — autopilot**: применяем сразу, селлер видит ленту постфактум + дневной Telegram-дайджест (интеграция уже есть).
5. Всё в `ai_decisions`: промпт-хеш, ответ, токены, вердикт guardrails, результат применения. Через 3–7 дней джоба оценивает эффект (ДРР до/после) — это и метрика качества ИИ, и материал для few-shot улучшения промпта.

Fallback: при недоступности LLM (RPM, 5xx) кабинеты с `ozon_ai_autopilot` обслуживаются детерминированной стратегией `ozon_cpc_target_drr` (математика из `bidengine.CalculateBid` — ACoS-логика переносится 1:1, ДРР = ACoS).

### 2.6 HTTP API (новые роуты, тот же router)
```
/api/v1/ozon/cabinets…                      (через общий seller-cabinets + фильтр marketplace)
/api/v1/ozon/campaigns          GET POST    /{id} GET PATCH
/api/v1/ozon/campaigns/{id}/{activate,deactivate}  POST
/api/v1/ozon/campaigns/{id}/products        GET PUT POST DELETE
/api/v1/ozon/campaigns/{id}/stats           GET
/api/v1/ozon/cpo/products                   GET
/api/v1/ozon/cpo/bids                       POST
/api/v1/ozon/search-queries                 GET
/api/v1/ozon/prices                         GET · /bulk POST
/api/v1/ozon/price-changes                  GET · /{id}/rollback POST
/api/v1/ozon/ai/decisions                   GET
/api/v1/ozon/ai/decisions/{id}/{approve,reject}  POST
/api/v1/ozon/ai/runs                        GET · /run POST (ручной прогон)
/api/v1/ozon/bid-changes                    GET · /{id}/rollback POST
```
Стратегии — через существующие `/strategies` (типы `ozon_*`, кабинет определяет маркетплейс).

---

## 3. Frontend: чёткое разделение WB / Ozon

Принцип: **один модуль, два мира, переключатель наверху**. WB-роуты и код не трогаем.

1. **Переключатель маркетплейса** в `AdsIntelligenceLayout` (паттерн уже есть — `MarketplaceSelector.tsx`, где Ozon стоит `enabled: false`): два таба **Wildberries | Ozon** с брендовыми цветами (WB-фиолетовый / Ozon-синий #005BFF), выбор в zustand + localStorage `ads-marketplace:{workspaceId}`.
2. **Роуты**: WB остаётся на текущих путях (`/ads-intelligence/*` — ничего не ломаем). Ozon — под префиксом:
```
/ads-intelligence/ozon                AI-центр (главная озона)
/ads-intelligence/ozon/campaigns      кампании CPC + вкладка CPO
/ads-intelligence/ozon/campaigns/:id  детали: товары, ставки, статистика, история ИИ
/ads-intelligence/ozon/queries        поисковые запросы
/ads-intelligence/ozon/prices         репрайсер (цены, индекс цен, конкуренты)
/ads-intelligence/ozon/strategies     стратегии (переисп. StrategiesPage с фильтром типов)
/ads-intelligence/ozon/settings       автопилот: уровень 1/2/3, лимиты
```
3. **Код**: `src/modules/ads-intelligence/ozon/` (pages + api/ozonApi.ts на общем `adsClient`). Общие компоненты (ShopSelector, DataHealthAlert, таблицы, StrategyActivityPanel) переиспользуем; `getSellerCabinets` получает параметр `marketplace` вместо жёсткого фильтра `=== 'WildBerries'` (adsIntelligenceApi.ts:1781).
4. **Ключевая новая страница — «AI-центр Ozon»**: статус автопилота (shadow/copilot/autopilot), лента решений ИИ (карточка: действие, обоснование, ожидаемый эффект, кнопки применить/отклонить в режиме copilot), график ДРР/расход/заказы, счётчик «ИИ сэкономил/заработал за 30 дней». Это витрина продукта.
5. Sidebar: пункт «Реклама и цены» получает подпункты «Wildberries» и «Ozon» (или бейдж «NEW» на переключателе). Заголовок layout перестаёт хардкодить «Wildberries» (`AdsIntelligenceLayout.tsx:89`).

---

## 4. Чем Ozon-модуль будет **лучше** WB-модуля

1. **ИИ-автопилот** — на WB его нет (там rule-based рекомендации). Лента решений с обоснованиями на русском — сильный продуктовый дифференциатор.
2. **«Пол маржи» точнее**: комиссии, эквайринг и логистика приходят из `/v5/product/info/prices` самим Ozon'ом — меньше зависимость от ручного заполнения юнит-экономики.
3. **Цены конкурентов бесплатно** (price_indexes + pricing-strategy/competitors) — стратегия «Слежение за конкурентами» без парсинга.
4. **CPO — «безопасная» реклама**: списание только при заказе. Дефолтный совет ИИ новым кабинетам: включить CPO на весь ликвидный ассортимент с мин. ставками — гарантированно неубыточный старт.
5. **TARGET_CIR как страховка**: для кампаний, где ИИ не уверен, можно делегировать ДРР самому Ozon и следить сверху.

## 5. Риски и ограничения
- Данные Performance API с лагом ~сутки → не обещать «поминутный биддинг»; честный цикл — 4–6 часов.
- `bids/set` для CPO в % — deprecated, Ozon переводит на фикс-ставки → сразу строить на `get_cpo_min_bids`.
- `dailyBudget` у CPC deprecated с 22.05.2026 → основной параметр `weeklyBudget`.
- GLM free tier 40 RPM — на 100+ кабинетов очередь прогонов растянется; батчим и держим детерминированный fallback. При росте — платный тир NIM или прямой Z.AI/другой провайдер (клиент OpenAI-совместимый, смена = env).
- Ключ Seller API живёт 6 мес — мониторить `expires_at`, слать уведомление селлеру за 2 недели.
- Позиции/воронка в полном объёме требуют подписки Ozon Premium Plus у селлера — деградировать мягко.

## 6. Фазы
| Фаза | Состав | Результат |
|---|---|---|
| **1. Фундамент** | marketplace в cabinets, ozon-клиенты, sync кампаний/статистики/цен, read-only UI + переключатель WB/Ozon | Селлер видит свою Ozon-рекламу в Sellico |
| **2. Управление** | ставки/бюджеты/пауза/товары с фронта, CPO-управление, детерминированные стратегии (target-ДРР), reconcile | Ручное управление + автостратегии |
| **3. ИИ-автопилот** | llm-клиент, ai_manager, guardrails, ai_decisions, AI-центр на фронте; запуск в shadow → copilot → autopilot | Главная фича |
| **4. Репрайсер Ozon** | margin floor + competitor follow на данных Ozon, календарь цен | Паритет с WB + лучше |

Фазы 1–2 — чистая инженерия по готовым лекалам WB. Фаза 3 стартует в shadow-режиме уже на реальных данных фазы 1.
