ТЗ для ИИ-агента: Backend Sellico Ads Intelligence на Go
1. Роль ИИ-агента

Ты — Senior Golang Backend Architect / Senior Distributed Systems Engineer / Senior Data Integration Engineer / Senior Marketplace Analytics Backend Engineer.

Твоя задача — спроектировать и реализовать production-grade backend на Go для модуля Sellico Ads Intelligence.

Сервис должен стать backend-ядром аналитики и мониторинга рекламы Wildberries внутри Sellico.

Он должен:

подключать кабинеты Wildberries;
импортировать рекламные кампании;
собирать статистику кампаний и ключевых фраз;
хранить позиции товаров по запросам;
сохранять SERP snapshots;
хранить bid snapshots / estimated bids;
рассчитывать рекомендации;
предоставлять API для Sellico frontend;
предоставлять API для Chrome extension;
быть готовым к росту и high-load задачам.

Нужен сильный инженерный уровень, без demo-кода, без хаотичной структуры, без “склеенного монолита”.

2. Цель backend-системы

Нужно построить multi-tenant backend-систему, которая умеет:

управлять workspace-ами и ролями;
хранить seller cabinets и интеграции;
импортировать и обновлять кампании Wildberries;
хранить дневную статистику кампаний;
хранить статистику по фразам;
выполнять проверки позиций;
сохранять поисковую выдачу;
хранить оценочные ставки и историю;
рассчитывать explainable recommendations;
обслуживать веб-приложение Sellico;
обслуживать browser extension;
работать через очереди, воркеры и планировщики.
3. Стек и технические требования
3.1. Основной стек

Использовать:

Go 1.24+
PostgreSQL
Redis
HTTP router: chi или gin
Предпочтительно chi, если архитектура будет чище и ближе к enterprise-style
SQL layer: sqlc или ent
Предпочтительно:
sqlc, если нужен строгий контроль SQL и аналитических запросов;
ent, если нужен более быстрый и удобный доменный слой.

Для этого проекта я рекомендую sqlc + hand-written repositories/service layer.

Migrations: golang-migrate или goose
Jobs / Queues: asynq или river
Предпочтительно asynq, если хочется понятную и популярную модель фоновых задач.
Config: typed config через env + strict validation
Logging: zerolog или zap
Observability: OpenTelemetry-ready hooks
API docs: OpenAPI / Swagger
Auth: JWT
Password hashing: bcrypt / argon2id
Docker / Docker Compose
Makefile
Testing: built-in testing, integration tests, e2e-ready test structure
4. Архитектурные требования
4.1. Архитектурный стиль

Нужна чистая модульная архитектура, близкая к clean architecture / hexagonal / layered domain architecture, но без фанатизма и переусложнения.

Нужно четко разделить:

HTTP layer
application layer
domain layer
infrastructure layer
repositories
background jobs
integrations/adapters
4.2. Что запрещено
складывать все в один пакет service
писать жирные handlers
писать бизнес-логику прямо в transport/http
смешивать sql/models/api contracts/domain objects без структуры
использовать глобальные синглтоны без необходимости
писать “магический” код без типизированных контрактов
делать giant files по 1500+ строк
делать логику sync/import внутри контроллеров
4.3. Multi-tenant

Система обязана быть multi-tenant:

каждый workspace изолирован;
все tenant-данные связаны с workspace_id;
в API и jobs обязательно tenant scoping;
невозможно получить чужие данные через API;
воркеры и sync jobs tenant-aware.
4.4. Explainable backend

Все рекомендации и аналитические выводы должны быть объяснимыми.

Нельзя строить backend как black box.

5. Рекомендуемая структура проекта

Ниже ожидаемая профессиональная структура проекта.

cmd/
  api/
    main.go
  worker/
    main.go

internal/
  app/
    api/
      server.go
      router.go
      middleware/
    worker/
      worker.go
      queues.go
      schedulers.go

  config/
    config.go
    env.go
    validation.go

  domain/
    auth/
    user/
    workspace/
    role/
    cabinet/
    product/
    campaign/
    campaignstat/
    phrase/
    phrasestat/
    position/
    serp/
    bid/
    recommendation/
    export/
    extension/
    auditlog/
    jobrun/

  application/
    auth/
    workspace/
    cabinet/
    product/
    campaign/
    phrase/
    position/
    serp/
    bid/
    recommendation/
    export/
    extension/
    sync/

  infrastructure/
    db/
      postgres/
        migrations/
        queries/
        sqlc/
        repositories/
        tx/
    redis/
    queue/
    logger/
    auth/
    crypto/
    telemetry/
    clock/
    idgen/
    storage/

  integrations/
    wildberries/
      client/
      dto/
      mapper/
      adapter/
      ratelimit/
      retry/
      sync/

  transport/
    http/
      handlers/
      dto/
      mapper/
      middleware/
      response/
    jobs/
      processors/

  pkg/
    pointer/
    lo/
    pagination/
    errors/
    money/
    dates/
    validator/

api/
  openapi/

sql/
  queries/

migrations/

deploy/
  docker/
  compose/

test/
  integration/
  e2e/
  fixtures/

Makefile
go.mod
README.md

Если агент предложит еще более сильную структуру — можно улучшить, но она должна остаться:

чистой;
предсказуемой;
расширяемой;
enterprise-level.
6. Границы MVP backend
В MVP обязательно реализовать
Аутентификация
Workspaces и members
RBAC basic
Seller cabinets
Wildberries integration skeleton
Campaigns
Campaign daily stats
Campaign phrases
Phrase daily stats
Position tracking targets
Position snapshots
SERP snapshots
SERP result items
Bid snapshots / estimated bid ranges
Recommendations
Extension session API
Background jobs
Schedulers
Audit log basic
Health checks
Пока не углублять
billing
full event sourcing
websocket streaming
ML scoring
advanced anomaly detection
real attribution engine for external traffic
full autonomous bid management
7. Доменные модули
7.1. Auth

Функции:

login
refresh
logout
me
jwt auth
password hashing
session management

Сущности:

User
Session
7.2. Workspaces

Функции:

создать workspace
список workspaces
members
роли
доступ пользователя к workspace

Сущности:

Workspace
WorkspaceMember
Role
Permission
7.3. Seller Cabinets

Функции:

создать кабинет
привязать кабинет к workspace
активировать/деактивировать
хранить внешние идентификаторы

Сущности:

SellerCabinet
CabinetIntegration
CabinetSettings
7.4. Products

Функции:

импорт товаров
поиск по sku / nmID / brand
хранение связки продукт ↔ кабинет
хранение change events later-ready

Сущности:

Product
ProductIdentifier
ProductChangeEvent
Brand
7.5. Campaigns

Функции:

import campaigns
list campaigns
campaign detail
campaign summary
filters and pagination

Сущности:

Campaign
CampaignStatus
CampaignType
7.6. Campaign Daily Stats

Функции:

upsert daily stats
history by date range
compare periods
aggregate KPIs

Сущности:

CampaignDailyStat

Поля:

id
workspace_id
cabinet_id
campaign_id
stat_date
impressions
clicks
ctr
cpc
spend
carts
cart_rate
orders
conversion_rate
cpo
revenue
drr
raw_payload
imported_at
7.7. Campaign Phrases

Функции:

import phrases
normalize phrases
labels
favorites
negative phrase suggestions
cluster references

Сущности:

CampaignPhrase
PhraseLabel
PhraseCluster
FavoritePhrase
NegativePhraseSuggestion
7.8. Phrase Daily Stats

Функции:

history
trends
performance scoring
weak/bad phrase detection

Сущности:

PhraseDailyStat

Поля:

id
workspace_id
phrase_id
stat_date
impressions
clicks
ctr
cpc
spend
carts
orders
cpo
avg_position
best_position
frequency
bid_estimate_min
bid_estimate_max
bid_confidence
imported_at
7.9. Positions

Функции:

create tracking target
manual position check
history
bulk checks
region-based checks
alert candidates

Сущности:

PositionTrackingTarget
PositionSnapshot
Region
7.10. SERP

Функции:

store search result snapshot
store result items
ad/organic labeling
compare snapshots
get snapshot history

Сущности:

SerpSnapshot
SerpResultItem
SerpRequestContext
7.11. Bids

Функции:

store bid ranges
confidence scoring
bind bid estimates to query / serp / phrase / campaign
bid history

Сущности:

BidSnapshot
BidEstimate
7.12. Recommendations

Функции:

generate recommendations
deduplicate
resolve / dismiss
explain recommendation
priority / severity / confidence

Сущности:

Recommendation
RecommendationType
RecommendationStatus
RecommendationSource

Типы рекомендаций:

raise_bid
lower_bid
pause_phrase
add_negative_phrase
improve_listing
check_price
improve_delivery
improve_main_image
review_cluster
review_campaign
7.13. Exports

Функции:

create export task
store export metadata
track status

Сущности:

ExportTask
ExportFileMeta
7.14. Extension

Функции:

extension auth/session start
version check
page context intake
widget data response
client event logging

Сущности:

ExtensionSession
ExtensionContextEvent
ExtensionClientVersion
7.15. Audit Log

Функции:

log critical domain actions
log auth events
log integration events
log recommendation status changes

Сущности:

AuditLog
7.16. Job Runs

Функции:

track processor runs
job statuses
retries
error messages
duration

Сущности:

JobRun
8. База данных
8.1. Обязательные таблицы
users
sessions
workspaces
workspace_members
roles
permissions
seller_cabinets
cabinet_integrations
products
product_identifiers
product_change_events
campaigns
campaign_daily_stats
campaign_phrases
phrase_labels
phrase_clusters
phrase_daily_stats
position_tracking_targets
position_snapshots
regions
serp_snapshots
serp_result_items
bid_snapshots
recommendations
export_tasks
extension_sessions
extension_context_events
audit_logs
job_runs
8.2. Общие требования к БД
UUID primary keys
created_at, updated_at везде
deleted_at там, где нужен soft delete
workspace_id во всех tenant tables
уникальные ограничения для идемпотентности
raw_payload JSONB там, где это оправдано
аналитические таблицы должны иметь правильные composite indexes
later-ready под partitioning
9. Ключевые индексы

Обязательно предусмотреть индексы:

(workspace_id, cabinet_id)
(workspace_id, campaign_id, stat_date desc)
(workspace_id, phrase_id, stat_date desc)
(workspace_id, product_id)
(workspace_id, query, region_code, checked_at desc)
(workspace_id, recommendation_status, created_at desc)
(workspace_id, campaign_status, updated_at desc)
(workspace_id, cabinet_id, external_id)
(workspace_id, normalized_phrase)
gin/jsonb index только там, где действительно нужен
10. HTTP API
10.1. Общие требования
REST API
versioned routes: /api/v1
consistent response envelope
request validation
pagination/filtering/sorting
workspace-aware auth
middleware-based request context
swagger/openapi
10.2. Формат ответа

Придерживаться одного контракта:

{
  "data": {},
  "meta": {},
  "error": null
}

Ошибки:

{
  "data": null,
  "meta": {},
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "invalid payload",
    "details": []
  }
}
10.3. Группы endpoint
Auth
POST /api/v1/auth/login
POST /api/v1/auth/refresh
POST /api/v1/auth/logout
GET /api/v1/auth/me
Workspaces
GET /api/v1/workspaces
POST /api/v1/workspaces
GET /api/v1/workspaces/:id
GET /api/v1/workspaces/:id/members
Cabinets
GET /api/v1/cabinets
POST /api/v1/cabinets
GET /api/v1/cabinets/:id
POST /api/v1/cabinets/:id/sync
Products
GET /api/v1/products
GET /api/v1/products/:id
Campaigns
GET /api/v1/campaigns
GET /api/v1/campaigns/:id
GET /api/v1/campaigns/:id/daily-stats
GET /api/v1/campaigns/:id/phrases
GET /api/v1/campaigns/:id/recommendations
Phrases
GET /api/v1/phrases
GET /api/v1/phrases/:id
GET /api/v1/phrases/:id/daily-stats
POST /api/v1/phrases/:id/favorite
POST /api/v1/phrases/:id/negative-suggestion
Positions
GET /api/v1/positions/targets
POST /api/v1/positions/targets
POST /api/v1/positions/check
GET /api/v1/positions/history
SERP
POST /api/v1/serp/check
GET /api/v1/serp/history
GET /api/v1/serp/:id
Bids
GET /api/v1/bids/history
GET /api/v1/bids/estimates
Recommendations
GET /api/v1/recommendations
POST /api/v1/recommendations/:id/resolve
POST /api/v1/recommendations/:id/dismiss
Exports
POST /api/v1/exports
GET /api/v1/exports/:id
Extension
POST /api/v1/extension/session/start
POST /api/v1/extension/context
GET /api/v1/extension/widgets/search
GET /api/v1/extension/widgets/product
GET /api/v1/extension/widgets/campaign
Health
GET /health/live
GET /health/ready
11. Очереди и воркеры
11.1. Очереди

Создать отдельные очереди:

wb-import-campaigns
wb-import-campaign-stats
wb-import-phrases
position-checks
serp-scans
bid-estimation
recommendation-generation
exports
extension-events-processing
11.2. Требования к processors

Каждый processor должен:

быть идемпотентным;
поддерживать retry;
логировать start / success / fail;
писать JobRun;
иметь structured logs;
иметь max retry policy;
уметь работать tenant-aware;
иметь timeout/cancellation support через context.Context.
11.3. Scheduler

Нужен scheduler для:

periodic cabinet sync;
campaigns refresh;
daily stats sync;
phrase stats sync;
priority position checks;
recommendation generation;
cleanup jobs;
stale sessions cleanup.
12. Интеграционный слой Wildberries

Нужно сделать integration module как отдельный слой.

12.1. Обязательно разделить
API client
DTO внешнего API
mappers
adapter interfaces
sync services
retry/rate-limit policies
12.2. Структура
internal/integrations/wildberries/
  client/
  dto/
  mapper/
  adapter/
  ratelimit/
  retry/
  sync/
12.3. Что должен уметь слой
safe HTTP requests
timeout handling
status code handling
rate limit handling
retry with backoff
external response logging
external dto to internal model mapping
partial sync tolerance
12.4. Требование

Не связывать бизнес-логику напрямую с внешними DTO Wildberries.

Обязательно нужен mapping в internal domain/application models.

13. Логика синков
13.1. Импорт кампаний
получить seller cabinet;
получить кампании из integration client;
map to internal model;
upsert campaigns;
mark stale/inactive where needed;
create downstream jobs for stats/phrases.
13.2. Импорт daily stats
взять кампании;
запросить статистику за период;
нормализовать;
upsert по (campaign_id, stat_date);
обновить sync state;
пересчитать campaign summaries if needed.
13.3. Импорт phrases
получить phrases;
нормализовать строку;
upsert phrases;
создать downstream phrase stats jobs;
создать recommendation generation job.
13.4. Position checks
получить tracking target;
выполнить collector logic;
сохранить snapshot;
сравнить с предыдущей позицией;
при сильной просадке создать candidate recommendation.
13.5. SERP scans
принять query + region;
собрать выдачу;
сохранить snapshot;
сохранить result items;
отметить ad/organic;
связать с known products if possible.
13.6. Bid estimation
использовать serp context / phrase stats / historical positions;
оценить bid range;
сохранить min/max/confidence;
не выдавать “точную ставку”, если данных недостаточно.
14. Recommendation engine v1

Нужен отдельный application service.

14.1. Требование

Все рекомендации должны быть explainable.

У каждой рекомендации должны быть:

title
description
recommendation_type
severity
confidence
source_metrics
next_action
source_entity_type
source_entity_id
14.2. Источники
campaign stats
phrase stats
positions
serp data
bid estimates
product context
delivery/logistics context if available
14.3. Правила v1

Примеры:

много показов и ноль кликов → проверить релевантность / фото / заголовок
много кликов и ноль заказов → плохая фраза или слабая карточка
высокая частота + низкая позиция + хорошая конверсия → повысить ставку
высокий CPC + плохой CPO → снизить ставку или остановить фразу
падение позиции по региону + ухудшение доставки → проверить склад/логистику
высокий расход без заказов → пересмотреть ключ / выключить / минусовать
14.4. Deduplication

Нужна дедупликация рекомендаций:

не плодить одно и то же каждые сутки;
обновлять existing recommendation, если проблема сохраняется;
закрывать recommendation, если ситуация ушла.
15. Auth и безопасность
15.1. Auth
JWT access token
refresh token
secure session storage
password hashing
middleware auth context
15.2. RBAC
workspace-scoped roles
owner
manager
analyst
viewer
15.3. Интеграционные секреты
encrypted storage for WB tokens
secrets only via config/env
no plaintext credentials in logs
no token leakage in API
15.4. Audit log

Логировать:

login/logout
cabinet connect/update
sync trigger
recommendation resolve/dismiss
extension session start
16. Наблюдаемость и логи
Structured logging

Каждый важный лог должен включать:

request_id
user_id
workspace_id
module
action
duration_ms
result
error_code
Обязательно логировать
external requests
failed syncs
retries
failed jobs
auth events
extension events
recommendation runs
Health

Сделать:

liveness
readiness
db health
redis health
queue health basic
17. Конфигурация

Сделать строгий typed config через env.

Пример env:

APP_ENV=development
APP_PORT=8080

POSTGRES_DSN=
REDIS_ADDR=
REDIS_PASSWORD=

JWT_ACCESS_SECRET=
JWT_REFRESH_SECRET=
JWT_ACCESS_TTL=15m
JWT_REFRESH_TTL=720h

ENCRYPTION_KEY=

WB_API_BASE_URL=
WB_API_TIMEOUT=10s

LOG_LEVEL=debug
QUEUE_PREFIX=sellico

Требование:

обязательная валидация env при старте;
приложение должно падать, если критичные env не заданы.
18. Тестирование

Нужно сделать:

Unit tests
services
recommendation rules
validation helpers
auth logic
Integration tests
repositories
db transactions
sync flows
workspace isolation
E2E basics
login
create workspace
create cabinet
list campaigns
trigger sync
get recommendations
Приоритет тестов
auth
workspace isolation
sync idempotency
campaign import
phrase import
position snapshot creation
recommendation generation
19. Что должен сделать ИИ-агент
Шаг 1. Архитектура
показать итоговую архитектуру
показать дерево каталогов
описать слои
описать key design decisions
Шаг 2. Схема БД
спроектировать таблицы
связи
индексы
миграции
Шаг 3. Инфраструктура
config
logger
db
redis
queue
health
middleware base
Шаг 4. Core modules
auth
users
workspaces
roles/rbac
audit log
Шаг 5. Domain modules
cabinets
products
campaigns
campaign stats
phrases
phrase stats
positions
serp
bids
recommendations
exports
extension
Шаг 6. Integrations
wildberries client
adapters
mappers
sync services
Шаг 7. Jobs
queues
processors
scheduler
job runs
Шаг 8. HTTP API
handlers
dto
middleware
error mapping
swagger/openapi
Шаг 9. Recommendation engine
rules
dedupe
explanation
status transitions
Шаг 10. Tests
unit
integration
e2e basics
Шаг 11. Docs
README
local run guide
migrations guide
worker run guide
20. Кодстайл и стандарты качества

ИИ должен писать код так:

strongly typed
idiomatic Go
small focused packages
no cyclic dependencies
context-aware operations
clear interfaces only where they are justified
proper transactions
clean error wrapping
no magic strings
no package-level state abuse
proper constructor functions
proper dependency injection
idempotent sync implementations

Запрещено:

превращать проект в набор utility-файлов
использовать giant models.go и service.go
делать все через empty interfaces
тащить domain logic в http handlers
писать половину проекта через TODO вместо реализации
бездумно генерировать 100 интерфейсов без пользы
21. Требования к производительности

Система должна быть рассчитана на:

десятки workspace
сотни seller cabinets
тысячи campaigns
десятки тысяч phrases
сотни тысяч stats rows
регулярные position/serp scans
большое число background jobs

Нужны:

pagination
proper indexes
batch inserts/upserts
minimal N+1
queue-based heavy tasks
optimized reads
concurrency control in workers
22. Требования к extension API

Нужен отдельный backend слой под extension.

Должно уметь:
проверить client version
создать session
принять page context
отдать widget payload
Search widget payload
query
frequency
current competitors
known positions
bid estimate range
query recommendations
Product widget payload
product summary
tracked phrases
current positions
competitor hints
content/listing recommendations
Campaign widget payload
campaign summary
bad phrases
phrase issues
recommendations
23. Что должно получиться в итоге

ИИ-агент должен выдать:

Полноценный backend-проект на Go
Чистую архитектуру
SQL schema / migrations
Core modules
Domain modules
Wildberries integration skeleton
Workers / queues / schedulers
REST API
Swagger/OpenAPI
Tests
Docker config
README
24. Финальный промт для ИИ-агента под Go

Ниже готовый блок, который можно вставить в IDE.

Ты — Senior Golang Backend Architect / Senior Distributed Systems Engineer / Senior Marketplace Analytics Backend Engineer.

Разработай production-grade backend на Go для модуля Sellico Ads Intelligence для Wildberries.

Цель системы:
создать multi-tenant backend, который подключает seller cabinets Wildberries, импортирует рекламные кампании, дневную статистику, статистику по ключевым фразам, хранит позиции товаров, SERP snapshots, bid snapshots, рекомендации и отдает API для Sellico web frontend и Chrome extension.

Используй стек:
- Go 1.24+
- PostgreSQL
- Redis
- chi или gin
- sqlc или ent
- asynq или river
- JWT auth
- OpenAPI/Swagger
- Docker
- Makefile

Предпочтительный стек реализации:
- chi
- sqlc
- asynq
- zerolog
- golang-migrate

Главные требования:
- production-grade code
- clean modular architecture
- multi-tenant workspace isolation
- RBAC
- background jobs
- idempotent sync operations
- explainable recommendation engine
- typed config
- structured logging
- health checks
- testable architecture
- no business logic in HTTP handlers
- no monolithic god services
- idiomatic Go
- proper use of context.Context
- no cyclic dependencies

Нужна архитектура с разделением на:
- domain
- application
- infrastructure
- integrations
- transport/http
- worker/jobs

Ожидаемая структура проекта:
- cmd/api
- cmd/worker
- internal/app
- internal/config
- internal/domain
- internal/application
- internal/infrastructure
- internal/integrations/wildberries
- internal/transport/http
- internal/transport/jobs
- migrations
- api/openapi
- test

Нужно реализовать backend модули:
- auth
- users
- workspaces
- roles/permissions
- seller-cabinets
- products
- campaigns
- campaign-daily-stats
- campaign-phrases
- phrase-daily-stats
- positions
- serp
- bids
- recommendations
- exports
- extension
- audit-log
- job-runs
- integrations/wildberries
- health

Обязательные сущности БД:
- users
- sessions
- workspaces
- workspace_members
- roles
- permissions
- seller_cabinets
- cabinet_integrations
- products
- product_identifiers
- product_change_events
- campaigns
- campaign_daily_stats
- campaign_phrases
- phrase_labels
- phrase_clusters
- phrase_daily_stats
- position_tracking_targets
- position_snapshots
- regions
- serp_snapshots
- serp_result_items
- bid_snapshots
- recommendations
- export_tasks
- extension_sessions
- extension_context_events
- audit_logs
- job_runs

Главные backend возможности MVP:
1. JWT auth
2. workspaces и members
3. seller cabinets
4. campaigns read model
5. import campaign stats
6. import campaign phrases
7. phrase stats history
8. position checks
9. serp snapshots
10. bid estimate storage
11. recommendation engine v1
12. extension session API
13. queues and schedulers
14. audit logs
15. health checks

Очереди:
- wb-import-campaigns
- wb-import-campaign-stats
- wb-import-phrases
- position-checks
- serp-scans
- bid-estimation
- recommendation-generation
- exports
- extension-events-processing

Требования к recommendation engine v1:
создавай explainable recommendations на основе campaign stats, phrase stats, positions, bid ranges, serp data и product context.
У каждой рекомендации должны быть:
- title
- description
- type
- severity
- confidence
- source metrics
- actionable next step

Примеры правил:
- много показов и ноль кликов -> issue with relevance / listing CTR
- много кликов и ноль заказов -> bad phrase or weak listing
- высокая частота, слабая позиция, хорошая конверсия -> raise bid
- высокий CPC, плохой CPO -> lower bid or pause
- просадка позиции в регионе + ухудшение доставки -> check warehouse/delivery

Требования к API:
- REST API
- routes versioned via /api/v1
- pagination/filtering/sorting
- workspace-aware
- unified response envelope
- request validation
- openapi docs

Формат ответа API:
{
  "data": {},
  "meta": {},
  "error": null
}

Требования к безопасности:
- workspace isolation
- RBAC
- encrypted storage of integration tokens
- no secrets in logs
- audit logging of critical actions

Требования к sync flows:
- idempotent imports
- upsert patterns
- retry/backoff
- sync state tracking
- external DTO mapping to internal models
- no direct coupling of business logic to external API schemas

Требования к quality:
- idiomatic Go
- proper package boundaries
- constructor-based dependency wiring
- meaningful domain services
- proper transaction handling
- no giant god services
- no business logic in handlers

Сначала обязательно:
1. покажи целевую архитектуру
2. покажи дерево каталогов
3. покажи схему БД и связи
4. покажи план реализации этапами

Только после этого начинай генерировать код.

Далее реализуй проект пошагово:
1. project bootstrap
2. config/logging/db/redis/queue infra
3. migrations and sqlc setup
4. auth/workspaces/rbac
5. seller cabinets/products/campaigns
6. campaign stats/phrases/phrase stats
7. positions/serp/bids
8. recommendation engine
9. extension API
10. jobs/processors/schedulers
11. openapi docs
12. tests
13. readme

Не упрощай архитектуру. Не делай demo-level backend. Пиши как для реального SaaS-продукта с перспективой масштабирования.
25. Что лучше дописать сверху в Windsurf / Cursor

Я бы добавил перед промтом еще это:

Важно:
- сначала спроектируй архитектуру и согласованную структуру каталогов;
- затем выведи SQL schema/table design;
- затем покажи список migration files;
- только потом начинай генерацию кода;
- не создавай лишние интерфейсы там, где они не нужны;
- используй sqlc для typed queries и repository layer поверх него;
- все sync операции делай идемпотентными.
26. Мой практический совет по стеку Go именно для этого модуля

Если делать на Go, я бы рекомендовал тебе вот такую связку:

Go 1.24+
chi
sqlc
PostgreSQL
Redis
asynq
zerolog
golang-migrate
swaggo или генерация openapi отдельно
Docker Compose для локалки
Почему именно так
chi — легкий, чистый, без лишней магии;
sqlc — очень хороший контроль запросов и типобезопасность;
asynq — понятные воркеры и фоновые задачи;
zerolog — быстрый и удобный structured logging;
golang-migrate — стандартный и понятный путь.
27. Что я бы еще сделал следующим сообщением

Самый полезный следующий шаг — подготовить тебе второй специализированный промт только под integration/sync engine на Go, чтобы ИИ отдельно качественно собрал:

wildberries client
retry/backoff
rate limiter
sync orchestrators
job processors
import pipelines
idempotent upserts

Именно этот блок обычно ломается у агентов чаще всего. Могу сразу сделать его отдельно.