# Бэкапы и Disaster Recovery

## Текущая схема

| Уровень | Где | Retention | Шифрование | Owner |
|---------|-----|-----------|------------|-------|
| Локальный дамп | `/opt/sellico/backups/` (VPS) | 7 дней | нет (на хосте) | cron |
| Offsite | Yandex Object Storage `s3://sellico-backups/postgres/` | 30 дней (lifecycle) | GPG AES256 | cron |
| Restore-check | временная БД `sellico_restore_check` | 1 запуск | — | cron |

## Расписание (cron на VPS)

```cron
0  3 * * * /opt/sellico/scripts/backup-db.sh    >> /var/log/sellico-backup.log 2>&1
30 4 * * * /opt/sellico/scripts/restore-check.sh >> /var/log/sellico-restore-check.log 2>&1
```

Обе строки устанавливаются `scripts/deploy.sh setup`.

## Настройка offsite-копии (одноразово)

1. **Завести бакет в Yandex Cloud** (или любой S3-совместимый):
   ```
   yc storage bucket create --name sellico-backups
   yc iam service-account create --name sellico-backups-writer
   yc iam access-key create --service-account-name sellico-backups-writer
   ```
   Сохранить `access_key_id` и `secret`.

2. **Lifecycle rule** для автоматического удаления через 30 дней — задаётся в консоли Yandex Cloud в разделе бакета:
   - Префикс: `postgres/`
   - Действие: Delete
   - Возраст: 30 дней

3. **GPG passphrase** на VPS:
   ```bash
   sudo install -m 0600 /dev/stdin /etc/sellico/backup-gpg.pass <<< 'СГЕНЕРИРОВАННАЯ_СТРОКА_ИЗ_OPENSSL_RAND'
   ```
   Passphrase **обязательно сохранить в password manager** — без неё бэкапы не восстановить.

4. **Передать переменные** в окружение cron — добавить в `/etc/cron.d/sellico-backup` (либо в шапку самого cron-line):
   ```cron
   S3_ENDPOINT=https://storage.yandexcloud.net
   S3_BUCKET=sellico-backups
   S3_PREFIX=postgres/
   AWS_ACCESS_KEY_ID=YOUR_KEY_ID
   AWS_SECRET_ACCESS_KEY=YOUR_SECRET
   BACKUP_GPG_PASSPHRASE_FILE=/etc/sellico/backup-gpg.pass
   PGPASSWORD=...
   0 3 * * * root /opt/sellico/scripts/backup-db.sh >> /var/log/sellico-backup.log 2>&1
   ```

5. **Проверить вручную**:
   ```bash
   sudo -E /opt/sellico/scripts/backup-db.sh
   aws --endpoint-url=https://storage.yandexcloud.net s3 ls s3://sellico-backups/postgres/
   ```

## Восстановление в случае инцидента

1. **Скачать последний дамп**:
   ```bash
   aws --endpoint-url=https://storage.yandexcloud.net s3 cp \
     s3://sellico-backups/postgres/sellico_20260427_030001.dump.gpg /tmp/restore.gpg
   ```

2. **Расшифровать**:
   ```bash
   gpg --batch --passphrase-file /etc/sellico/backup-gpg.pass --decrypt \
       /tmp/restore.gpg > /tmp/restore.dump
   ```

3. **Восстановить в новую БД**:
   ```bash
   psql -U postgres -c 'CREATE DATABASE sellico_restored;'
   pg_restore -U sellico -d sellico_restored --no-owner --no-privileges /tmp/restore.dump
   ```

4. **Переключить приложение** через смену `DATABASE_URL` в `.env` и `docker compose restart api worker`.

## Проверка здоровья (что мониторить)

Алерты, которые должны быть подключены к Telegram-каналу:

- **`absent(probe_success{job="backup"})`** — backup-cron не отрабатывал >25ч
- **`probe_success{job="restore-check"} == 0`** — restore-check провалился
- **`(time() - sellico_backup_last_success_timestamp) > 86400`** — последний успешный бэкап старше суток

Эти метрики экспортирует отдельный sidecar (Sprint 4 plan), либо парсится `/var/log/sellico-*.log`.

## Тренировки восстановления (DR-drill)

Раз в квартал вручную:
1. Поднять отдельный staging-VPS
2. Скачать самый старый бэкап из S3 (тестируем, что lifecycle ещё не удалил)
3. Расшифровать и восстановить
4. Поднять API против восстановленной БД
5. Прогнать smoke-тесты
6. Замерить RTO (целевое: ≤2 часа), RPO (≤24 часа)

Результаты записывать в `docs/runbook/dr-drills.md` (заведём в Sprint 4).
