#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR="/opt/tts-agent"
PUBLIC_ORIGIN="http://39.102.136.31"

if [[ ! -f /swapfile ]]; then
  fallocate -l 2G /swapfile
  chmod 600 /swapfile
  mkswap /swapfile >/dev/null
fi

if ! swapon --show=NAME --noheadings | grep -qx '/swapfile'; then
  swapon /swapfile
fi

if ! grep -qE '^/swapfile[[:space:]]' /etc/fstab; then
  printf '/swapfile none swap sw 0 0\n' >> /etc/fstab
fi

cd "$APP_DIR"
tar -xzf deployment-source.tar.gz

upsert_env() {
  local key="$1"
  local value="$2"
  if grep -q "^${key}=" .env; then
    sed -i "s|^${key}=.*$|${key}=${value}|" .env
  else
    printf '%s=%s\n' "$key" "$value" >> .env
  fi
}

if [[ ! -f .production-secrets-initialized ]]; then
  postgres_password="$(openssl rand -hex 24)"
  jwt_secret="$(openssl rand -hex 32)"

  upsert_env POSTGRES_USER tts_agent
  upsert_env POSTGRES_PASSWORD "$postgres_password"
  upsert_env POSTGRES_DB tts_agent
  upsert_env JWT_SECRET_KEY "$jwt_secret"
  upsert_env DATABASE_URL "postgres://tts_agent:${postgres_password}@postgres:5432/tts_agent?sslmode=disable"
  upsert_env DATABASE_READONLY_URL "postgres://agent_readonly:readonly_secret@postgres:5432/tts_agent?sslmode=disable"
  upsert_env REDIS_URL redis://redis:6379/0
  upsert_env CORS_ORIGINS "$PUBLIC_ORIGIN"
  upsert_env WORKER_CONCURRENCY 1
  upsert_env WORKER_USER_CONCURRENCY 1
  install -m 600 /dev/null .production-secrets-initialized
fi

chmod 600 .env
if [[ -f deployment-images.tar ]]; then
  docker load -i deployment-images.tar
fi
if [[ -f deployment-base-images.tar ]]; then
  docker load -i deployment-base-images.tar
fi
docker compose --env-file .env -f docker-compose.prod.yml config --quiet
docker compose --env-file .env -f docker-compose.prod.yml up -d

for _ in $(seq 1 30); do
  if curl --fail --silent --max-time 3 http://127.0.0.1/api/health/ready >/dev/null; then
    docker compose --env-file .env -f docker-compose.prod.yml ps
    exit 0
  fi
  sleep 2
done

docker compose --env-file .env -f docker-compose.prod.yml ps
docker compose --env-file .env -f docker-compose.prod.yml logs --tail=100 backend worker frontend
exit 1
