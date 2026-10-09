#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR="${APP_DIR:-/opt/tts-agent}"
IMAGE_ARCHIVE="${1:-$APP_DIR/deployment-images.tar}"
COMPOSE=(docker compose --project-directory "$APP_DIR" --env-file "$APP_DIR/.env" -f "$APP_DIR/docker-compose.prod.yml")

if [[ ! -f "$IMAGE_ARCHIVE" ]]; then
  echo "image archive not found: $IMAGE_ARCHIVE" >&2
  exit 2
fi

if [[ -x /usr/local/sbin/tts-agent-backup ]]; then
  /usr/local/sbin/tts-agent-backup >/dev/null
fi

docker image inspect tts-agent-backend:latest >/dev/null
docker image inspect tts-agent-frontend:latest >/dev/null
docker image tag tts-agent-backend:latest tts-agent-backend:rollback
docker image tag tts-agent-frontend:latest tts-agent-frontend:rollback

docker load -i "$IMAGE_ARCHIVE"
"${COMPOSE[@]}" up -d --force-recreate backend worker frontend

for _ in $(seq 1 45); do
  if curl --fail --silent --max-time 3 http://127.0.0.1/api/health/ready >/dev/null; then
    if [[ "${EVAL_GATE_ENABLED:-1}" = "1" ]]; then
      if ! EVAL_LIMIT="${EVAL_RELEASE_LIMIT:-5}" "$APP_DIR/deploy/eval-gate.sh"; then
        echo "release evaluation gate failed; rolling back" >&2
        "$APP_DIR/deploy/rollback.sh"
        exit 1
      fi
    fi
    echo '{"status":"ok","release":"completed"}'
    exit 0
  fi
  sleep 2
done

echo "release health check failed; rolling back" >&2
"$APP_DIR/deploy/rollback.sh"
exit 1
