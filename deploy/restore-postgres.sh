#!/usr/bin/env bash
set -Eeuo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 BACKUP_FILE TARGET_DATABASE" >&2
  exit 2
fi

APP_DIR="${APP_DIR:-/opt/tts-agent}"
DATABASE_USER="${POSTGRES_USER:-tts_agent}"
backup_file="$1"
target_database="$2"
COMPOSE=(docker compose --project-directory "$APP_DIR" --env-file "$APP_DIR/.env" -f "$APP_DIR/docker-compose.prod.yml")

if [[ ! -f "$backup_file" ]]; then
  echo "backup file not found: $backup_file" >&2
  exit 2
fi
if [[ ! "$target_database" =~ ^[a-zA-Z][a-zA-Z0-9_]{0,62}$ ]]; then
  echo "invalid target database name" >&2
  exit 2
fi
if [[ "$target_database" == "tts_agent" ]]; then
  echo "refusing to restore over the production database" >&2
  exit 2
fi

exists="$("${COMPOSE[@]}" exec -T postgres psql -U "$DATABASE_USER" -d postgres -Atc "SELECT 1 FROM pg_database WHERE datname = '${target_database}'")"
if [[ "$exists" == "1" ]]; then
  echo "target database already exists: $target_database" >&2
  exit 2
fi

"${COMPOSE[@]}" exec -T postgres createdb -U "$DATABASE_USER" "$target_database"
if ! "${COMPOSE[@]}" exec -T postgres pg_restore -U "$DATABASE_USER" -d "$target_database" --no-owner --no-privileges < "$backup_file"; then
  "${COMPOSE[@]}" exec -T postgres dropdb -U "$DATABASE_USER" --if-exists "$target_database"
  exit 1
fi

migration_count="$("${COMPOSE[@]}" exec -T postgres psql -U "$DATABASE_USER" -d "$target_database" -Atc 'SELECT COUNT(*) FROM schema_migrations')"
printf '{"status":"ok","database":"%s","migration_count":%s}\n' "$target_database" "$migration_count"
