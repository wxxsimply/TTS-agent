#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR="${APP_DIR:-/opt/tts-agent}"
BACKUP_DIR="${BACKUP_DIR:-/var/backups/tts-agent/postgres}"
DATABASE_NAME="${POSTGRES_DB:-tts_agent}"
DATABASE_USER="${POSTGRES_USER:-tts_agent}"
COMPOSE=(docker compose --project-directory "$APP_DIR" --env-file "$APP_DIR/.env" -f "$APP_DIR/docker-compose.prod.yml")

install -d -m 700 "$BACKUP_DIR"
umask 077

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_file="$BACKUP_DIR/${DATABASE_NAME}_${timestamp}.dump"
temporary_file="$BACKUP_DIR/.${DATABASE_NAME}_${timestamp}.tmp"

cleanup() {
  rm -f -- "$temporary_file"
}
trap cleanup EXIT

"${COMPOSE[@]}" exec -T postgres pg_dump \
  -U "$DATABASE_USER" \
  -d "$DATABASE_NAME" \
  --format=custom \
  --compress=6 > "$temporary_file"

test -s "$temporary_file"
"${COMPOSE[@]}" exec -T postgres pg_restore --list < "$temporary_file" >/dev/null
mv -- "$temporary_file" "$backup_file"
chmod 600 "$backup_file"

find "$BACKUP_DIR" -maxdepth 1 -type f -name "${DATABASE_NAME}_*.dump" -mtime +7 -delete

size_bytes="$(stat -c '%s' "$backup_file")"
printf '{"status":"ok","backup":"%s","size_bytes":%s}\n' "$backup_file" "$size_bytes"
