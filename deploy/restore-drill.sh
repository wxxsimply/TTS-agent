#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR="${APP_DIR:-/opt/tts-agent}"
BACKUP_DIR="${BACKUP_DIR:-/var/backups/tts-agent/postgres}"
DATABASE_USER="${POSTGRES_USER:-tts_agent}"
COMPOSE=(docker compose --project-directory "$APP_DIR" --env-file "$APP_DIR/.env" -f "$APP_DIR/docker-compose.prod.yml")

"${APP_DIR}/deploy/backup-postgres.sh" >/dev/null
latest_backup="$(find "$BACKUP_DIR" -maxdepth 1 -type f -name 'tts_agent_*.dump' -printf '%T@ %p\n' | sort -nr | head -n 1 | cut -d' ' -f2-)"
test -n "$latest_backup"

target_database="tts_agent_restore_$(date -u +%Y%m%d%H%M%S)"
cleanup() {
  "${COMPOSE[@]}" exec -T postgres dropdb -U "$DATABASE_USER" --if-exists "$target_database" >/dev/null 2>&1 || true
}
trap cleanup EXIT

"${APP_DIR}/deploy/restore-postgres.sh" "$latest_backup" "$target_database" >/dev/null
user_count="$("${COMPOSE[@]}" exec -T postgres psql -U "$DATABASE_USER" -d "$target_database" -Atc 'SELECT COUNT(*) FROM app_users')"
run_count="$("${COMPOSE[@]}" exec -T postgres psql -U "$DATABASE_USER" -d "$target_database" -Atc 'SELECT COUNT(*) FROM agent_runs')"
printf '{"status":"ok","backup":"%s","users":%s,"runs":%s}\n' "$latest_backup" "$user_count" "$run_count"
