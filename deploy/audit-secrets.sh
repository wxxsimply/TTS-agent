#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR="${APP_DIR:-/opt/tts-agent}"
COMPOSE=(docker compose --project-directory "$APP_DIR" --env-file "$APP_DIR/.env" -f "$APP_DIR/docker-compose.prod.yml")
temporary_log="$(mktemp)"

cleanup() {
  rm -f -- "$temporary_log"
}
trap cleanup EXIT

"${COMPOSE[@]}" logs --no-color backend worker frontend > "$temporary_log"
leaked_keys=()

while IFS='=' read -r key value; do
  case "$key" in
    POSTGRES_PASSWORD|JWT_SECRET_KEY|DEEPSEEK_API_KEY|EMAIL_SMTP_PASSWORD|DATABASE_URL|DATABASE_READONLY_URL)
      if [[ ${#value} -ge 8 ]] && grep -Fq -- "$value" "$temporary_log"; then
        leaked_keys+=("$key")
      fi
      ;;
  esac
done < "$APP_DIR/.env"

if grep -Eq '"(access_token|refresh_token|password|verification_code)"[[:space:]]*:' "$temporary_log"; then
  leaked_keys+=("structured-sensitive-field")
fi

if (( ${#leaked_keys[@]} > 0 )); then
  printf 'sensitive log audit failed for keys: %s\n' "${leaked_keys[*]}" >&2
  exit 1
fi

echo '{"status":"ok","sensitive_values_in_logs":0}'
