#!/usr/bin/env bash
# deploy.sh — Deploy/update Sellico on Timeweb VPS
#
# Usage:
#   First-time setup:  ./scripts/deploy.sh setup
#   Update (pull+restart): ./scripts/deploy.sh update
#   Full restart:      ./scripts/deploy.sh restart

set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/opt/sellico}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.prod.yml}"

log() { echo "[$(date -Iseconds)] $*"; }

install_systemd_units() {
  local unit
  for unit in sellico.service sellico-backup.service sellico-backup.timer \
              sellico-restore-check.service sellico-restore-check.timer; do
    if [[ -f "$DEPLOY_DIR/scripts/$unit" ]]; then
      install -m 0644 "$DEPLOY_DIR/scripts/$unit" "/etc/systemd/system/$unit"
    fi
  done

  install -d -m 0750 -o admin_reprice -g admin_reprice "$DEPLOY_DIR/backups"
  install -d -m 0755 -o admin_reprice -g admin_reprice /var/lib/node_exporter/textfile
  systemctl daemon-reload
  systemctl enable sellico
  systemctl enable --now sellico-backup.timer sellico-restore-check.timer

  # The timers replace legacy cron entries. Keeping both would permit two
  # independent backup/restore jobs to overlap after an upgrade.
  (crontab -l 2>/dev/null || true) \
    | sed "/backup-db.sh\|restore-check.sh/d" \
    | crontab -
}

case "${1:-update}" in

  setup)
    log "=== First-time setup ==="

    # Install Docker if missing
    if ! command -v docker &>/dev/null; then
      log "Installing Docker..."
      curl -fsSL https://get.docker.com | sh
      systemctl enable docker
      systemctl start docker
    fi

    # Install Docker Compose plugin if missing
    if ! docker compose version &>/dev/null; then
      log "Installing Docker Compose plugin..."
      apt-get update && apt-get install -y docker-compose-plugin
    fi

    # Create deploy directory
    mkdir -p "$DEPLOY_DIR"
    log "Deploy directory: $DEPLOY_DIR"

    # Remind about .env
    if [ ! -f "$DEPLOY_DIR/.env" ]; then
      log "WARNING: $DEPLOY_DIR/.env not found!"
      log "Copy .env.prod.example to $DEPLOY_DIR/.env and fill in production secrets."
      exit 1
    fi

    install_systemd_units
    log "Systemd service and verified backup/restore timers installed"

    # Start services
    cd "$DEPLOY_DIR"
    docker compose -f "$COMPOSE_FILE" up -d
    log "=== Setup complete ==="
    ;;

  update)
    log "=== Updating Sellico ==="
    cd "$DEPLOY_DIR"

    if [[ "$(id -u)" == "0" ]]; then
      install_systemd_units
      log "Systemd units refreshed"
    else
      log "Systemd units were not refreshed (update is not running as root)"
    fi

    docker compose -f "$COMPOSE_FILE" pull prometheus cadvisor node-exporter || true

    # api/worker: CI (.github/workflows/cd.yml) already builds and pushes both
    # images to ghcr.io on every push to main — only its SSH deploy step fails,
    # for lack of secrets. Rebuilding them here burned ~2 minutes of the
    # production box's CPU reproducing an artefact that already existed.
    #
    # Pinned to the checked-out commit, never to :latest — pulling :latest right
    # after `git pull` can silently deploy the PREVIOUS build while CI is still
    # running. If the image for this exact commit is not published (CI still
    # building, or it failed), fall back to building locally.
    sha="$(git rev-parse --short=7 HEAD 2>/dev/null || echo unknown)"
    api_image="ghcr.io/panfiloveshow/sellico-ads-intelligence-backend-api:${sha}"
    worker_image="ghcr.io/panfiloveshow/sellico-ads-intelligence-backend-worker:${sha}"

    if [ "$sha" != "unknown" ] \
       && docker pull "$api_image" >/dev/null 2>&1 \
       && docker pull "$worker_image" >/dev/null 2>&1; then
      export API_IMAGE="$api_image"
      export WORKER_IMAGE="$worker_image"
      log "Using prebuilt images from ghcr.io ($sha)"
    else
      log "No prebuilt images for $sha (CI still running, failed, or registry unreachable) — building locally"
      docker compose -f "$COMPOSE_FILE" build api worker
      log "Images built"
    fi

    docker compose -f "$COMPOSE_FILE" run --rm migrate
    log "Migrations applied"

    # Bring up the default production profile. Grafana and Alertmanager remain
    # opt-in and cannot become externally reachable through this command.
    docker compose -f "$COMPOSE_FILE" up -d --remove-orphans
    log "Services up"

    # Cleanup old images
    docker image prune -f
    log "=== Update complete ==="
    ;;

  monitoring)
    # Convenience target: start ONLY the monitoring stack on a running cluster
    # (e.g. when retrofitting monitoring onto a deployment that started without it).
    log "=== Starting monitoring stack ==="
    cd "$DEPLOY_DIR"
    # Grafana is opt-in and must never come up with a blank admin password —
    # that is exactly what happened while the variable was merely warned about.
    if ! grep -qE '^GRAFANA_ADMIN_PASSWORD=.+' .env 2>/dev/null; then
      echo "GRAFANA_ADMIN_PASSWORD не задан в .env — Grafana поднялась бы с пустым паролем администратора." >&2
      echo "Сгенерировать:  echo \"GRAFANA_ADMIN_PASSWORD=\$(openssl rand -base64 24)\" >> .env" >&2
      exit 1
    fi
    docker compose -f "$COMPOSE_FILE" --profile monitoring up -d prometheus grafana cadvisor node-exporter
    log "Monitoring up. Access via SSH tunnel:"
    log "  ssh -L 3000:127.0.0.1:3000 admin_reprice@$(hostname -I | awk '{print $1}')"
    log "Then open http://localhost:3000 (admin / \$GRAFANA_ADMIN_PASSWORD)"
    ;;

  restart)
    log "=== Full restart ==="
    cd "$DEPLOY_DIR"
    docker compose -f "$COMPOSE_FILE" down
    docker compose -f "$COMPOSE_FILE" up -d
    log "=== Restart complete ==="
    ;;

  logs)
    cd "$DEPLOY_DIR"
    docker compose -f "$COMPOSE_FILE" logs -f --tail=100 "${2:-api}"
    ;;

  status)
    cd "$DEPLOY_DIR"
    docker compose -f "$COMPOSE_FILE" ps
    echo ""
    docker compose -f "$COMPOSE_FILE" logs --tail=5 api worker
    ;;

  backup)
    log "Running manual backup..."
    bash "$DEPLOY_DIR/scripts/backup-db.sh"
    ;;

  *)
    echo "Usage: $0 {setup|update|restart|logs [service]|status|backup}"
    exit 1
    ;;
esac
