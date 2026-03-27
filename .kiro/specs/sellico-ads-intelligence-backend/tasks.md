# План реализации: Sellico Ads Intelligence Backend

## Обзор

Пошаговая реализация production-grade Go backend для аналитики рекламы Wildberries. Каждый шаг строится на предыдущих, начиная с инфраструктуры и заканчивая интеграцией всех компонентов. Язык реализации: Go 1.24+.

## Задачи

- [x] 1. Инициализация проекта и инфраструктурный слой
  - [x] 1.1 Создать структуру проекта и go.mod
    - Создать директории: `cmd/api/`, `cmd/worker/`, `internal/config/`, `internal/domain/`, `internal/pkg/`, `internal/transport/`, `internal/service/`, `internal/repository/`, `internal/integration/`, `internal/worker/`, `migrations/`
    - Создать `go.mod` с зависимостями: chi, sqlc, asynq, zerolog, golang-migrate, google/uuid, pgx/v5, redis/v9, testify, rapid
    - Создать `Makefile` с целями: build, test, migrate-up, migrate-down, sqlc-generate, lint, docker-up, docker-down
    - Создать `Dockerfile` (multi-stage build) и `docker-compose.yml` (api, worker, postgres, redis)
    - Создать `.env.example` со всеми переменными окружения
    - _Требования: 18.5, 21.1_

  - [x] 1.2 Реализовать загрузку конфигурации из переменных окружения
    - Создать `internal/config/config.go` с типизированной структурой Config
    - Реализовать загрузку из env vars с валидацией обязательных полей (DATABASE_URL, REDIS_URL, JWT_SECRET, ENCRYPTION_KEY)
    - При отсутствии обязательной переменной — завершение процесса с описательным сообщением
    - _Требования: 18.1, 18.2_

  - [ ]* 1.3 Написать property-тест для конфигурации
    - **Property 23: Конфигурация — обязательные переменные окружения**
    - **Проверяет: Требования 18.1, 18.2**

  - [x] 1.4 Реализовать доменные модели
    - Создать `internal/domain/model.go` со всеми доменными структурами: User, Workspace, WorkspaceMember, SellerCabinet, Campaign, CampaignStat, Phrase, PhraseStat, Product, Position, SERPSnapshot, SERPResultItem, BidSnapshot, Recommendation, Export, ExtensionSession, AuditLog, JobRun, RefreshToken
    - Определить типы ролей (owner, manager, analyst, viewer), статусы, enum-ы
    - _Требования: 3.1, 21.2, 21.3, 21.4_

  - [x] 1.5 Реализовать криптографические утилиты
    - Создать `internal/pkg/crypto/aes.go` — AES-256-GCM encrypt/decrypt для API-токенов
    - Создать `internal/pkg/crypto/argon2.go` — argon2id hash/verify для паролей
    - _Требования: 19.1, 19.2, 19.3, 19.4_

  - [ ]* 1.6 Написать property-тест для AES-256-GCM round-trip
    - **Property 6: Шифрование токенов — AES-256-GCM round-trip**
    - **Проверяет: Требования 4.1, 4.4, 19.1**

  - [x] 1.7 Реализовать JWT утилиты
    - Создать `internal/pkg/jwt/jwt.go` — генерация и валидация JWT access/refresh токенов
    - Access token: configurable TTL (default 15m), содержит user_id
    - Refresh token: configurable TTL (default 7d), хранится как SHA-256 hash в БД
    - _Требования: 1.1, 1.2, 1.3_

  - [x] 1.8 Реализовать вспомогательные пакеты
    - Создать `internal/pkg/envelope/envelope.go` — Response_Envelope (data, meta, errors)
    - Создать `internal/pkg/pagination/pagination.go` — парсинг page/per_page из query params
    - Создать `internal/pkg/validate/validate.go` — валидация входных данных
    - Создать `internal/pkg/apperror/error.go` — типизированные ошибки (AppError: NotFound, Unauthorized, Forbidden, Validation, Internal, WBAPIError, DecryptionFail)
    - _Требования: 17.2, 17.3, 17.4_

- [x] 2. База данных: миграции, sqlc-запросы и репозиторий
  - [x] 2.1 Создать SQL-миграции для всех 17 таблиц
    - Создать `migrations/000001_init.up.sql` и `migrations/000001_init.down.sql`
    - Таблицы: users, refresh_tokens, workspaces, workspace_members, seller_cabinets, campaigns, campaign_stats, phrases, phrase_stats, products, positions, serp_snapshots, serp_result_items, bid_snapshots, recommendations, exports, extension_sessions, audit_logs, job_runs
    - UUID первичные ключи, created_at/updated_at, soft delete (deleted_at) где нужно
    - Все индексы согласно дизайну, workspace_id для tenant scope
    - _Требования: 21.1, 21.2, 21.3, 21.4, 21.5_

  - [x] 2.2 Настроить sqlc и написать SQL-запросы
    - Создать `sqlc.yaml` с конфигурацией для PostgreSQL + pgx/v5
    - Создать SQL-файлы в `internal/repository/queries/`: users.sql, workspaces.sql, workspace_members.sql, seller_cabinets.sql, campaigns.sql, campaign_stats.sql, phrases.sql, phrase_stats.sql, products.sql, positions.sql, serp_snapshots.sql, serp_result_items.sql, bid_snapshots.sql, recommendations.sql, exports.sql, extension_sessions.sql, audit_logs.sql, job_runs.sql, refresh_tokens.sql
    - Каждый файл: CRUD операции, upsert где нужно, фильтрация по workspace_id, пагинация, сортировка, фильтрация по диапазону дат
    - Сгенерировать Go-код через `sqlc generate`
    - _Требования: 21.5, 21.6_

  - [x] 2.3 Реализовать Redis cache
    - Создать `internal/repository/cache/redis.go` — реализация интерфейса Cache (Get, Set, Delete, InvalidateByPrefix)
    - _Требования: 18.3_

- [x] 3. Чекпоинт — Убедиться, что миграции применяются, sqlc генерирует код без ошибок
  - Убедиться, что все тесты проходят, задать вопросы пользователю при необходимости.

- [x] 4. Transport Layer: middleware и роутер
  - [x] 4.1 Реализовать middleware аутентификации (JWT)
    - Создать `internal/transport/middleware/auth.go` — извлечение и валидация JWT из заголовка Authorization
    - Добавить user_id в context запроса
    - При невалидном/истёкшем токене — HTTP 401 в Response_Envelope
    - _Требования: 1.5, 17.6_

  - [x] 4.2 Реализовать middleware tenant scope
    - Создать `internal/transport/middleware/tenant.go` — извлечение workspace_id из заголовка или URL
    - Проверка, что пользователь является участником workspace
    - При доступе к чужому workspace — HTTP 403
    - Добавить workspace_id в context запроса
    - _Требования: 2.2, 2.5_

  - [x] 4.3 Написать property-тест для tenant isolation
    - **Property 3: Tenant isolation — данные изолированы по workspace_id**
    - **Проверяет: Требования 2.2, 2.5**

  - [x] 4.4 Реализовать middleware RBAC
    - Создать `internal/transport/middleware/rbac.go` — проверка роли пользователя для эндпоинта
    - viewer: только чтение; analyst: нет доступа к seller cabinets и members; manager/owner: полный доступ
    - При недостаточных правах — HTTP 403
    - _Требования: 3.1, 3.2, 3.3, 3.5_

  - [x] 4.5 Написать property-тест для RBAC
    - **Property 5: RBAC — роли ограничивают доступ к операциям**
    - **Проверяет: Требования 3.2, 3.3, 3.5**

  - [x] 4.6 Реализовать вспомогательные middleware
    - Создать `internal/transport/middleware/requestid.go` — генерация и инъекция request_id
    - Создать `internal/transport/middleware/logging.go` — логирование запросов через zerolog (request_id, user_id, workspace_id)
    - Создать `internal/transport/middleware/recovery.go` — перехват panic, логирование stack trace, возврат HTTP 500 без деталей
    - _Требования: 20.1, 20.2, 20.5_

  - [x] 4.7 Написать property-тест для recovery middleware
    - **Property 27: Ошибки — HTTP 500 без внутренних деталей**
    - **Проверяет: Требования 20.5, 19.5**

  - [x] 4.8 Создать DTO запросов и ответов
    - Создать `internal/transport/dto/request.go` — DTO для всех входящих запросов с тегами валидации
    - Создать `internal/transport/dto/response.go` — DTO для всех ответов, Response_Envelope
    - _Требования: 17.2, 17.4_

  - [x] 4.9 Написать property-тест для Response_Envelope и пагинации
    - **Property 20: API формат — Response_Envelope и пагинация**
    - **Проверяет: Требования 17.2, 17.3**

  - [x] 4.10 Настроить chi роутер
    - Создать `internal/transport/router.go` — регистрация всех маршрутов с middleware
    - Префикс /api/v1 для всех эндпоинтов
    - Публичные маршруты: auth, health
    - Защищённые маршруты: все остальные (auth + tenant + RBAC middleware)
    - _Требования: 17.1, 17.6_

- [x] 5. Аутентификация и управление сессиями
  - [x] 5.1 Реализовать AuthService
    - Создать `internal/service/auth.go` — Register, Login, RefreshToken, Logout
    - Register: валидация email/password, хеширование argon2id, создание пользователя, генерация JWT пары
    - Login: проверка credentials, генерация JWT пары
    - RefreshToken: валидация refresh token (SHA-256 hash lookup), генерация новой пары, инвалидация старого
    - Logout: инвалидация refresh token (revoked = true)
    - _Требования: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6_

  - [x] 5.2 Написать property-тест для round-trip регистрации и логина
    - **Property 1: Аутентификация — round-trip регистрации и логина**
    - **Проверяет: Требования 1.1, 1.2, 19.4**

  - [x] 5.3 Написать property-тест для refresh и инвалидации токенов
    - **Property 2: Токены — refresh round-trip и инвалидация**
    - **Проверяет: Требования 1.3, 1.4, 1.5, 1.6**

  - [x] 5.4 Реализовать HTTP-хендлеры аутентификации
    - Создать `internal/transport/handler/auth.go` — POST /auth/register, /auth/login, /auth/refresh, /auth/logout
    - Валидация входных данных, вызов AuthService, формирование Response_Envelope
    - _Требования: 1.1, 1.2, 1.3, 1.4_

  - [x] 5.5 Написать unit-тесты для хендлеров аутентификации
    - Тесты: успешная регистрация, дублирование email, невалидные credentials, refresh с невалидным токеном
    - _Требования: 1.1, 1.2, 1.5, 1.6_

- [x] 6. Workspace, участники и RBAC
  - [x] 6.1 Реализовать WorkspaceService
    - Создать `internal/service/workspace.go` — Create, List, Get, InviteMember, UpdateMemberRole, RemoveMember
    - Create: создание workspace + назначение создателю роли owner
    - List: только workspace, в которых пользователь является участником
    - InviteMember: добавление пользователя с указанной ролью
    - UpdateMemberRole: изменение роли + запись в audit_log
    - _Требования: 2.1, 2.3, 2.4, 3.4_

  - [x] 6.2 Написать property-тест для создания workspace
    - **Property 4: Workspace — создатель получает роль owner**
    - **Проверяет: Требования 2.1, 2.3**

  - [x] 6.3 Реализовать HTTP-хендлеры workspace
    - Создать `internal/transport/handler/workspace.go` — CRUD workspace, управление участниками
    - POST /workspaces, GET /workspaces, GET /workspaces/{id}, POST /workspaces/{id}/members, PATCH /workspaces/{id}/members/{memberId}, DELETE /workspaces/{id}/members/{memberId}
    - _Требования: 2.1, 2.3, 2.4_

  - [x] 6.4 Написать unit-тесты для workspace хендлеров
    - Тесты: создание workspace, список workspace пользователя, приглашение участника, изменение роли
    - _Требования: 2.1, 2.3, 2.4, 3.4_

- [x] 7. Seller Cabinet и шифрование токенов
  - [x] 7.1 Реализовать SellerCabinetService
    - Создать `internal/service/seller_cabinet.go` — Create, List, Get, Delete
    - Create: шифрование API-токена AES-256-GCM, тестовый запрос к WB API, сохранение при успехе
    - List: кабинеты текущего workspace, без расшифрованных токенов в ответе
    - Delete: soft delete + остановка связанных фоновых задач
    - _Требования: 4.1, 4.2, 4.3, 4.4, 4.5_

  - [x] 7.2 Написать property-тест для валидации токена WB API
    - **Property 7: Валидация токена WB API при создании Seller Cabinet**
    - **Проверяет: Требования 4.2, 4.3**

  - [x] 7.3 Реализовать HTTP-хендлеры seller cabinet
    - Создать `internal/transport/handler/seller_cabinet.go` — POST /seller-cabinets, GET /seller-cabinets, GET /seller-cabinets/{id}, DELETE /seller-cabinets/{id}
    - RBAC: только owner и manager
    - _Требования: 4.1, 4.4, 4.5_

  - [x] 7.4 Написать unit-тесты для seller cabinet
    - Тесты: создание с валидным/невалидным токеном, список без расшифрованных токенов, soft delete
    - _Требования: 4.1, 4.2, 4.3, 4.4, 4.5, 19.1, 19.5_

- [x] 8. Чекпоинт — Убедиться, что auth, workspace, seller cabinet работают корректно
  - Убедиться, что все тесты проходят, задать вопросы пользователю при необходимости.

- [x] 9. Интеграционный слой: WB_Client
  - [x] 9.1 Реализовать базовый HTTP-клиент WB API
    - Создать `internal/integration/wb/client.go` — HTTP-клиент с retry (экспоненциальная задержка, до 3 попыток), rate-limiting (token bucket per Seller_Cabinet), обработка HTTP 429 (Retry-After) и HTTP 5xx
    - Конфигурируемый base URL через env (WB_API_BASE_URL)
    - Логирование всех запросов/ответов через zerolog (level: debug)
    - _Требования: 16.1, 16.4, 16.5, 16.6, 16.7_

  - [x] 9.2 Написать property-тест для retry и rate-limiting WB_Client
    - **Property 19: WB_Client — retry и rate-limiting**
    - **Проверяет: Требования 16.1, 16.5, 16.6**

  - [x] 9.3 Реализовать методы WB API
    - Создать `internal/integration/wb/campaigns.go` — ListCampaigns
    - Создать `internal/integration/wb/statistics.go` — GetCampaignStats
    - Создать `internal/integration/wb/search_clusters.go` — ListSearchClusters, GetSearchClusterStats
    - Создать `internal/integration/wb/bids.go` — GetRecommendedBids
    - Создать `internal/integration/wb/config_api.go` — GetCategoryConfig (cpm_min)
    - Создать `internal/integration/wb/products.go` — ListProducts
    - Создать `internal/integration/wb/analytics.go` — GetSalesFunnel, GetSellerAnalytics
    - Поддержка кампаний типа 9 (unified), bid_type (manual/unified), payment_type (cpm/cpc)
    - _Требования: 16.2, 16.3, 16.8_

  - [x] 9.4 Реализовать DTO и mappers WB API
    - Создать `internal/integration/wb/dto.go` — WBCampaignDTO, WBCampaignStatDTO, WBSearchClusterDTO, WBSearchClusterStatDTO, WBBidDTO, WBCategoryConfigDTO, WBProductDTO, WBSalesFunnelDTO
    - Создать `internal/integration/wb/mapper.go` — маппинг WB DTO → доменные модели
    - Версионирование DTO для адаптации к изменениям WB API
    - _Требования: 16.2, 16.8_

  - [x] 9.5 Написать unit-тесты для mappers WB API
    - Тесты: маппинг каждого DTO в доменную модель, edge cases (пустые поля, нулевые значения)
    - _Требования: 16.2_

- [x] 10. Интеграционный слой: WB_Catalog_Parser
  - [x] 10.1 Реализовать WB_Catalog_Parser
    - Создать `internal/integration/catalog/parser.go` — SearchProducts, FindProductPosition
    - HTTP-клиент к search.wb.ru с proxy-ротацией, retry (до 3 попыток), конфигурируемый rate-limiting
    - Парсинг JSON-ответа → CatalogProduct с позициями
    - _Требования: 23.1, 23.2, 23.3, 23.4, 23.9_

  - [x] 10.2 Реализовать proxy-ротацию
    - Создать `internal/integration/catalog/proxy.go` — пул прокси, round-robin, исключение заблокированных
    - HTTP 403: переключение прокси, пометка текущего как заблокированного
    - Graceful degradation при недоступности всех прокси
    - _Требования: 23.1, 23.5, 23.6_

  - [x] 10.3 Реализовать DTO парсера
    - Создать `internal/integration/catalog/dto.go` — CatalogProduct, CatalogSearchResponse
    - _Требования: 23.2_

  - [x] 10.4 Написать property-тест для WB_Catalog_Parser
    - **Property 25: WB_Catalog_Parser — поиск и определение позиции**
    - **Проверяет: Требования 23.2, 23.3**

- [x] 11. Кампании: сервис, хендлеры, импорт
  - [x] 11.1 Реализовать CampaignService
    - Создать `internal/service/campaign.go` — List, Get, GetStats
    - List: кампании workspace с пагинацией и фильтрацией
    - GetStats: статистика кампании с фильтрацией по диапазону дат
    - _Требования: 5.3, 6.6_

  - [x] 11.2 Реализовать HTTP-хендлеры кампаний
    - Создать `internal/transport/handler/campaign.go` — GET /campaigns, GET /campaigns/{id}, GET /campaigns/{id}/stats, GET /campaigns/{id}/phrases
    - _Требования: 6.6, 7.5_

  - [ ]* 11.3 Написать property-тест для фильтрации по диапазону дат
    - **Property 21: Фильтрация по диапазону дат**
    - **Проверяет: Требования 6.6, 8.3, 9.4, 10.4, 15.3**

  - [ ]* 11.4 Написать property-тест для валидации входных данных
    - **Property 22: Валидация входных данных**
    - **Проверяет: Требования 17.4**

- [ ] 12. Фразы (Search Clusters): сервис и хендлеры
  - [ ] 12.1 Реализовать PhraseService
    - Создать `internal/service/phrase.go` — ListByCampaign, GetStats
    - ListByCampaign: фразы кампании с последним Bid_Snapshot, пагинация и сортировка
    - GetStats: статистика фразы с фильтрацией по диапазону дат
    - _Требования: 7.5, 10.5_

  - [ ] 12.2 Реализовать HTTP-хендлеры фраз
    - Создать `internal/transport/handler/phrase.go` — GET /phrases/{id}, GET /phrases/{id}/stats, GET /phrases/{id}/bids
    - _Требования: 7.5, 10.4_

- [ ] 13. Продукты, позиции, SERP, ставки: сервисы и хендлеры
  - [ ] 13.1 Реализовать ProductService и хендлеры
    - Создать `internal/service/product.go` — List, Get
    - Создать `internal/transport/handler/product.go` — GET /products, GET /products/{id}, GET /products/{id}/positions
    - Пагинация, фильтрация по названию, сортировка; HTTP 404 при отсутствии
    - _Требования: 22.3, 22.4, 22.5_

  - [ ] 13.2 Реализовать PositionService и хендлеры
    - Создать `internal/service/position.go` — GetHistory, GetAggregated
    - Создать `internal/transport/handler/position.go` — GET /positions, GET /positions/aggregate
    - Фильтрация по товару, запросу, региону, диапазону дат; агрегация (средняя позиция)
    - _Требования: 8.3, 8.5_

  - [ ]* 13.3 Написать property-тест для позиций и агрегации
    - **Property 12: Позиции товаров — сохранение и агрегация**
    - **Проверяет: Требования 8.1, 8.2, 8.5**

  - [ ] 13.4 Реализовать SERPService и хендлеры
    - Создать `internal/service/serp.go` — List, Get
    - Создать `internal/transport/handler/serp.go` — GET /serp-snapshots, GET /serp-snapshots/{id}
    - Фильтрация по запросу, региону, диапазону дат; вложенные SERP_Result_Item
    - _Требования: 9.3, 9.4_

  - [ ] 13.5 Реализовать BidService и хендлеры
    - Создать `internal/service/bid.go` — GetHistory
    - Создать `internal/transport/handler/bid.go` — (история ставок через GET /phrases/{id}/bids)
    - Фильтрация по фразе и диапазону дат
    - _Требования: 10.4_

- [ ] 14. Чекпоинт — Убедиться, что все CRUD-сервисы и хендлеры работают
  - Убедиться, что все тесты проходят, задать вопросы пользователю при необходимости.

- [ ] 15. Рекомендации, экспорт, аудит: сервисы и хендлеры
  - [ ] 15.1 Реализовать RecommendationService и хендлеры
    - Создать `internal/service/recommendation.go` — List, UpdateStatus, Generate (rule-based engine v1)
    - Типы: bid_adjustment, position_drop, low_ctr, high_spend_low_orders, new_competitor
    - Дедупликация: не создавать дубликат, если активная рекомендация того же типа для той же сущности существует
    - Создать `internal/transport/handler/recommendation.go` — GET /recommendations, PATCH /recommendations/{id}
    - UpdateStatus: запись в audit_log
    - _Требования: 11.1, 11.2, 11.3, 11.4, 11.5_

  - [ ]* 15.2 Написать property-тест для рекомендаций и дедупликации
    - **Property 15: Рекомендации — генерация и дедупликация**
    - **Проверяет: Требования 11.1, 11.2, 11.3**

  - [ ] 15.3 Реализовать ExportService и хендлеры
    - Создать `internal/service/export.go` — Create, GetStatus, GenerateFile (CSV/XLSX)
    - Жизненный цикл: pending → processing → completed/failed
    - Создать `internal/transport/handler/export.go` — POST /exports, GET /exports/{id}, GET /exports/{id}/download
    - _Требования: 12.1, 12.2, 12.3, 12.4, 12.5_

  - [ ]* 15.4 Написать property-тест для жизненного цикла экспорта
    - **Property 16: Экспорт — жизненный цикл**
    - **Проверяет: Требования 12.1, 12.2, 12.3, 12.4**

  - [ ] 15.5 Реализовать AuditService и хендлеры
    - Создать `internal/service/audit.go` — Log, List
    - Создать `internal/transport/handler/audit.go` — GET /audit-logs
    - Фильтрация по action, entity_type, user_id, диапазону дат; пагинация и сортировка
    - Доступ ограничен ролями owner и manager
    - _Требования: 15.1, 15.2, 15.3, 15.4, 15.5_

  - [ ]* 15.6 Написать property-тест для аудита
    - **Property 18: Аудит — запись операций записи**
    - **Проверяет: Требования 15.1, 15.2, 15.4**

- [ ] 16. Chrome Extension: сервис и хендлеры
  - [ ] 16.1 Реализовать ExtensionService и хендлеры
    - Создать `internal/service/extension.go` — CreateSession, GetContextData, CheckVersion
    - GetContextData: по типу страницы (search/product) возвращает релевантные данные (позиции, статистика, рекомендации)
    - Создать `internal/transport/handler/extension.go` — POST /extension/sessions, POST /extension/context, GET /extension/version
    - _Требования: 13.1, 13.2, 13.3, 13.4, 13.5_

  - [ ]* 16.2 Написать property-тест для контекстных данных extension
    - **Property 28: Extension — контекстные данные по типу страницы**
    - **Проверяет: Требования 13.2, 13.4, 13.5**

- [ ] 17. Worker: обработчики фоновых задач
  - [ ] 17.1 Настроить asynq worker и scheduler
    - Создать `internal/worker/registry.go` — регистрация всех task handlers
    - Создать `internal/worker/scheduler.go` — настройка периодических задач (cron-расписание для каждого типа)
    - Приоритеты очередей: critical=6, default=3, low=1
    - Retry policy: до 3 попыток, экспоненциальная задержка (1min, 5min, 15min)
    - _Требования: 14.1, 14.2_

  - [ ] 17.2 Реализовать обработчик импорта кампаний
    - Создать `internal/worker/handler/import_campaigns.go` — задача wb:import:campaigns
    - Для каждого активного Seller_Cabinet: расшифровка токена, запрос ListCampaigns через WB_Client, upsert кампаний
    - Запись результата в job_run (created/updated counts)
    - Tenant scope через workspace_id в payload
    - _Требования: 5.1, 5.2, 5.3, 5.4, 5.5_

  - [ ]* 17.3 Написать property-тест для upsert идемпотентности
    - **Property 8: Upsert идемпотентность**
    - **Проверяет: Требования 5.2, 6.5, 7.2, 22.2**

  - [ ]* 17.4 Написать property-тест для привязки кампаний к workspace
    - **Property 9: Импорт кампаний — привязка к workspace**
    - **Проверяет: Требования 5.3, 5.5**

  - [ ] 17.5 Реализовать обработчик импорта статистики кампаний
    - Создать `internal/worker/handler/import_campaign_stats.go` — задача wb:import:campaign-stats
    - Запрос дневной статистики (impressions, clicks, spend) из WB Advertising API
    - Upsert campaign_stat по ключу (campaign_id, date)
    - _Требования: 6.1, 6.2, 6.5, 6.7_

  - [ ]* 17.6 Написать property-тест для целостности статистики кампаний
    - **Property 10: Импорт статистики кампаний — целостность данных**
    - **Проверяет: Требования 6.1, 6.2**

  - [ ] 17.7 Реализовать обработчик импорта Sales Funnel
    - Создать `internal/worker/handler/import_sales_funnel.go` — задача wb:import:sales-funnel
    - Запрос Sales Funnel v3 (views, addToCart, orders) из WB Analytics API
    - Проверка доступности подписки "Jam"; при недоступности — пропуск с предупреждением в job_run
    - _Требования: 6.3, 6.4_

  - [ ] 17.8 Реализовать обработчик импорта Search Clusters и статистики
    - Создать `internal/worker/handler/import_phrases.go` — задача wb:import:phrases
    - Запрос Search Clusters из WB API, маппинг на Phrase, upsert
    - Создать `internal/worker/handler/import_phrase_stats.go` — задача wb:import:phrase-stats
    - Запрос статистики Search Clusters, сохранение phrase_stat
    - _Требования: 7.1, 7.2, 7.3, 7.4_

  - [ ]* 17.9 Написать property-тест для маппинга Search Clusters
    - **Property 11: Импорт Search Clusters — маппинг и статистика**
    - **Проверяет: Требования 7.1, 7.3, 7.4**

  - [ ] 17.10 Реализовать обработчик импорта продуктов
    - Добавить импорт Products в обработчик import_campaigns или создать отдельный
    - Запрос ListProducts через WB_Client, upsert по wb_product_id
    - Привязка к workspace через seller_cabinet
    - _Требования: 22.1, 22.2_

- [ ] 18. Worker: позиции, SERP, ставки, рекомендации, экспорт
  - [ ] 18.1 Реализовать обработчик проверки позиций
    - Создать `internal/worker/handler/position_checks.go` — задача position:check
    - Для каждого отслеживаемого товара: запрос позиции через WB_Catalog_Parser по запросам и регионам
    - Сохранение Position (position = -1 если не найден)
    - Fallback на medianPosition из Seller Analytics при недоступности парсера
    - Retry с экспоненциальной задержкой (до 3 попыток)
    - _Требования: 8.1, 8.2, 8.4, 8.6, 8.7_

  - [ ] 18.2 Реализовать обработчик SERP-сканирования
    - Создать `internal/worker/handler/serp_scans.go` — задача serp:scan
    - Запрос поисковой выдачи через WB_Catalog_Parser
    - Сохранение SERP_Snapshot + SERP_Result_Item (position, product_id, title, price, rating, reviews_count)
    - _Требования: 9.1, 9.2, 9.5_

  - [ ]* 18.3 Написать property-тест для SERP Snapshots
    - **Property 13: SERP Snapshots — сохранение с items**
    - **Проверяет: Требования 9.1, 9.2**

  - [ ] 18.4 Реализовать обработчик сбора ставок
    - Создать `internal/worker/handler/bid_estimation.go` — задача bid:estimation
    - Запрос рекомендованных ставок (competitive_bid, leadership_bid) из WB API
    - Запрос cpm_min из Configuration API (categories)
    - Сохранение Bid_Snapshot
    - _Требования: 10.1, 10.2, 10.3, 10.6_

  - [ ]* 18.5 Написать property-тест для Bid Snapshots
    - **Property 14: Bid Snapshots — три типа ставок**
    - **Проверяет: Требования 10.1, 10.2, 10.3, 10.5**

  - [ ] 18.6 Реализовать обработчик генерации рекомендаций
    - Создать `internal/worker/handler/recommendation_gen.go` — задача recommendation:generate
    - Анализ Campaign_Stat, Phrase_Stat, Position, Bid_Snapshot
    - Генерация Recommendation с дедупликацией
    - _Требования: 11.1, 11.2, 11.3_

  - [ ] 18.7 Реализовать обработчик экспорта
    - Создать `internal/worker/handler/exports.go` — задача export:generate
    - Генерация CSV/XLSX файлов, сохранение в хранилище
    - Обновление статуса Export (completed/failed)
    - _Требования: 12.2, 12.3, 12.5_

  - [ ] 18.8 Реализовать обработчик Seller Analytics
    - Создать `internal/worker/handler/import_seller_analytics.go` — задача wb:import:seller-analytics
    - Запрос CSV-отчёта из WB Seller Analytics API, парсинг CSV
    - Проверка подписки "Jam"; при недоступности — пропуск с предупреждением
    - Upsert по ключу (seller_cabinet_id, query, date)
    - _Требования: 24.1, 24.2, 24.3, 24.5_

  - [ ] 18.9 Реализовать обработчик событий Chrome extension
    - Создать `internal/worker/handler/extension_events.go` — задача extension:events
    - _Требования: 14.1_

  - [ ]* 18.10 Написать property-тест для записи Job Run
    - **Property 17: Job Run — запись результатов задач**
    - **Проверяет: Требования 14.3, 14.4**

- [ ] 19. Чекпоинт — Убедиться, что все worker handlers работают корректно
  - Убедиться, что все тесты проходят, задать вопросы пользователю при необходимости.

- [ ] 20. Health checks, логирование, наблюдаемость
  - [ ] 20.1 Реализовать health check хендлеры
    - Создать `internal/transport/handler/health.go` — GET /health/live (всегда 200), GET /health/ready (проверка PostgreSQL, Redis)
    - _Требования: 18.3_

  - [ ] 20.2 Настроить структурированное логирование
    - Настроить zerolog для JSON-формата во всех компонентах
    - HTTP-запросы: request_id, user_id, workspace_id в каждой записи
    - Фоновые задачи: job_id, task_type, workspace_id в каждой записи
    - _Требования: 20.1, 20.2, 20.3_

  - [ ]* 20.3 Написать property-тест для контекстных полей логирования
    - **Property 26: Логирование — контекстные поля**
    - **Проверяет: Требования 20.2, 20.3**

  - [ ] 20.4 Подготовить интеграцию с OpenTelemetry
    - Добавить заглушки для экспорта трейсов и метрик через OpenTelemetry SDK
    - _Требования: 20.4_

  - [ ]* 20.5 Написать property-тест для soft delete
    - **Property 24: Soft delete — скрытие из обычных запросов**
    - **Проверяет: Требования 4.5, 21.4**

- [ ] 21. Точки входа: cmd/api и cmd/worker
  - [ ] 21.1 Реализовать точку входа API Server
    - Создать `cmd/api/main.go` — загрузка конфигурации, подключение к PostgreSQL и Redis, инициализация всех сервисов, настройка роутера, запуск HTTP-сервера
    - Graceful shutdown при SIGTERM
    - _Требования: 18.4, 18.5_

  - [ ] 21.2 Реализовать точку входа Worker
    - Создать `cmd/worker/main.go` — загрузка конфигурации, подключение к PostgreSQL и Redis, инициализация сервисов, регистрация task handlers, запуск asynq server + scheduler
    - Graceful shutdown при SIGTERM
    - _Требования: 14.1, 14.2, 18.4, 18.5_

- [ ] 22. Интеграция и финальная сборка
  - [ ] 22.1 Подключить все компоненты в роутере
    - Убедиться, что все 50+ эндпоинтов зарегистрированы в router.go с правильными middleware
    - Проверить цепочки middleware: auth → tenant → RBAC для каждой группы маршрутов
    - _Требования: 17.1, 17.6_

  - [ ] 22.2 Подключить все worker handlers в registry
    - Убедиться, что все 12 типов задач зарегистрированы в registry.go
    - Проверить расписание scheduler для каждого типа задач
    - _Требования: 14.1, 14.2_

  - [ ] 22.3 Создать OpenAPI/Swagger спецификацию
    - Сгенерировать или написать OpenAPI spec для всех эндпоинтов
    - _Требования: 17.5_

- [ ] 23. Финальный чекпоинт — Полная проверка системы
  - Убедиться, что все тесты проходят, Docker Compose запускается, миграции применяются, API отвечает на health checks. Задать вопросы пользователю при необходимости.

## Примечания

- Задачи, помеченные `*`, являются опциональными и могут быть пропущены для ускорения MVP
- Каждая задача ссылается на конкретные требования для трассируемости
- Чекпоинты обеспечивают инкрементальную валидацию
- Property-тесты проверяют универсальные свойства корректности (библиотека: pgregory.net/rapid)
- Unit-тесты проверяют конкретные примеры и edge cases (testify/assert)
- Все property-тесты помечаются тегом: `Feature: sellico-ads-intelligence-backend, Property {N}: {title}`
