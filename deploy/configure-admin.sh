#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR="${APP_DIR:-/opt/tts-agent}"
COMPOSE=(docker compose --project-directory "$APP_DIR" --env-file "$APP_DIR/.env" -f "$APP_DIR/docker-compose.prod.yml")

admin_email="$("${COMPOSE[@]}" exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "SELECT email FROM app_users ORDER BY created_at ASC LIMIT 1"')"
if [[ -z "$admin_email" ]]; then
  echo "no application user exists; ADMIN_EMAILS was not changed" >&2
  exit 0
fi

if grep -q '^ADMIN_EMAILS=' "$APP_DIR/.env"; then
  sed -i "s|^ADMIN_EMAILS=.*$|ADMIN_EMAILS=$admin_email|" "$APP_DIR/.env"
else
  printf 'ADMIN_EMAILS=%s\n' "$admin_email" >> "$APP_DIR/.env"
fi
chmod 600 "$APP_DIR/.env"
echo '{"status":"ok","admin_configured":true}'
