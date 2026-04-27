# Changelog

All notable changes to Sellico Ads Intelligence Backend.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versioning follows [SemVer](https://semver.org/).

## [Unreleased]

### Added — v1.0 roadmap (Sprints 1–8)

#### Security & deployment (Sprint 1)
- HTTPS production config (`nginx/nginx.prod.conf`) with TLS 1.2/1.3,
  modern ciphers, OCSP stapling, HSTS, ACME http-01 challenge support.
- One-shot `scripts/setup-ssl.sh` (certbot --standalone) and
  zero-downtime `scripts/renew-ssl.sh` (certbot --webroot + nginx reload
  via deploy hook).
- Offsite PostgreSQL backups: GPG-encrypted, S3-compatible upload from
  `scripts/backup-db.sh`. Daily `scripts/restore-check.sh` smoke test.
- GitHub Actions hardening: secret leak fixed (`docker login` no longer
  echoed in SSH script), Dependabot config for github-actions / gomod /
  docker, all actions pinned to specific minor versions, SSH host
  fingerprint verification.

#### Stability & memory (Sprint 2)
- Consolidated `docker-compose.server.yml` → `docker-compose.prod.yml`
  (single source of truth).
- `internal/app.applyMemoryLimit()` reads cgroup v1/v2 limit and applies
  80% as Go runtime soft limit (cgroup-aware GOMEMLIMIT, survives missing env).
- WB rate-limiter and circuit-breaker maps replaced by bounded LRU+TTL
  cache (`internal/integration/wb/cache.go`) — capacity 1000, TTL 1h.
- Prometheus + Grafana de-exposed from public ports; Grafana configured
  for nginx sub-path serving.
- Container hardening across the stack: `security_opt: no-new-privileges`,
  `cap_drop: ALL` (with targeted `cap_add` only where needed), `read_only`
  + tmpfs on api/worker/nginx.

#### Refactoring & code quality (Sprint 3)
- `internal/service/ads_read.go` decomposed 1487 LOC → six files in same
  package, each ≤ 500 LOC.
- `loadNormQueryAggregates` deduplicated — was a near-clone of
  `loadNormQueryAggregatesWithNMIDs`.
- Per-query data caps lifted from constants to env (`ADS_READ_ENTITY_LIMIT`,
  `ADS_READ_STATS_LIMIT`) with `WithAdsReadLimits` functional option.
- New `tools/check-openapi-drift/` static analysis tool wired into CI;
  whitelist file (`known-gaps.txt`) carries the current baseline so CI
  fails only on NEW drift.

#### Observability & crypto (Sprint 4)
- Alertmanager + 9 alert rules → Telegram (page vs warn severity routing,
  inhibit rules to suppress cascading noise).
- cAdvisor + node-exporter wired into `docker-compose.prod.yml`.
- Prometheus config gained scrape jobs for the new exporters and
  `rule_files: alerts.yml`.
- Versioned encryption keyring (`crypto.Keyring`, `EncryptWithKeyring`,
  `DecryptWithKeyring`). Wire format `v<N>:<base64>`; legacy unversioned
  ciphertext still accepted.
- New `cmd/rotate-encryption-key` admin tool with dry-run / --apply modes.
- Operator docs: `docs/deployment/key-rotation.md`.

#### Frontend scaffold (Sprint 5)
- React 19 + TypeScript 5.7 + Vite 6 + MUI v6 + TanStack Query 5 + RR7
  scaffold under `/frontend`.
- AuthProvider with in-memory access token + HttpOnly refresh cookie +
  single-flighted refresh on 401.
- AppLayout shell with sidebar/topbar; LoginPage; CommandCenterPage stub.
- Dockerfile + standalone nginx-spa.conf for ad-hoc preview deploys.

#### Browser extension v1.0 (Sprint 8)
- Manifest hardened: removed unused `activeTab` and `scripting`
  permissions; localhost moved to `optional_host_permissions`; CSP added;
  `version_name`, `homepage_url`, full icons, `minimum_chrome_version`.
- `scripts/pack-extension.sh` rewritten for cross-platform (CI Linux +
  macOS), reads private key from `EXTENSION_PRIVATE_KEY` env in CI, emits
  both CRX (self-host) and zip (Chrome Web Store).
- New `.github/workflows/extension-release.yml` triggers on
  `extension-v*` tags, attaches CRX + zip to GitHub Release.
- `extension/chromium/PRIVACY.md` (effective 2026-04-27).
- `extension/chromium/CHROME_WEB_STORE.md` submission checklist with RU
  and EN listing copy.

#### Documentation (Final)
- `docs/ARCHITECTURE.md` — backend layers, data flow, debug map.
- `docs/adr/` — ADR-0001 (singleflight), ADR-0002 (HS256 JWT), ADR-0003
  (AES-GCM keyring), ADR-0004 (REST not GraphQL).
- `docs/deployment/{ssl,backups,key-rotation}.md` — operator runbooks.
- This CHANGELOG.

### Out of band (operator actions still required for v1.0 deploy)

- [ ] Provision Yandex Object Storage bucket + service account + 30-day
      lifecycle rule (`docs/deployment/backups.md`).
- [ ] Generate GPG passphrase, place at `/etc/sellico/backup-gpg.pass`.
- [ ] Add `BACKUP_GPG_PASSPHRASE_FILE` and S3 credentials to cron env.
- [ ] Run `sudo DOMAIN=… EMAIL=… scripts/setup-ssl.sh` once on the VPS
      to bootstrap Let's Encrypt cert + renewal cron.
- [ ] Add GitHub Secrets: `DEPLOY_HOST_FINGERPRINT`, `GHCR_TOKEN`,
      `EXTENSION_PRIVATE_KEY` (base64).
- [ ] Place Telegram bot token at
      `monitoring/alertmanager-secrets/bot_token` (chmod 0400) and set
      `ALERT_CHAT_ID` / `ONCALL_CHAT_ID` env on the host.

---

## [0.9.0] — Beta (state at start of v1.0 roadmap, commit b527e86)

Prior to the v1.0 roadmap. See `git log b527e86` for granular history.

- 16 domain modules implemented (auth, workspaces, cabinets, campaigns,
  phrases, stats, positions, SERP, bids, recommendations, exports,
  audit, jobs, extension API).
- 15 PostgreSQL migrations.
- 52 OpenAPI endpoints.
- 56 Go test files; property-based tests on auth and recommendations.
- CI/CD pipeline (GitHub Actions → GHCR → SSH deploy on Timeweb VPS).
- Prometheus + Grafana base monitoring.
- Asynq workers with 7 queues.
- Chrome MV3 browser extension v0.1.0 (alpha, load-unpacked only).
- Several emergency OOM fixes (singleflight, ID-only cross-ref maps,
  campaign↔product reduction) — see commits `15b161a`, `8bf53d6`.
