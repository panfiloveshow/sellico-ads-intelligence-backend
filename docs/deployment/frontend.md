# Frontend deployment checklist

> Этот деплой делает **только пользователь** (не AI-агент).
> Все предварительные шаги — `pnpm build`, тесты, коммиты — уже выполняются автоматически в pipeline разработки.

## Что мы публикуем

`frontend/dist/` — статический React-бандл, собирается через `pnpm build` в Sprint 6/7 итерациях.
- ~340 KB JS / ~180 KB gzip, разбитый на 5 чанков (`react`, `mui`, `tanstack`, `vendor`, `index`)
- 1 HTML (`index.html`)
- source maps (для production-debugging; можно отключить через `vite.config.ts → build.sourcemap=false` если не нужно)

После деплоя `https://ads.sellico.ru/` должен отдавать React-app, а `/api/*` — продолжать проксироваться в Go-backend.

## Подготовка (одноразово, на VPS)

### 1. Установить Node 22 и pnpm на сервер ИЛИ деплоить готовый build

**Вариант А — собирать build на сервере** (не рекомендую: 1.9 GB RAM на VPS, на pnpm install уйдёт 4-5 минут, плюс надо ставить Node):

```bash
# на VPS
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo bash -
sudo apt install -y nodejs
sudo npm install -g pnpm
```

**Вариант Б — собирать локально, копировать `dist/`** (рекомендую):

```bash
# на твоей машине
cd ~/Documents/ПРОЕКТЫ/marketing/frontend
pnpm install
pnpm build

# Затем скопировать на сервер (см. шаг 3 ниже)
```

### 2. Создать на VPS директорию для статики

```bash
ssh admin_reprice@72.56.250.9
sudo mkdir -p /var/www/sellico-frontend
sudo chown admin_reprice:admin_reprice /var/www/sellico-frontend
exit
```

## Каждый деплой (3 минуты)

### 3. Скопировать `dist/` на сервер

```bash
# с локальной машины
cd ~/Documents/ПРОЕКТЫ/marketing
rsync -avz --delete frontend/dist/ admin_reprice@72.56.250.9:/var/www/sellico-frontend/
```

`--delete` удалит на сервере файлы, которых нет в новом `dist/` (старые хешированные js-чанки от прошлых сборок). Это правильно — иначе папка будет распухать.

### 4. Добавить SPA-блок в nginx

Открыть `nginx/nginx.prod.conf` — внутри `server { listen 443 ssl; ... }` блока добавить ПЕРЕД блоком `include /etc/nginx/conf.d/proxy.inc;`:

```nginx
    # SPA frontend — ловит всё, что не /api/* и не /openapi.yaml/docs/health/metrics.
    # Должно идти ДО include proxy.inc, иначе catch-all из proxy.inc возьмёт верх.
    root /var/www/sellico-frontend;
    index index.html;

    # Хешированные js/css/woff2 — кэш 1 год, immutable
    location /assets/ {
        try_files $uri =404;
        expires 1y;
        add_header Cache-Control "public, immutable" always;
    }

    # index.html — никогда не кэшируем (новые сборки должны попадать к юзеру сразу)
    location = /index.html {
        try_files $uri =404;
        add_header Cache-Control "no-cache, no-store, must-revalidate" always;
    }

    # Корень и любой не-API путь — отдаём index.html (SPA fallback для React Router)
    location / {
        try_files $uri /index.html;
    }
```

И в `docker-compose.prod.yml` добавить mount к nginx-сервису:

```yaml
  nginx:
    # ... существующие volumes ...
    volumes:
      - ./nginx/nginx.prod.conf:/etc/nginx/conf.d/default.conf:ro
      - ./nginx/proxy.inc:/etc/nginx/conf.d/proxy.inc:ro
      - ./nginx/ssl:/etc/nginx/ssl:ro
      - ./nginx/acme:/var/www/certbot:ro
      - /var/www/sellico-frontend:/var/www/sellico-frontend:ro   # ← добавить эту строку
```

### 5. Перезагрузить nginx

```bash
ssh admin_reprice@72.56.250.9
cd /opt/sellico

# Применить compose-изменения (если правил docker-compose.prod.yml — иначе skip)
docker compose -f docker-compose.prod.yml up -d --force-recreate nginx

# ИЛИ если только nginx.prod.conf поменял (не compose) — достаточно reload без перезапуска контейнера:
docker compose -f docker-compose.prod.yml exec nginx nginx -t   # проверить syntax
docker compose -f docker-compose.prod.yml exec nginx nginx -s reload
```

### 6. Проверить

```bash
curl -s -o /dev/null -w "%{http_code}\n" https://ads.sellico.ru/
# → 200

curl -sI https://ads.sellico.ru/ | head -5
# → Должен быть Content-Type: text/html

curl -s https://ads.sellico.ru/ | head -20
# → Должен показать <!doctype html><html lang="ru">... (HTML React-app)

curl -s https://ads.sellico.ru/api/v1/health/ready
# → {"data":{"status":"ready"}}  — API всё ещё работает
```

В браузере — открыть `https://ads.sellico.ru/`, увидеть форму логина React-app.

## Откат

Если что-то пошло не так:

**1. Быстрый откат — отключить SPA в nginx:**
Закомментировать блок `root /var/www/sellico-frontend; ...` в `nginx.prod.conf`, `nginx -s reload`. Сайт вернётся к серверному ответу `/api/*` без React UI.

**2. Откат на предыдущую версию:**
Если делал `rsync --delete`, предыдущий dist потерян. Чтобы избежать этого в следующий раз — версионируй deploy:

```bash
# Лучше так:
rsync -avz frontend/dist/ admin_reprice@72.56.250.9:/var/www/sellico-frontend.new/
ssh admin_reprice@72.56.250.9 'mv /var/www/sellico-frontend /var/www/sellico-frontend.old.$(date +%s) && mv /var/www/sellico-frontend.new /var/www/sellico-frontend && docker compose exec nginx nginx -s reload'
# Откатиться: вернуть .old. через mv обратно
```

## Когда фронт готов к v1.0 (B.6 polish completed)

- [x] Code-split (5 чанков) — bundle ≤ 80 KB gzip на чанк
- [x] ErrorBoundary вокруг routes
- [x] Базовая a11y (ARIA labels на CircularProgress, role=status)
- [x] vitest тесты на formatters (14/14 green)
- [ ] Storybook для components (отложено)
- [ ] Полный axe-core a11y pass (отложено — дойдёт когда будут реальные данные для тестирования)
- [ ] Lighthouse ≥ 90 (отложено — после Track A когда есть реальные данные)
- [ ] Playwright e2e (отложено)
- [ ] i18n (отложено)

Status: **MVP-готов к публикации**. Polish идёт фоном, не блокирует деплой.
