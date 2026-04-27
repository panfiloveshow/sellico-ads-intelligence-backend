# Monitoring stack — Prometheus + Grafana + cAdvisor + node-exporter

Без HTTPS Grafana не выставлена наружу. Доступ только через **SSH-туннель** —
никто из интернета не может попасть на дашборды, даже зная пароль.

## Что включено (default profile)

| Сервис | Что метит | Доступ снаружи | Память |
|---|---|---|---|
| `prometheus` | scrape api/cadvisor/node-exporter, eval rules | нет (внутренний) | 512 MB |
| `grafana` | дашборды + UI | через SSH-туннель | 256 MB |
| `cadvisor` | per-container CPU/memory/OOM events | нет | 256 MB |
| `node-exporter` | host CPU/disk/filesystem/network | нет | 128 MB |

`alertmanager` отключён (profile=`alerts`) — нужен Telegram bot setup, ставится отдельно.

## Первый запуск на проде

Уже в `scripts/deploy.sh update` — следующий запуск подтянет всё.

Если хочется поднять только monitoring на живом кластере (без рестарта api/worker):

```bash
ssh admin_reprice@72.56.250.9
cd /opt/sellico
./scripts/deploy.sh monitoring
```

## Доступ к Grafana

```bash
# На своей машине:
ssh -L 3000:grafana:3000 admin_reprice@72.56.250.9

# Открыть в браузере:
open http://localhost:3000
# логин: admin
# пароль: $GRAFANA_ADMIN_PASSWORD из /opt/sellico/.env
```

При первом входе Grafana попросит сменить пароль — менять или нет, на твоё усмотрение
(новый пароль перетрётся при пересоздании контейнера; для постоянной смены
обновить переменную в `.env` и `docker compose up -d --force-recreate grafana`).

## Доступ к Prometheus

```bash
ssh -L 9090:prometheus:9090 admin_reprice@72.56.250.9
open http://localhost:9090
```

Полезные ad-hoc запросы:
- `up` — кто scrape'ится (1=ok, 0=miss)
- `rate(sellico_http_requests_total[5m])` — req/s по endpoint
- `histogram_quantile(0.95, sum by (le) (rate(sellico_http_request_duration_seconds_bucket[5m])))` — p95 latency
- `container_memory_usage_bytes{name=~"sellico.*"}` — память контейнеров
- `node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes` — host RAM headroom

## Дашборды

Auto-provisioned из `monitoring/grafana/provisioning/dashboards/json/`. На сейчас:
- `sellico-api.json` — request rate, p95 latency, errors, in-flight

Чтобы добавить новый:
1. Сделать в Grafana UI (Create → Dashboard)
2. Export → JSON
3. Сохранить в `monitoring/grafana/provisioning/dashboards/json/<name>.json`
4. Закоммитить — провайдер `Sellico` подхватит в течение 30 секунд

## Алерты (опционально, требует Telegram bot)

Когда нужны pageble-алерты:
1. Создать Telegram-бота через @BotFather, получить token
2. Создать чат для алертов, добавить бота, узнать chat_id (через `https://api.telegram.org/bot<token>/getUpdates` после первого сообщения)
3. На VPS:
   ```bash
   echo -n "<bot-token>" | sudo tee /opt/sellico/monitoring/alertmanager-secrets/bot_token
   sudo chmod 0400 /opt/sellico/monitoring/alertmanager-secrets/bot_token
   ```
4. В `/opt/sellico/.env`:
   ```
   ALERT_CHAT_ID=<chat-id>
   ONCALL_CHAT_ID=<oncall-chat-id-or-blank-to-reuse-ALERT>
   ```
5. Запустить alertmanager + раскомментировать `alerting:` блок в `monitoring/prometheus.yml`:
   ```bash
   docker compose -f docker-compose.prod.yml --profile alerts up -d alertmanager
   docker compose kill -s HUP prometheus
   ```
6. Тест: вручную trigger metric (`stress-ng --vm 1 --vm-bytes 800M --timeout 30s`) → должен прийти `ContainerHighMemory` в Telegram

## Откат

Снять только monitoring stack (api/worker остаются):
```bash
docker compose -f docker-compose.prod.yml stop prometheus grafana cadvisor node-exporter
docker compose -f docker-compose.prod.yml rm -f prometheus grafana cadvisor node-exporter
```

Volumes (`prometheus_data`, `grafana_data`) сохранятся — данные не пропадут.
