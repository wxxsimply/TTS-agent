#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR="${APP_DIR:-/opt/tts-agent}"
REPORT_DIR="${EVAL_REPORT_DIR:-/var/lib/tts-agent-evals}"
LIMIT="${EVAL_LIMIT:-10}"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$REPORT_DIR"

ARGS=(/app/eval -dataset /app/tests/eval/benchmark.json -output "/reports/eval-$TIMESTAMP.json" -limit "$LIMIT")
if [[ -f "$REPORT_DIR/baseline.json" ]]; then
  ARGS+=( -baseline /reports/baseline.json )
fi

docker compose --project-directory "$APP_DIR" --env-file "$APP_DIR/.env" -f "$APP_DIR/docker-compose.prod.yml" \
  run --rm --no-deps -v "$REPORT_DIR:/reports" backend "${ARGS[@]}"
cp "$REPORT_DIR/eval-$TIMESTAMP.json" "$REPORT_DIR/latest.json"
if [[ ! -f "$REPORT_DIR/baseline.json" ]]; then
  cp "$REPORT_DIR/latest.json" "$REPORT_DIR/baseline.json"
fi
