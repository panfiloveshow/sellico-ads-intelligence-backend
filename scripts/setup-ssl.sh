#!/usr/bin/env bash
# setup-ssl.sh — One-time TLS certificate bootstrap for the production VPS.
#
# Acquires a Let's Encrypt certificate via certbot --standalone (port 80 must be
# free during issuance) and copies it into ./nginx/ssl/ so the nginx container
# can mount it. After this runs once, scripts/renew-ssl.sh handles renewals.
#
# Usage:
#   sudo DOMAIN=api.sellico.ru EMAIL=ops@sellico.ru ./scripts/setup-ssl.sh
#
# Idempotent: re-running on a host that already has a cert just refreshes the copy.

set -euo pipefail

DOMAIN="${DOMAIN:?DOMAIN env var required (e.g. api.sellico.ru)}"
EMAIL="${EMAIL:?EMAIL env var required (for Lets Encrypt expiry notices)}"
DEPLOY_DIR="${DEPLOY_DIR:-/opt/sellico}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.prod.yml}"

log() { echo "[$(date -Iseconds)] $*"; }

if [[ $EUID -ne 0 ]]; then
  echo "ERROR: setup-ssl.sh must be run as root (needs port 80, /etc/letsencrypt write)" >&2
  exit 1
fi

# Install certbot if missing
if ! command -v certbot &>/dev/null; then
  log "Installing certbot..."
  apt-get update -qq
  apt-get install -y certbot
fi

# Stop nginx so port 80 is free for certbot --standalone
if [ -d "$DEPLOY_DIR" ]; then
  log "Stopping nginx container so port 80 is free..."
  ( cd "$DEPLOY_DIR" && docker compose -f "$COMPOSE_FILE" stop nginx ) || true
fi

# Acquire (or renew) the certificate
log "Requesting cert for $DOMAIN..."
certbot certonly \
  --standalone \
  --non-interactive \
  --agree-tos \
  --email "$EMAIL" \
  --domain "$DOMAIN" \
  --rsa-key-size 4096 \
  --keep-until-expiring

# Copy certs into the nginx mount path
SSL_DIR="$DEPLOY_DIR/nginx/ssl"
mkdir -p "$SSL_DIR"
install -m 0644 "/etc/letsencrypt/live/$DOMAIN/fullchain.pem" "$SSL_DIR/fullchain.pem"
install -m 0600 "/etc/letsencrypt/live/$DOMAIN/privkey.pem"   "$SSL_DIR/privkey.pem"
log "Certificates copied to $SSL_DIR"

# Install renewal cron if missing
RENEW_LINE="0 4 * * 1 $DEPLOY_DIR/scripts/renew-ssl.sh >> /var/log/sellico-ssl-renew.log 2>&1"
if ! crontab -l 2>/dev/null | grep -q "renew-ssl.sh"; then
  ( crontab -l 2>/dev/null; echo "$RENEW_LINE" ) | crontab -
  log "Renewal cron installed (weekly Mon 04:00)"
fi

# Bring nginx back up — now with HTTPS
log "Starting nginx with HTTPS config..."
( cd "$DEPLOY_DIR" && docker compose -f "$COMPOSE_FILE" up -d nginx )

log "=== SSL setup complete. Verify: curl -I https://$DOMAIN/health/ready ==="
