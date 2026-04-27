# Ротация ENCRYPTION_KEY (WB API tokens)

WB API-токены `seller_cabinets.encrypted_token` шифруются AES-256-GCM ключом из `ENCRYPTION_KEY`. Ниже — процедура замены ключа без даунтайма с использованием версионированного keyring (см. `internal/pkg/crypto/aes.go`).

## Когда ротировать

- Раз в 12 месяцев плановая ротация
- Подозрение на компрометацию (потенциально утечка `.env`, доступ ушёл уволенному)
- Смена шифр-протоколов / обновление длины ключа

## Шаги (≈ 30 минут даунтайма-фри миграции)

### 1. Сгенерировать новый ключ

```bash
NEW_KEY_HEX=$(openssl rand -hex 32)
echo "$NEW_KEY_HEX"   # сохрани в password manager СРАЗУ
```

### 2. Добавить keyring-переменные в `/opt/sellico/.env`

Текущее значение `ENCRYPTION_KEY` — это **v1**. Новое — **v2**:

```bash
# было:
ENCRYPTION_KEY=<old-key-hex>

# стало:
ENCRYPTION_KEY=<old-key-hex>           # оставляем для legacy fallback
ENCRYPTION_KEYS_V1=<old-key-hex>       # явное v1
ENCRYPTION_KEYS_V2=<new-key-hex>       # новое v2 (latest)
```

### 3. Перезапустить api/worker

```bash
cd /opt/sellico
docker compose -f docker-compose.prod.yml up -d --force-recreate api worker
```

После рестарта оба сервиса:
- Читают токены, зашифрованные **любой** из v1/v2 (DecryptWithKeyring)
- Записывают **только** v2 (новые `INSERT`/`UPDATE` через EncryptWithKeyring)

### 4. Запустить миграцию существующих данных

Сначала dry-run:

```bash
DATABASE_URL="postgres://sellico:...@postgres:5432/sellico?sslmode=disable" \
ENCRYPTION_KEYS_V1=<old-key-hex> \
ENCRYPTION_KEYS_V2=<new-key-hex> \
docker compose -f docker-compose.prod.yml run --rm \
  -e DATABASE_URL -e ENCRYPTION_KEYS_V1 -e ENCRYPTION_KEYS_V2 \
  api /app/rotate-encryption-key
```

Должен вывести что-то вроде:
```
seller_cabinets: scanned=42, already_current=0, rotated=42, failed=0, dry_run=true
```

Если `failed=0` — повторить с `--apply`:

```bash
docker compose -f docker-compose.prod.yml run --rm \
  -e DATABASE_URL -e ENCRYPTION_KEYS_V1 -e ENCRYPTION_KEYS_V2 \
  api /app/rotate-encryption-key --apply
```

### 5. Проверить, что все токены на v2

```sql
SELECT
  COUNT(*) FILTER (WHERE encrypted_token LIKE 'v2:%') AS v2,
  COUNT(*) FILTER (WHERE encrypted_token LIKE 'v1:%') AS v1,
  COUNT(*) FILTER (WHERE encrypted_token NOT LIKE 'v_:%') AS legacy
FROM seller_cabinets WHERE deleted_at IS NULL;
```

Ожидание: `v2 = N, v1 = 0, legacy = 0`.

### 6. Удалить старый ключ

После 1 недели наблюдения (на случай отката):

```bash
# /opt/sellico/.env — удалить:
# ENCRYPTION_KEYS_V1=<old-key-hex>

# Оставить:
ENCRYPTION_KEY=<new-key-hex>     # синхронизировано с v2
ENCRYPTION_KEYS_V2=<new-key-hex>
```

Перезапустить api/worker. Cтарый ключ можно безопасно удалить из password manager после третьего успешного чтения (≈ месяц).

## Откат при проблемах на шаге 3-4

Если приложение не стартует после добавления keyring-переменных:

```bash
# /opt/sellico/.env — закомментировать новые строки:
# ENCRYPTION_KEYS_V1=...
# ENCRYPTION_KEYS_V2=...

# Оставить только legacy:
ENCRYPTION_KEY=<old-key-hex>

docker compose -f docker-compose.prod.yml up -d --force-recreate api worker
```

Поскольку ни одного `v2:` токена ещё нет в БД, всё работает как раньше.

Если уже запустили `--apply` и есть `v2:` токены, но новый ключ потерян:
данные **расшифровать невозможно** (это безопасное поведение). Восстановить
из бэкапа предыдущего дня (`docs/deployment/backups.md`).

## Тренировка ротации (рекомендую раз в полгода)

На staging-VPS проиграть полный сценарий с проверкой:
- API отвечает 200 на `/health/ready` после каждого шага
- Sync-задачи (`/api/v1/seller-cabinets/{id}/sync`) проходят успешно
- Виджет в браузерном расширении продолжает получать данные
