#!/usr/bin/env bash
# renew-ssl.sh — Weekly cert renewal hook (installed by setup-ssl.sh).
#
# Uses certbot's --webroot mode so nginx stays up during renewal.
# After renewal, copies fresh certs into ./nginx/ssl/ and sends nginx SIGHUP.
#
# Usage (cron):
#   0 4 * * 1 /opt/sellico/scripts/renew-ssl.sh >> /var/log/sellico-ssl-renew.log 2>&1

set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/opt/sellico}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.prod.yml}"
SSL_DIR="$DEPLOY_DIR/nginx/ssl"
ACME_DIR="$DEPLOY_DIR/nginx/acme"

log() { echo "[$(date -Iseconds)] $*"; }

mkdir -p "$ACME_DIR"

# certbot --webroot doesn't need port 80 free — nginx serves the challenge from
# /var/www/certbot (already mounted from $ACME_DIR by docker-compose.prod.yml).
log "Renewing certs via webroot..."
certbot renew \
  --webroot \
  --webroot-path "$ACME_DIR" \
  --non-interactive \
  --quiet \
  --deploy-hook "$DEPLOY_DIR/scripts/renew-ssl-deploy-hook.sh"

log "Renewal check complete."
