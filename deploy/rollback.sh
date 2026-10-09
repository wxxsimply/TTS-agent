#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR="${APP_DIR:-/opt/tts-agent}"
COMPOSE=(docker compose --project-directory "$APP_DIR" --env-file "$APP_DIR/.env" -f "$APP_DIR/docker-compose.prod.yml")

docker image inspect tts-agent-backend:rollback >/dev/null
docker image inspect tts-agent-frontend:rollback >/dev/null
docker image tag tts-agent-backend:rollback tts-agent-backend:latest
docker image tag tts-agent-frontend:rollback tts-agent-frontend:latest
"${COMPOSE[@]}" up -d --force-recreate backend worker frontend

for _ in $(seq 1 45); do
  if curl --fail --silent --max-time 3 http://127.0.0.1/api/health/ready >/dev/null; then
    echo '{"status":"ok","rollback":"completed"}'
    exit 0
  fi
  sleep 2
done

echo "rollback health check failed" >&2
exit 1
