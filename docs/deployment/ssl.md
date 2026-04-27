# TLS / SSL для Sellico API

Продакшен использует Let's Encrypt сертификаты, выдаваемые через `certbot` в standalone-режиме.
Nginx терминирует TLS, проксирует чистым HTTP до контейнера `api`. HSTS включён.

## Первичная установка (на VPS, под root)

Предусловия:
- DNS-запись `api.sellico.ru` (или твой домен) указывает на IP VPS.
- Порт 80 открыт в файрволе (нужен один раз для `--standalone`).
- Репозиторий клонирован в `/opt/sellico` (или другую папку, указанную в `DEPLOY_DIR`).
- `.env` уже заполнен в `$DEPLOY_DIR/.env`.

Запуск:

```bash
sudo DOMAIN=api.sellico.ru EMAIL=ops@sellico.ru /opt/sellico/scripts/setup-ssl.sh
```

Скрипт:
1. Установит `certbot` (apt), если отсутствует.
2. Остановит nginx-контейнер, чтобы освободить порт 80.
3. Запросит сертификат `certbot certonly --standalone`.
4. Скопирует `fullchain.pem` + `privkey.pem` в `/opt/sellico/nginx/ssl/`.
5. Установит cron на еженедельный renew (понедельник 04:00).
6. Поднимет nginx обратно — уже с HTTPS (через `nginx.prod.conf`).

Проверка:

```bash
curl -I https://api.sellico.ru/health/ready
# Ожидается: HTTP/2 200, header Strict-Transport-Security: max-age=31536000...
```

## Автоматическое обновление

Cron-задача (устанавливается `setup-ssl.sh`):

```cron
0 4 * * 1 /opt/sellico/scripts/renew-ssl.sh >> /var/log/sellico-ssl-renew.log 2>&1
```

`renew-ssl.sh` использует `certbot renew --webroot` — nginx остаётся работать.
Challenge-файлы пишутся в `/opt/sellico/nginx/acme`, который смонтирован в контейнер
nginx как `/var/www/certbot:ro`.

После успешного renew certbot вызывает deploy-hook (`renew-ssl-deploy-hook.sh`),
который копирует свежие сертификаты в `nginx/ssl/` и делает `nginx -s reload`.

Без рестарта контейнера. Без даунтайма.

## Мониторинг истечения

Let's Encrypt пришлёт письма за 20, 10, 5 дней до истечения — туда указанный `EMAIL`.
Дополнительно в Sprint 4 настраивается алерт через Prometheus blackbox-exporter:

```yaml
# monitoring/blackbox.yml (будет добавлен в Sprint 4)
modules:
  ssl_expiry:
    prober: http
    timeout: 10s
    http:
      method: GET
      tls_config:
        insecure_skip_verify: false
```

## Если что-то пошло не так

| Симптом | Причина | Лечение |
|---------|---------|---------|
| `nginx: [emerg] cannot load certificate` | Файлов в `nginx/ssl/` нет | Запустить `setup-ssl.sh` |
| `HTTP 502 Bad Gateway` после renew | nginx не сделал reload | `docker compose exec nginx nginx -s reload` |
| Не проходит `curl https://...` | DNS / firewall | Проверить `dig api.sellico.ru`, `ufw status` |
| `certbot rate limit` | Слишком много попыток | Подождать; для тестов использовать `--staging` |

## Откат на HTTP (только для emergency)

Сменить в `docker-compose.server.yml` mount nginx-конфига обратно на `nginx.conf`
(dev-вариант, без SSL), убрать порт 443. Пересоздать контейнер:

```bash
docker compose -f docker-compose.server.yml up -d nginx
```

После исправления вернуть на `nginx.prod.conf`.
