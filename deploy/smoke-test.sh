#!/usr/bin/env bash
set -Eeuo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1/api}"
APP_DIR="${APP_DIR:-/opt/tts-agent}"
TEST_WORKER_RECOVERY="${TEST_WORKER_RECOVERY:-0}"
temporary_dir="$(mktemp -d)"
email="smoke-$(date -u +%Y%m%d%H%M%S)-$(openssl rand -hex 3)@example.test"
password="Smoke-$(openssl rand -hex 8)!"
idempotency_key="smoke-$(openssl rand -hex 16)"
worker_stopped="false"
COMPOSE=(docker compose --project-directory "$APP_DIR" --env-file "$APP_DIR/.env" -f "$APP_DIR/docker-compose.prod.yml")

cleanup() {
  if [[ "$worker_stopped" == "true" ]]; then
    "${COMPOSE[@]}" start worker >/dev/null 2>&1 || true
  fi
  rm -rf -- "$temporary_dir"
}
trap cleanup EXIT

json_value() {
  python3 -c 'import json,sys; value=json.load(sys.stdin); print(value[sys.argv[1]])' "$1"
}

request() {
  local method="$1"
  local path="$2"
  local output="$3"
  shift 3
  curl --fail-with-body --silent --show-error --max-time 45 -X "$method" "$BASE_URL$path" -o "$output" "$@"
}

request POST /auth/register "$temporary_dir/register.json" -H 'Content-Type: application/json' --data "{\"email\":\"$email\",\"password\":\"$password\"}"
access_token="$(json_value access_token < "$temporary_dir/register.json")"
refresh_token="$(json_value refresh_token < "$temporary_dir/register.json")"

request GET /auth/me "$temporary_dir/me.json" -H "Authorization: Bearer $access_token"
request POST /auth/login "$temporary_dir/login.json" -H 'Content-Type: application/json' --data "{\"email\":\"$email\",\"password\":\"$password\"}"
request POST /auth/refresh "$temporary_dir/refresh.json" -H 'Content-Type: application/json' --data "{\"refresh_token\":\"$refresh_token\"}"
access_token="$(json_value access_token < "$temporary_dir/refresh.json")"

request POST /conversations "$temporary_dir/conversation.json" -H 'Content-Type: application/json' -H "Authorization: Bearer $access_token" --data '{"title":"线上冒烟测试"}'
conversation_id="$(json_value id < "$temporary_dir/conversation.json")"

if [[ "$TEST_WORKER_RECOVERY" == "1" ]]; then
  "${COMPOSE[@]}" stop worker >/dev/null
  worker_stopped="true"

  cancel_key="smoke-cancel-$(openssl rand -hex 16)"
  cancel_payload="{\"query\":\"生成一份待取消的测试分析\",\"conversation_id\":\"$conversation_id\"}"
  request POST /agent-runs "$temporary_dir/cancel-run.json" -H 'Content-Type: application/json' -H "Authorization: Bearer $access_token" -H "Idempotency-Key: $cancel_key" --data "$cancel_payload"
  cancel_run_id="$(json_value run_id < "$temporary_dir/cancel-run.json")"
  request POST "/agent-runs/$cancel_run_id/cancel" "$temporary_dir/cancel-response.json" -H "Authorization: Bearer $access_token"
fi

submit_payload="{\"query\":\"统计示例数据中的订单总数\",\"conversation_id\":\"$conversation_id\"}"
request POST /agent-runs "$temporary_dir/run-1.json" -H 'Content-Type: application/json' -H "Authorization: Bearer $access_token" -H "Idempotency-Key: $idempotency_key" --data "$submit_payload"
request POST /agent-runs "$temporary_dir/run-2.json" -H 'Content-Type: application/json' -H "Authorization: Bearer $access_token" -H "Idempotency-Key: $idempotency_key" --data "$submit_payload"
run_id="$(json_value run_id < "$temporary_dir/run-1.json")"
duplicate_run_id="$(json_value run_id < "$temporary_dir/run-2.json")"
if [[ "$run_id" != "$duplicate_run_id" ]]; then
  echo "idempotency check failed" >&2
  exit 1
fi

if [[ "$TEST_WORKER_RECOVERY" == "1" ]]; then
  request GET "/agent-runs/$run_id" "$temporary_dir/queued-status.json" -H "Authorization: Bearer $access_token"
  queued_status="$(json_value status < "$temporary_dir/queued-status.json")"
  if [[ "$queued_status" != "queued" ]]; then
    echo "worker recovery precondition failed: expected queued, got $queued_status" >&2
    exit 1
  fi
  "${COMPOSE[@]}" start worker >/dev/null
  worker_stopped="false"
fi

terminal_status=""
for _ in $(seq 1 120); do
  request GET "/agent-runs/$run_id" "$temporary_dir/run-status.json" -H "Authorization: Bearer $access_token"
  status="$(json_value status < "$temporary_dir/run-status.json")"
  if [[ "$status" == "completed" || "$status" == "failed" || "$status" == "cancelled" ]]; then
    terminal_status="$status"
    break
  fi
  sleep 2
done

if [[ "$terminal_status" != "completed" ]]; then
  echo "agent run did not complete successfully: ${terminal_status:-timeout}" >&2
  exit 1
fi

request GET "/agent-runs/$run_id/result" "$temporary_dir/result.json" -H "Authorization: Bearer $access_token"
request GET "/conversations/$conversation_id/messages" "$temporary_dir/messages.json" -H "Authorization: Bearer $access_token"

printf '{"status":"ok","email":"%s","conversation_id":"%s","run_id":"%s","idempotency":"ok","worker_recovery":"%s"}\n' "$email" "$conversation_id" "$run_id" "$TEST_WORKER_RECOVERY"
