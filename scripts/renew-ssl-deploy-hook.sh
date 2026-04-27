#!/usr/bin/env bash
# renew-ssl-deploy-hook.sh — Called by certbot's --deploy-hook after a successful renewal.
# RENEWED_DOMAINS and RENEWED_LINEAGE are set by certbot.

set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/opt/sellico}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.server.yml}"
SSL_DIR="$DEPLOY_DIR/nginx/ssl"

log() { echo "[$(date -Iseconds)] [deploy-hook] $*"; }

log "Renewed lineage: ${RENEWED_LINEAGE:-unknown}"
install -m 0644 "$RENEWED_LINEAGE/fullchain.pem" "$SSL_DIR/fullchain.pem"
install -m 0600 "$RENEWED_LINEAGE/privkey.pem"   "$SSL_DIR/privkey.pem"

# Reload nginx without dropping connections
( cd "$DEPLOY_DIR" && docker compose -f "$COMPOSE_FILE" exec -T nginx nginx -s reload )
log "nginx reloaded with new cert."
