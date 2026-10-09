#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR="${APP_DIR:-/opt/tts-agent}"
STATE_DIR="${STATE_DIR:-/var/lib/tts-agent-monitor}"
DATABASE_USER="${POSTGRES_USER:-tts_agent}"
DATABASE_NAME="${POSTGRES_DB:-tts_agent}"
COMPOSE=(docker compose --project-directory "$APP_DIR" --env-file "$APP_DIR/.env" -f "$APP_DIR/docker-compose.prod.yml")
SERVICES=(postgres redis backend worker frontend)

install -d -m 700 "$STATE_DIR"
status="ok"
unhealthy_services=()

for service in "${SERVICES[@]}"; do
  container_id="$("${COMPOSE[@]}" ps -q "$service")"
  if [[ -z "$container_id" ]]; then
    unhealthy_services+=("$service:missing")
    status="error"
    continue
  fi
  state="$(docker inspect --format '{{.State.Status}}' "$container_id")"
  health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container_id")"
  if [[ "$state" != "running" || "$health" == "unhealthy" ]]; then
    unhealthy_services+=("$service:${state}/${health}")
    status="error"
  fi
done

if ! curl --fail --silent --max-time 5 http://127.0.0.1/api/health/ready >/dev/null; then
  unhealthy_services+=("public-health:failed")
  status="error"
fi

queue_stats="$("${COMPOSE[@]}" exec -T postgres psql -U "$DATABASE_USER" -d "$DATABASE_NAME" -At -F ',' -c "SELECT COUNT(*) FILTER (WHERE status IN ('queued','retry_wait')), COUNT(*) FILTER (WHERE status='running'), COUNT(*) FILTER (WHERE status='failed' AND updated_at >= NOW() - INTERVAL '24 hours') FROM agent_jobs")"
IFS=',' read -r queued_jobs running_jobs failed_jobs_24h <<< "$queue_stats"

disk_percent="$(df -P / | awk 'NR==2 {gsub(/%/, "", $5); print $5}')"
memory_available_kb="$(awk '/MemAvailable:/ {print $2}' /proc/meminfo)"
memory_total_kb="$(awk '/MemTotal:/ {print $2}' /proc/meminfo)"
memory_available_percent="$((memory_available_kb * 100 / memory_total_kb))"

if (( disk_percent >= 85 || memory_available_percent <= 10 )) && [[ "$status" == "ok" ]]; then
  status="warning"
fi
if (( failed_jobs_24h > 0 )) && [[ "$status" == "ok" ]]; then
  status="warning"
fi

unhealthy="$(IFS=';'; echo "${unhealthy_services[*]}")"
timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
payload="$(printf '{"timestamp":"%s","status":"%s","queued_jobs":%s,"running_jobs":%s,"failed_jobs_24h":%s,"disk_percent":%s,"memory_available_percent":%s,"unhealthy":"%s"}' "$timestamp" "$status" "$queued_jobs" "$running_jobs" "$failed_jobs_24h" "$disk_percent" "$memory_available_percent" "$unhealthy")"

temporary_file="$STATE_DIR/.last-status.tmp"
printf '%s\n' "$payload" > "$temporary_file"
chmod 600 "$temporary_file"
mv -- "$temporary_file" "$STATE_DIR/last-status.json"
echo "$payload"

if [[ "$status" == "error" ]]; then
  exit 1
fi
