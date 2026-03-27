# Документ требований: Sellico Ads Intelligence Backend

## Введение

Sellico Ads Intelligence Backend — production-grade backend-система на Go для аналитики и мониторинга рекламы Wildberries. Система предоставляет multi-tenant API для управления рекламными кампаниями, сбора статистики, отслеживания позиций товаров, анализа поисковой выдачи, оценки ставок и генерации рекомендаций. Проект охватывает ТОЛЬКО backend (REST API, фоновые задачи, интеграции). Фронтенд НЕ входит в scope.

## Глоссарий

- **Backend**: серверная часть системы, предоставляющая REST API, фоновые задачи и интеграции; фронтенд-код НЕ входит в scope проекта
- **API_Server**: HTTP-сервер, обрабатывающий REST-запросы от клиентов (web-приложение, Chrome extension)
- **Worker**: фоновый процесс, выполняющий асинхронные задачи из очередей (asynq)
- **Scheduler**: планировщик, создающий периодические задачи по расписанию
- **Workspace**: изолированная рабочая область (tenant) с собственными данными и пользователями
- **Seller_Cabinet**: подключённый кабинет продавца Wildberries с API-токеном
- **Campaign**: рекламная кампания Wildberries, импортированная из Seller_Cabinet
- **Campaign_Stat**: дневная статистика рекламной кампании (показы, клики, расходы, заказы)
- **Phrase**: ключевая фраза, привязанная к Campaign; в контексте WB API маппится на Search Cluster
- **Search_Cluster**: единица таргетинга в WB Advertising API (заменяет устаревшие fixed phrases с января 2026); содержит поисковые запросы, по которым показывается реклама
- **Phrase_Stat**: дневная статистика ключевой фразы (Search Cluster)
- **Position**: позиция товара в поисковой выдаче Wildberries по конкретному запросу
- **SERP_Snapshot**: снимок поисковой выдачи Wildberries на определённый момент времени
- **SERP_Result_Item**: отдельный элемент (товар) внутри SERP_Snapshot
- **Bid_Snapshot**: снимок рекомендованных ставок для ключевой фразы на определённый момент; содержит competitive_bid и leadership_bid из WB API, а также cpm_min из конфигурации категорий
- **Estimated_Bid_Range**: рекомендованные ставки (competitive_bid, leadership_bid, cpm_min) для фразы
- **WB_Catalog_Parser**: модуль парсинга публичного каталога Wildberries (search.wb.ru) для получения поисковой выдачи и позиций товаров; используется для данных, недоступных через официальный WB API
- **Recommendation**: сгенерированная рекомендация с объяснением (explainable), типом, severity, confidence
- **Export**: задача экспорта данных в файл (CSV/XLSX)
- **Extension_Session**: сессия Chrome extension, привязанная к пользователю и Workspace
- **Audit_Log**: запись аудита действий пользователей и системных событий
- **Job_Run**: запись о выполнении фоновой задачи (статус, время, ошибки)
- **WB_Client**: HTTP-клиент для взаимодействия с API Wildberries
- **RBAC**: ролевая модель доступа (owner, manager, analyst, viewer)
- **JWT**: JSON Web Token для аутентификации пользователей
- **Response_Envelope**: унифицированная обёртка ответа API с полями data, meta, errors
- **Tenant_Scope**: изоляция данных по workspace_id во всех запросах и задачах

## Технологический стек

| Категория | Технология | Обоснование |
|---|---|---|
| Язык | Go 1.24+ | Производительность, строгая типизация, отличная поддержка concurrency |
| База данных | PostgreSQL | Надёжная RDBMS, JSONB, composite indexes, partitioning-ready |
| Кэш / Очереди | Redis | Быстрый in-memory store, бэкенд для asynq |
| HTTP Router | chi | Лёгкий, идиоматичный, middleware-friendly, enterprise-style |
| SQL Layer | sqlc | Строгий контроль SQL, типобезопасная кодогенерация из SQL-запросов |
| Миграции | golang-migrate | Стандартный инструмент, версионирование схемы БД |
| Фоновые задачи | asynq | Понятная модель очередей и воркеров поверх Redis |
| Логирование | zerolog | Быстрый structured logging в JSON |
| Аутентификация | JWT (access + refresh tokens) | Stateless auth для API |
| Хеширование паролей | argon2id | Современный и безопасный алгоритм |
| Шифрование токенов | AES-256-GCM | Шифрование API-токенов WB в БД |
| Observability | OpenTelemetry-ready hooks | Подготовка к трейсингу и метрикам |
| API Docs | OpenAPI / Swagger | Документирование REST API |
| Контейнеризация | Docker, Docker Compose | Локальная разработка и деплой |
| Сборка | Makefile | Единая точка входа для build/test/migrate/lint |
| Тестирование | Go built-in testing + integration tests | Unit, integration, e2e-ready структура |

## Ограничения проекта

1. Проект охватывает ТОЛЬКО backend-часть: REST API, фоновые задачи (workers/schedulers), интеграции с Wildberries, базу данных и инфраструктуру.
2. Фронтенд, UI-компоненты, HTML-шаблоны, статические файлы и любой клиентский код НЕ входят в scope и НЕ реализуются.
3. Backend предоставляет REST API, который потребляется внешними клиентами (web-приложение Sellico, Chrome extension).

## Анализ реализуемости через WB API

По результатам исследования Wildberries Advertising API (март 2026) определены источники данных для каждого функционального блока системы.

### Доступно через официальный WB Advertising API

| Функциональный блок | Метод WB API | Примечания |
|---|---|---|
| Кампании (список, создание, управление) | Campaign Management API | Тип 9 (unified), bid_type: manual/unified, payment_type: cpm/cpc |
| Статистика кампаний (показы, клики, расходы) | Campaign Statistics API | Не содержит заказы/выручку |
| Search Clusters (ключевые фразы) | Search Cluster Bid List, Set Bids, Delete Bids, Search Cluster Statistics | Старые методы fixed phrases отключены с января 2026 |
| Рекомендованные ставки | Getting Recommended Bids (февраль 2026) | Возвращает competitive_bid и leadership_bid по campaign ID и WB article |
| Минимальные ставки по категориям | Configuration API (categories) | Возвращает cpm_min по категориям |
| Минус-фразы | Set/Delete Negative Phrases | Только для manual bid кампаний |
| Товары | Product Cards List | nmId, chrtId, vendorCode |
| Sales Funnel v3 | Analytics API | views, addToCart, orders, addToWishList; требует подписки "Jam" |
| Seller Analytics CSV | Analytics API | Отчёты по поисковым запросам, medianPosition, частотность; требует подписки "Jam" |

### Недоступно через официальный WB API (требуется парсинг)

| Функциональный блок | Причина | Решение |
|---|---|---|
| Позиции товаров в поисковой выдаче (real-time) | Нет API-метода "позиция товара X по запросу Y"; Seller Analytics даёт только medianPosition (агрегированная за период) | Парсинг публичного каталога search.wb.ru через WB_Catalog_Parser |
| SERP Snapshots (полная поисковая выдача) | Нет API для получения полной поисковой выдачи | Парсинг публичного каталога search.wb.ru через WB_Catalog_Parser |
| Дневная статистика кампаний с заказами/выручкой | Рекламный API даёт impressions/clicks/spend; заказы/выручка через Sales Funnel (analytics), но не привязаны к рекламной кампании напрямую | Собственная атрибуция или использование Sales Funnel v3 отдельно |

### Ограничения и риски

1. WB API активно развивается (18 обновлений в декабре 2025); необходимо закладывать адаптивность интеграционного слоя.
2. Домены WB API перешли с wb.ru на wildberries.ru.
3. Все кампании теперь тип 9 (unified) с bid_type (manual/unified) и payment_type (cpm/cpc).
4. Парсинг search.wb.ru — неофициальный подход; возможны блокировки, изменения формата, необходимость proxy-ротации.
5. Sales Funnel v3 и Seller Analytics CSV требуют подписки "Jam" — необходима проверка доступности перед использованием.

## Требования

### Требование 1: Аутентификация и управление сессиями

**User Story:** Как пользователь, я хочу регистрироваться, входить в систему и управлять сессиями через JWT, чтобы безопасно работать с API.

#### Критерии приёмки

1. WHEN пользователь отправляет валидные email и пароль на эндпоинт регистрации, THE API_Server SHALL создать нового пользователя с хешированным паролем (argon2id) и вернуть JWT access token и refresh token в Response_Envelope.
2. WHEN пользователь отправляет валидные credentials на эндпоинт логина, THE API_Server SHALL проверить пароль, сгенерировать JWT access token и refresh token и вернуть их в Response_Envelope.
3. WHEN пользователь отправляет валидный refresh token, THE API_Server SHALL выдать новый access token и новый refresh token.
4. WHEN пользователь отправляет запрос на logout, THE API_Server SHALL инвалидировать текущий refresh token.
5. IF access token истёк или невалиден, THEN THE API_Server SHALL вернуть HTTP 401 с описанием ошибки в Response_Envelope.
6. IF refresh token истёк или инвалидирован, THEN THE API_Server SHALL вернуть HTTP 401 и потребовать повторный логин.

### Требование 2: Управление Workspace и multi-tenancy

**User Story:** Как пользователь, я хочу создавать и управлять Workspace, чтобы изолировать данные разных проектов или команд.

#### Критерии приёмки

1. WHEN аутентифицированный пользователь создаёт Workspace, THE API_Server SHALL создать Workspace и назначить создателю роль owner.
2. THE API_Server SHALL изолировать все данные по workspace_id (Tenant_Scope) во всех запросах и фоновых задачах.
3. WHEN пользователь запрашивает список Workspace, THE API_Server SHALL вернуть только те Workspace, в которых пользователь является участником.
4. WHEN owner приглашает пользователя в Workspace с указанной ролью, THE API_Server SHALL добавить пользователя с указанной ролью RBAC.
5. IF пользователь пытается получить доступ к данным чужого Workspace, THEN THE API_Server SHALL вернуть HTTP 403.

### Требование 3: Ролевая модель доступа (RBAC)

**User Story:** Как owner Workspace, я хочу назначать роли участникам, чтобы контролировать уровень доступа к данным и операциям.

#### Критерии приёмки

1. THE API_Server SHALL поддерживать четыре роли: owner, manager, analyst, viewer.
2. WHEN пользователь с ролью viewer пытается выполнить операцию записи, THE API_Server SHALL вернуть HTTP 403.
3. WHEN пользователь с ролью analyst пытается управлять Seller_Cabinet или участниками Workspace, THE API_Server SHALL вернуть HTTP 403.
4. WHEN owner изменяет роль участника Workspace, THE API_Server SHALL обновить роль и записать событие в Audit_Log.
5. THE API_Server SHALL проверять роль пользователя при каждом запросе к защищённым эндпоинтам.

### Требование 4: Управление Seller Cabinet

**User Story:** Как manager, я хочу подключать кабинеты продавцов Wildberries, чтобы импортировать данные о рекламных кампаниях.

#### Критерии приёмки

1. WHEN пользователь с ролью owner или manager добавляет Seller_Cabinet с API-токеном WB, THE API_Server SHALL зашифровать токен перед сохранением в базу данных.
2. WHEN Seller_Cabinet создан, THE API_Server SHALL выполнить тестовый запрос к WB API для проверки валидности токена.
3. IF тестовый запрос к WB API завершился ошибкой, THEN THE API_Server SHALL вернуть ошибку валидации и не сохранять Seller_Cabinet.
4. WHEN пользователь запрашивает список Seller_Cabinet, THE API_Server SHALL вернуть кабинеты только текущего Workspace без расшифрованных токенов.
5. WHEN пользователь удаляет Seller_Cabinet, THE API_Server SHALL выполнить soft delete и остановить связанные фоновые задачи.

### Требование 5: Импорт и синхронизация кампаний

**User Story:** Как пользователь, я хочу автоматически импортировать рекламные кампании из Wildberries, чтобы видеть актуальные данные.

#### Критерии приёмки

1. WHEN Scheduler запускает задачу wb-import-campaigns, THE Worker SHALL запросить список кампаний из WB API для каждого активного Seller_Cabinet.
2. WHEN WB_Client возвращает список кампаний, THE Worker SHALL создать новые Campaign или обновить существующие (upsert) идемпотентно.
3. THE Worker SHALL привязать каждую Campaign к соответствующему Workspace через Seller_Cabinet.
4. IF WB_Client возвращает ошибку при импорте, THEN THE Worker SHALL записать ошибку в Job_Run и повторить попытку с экспоненциальной задержкой (до 3 попыток).
5. WHEN импорт кампаний завершён, THE Worker SHALL записать результат (количество созданных/обновлённых) в Job_Run.

### Требование 6: Дневная статистика кампаний

**User Story:** Как аналитик, я хочу видеть дневную статистику рекламных кампаний, чтобы оценивать эффективность рекламы.

**Примечание по источникам данных:** Показы, клики и расходы доступны через WB Advertising API (Campaign Statistics). Заказы и выручка доступны через Sales Funnel v3 (Analytics API, требует подписки "Jam") и не привязаны к рекламной кампании напрямую. Для связки рекламных расходов с заказами необходима собственная атрибуция на стороне backend.

#### Критерии приёмки

1. WHEN Scheduler запускает задачу wb-import-campaign-stats, THE Worker SHALL запросить дневную статистику (impressions, clicks, spend) из WB Advertising API для каждой активной Campaign.
2. WHEN WB_Client возвращает статистику, THE Worker SHALL сохранить Campaign_Stat с полями: дата, показы, клики, расходы.
3. WHEN Scheduler запускает задачу wb-import-sales-funnel, THE Worker SHALL запросить данные Sales Funnel v3 (views, addToCart, orders) из WB Analytics API для товаров активных Campaign.
4. IF подписка "Jam" недоступна для Seller_Cabinet, THEN THE Worker SHALL пропустить импорт Sales Funnel, записать предупреждение в Job_Run и продолжить работу с данными из Advertising API.
5. THE Worker SHALL выполнять upsert Campaign_Stat по ключу (campaign_id, date) идемпотентно.
6. WHEN пользователь запрашивает статистику кампании через API, THE API_Server SHALL вернуть Campaign_Stat с поддержкой фильтрации по диапазону дат и пагинации.
7. IF WB_Client возвращает ошибку, THEN THE Worker SHALL записать ошибку в Job_Run и повторить попытку.

### Требование 7: Search Clusters кампаний

**User Story:** Как пользователь, я хочу видеть Search Clusters (ключевые фразы) рекламных кампаний и их статистику, чтобы оптимизировать рекламу.

**Примечание по WB API:** С января 2026 WB API работает через Search Clusters вместо устаревших fixed phrases. Доменная модель Phrase маппится на Search Cluster из WB API. Доступные методы: Search Cluster Bid List, Set Bids, Delete Bids, Search Cluster Statistics.

#### Критерии приёмки

1. WHEN Scheduler запускает задачу wb-import-phrases, THE Worker SHALL запросить Search Clusters из WB API (Search Cluster Bid List) для каждой активной Campaign.
2. WHEN WB_Client возвращает Search Clusters, THE Worker SHALL создать или обновить Phrase (upsert) идемпотентно, маппируя Search Cluster на доменную модель Phrase.
3. WHEN Scheduler запускает задачу wb-import-phrase-stats, THE Worker SHALL запросить статистику по каждому Search Cluster через Search Cluster Statistics API.
4. WHEN WB_Client возвращает статистику Search Clusters, THE Worker SHALL сохранить Phrase_Stat с полями: дата, показы, клики, расходы, конверсии, стоимость по отдельным поисковым запросам.
5. WHEN пользователь запрашивает фразы кампании через API, THE API_Server SHALL вернуть список Phrase с последней Phrase_Stat и поддержкой пагинации и сортировки.

### Требование 8: Отслеживание позиций товаров

**User Story:** Как пользователь, я хочу отслеживать позиции товаров в поисковой выдаче Wildberries, чтобы оценивать видимость товаров.

**Примечание по источникам данных:** Real-time позиции товаров недоступны через официальный WB API. Основной источник — парсинг публичного каталога search.wb.ru через WB_Catalog_Parser. Дополнительно: medianPosition из Seller Analytics CSV (требует подписки "Jam") используется как fallback и для верификации данных парсинга.

#### Критерии приёмки

1. WHEN Scheduler запускает задачу position-checks, THE Worker SHALL запросить позицию каждого отслеживаемого товара через WB_Catalog_Parser по заданным поисковым запросам и регионам.
2. WHEN WB_Catalog_Parser определяет позицию товара, THE Worker SHALL сохранить Position с полями: product_id, query, region, position, page, timestamp.
3. WHEN пользователь запрашивает историю позиций через API, THE API_Server SHALL вернуть Position с фильтрацией по товару, запросу, региону и диапазону дат.
4. IF товар не найден в поисковой выдаче, THEN THE Worker SHALL сохранить Position со значением position = -1.
5. THE API_Server SHALL поддерживать агрегированные данные позиций (средняя позиция за период) при запросе.
6. IF WB_Catalog_Parser недоступен (блокировка, ошибка сети), THEN THE Worker SHALL использовать medianPosition из Seller Analytics CSV как fallback и записать предупреждение в Job_Run.
7. IF WB_Catalog_Parser возвращает ошибку для конкретного запроса, THEN THE Worker SHALL повторить попытку с экспоненциальной задержкой (до 3 попыток) и записать результат в Job_Run.

### Требование 9: Снимки поисковой выдачи (SERP)

**User Story:** Как пользователь, я хочу сохранять снимки поисковой выдачи Wildberries, чтобы анализировать конкурентное окружение.

**Примечание по источникам данных:** Полная поисковая выдача недоступна через официальный WB API. SERP Snapshots получаются через парсинг публичного каталога search.wb.ru через WB_Catalog_Parser.

#### Критерии приёмки

1. WHEN Scheduler запускает задачу serp-scans, THE Worker SHALL выполнить поисковый запрос через WB_Catalog_Parser и сохранить SERP_Snapshot.
2. WHEN SERP_Snapshot создан, THE Worker SHALL сохранить каждый товар из выдачи как SERP_Result_Item с полями: position, product_id, title, price, rating, reviews_count.
3. WHEN пользователь запрашивает SERP_Snapshot через API, THE API_Server SHALL вернуть снимок с вложенными SERP_Result_Item и поддержкой пагинации.
4. THE API_Server SHALL поддерживать фильтрацию SERP_Snapshot по запросу, региону и диапазону дат.
5. IF WB_Catalog_Parser возвращает ошибку при получении поисковой выдачи, THEN THE Worker SHALL записать ошибку в Job_Run и повторить попытку с экспоненциальной задержкой.

### Требование 10: Рекомендованные ставки (Bids)

**User Story:** Как пользователь, я хочу видеть рекомендованные ставки по ключевым фразам, чтобы принимать решения о бюджете рекламы.

**Примечание по источникам данных:** Рекомендованные ставки (competitive_bid, leadership_bid) доступны через метод "Getting Recommended Bids" WB API (февраль 2026) по campaign ID и WB article. Минимальные ставки по категориям (cpm_min) доступны через Configuration API.

#### Критерии приёмки

1. WHEN Scheduler запускает задачу bid-estimation, THE Worker SHALL запросить рекомендованные ставки из WB API (Getting Recommended Bids) для активных Phrase по campaign ID и WB article.
2. WHEN WB_Client возвращает данные о ставках, THE Worker SHALL сохранить Bid_Snapshot с полями: phrase_id, competitive_bid, leadership_bid, cpm_min, timestamp.
3. WHEN Worker запрашивает ставки, THE Worker SHALL также запросить cpm_min из Configuration API (categories) и включить в Bid_Snapshot.
4. WHEN пользователь запрашивает историю ставок через API, THE API_Server SHALL вернуть Bid_Snapshot с фильтрацией по фразе и диапазону дат.
5. THE API_Server SHALL вернуть текущие рекомендованные ставки (последний Bid_Snapshot: competitive_bid, leadership_bid, cpm_min) для каждой Phrase при запросе списка фраз.
6. IF WB_Client не возвращает данные о ставках для фразы, THEN THE Worker SHALL пропустить фразу и записать предупреждение в лог.

### Требование 11: Рекомендации (Recommendations)

**User Story:** Как пользователь, я хочу получать объяснимые рекомендации по оптимизации рекламы, чтобы улучшать результаты кампаний.

#### Критерии приёмки

1. WHEN Scheduler запускает задачу recommendation-generation, THE Worker SHALL проанализировать Campaign_Stat, Phrase_Stat, Position и Bid_Snapshot для генерации Recommendation.
2. THE Worker SHALL создать Recommendation с полями: title, description, type, severity, confidence, source_metrics (JSONB), next_action.
3. THE Worker SHALL выполнять дедупликацию Recommendation: не создавать повторную рекомендацию, если аналогичная активная уже существует для той же Campaign или Phrase.
4. WHEN пользователь запрашивает рекомендации через API, THE API_Server SHALL вернуть список Recommendation с фильтрацией по type, severity и campaign_id.
5. WHEN пользователь отмечает Recommendation как выполненную или отклонённую, THE API_Server SHALL обновить статус и записать событие в Audit_Log.

### Требование 12: Экспорт данных

**User Story:** Как пользователь, я хочу экспортировать данные в файлы, чтобы анализировать их во внешних инструментах.

#### Критерии приёмки

1. WHEN пользователь запрашивает экспорт данных (кампании, статистика, фразы, позиции), THE API_Server SHALL создать задачу Export в очереди exports.
2. WHEN Worker обрабатывает задачу Export, THE Worker SHALL сгенерировать файл в запрошенном формате (CSV или XLSX).
3. WHEN файл экспорта готов, THE Worker SHALL сохранить файл в хранилище и обновить статус Export на completed с ссылкой на файл.
4. WHEN пользователь запрашивает статус экспорта, THE API_Server SHALL вернуть текущий статус Export (pending, processing, completed, failed).
5. IF генерация файла экспорта завершилась ошибкой, THEN THE Worker SHALL обновить статус Export на failed с описанием ошибки.

### Требование 13: API для Chrome Extension

**User Story:** Как пользователь Chrome extension, я хочу получать данные о товарах и кампаниях в контексте страницы Wildberries, чтобы видеть аналитику прямо в браузере.

#### Критерии приёмки

1. WHEN Chrome extension отправляет запрос на создание Extension_Session, THE API_Server SHALL создать сессию, привязанную к пользователю и Workspace.
2. WHEN Chrome extension отправляет контекст страницы (URL, тип страницы), THE API_Server SHALL вернуть релевантные данные (Position, Campaign_Stat, Recommendation) для данного контекста.
3. THE API_Server SHALL предоставить эндпоинт проверки версии extension для уведомления о необходимости обновления.
4. WHEN Chrome extension запрашивает widget data для страницы поиска, THE API_Server SHALL вернуть позиции отслеживаемых товаров и SERP-данные для текущего запроса.
5. WHEN Chrome extension запрашивает widget data для страницы товара, THE API_Server SHALL вернуть статистику кампаний и рекомендации, связанные с данным товаром.

### Требование 14: Фоновые задачи и планировщик

**User Story:** Как система, я хочу выполнять фоновые задачи по расписанию и из очередей, чтобы данные обновлялись автоматически.

#### Критерии приёмки

1. THE Worker SHALL обрабатывать задачи из очередей: wb-import-campaigns, wb-import-campaign-stats, wb-import-sales-funnel, wb-import-phrases, wb-import-phrase-stats, wb-import-seller-analytics, position-checks, serp-scans, bid-estimation, recommendation-generation, exports, extension-events-processing.
2. THE Scheduler SHALL создавать периодические задачи по настроенному расписанию для каждого типа задач.
3. WHEN задача завершена (успешно или с ошибкой), THE Worker SHALL записать Job_Run с полями: task_type, status, started_at, finished_at, error_message, metadata (JSONB).
4. IF задача завершилась ошибкой после всех повторных попыток, THEN THE Worker SHALL записать Job_Run со статусом failed и полным описанием ошибки.
5. THE Worker SHALL обрабатывать задачи с учётом Tenant_Scope (workspace_id) для изоляции данных.

### Требование 15: Аудит действий

**User Story:** Как owner, я хочу видеть журнал действий пользователей и системных событий, чтобы контролировать безопасность и изменения.

#### Критерии приёмки

1. WHEN пользователь выполняет операцию записи (создание, обновление, удаление), THE API_Server SHALL записать событие в Audit_Log с полями: user_id, workspace_id, action, entity_type, entity_id, metadata (JSONB), timestamp.
2. WHEN системная задача выполняет значимое действие, THE Worker SHALL записать событие в Audit_Log.
3. WHEN пользователь запрашивает Audit_Log через API, THE API_Server SHALL вернуть записи с фильтрацией по action, entity_type, user_id и диапазону дат.
4. THE API_Server SHALL ограничить доступ к Audit_Log ролями owner и manager.
5. THE API_Server SHALL поддерживать пагинацию и сортировку при запросе Audit_Log.

### Требование 16: Интеграция с Wildberries API

**User Story:** Как система, я хочу взаимодействовать с API Wildberries через отдельный интеграционный слой, чтобы изолировать внешние зависимости.

**Примечание:** WB API активно развивается (18 обновлений в декабре 2025). Все кампании теперь тип 9 (unified) с bid_type (manual/unified) и payment_type (cpm/cpc). Домены перешли с wb.ru на wildberries.ru. Интеграционный слой должен быть адаптивным к изменениям API.

#### Критерии приёмки

1. THE WB_Client SHALL реализовать HTTP-клиент с retry-логикой (экспоненциальная задержка, до 3 попыток) и rate-limiting.
2. THE WB_Client SHALL использовать отдельные DTO для запросов и ответов WB API и mappers для преобразования в доменные модели.
3. THE WB_Client SHALL поддерживать кампании типа 9 (unified) с bid_type (manual/unified) и payment_type (cpm/cpc).
4. THE WB_Client SHALL использовать актуальные домены WB API (wildberries.ru) с возможностью конфигурации base URL через переменные окружения.
5. IF WB API возвращает HTTP 429 (rate limit), THEN THE WB_Client SHALL приостановить запросы и повторить после указанного интервала.
6. IF WB API возвращает HTTP 5xx, THEN THE WB_Client SHALL повторить запрос с экспоненциальной задержкой.
7. THE WB_Client SHALL логировать все запросы и ответы WB API с уровнем debug через zerolog.
8. THE WB_Client SHALL поддерживать версионирование DTO для адаптации к изменениям WB API без модификации доменных моделей.

### Требование 17: REST API — общие требования

**User Story:** Как клиент API, я хочу получать данные в унифицированном формате с поддержкой пагинации и фильтрации, чтобы удобно интегрироваться с backend.

#### Критерии приёмки

1. THE API_Server SHALL использовать префикс /api/v1 для всех эндпоинтов.
2. THE API_Server SHALL возвращать все ответы в формате Response_Envelope с полями: data, meta (pagination), errors.
3. THE API_Server SHALL поддерживать пагинацию (page, per_page), фильтрацию и сортировку для всех list-эндпоинтов.
4. IF запрос содержит невалидные параметры, THEN THE API_Server SHALL вернуть HTTP 400 с описанием ошибок валидации в Response_Envelope.
5. THE API_Server SHALL предоставлять OpenAPI/Swagger-спецификацию для документирования всех эндпоинтов.
6. THE API_Server SHALL включать workspace_id в контекст каждого аутентифицированного запроса для обеспечения Tenant_Scope.

### Требование 18: Конфигурация и запуск

**User Story:** Как DevOps-инженер, я хочу настраивать систему через переменные окружения с валидацией, чтобы безопасно деплоить сервис.

#### Критерии приёмки

1. THE API_Server SHALL загружать конфигурацию из переменных окружения с типизированной структурой.
2. IF обязательная переменная окружения отсутствует или невалидна, THEN THE API_Server SHALL завершить процесс с описательным сообщением об ошибке.
3. THE API_Server SHALL предоставлять эндпоинты health check: /health/live (liveness), /health/ready (readiness с проверкой PostgreSQL, Redis, очередей).
4. THE API_Server SHALL поддерживать graceful shutdown при получении сигнала SIGTERM.
5. THE API_Server SHALL запускаться через Docker Compose с PostgreSQL, Redis и сервисами api/worker.

### Требование 19: Безопасность хранения данных

**User Story:** Как пользователь, я хочу быть уверен, что мои API-токены Wildberries хранятся в зашифрованном виде, чтобы минимизировать риски утечки.

#### Критерии приёмки

1. THE API_Server SHALL шифровать API-токены Seller_Cabinet перед сохранением в базу данных с использованием AES-256-GCM.
2. THE API_Server SHALL расшифровывать токены только в момент использования для запросов к WB API.
3. THE API_Server SHALL хранить ключ шифрования отдельно от базы данных (в переменной окружения).
4. THE API_Server SHALL хешировать пароли пользователей с использованием argon2id.
5. IF запрос на расшифровку токена завершился ошибкой, THEN THE API_Server SHALL записать событие в Audit_Log и вернуть ошибку без раскрытия деталей шифрования.

### Требование 20: Наблюдаемость и логирование

**User Story:** Как DevOps-инженер, я хочу иметь структурированные логи и метрики, чтобы мониторить состояние системы.

#### Критерии приёмки

1. THE API_Server SHALL использовать zerolog для структурированного логирования в формате JSON.
2. THE API_Server SHALL включать request_id, user_id и workspace_id в каждую запись лога для HTTP-запросов.
3. THE Worker SHALL включать job_id, task_type и workspace_id в каждую запись лога для фоновых задач.
4. THE API_Server SHALL быть подготовлен к интеграции с OpenTelemetry (экспорт трейсов и метрик).
5. IF возникает необработанная ошибка, THEN THE API_Server SHALL записать полный stack trace в лог с уровнем error и вернуть клиенту HTTP 500 без внутренних деталей.

### Требование 21: База данных и миграции

**User Story:** Как разработчик, я хочу управлять схемой базы данных через миграции, чтобы обеспечить воспроизводимость и версионирование.

#### Критерии приёмки

1. THE Backend SHALL использовать golang-migrate для управления миграциями PostgreSQL.
2. THE Backend SHALL использовать UUID в качестве первичных ключей для всех таблиц.
3. THE Backend SHALL включать поля created_at и updated_at для всех таблиц.
4. THE Backend SHALL реализовать soft delete (поле deleted_at) для таблиц, требующих восстановления данных.
5. THE Backend SHALL включать workspace_id в таблицы, требующие Tenant_Scope, с соответствующими индексами.
6. THE Backend SHALL использовать sqlc для генерации типобезопасного Go-кода из SQL-запросов.

### Требование 22: Продукты (Products)

**User Story:** Как пользователь, я хочу видеть товары, привязанные к моим кабинетам Wildberries, чтобы отслеживать их в рекламных кампаниях.

#### Критерии приёмки

1. WHEN Worker импортирует кампании, THE Worker SHALL также импортировать связанные товары (Products) и привязать их к Workspace через Seller_Cabinet.
2. THE Worker SHALL выполнять upsert Products по внешнему идентификатору WB (wb_product_id) идемпотентно.
3. WHEN пользователь запрашивает список Products через API, THE API_Server SHALL вернуть товары текущего Workspace с поддержкой пагинации, фильтрации по названию и сортировки.
4. THE API_Server SHALL возвращать для каждого Product поля: id, wb_product_id, title, brand, category, image_url, price.
5. IF Product не найден при запросе по id, THEN THE API_Server SHALL вернуть HTTP 404 с описанием ошибки в Response_Envelope.


### Требование 23: Парсинг публичного каталога Wildberries

**User Story:** Как система, я хочу получать данные о поисковой выдаче и позициях товаров через парсинг публичного каталога Wildberries, чтобы предоставлять real-time данные, недоступные через официальный WB API.

**Примечание:** Парсинг search.wb.ru — неофициальный подход. Модуль WB_Catalog_Parser должен быть изолирован, устойчив к блокировкам и изменениям формата, с поддержкой proxy-ротации.

#### Критерии приёмки

1. THE WB_Catalog_Parser SHALL реализовать HTTP-клиент с поддержкой proxy-ротации, retry-логикой (экспоненциальная задержка, до 3 попыток) и конфигурируемым rate-limiting.
2. WHEN WB_Catalog_Parser получает запрос на поисковую выдачу, THE WB_Catalog_Parser SHALL выполнить запрос к search.wb.ru с указанным поисковым запросом и регионом и вернуть структурированный список товаров с позициями.
3. WHEN WB_Catalog_Parser получает запрос на позицию товара, THE WB_Catalog_Parser SHALL определить позицию указанного товара в поисковой выдаче по product_id.
4. THE WB_Catalog_Parser SHALL поддерживать конфигурируемые интервалы между запросами (минимальная задержка между запросами) через переменные окружения.
5. IF WB_Catalog_Parser получает HTTP 403 или HTTP 429, THEN THE WB_Catalog_Parser SHALL переключиться на следующий proxy из пула и повторить запрос.
6. IF все proxy в пуле недоступны или заблокированы, THEN THE WB_Catalog_Parser SHALL вернуть ошибку graceful degradation и записать событие в лог с уровнем error.
7. IF формат ответа search.wb.ru изменился и парсинг невозможен, THEN THE WB_Catalog_Parser SHALL вернуть ошибку с описанием несоответствия формата и записать событие в лог с уровнем error.
8. THE WB_Catalog_Parser SHALL логировать все запросы (URL, proxy, статус ответа, время выполнения) через zerolog с уровнем debug.
9. THE WB_Catalog_Parser SHALL быть реализован как отдельный пакет (package) с собственным интерфейсом, изолированным от остальной бизнес-логики.

### Требование 24: Аналитика поисковых запросов (Seller Analytics)

**User Story:** Как пользователь, я хочу получать аналитику по поисковым запросам из Seller Analytics Wildberries, чтобы дополнять данные о позициях и эффективности ключевых фраз.

**Примечание:** Seller Analytics CSV доступен через WB Analytics API и требует подписки "Jam". Предоставляет medianPosition, частотность и конверсии по поисковым запросам. Дополняет данные из WB_Catalog_Parser.

#### Критерии приёмки

1. WHEN Scheduler запускает задачу wb-import-seller-analytics, THE Worker SHALL запросить отчёт по поисковым запросам из WB Seller Analytics API для каждого активного Seller_Cabinet.
2. WHEN WB_Client возвращает CSV-отчёт, THE Worker SHALL распарсить CSV и сохранить данные с полями: поисковый запрос, medianPosition, частотность, клики, конверсии, дата.
3. IF подписка "Jam" недоступна для Seller_Cabinet, THEN THE Worker SHALL пропустить импорт Seller Analytics, записать предупреждение в Job_Run и продолжить работу.
4. WHEN пользователь запрашивает аналитику поисковых запросов через API, THE API_Server SHALL вернуть данные с фильтрацией по запросу, диапазону дат и поддержкой пагинации и сортировки.
5. THE Worker SHALL выполнять upsert данных Seller Analytics по ключу (seller_cabinet_id, query, date) идемпотентно.
6. THE Worker SHALL использовать данные medianPosition из Seller Analytics как дополнение к real-time позициям из WB_Catalog_Parser для верификации и обогащения данных Position.
