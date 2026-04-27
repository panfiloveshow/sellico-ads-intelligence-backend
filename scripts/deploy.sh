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

    # Install systemd service
    if [ -f "$DEPLOY_DIR/scripts/sellico.service" ]; then
      cp "$DEPLOY_DIR/scripts/sellico.service" /etc/systemd/system/sellico.service
      systemctl daemon-reload
      systemctl enable sellico
      log "Systemd service installed and enabled"
    fi

    # Install backup + restore-check crons
    BACKUP_LINE="0 3 * * * $DEPLOY_DIR/scripts/backup-db.sh >> /var/log/sellico-backup.log 2>&1"
    RESTORE_LINE="30 4 * * * $DEPLOY_DIR/scripts/restore-check.sh >> /var/log/sellico-restore-check.log 2>&1"
    ( crontab -l 2>/dev/null | grep -v "backup-db.sh\|restore-check.sh"
      echo "$BACKUP_LINE"
      echo "$RESTORE_LINE"
    ) | crontab -
    log "Backup cron installed (daily 03:00) + restore-check cron (daily 04:30)"

    # Start services
    cd "$DEPLOY_DIR"
    docker compose -f "$COMPOSE_FILE" up -d
    log "=== Setup complete ==="
    ;;

  update)
    log "=== Updating Sellico ==="
    cd "$DEPLOY_DIR"

    # Pull latest images for prebuilt services (monitoring stack);
    # api/worker are build-from-source on this host so `pull` is a no-op for them.
    docker compose -f "$COMPOSE_FILE" pull prometheus grafana cadvisor node-exporter || true

    # Build api+worker from current source (needed when running `git pull` first).
    docker compose -f "$COMPOSE_FILE" build api worker
    log "Images built"

    # Bring up everything in the default profile (api, worker, postgres, redis,
    # nginx, prometheus, grafana, cadvisor, node-exporter). Alertmanager stays
    # off — it's behind the "alerts" profile and requires Telegram bot setup.
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
    docker compose -f "$COMPOSE_FILE" up -d prometheus grafana cadvisor node-exporter
    log "Monitoring up. Access via SSH tunnel:"
    log "  ssh -L 3000:grafana:3000 admin_reprice@$(hostname -I | awk '{print $1}')"
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
